package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wintmux/internal/ipc"
	"wintmux/internal/pty"
	"wintmux/internal/scrollback"
	"wintmux/internal/vt"
)

// ControlInfo is written to the socket path file so CLI clients can
// discover the daemon's TCP port.
type ControlInfo struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

// Daemon manages a single session: one ConPTY process, a scrollback
// buffer, and a TCP server for IPC.
type Daemon struct {
	socketPath   string
	sessionName  string
	terminal     pty.Terminal
	buffer       *scrollback.Buffer
	listener     net.Listener
	pipePaneMu   sync.Mutex
	pipePaneFile *os.File
	done         chan struct{} // closed when child process exits
}

// Run is the main entry point for a daemon process. It creates the
// terminal, starts the IPC server, and blocks until the child exits
// and the grace period elapses.
func Run(socketPath, sessionName, workdir, command string, cols, rows int) error {
	freeConsole()
	term, err := pty.New(cols, rows, command, workdir, nil)
	if err != nil {
		return fmt.Errorf("create terminal: %w", err)
	}

	d := &Daemon{
		socketPath:  socketPath,
		sessionName: sessionName,
		terminal:    term,
		buffer:      scrollback.New(2000),
		done:        make(chan struct{}),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		term.Close()
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = listener

	addr := listener.Addr().(*net.TCPAddr)
	info := ControlInfo{Port: addr.Port, PID: os.Getpid()}
	if err := writeControlFile(socketPath, info); err != nil {
		listener.Close()
		term.Close()
		return fmt.Errorf("write control file: %w", err)
	}

	// Redirect log output to a file next to the control file for debugging.
	logPath := socketPath + ".log"
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644); err == nil {
		log.SetOutput(lf)
		defer lf.Close()
	}

	log.Printf("daemon: session=%s pid=%d port=%d socket=%s", sessionName, info.PID, info.Port, socketPath)

	go d.readOutput()
	go d.watchProcess()

	d.acceptConnections()
	d.cleanup()
	return nil
}

// readOutput continuously reads from the terminal and feeds data into
// the scrollback buffer (and optional pipe-pane file).
func (d *Daemon) readOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := d.terminal.Read(buf)
		if n > 0 {
			data := buf[:n]
			d.buffer.Write(data)

			d.pipePaneMu.Lock()
			if d.pipePaneFile != nil {
				d.pipePaneFile.Write(data)
			}
			d.pipePaneMu.Unlock()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("daemon: read error: %v", err)
			}
			return
		}
	}
}

// watchProcess waits for the child to exit, then shuts down the daemon
// after a grace period.
func (d *Daemon) watchProcess() {
	d.terminal.Wait()
	log.Printf("daemon: child exited with code %d", d.terminal.ExitCode())
	close(d.done)
	time.Sleep(5 * time.Second)
	d.listener.Close()
}

func (d *Daemon) acceptConnections() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}
		go d.handleConnection(conn)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	var req ipc.Request
	if err := ipc.ReadMessage(conn, &req); err != nil {
		log.Printf("daemon: read request: %v", err)
		return
	}

	resp := d.dispatch(req)
	if err := ipc.WriteMessage(conn, resp); err != nil {
		log.Printf("daemon: write response: %v", err)
	}
}

func (d *Daemon) dispatch(req ipc.Request) ipc.Response {
	switch req.Action {
	case ipc.ActionPing:
		return ipc.Response{OK: true}
	case ipc.ActionSendKeys:
		return d.handleSendKeys(req)
	case ipc.ActionSendKey:
		return d.handleSendKey(req)
	case ipc.ActionCapture:
		return d.handleCapture(req)
	case ipc.ActionHasSession:
		return d.handleHasSession()
	case ipc.ActionKillSession:
		return d.handleKillSession()
	case ipc.ActionSetOption:
		return d.handleSetOption(req)
	case ipc.ActionPipePane:
		return d.handlePipePane(req)
	default:
		return ipc.Response{OK: false, Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

func (d *Daemon) handleSendKeys(req ipc.Request) ipc.Response {
	if req.Text != "" {
		if _, err := d.terminal.Write([]byte(req.Text)); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
	}
	if req.SendEnter {
		if _, err := d.terminal.Write([]byte("\r")); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
	}
	return ipc.Response{OK: true}
}

// keyMap translates tmux key names to the VT byte sequences expected by
// terminal applications.
var keyMap = map[string]string{
	"Enter":    "\r",
	"Escape":   "\x1b",
	"BSpace":   "\x7f",
	"Tab":      "\t",
	"Space":    " ",
	"C-c":      "\x03",
	"C-d":      "\x04",
	"C-z":      "\x1a",
	"Up":       "\x1b[A",
	"Down":     "\x1b[B",
	"Right":    "\x1b[C",
	"Left":     "\x1b[D",
	"Home":     "\x1b[H",
	"End":      "\x1b[F",
	"DC":       "\x1b[3~",
	"PageUp":   "\x1b[5~",
	"PageDown": "\x1b[6~",
}

func (d *Daemon) handleSendKey(req ipc.Request) ipc.Response {
	seq, ok := keyMap[req.Key]
	if !ok {
		return ipc.Response{OK: false, Error: fmt.Sprintf("unknown key: %s", req.Key)}
	}
	if _, err := d.terminal.Write([]byte(seq)); err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	return ipc.Response{OK: true}
}

func (d *Daemon) handleCapture(req ipc.Request) ipc.Response {
	lines := req.Lines
	if lines <= 0 {
		lines = 50
	}
	captured := d.buffer.LastWithPartial(lines)
	// Strip VT escape sequences from each line for clean text output.
	for i, line := range captured {
		captured[i] = vt.Strip(line)
	}
	output := strings.Join(captured, "\n")
	return ipc.Response{OK: true, Output: output}
}

func (d *Daemon) handleHasSession() ipc.Response {
	select {
	case <-d.done:
		return ipc.Response{OK: true, Exists: false}
	default:
		return ipc.Response{OK: true, Exists: true}
	}
}

func (d *Daemon) handleKillSession() ipc.Response {
	if err := d.terminal.Close(); err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	return ipc.Response{OK: true}
}

func (d *Daemon) handleSetOption(req ipc.Request) ipc.Response {
	switch req.Option {
	case "history-limit":
		n, err := strconv.Atoi(req.Value)
		if err != nil || n <= 0 {
			return ipc.Response{OK: false, Error: "invalid history-limit value"}
		}
		d.buffer.SetCapacity(n)
		return ipc.Response{OK: true}
	default:
		return ipc.Response{OK: false, Error: fmt.Sprintf("unknown option: %s", req.Option)}
	}
}

func (d *Daemon) handlePipePane(req ipc.Request) ipc.Response {
	d.pipePaneMu.Lock()
	defer d.pipePaneMu.Unlock()

	if d.pipePaneFile != nil {
		d.pipePaneFile.Close()
		d.pipePaneFile = nil
	}

	if req.ShellCmd == "" {
		return ipc.Response{OK: true}
	}

	path := extractPipePath(req.ShellCmd)
	if path == "" {
		return ipc.Response{OK: false, Error: "unsupported pipe-pane command (only 'cat >> path' supported)"}
	}

	os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	d.pipePaneFile = f
	return ipc.Response{OK: true}
}

func (d *Daemon) cleanup() {
	d.pipePaneMu.Lock()
	if d.pipePaneFile != nil {
		d.pipePaneFile.Close()
	}
	d.pipePaneMu.Unlock()

	d.terminal.Close()
	os.Remove(d.socketPath)
	log.Printf("daemon: cleaned up session %s", d.sessionName)
}

func writeControlFile(path string, info ControlInfo) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// extractPipePath parses "cat >> /path/to/file" and returns the file path.
func extractPipePath(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasPrefix(cmd, "cat") {
		return ""
	}
	cmd = strings.TrimPrefix(cmd, "cat")
	cmd = strings.TrimSpace(cmd)
	if !strings.HasPrefix(cmd, ">>") {
		return ""
	}
	cmd = strings.TrimPrefix(cmd, ">>")
	cmd = strings.TrimSpace(cmd)
	cmd = strings.Trim(cmd, "'\"")
	return cmd
}
