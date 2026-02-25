//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDaemon launches the wintmux daemon as a background process on
// Unix-like systems (used for development/testing on WSL2 and macOS).
func spawnDaemon(socketPath, sessionName, workdir, command string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	args := []string{
		"--daemon",
		"-S", socketPath,
		"new-session", "-d",
		"-s", sessionName,
	}
	if workdir != "" {
		args = append(args, "-c", workdir)
	}
	if command != "" {
		args = append(args, command)
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Start()
}
