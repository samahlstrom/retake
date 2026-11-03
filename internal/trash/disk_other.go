//go:build !unix

package trash

import "runtime"

var goos = runtime.GOOS

// sameDisk can't tell on this system: a rename across disks fails, and the folder stays.
func sameDisk(string, string) bool { return true }

func mountOf(string) string { return "" }

func isCrossDevice(error) bool { return false }
