// Command retakew is Retake's launcher on Windows: a program without a console window (built with
// -ldflags=-H=windowsgui) that runs retake.exe from its own folder with the same arguments, also without a window, and
// returns. The Start menu shortcut and starting at login (retake autostart on) run `retakew.exe start --app`, so
// Retake starts in the background and opens the browser, with no console window to close by mistake.
package main

import (
	"os"
	"path/filepath"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail("Retake can't find its own folder: " + err.Error())
	}
	retake := filepath.Join(filepath.Dir(exe), "retake.exe")
	if _, err := os.Stat(retake); err != nil {
		fail("retake.exe isn't next to retakew.exe (" + filepath.Dir(exe) + "). Install Retake again.")
	}
	if err := start(retake, os.Args[1:]); err != nil {
		fail("Retake couldn't start: " + err.Error())
	}
}
