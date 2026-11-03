//go:build !unix && !windows

package sharing

// alive can't tell on this system: a share's host counts while its row is there.
func alive(int) bool { return true }
