//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

// start: retakew is Windows' launcher; elsewhere run retake itself.
func start(string, []string) error {
	return errors.New("retakew is for Windows; run retake instead")
}

func fail(msg string) {
	_, _ = fmt.Fprintln(os.Stderr, "retakew:", msg)
	os.Exit(1)
}
