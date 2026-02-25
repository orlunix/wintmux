//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// spawnDaemon launches the wintmux daemon as a background process.
// Uses raw CreateProcessW instead of exec.Command to avoid Go runtime
// setting up inherited pipes that can interfere with ConPTY.
func spawnDaemon(socketPath, sessionName, workdir, command string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	parts := []string{
		`"` + exe + `"`,
		"--daemon",
		"-S", socketPath,
		"new-session", "-d",
		"-s", sessionName,
	}
	if workdir != "" {
		parts = append(parts, "-c", workdir)
	}
	if command != "" {
		parts = append(parts, command)
	}
	cmdLine := strings.Join(parts, " ")

	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return fmt.Errorf("UTF16PtrFromString: %w", err)
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi syscall.ProcessInformation

	// CREATE_NEW_PROCESS_GROUP (0x200) | CREATE_NO_WINDOW (0x08000000)
	flags := uint32(0x00000200 | 0x08000000)

	createErr := syscall.CreateProcess(
		nil,
		cmdLinePtr,
		nil, nil,
		false, // bInheritHandles = FALSE
		flags,
		nil, nil,
		&si, &pi,
	)
	if createErr != nil {
		return fmt.Errorf("CreateProcess: %w", createErr)
	}

	syscall.CloseHandle(pi.Thread)
	syscall.CloseHandle(pi.Process)
	return nil
}
