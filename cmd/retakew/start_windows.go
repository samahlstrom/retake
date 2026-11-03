//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// start runs retake with args in a console of its own that has no window (so the programs it starts, the agents and
// ffmpeg, open none either), detached from this launcher, and returns once it's running.
func start(retake string, args []string) error {
	cmd := exec.Command(retake, args...) //nolint:gosec // retake.exe next to this launcher
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// fail says what's wrong in a message box (there's no console to print to) and exits.
func fail(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("Retake")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
