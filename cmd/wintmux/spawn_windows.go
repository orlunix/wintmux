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
// Uses CREATE_BREAKAWAY_FROM_JOB so the daemon survives when the
// parent SSH session ends (OpenSSH uses Job Objects to kill children).
func spawnDaemon(socketPath, sessionName, workdir, command string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	parts := []string{exe, "--daemon", "-S", socketPath, "new-session", "-d", "-s", sessionName}
	if workdir != "" {
		parts = append(parts, "-c", workdir)
	}
	if command != "" {
		parts = append(parts, command)
	}
	cmdLine := strings.Join(parts, " ")

	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return fmt.Errorf("cmd line: %w", err)
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))

	var pi syscall.ProcessInformation
	// CREATE_NO_WINDOW (0x08000000): don't create a console window
	// CREATE_NEW_PROCESS_GROUP (0x00000200): separate Ctrl-C group
	// CREATE_BREAKAWAY_FROM_JOB (0x01000000): escape SSH's Job Object
	const flags = 0x08000000 | 0x00000200 | 0x01000000
	err = syscall.CreateProcess(
		nil,
		cmdLinePtr,
		nil, nil,
		false, // don't inherit handles
		flags,
		nil, nil,
		&si, &pi,
	)
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}
	syscall.CloseHandle(pi.Thread)
	syscall.CloseHandle(pi.Process)
	return nil
}
