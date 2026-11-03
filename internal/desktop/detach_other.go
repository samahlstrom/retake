//go:build !unix

package desktop

import "os/exec"

func detach(*exec.Cmd) {}
