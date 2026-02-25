package ipc

import (
	"encoding/json"
	"fmt"
	"io"
)

// Action identifies the type of IPC request sent from the CLI to the daemon.
type Action string

const (
	ActionSendKeys    Action = "send_keys"
	ActionSendKey     Action = "send_key"
	ActionCapture     Action = "capture_pane"
	ActionHasSession  Action = "has_session"
	ActionKillSession Action = "kill_session"
	ActionSetOption   Action = "set_option"
	ActionPipePane    Action = "pipe_pane"
	ActionAttach      Action = "attach"
	ActionPing        Action = "ping"
)

// Request is a JSON message sent from the CLI client to the session daemon.
type Request struct {
	Action    Action `json:"action"`
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Literal   bool   `json:"literal,omitempty"`
	SendEnter bool   `json:"send_enter,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Alternate bool   `json:"alternate,omitempty"`
	Join      bool   `json:"join,omitempty"`
	Option    string `json:"option,omitempty"`
	Value     string `json:"value,omitempty"`
	ShellCmd  string `json:"shell_cmd,omitempty"`
}

// Response is a JSON message sent from the session daemon back to the CLI client.
type Response struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
	Exists bool   `json:"exists,omitempty"`
}

const maxMessageSize = 10 * 1024 * 1024 // 10 MB

// WriteMessage serializes v as JSON and writes it to w with a 4-byte
// big-endian length prefix.
func WriteMessage(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	length := uint32(len(data))
	header := [4]byte{
		byte(length >> 24),
		byte(length >> 16),
		byte(length >> 8),
		byte(length),
	}

	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// ReadMessage reads a length-prefixed JSON message from r and unmarshals
// it into v.
func ReadMessage(r io.Reader, v interface{}) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	length := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	if length > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", length, maxMessageSize)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}
