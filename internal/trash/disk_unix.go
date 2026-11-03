//go:build unix

package trash

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

var goos = runtime.GOOS

// device is the disk a path is on.
func device(path string) (uint64, bool) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev is int32 on some systems
}

// sameDisk reports whether two paths are on the same disk.
func sameDisk(a, b string) bool {
	da, ok1 := device(a)
	db, ok2 := device(b)
	return ok1 && ok2 && da == db
}

// mountOf is the top of the disk path is on: the last folder above it with the same device.
func mountOf(path string) string {
	dev, ok := device(path)
	if !ok {
		return ""
	}
	cur := filepath.Clean(path)
	for {
		up := filepath.Dir(cur)
		if up == cur {
			return cur
		}
		if d, ok := device(up); !ok || d != dev {
			return cur
		}
		cur = up
	}
}

// recycleBin: there's no Recycle Bin here (Move only uses it when Options.GOOS says Windows).
func recycleBin(string) (string, error) { return "", ErrOtherDisk }

// isCrossDevice reports a rename across disks.
func isCrossDevice(err error) bool {
	var le *os.LinkError
	return errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV)
}
