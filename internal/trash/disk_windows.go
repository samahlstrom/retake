//go:build windows

package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

var goos = runtime.GOOS

// sameDisk reports whether two paths are on the same drive (or share).
func sameDisk(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

func mountOf(string) string { return "" }

// isCrossDevice reports a rename across drives (ERROR_NOT_SAME_DEVICE: a folder mounted from another volume).
func isCrossDevice(err error) bool {
	var le *os.LinkError
	return errors.As(err, &le) && errors.Is(le.Err, windows.ERROR_NOT_SAME_DEVICE)
}

// recycleBin is the person's folder in the Recycle Bin of path's drive: <drive>:\$Recycle.Bin\<their SID>, made when
// the drive's Recycle Bin is there but their folder isn't yet. A network share, or a drive without a Recycle Bin, is
// ErrOtherDisk: the folder stays where it is.
func recycleBin(path string) (string, error) {
	vol := filepath.VolumeName(path)
	if len(vol) != 2 || vol[1] != ':' {
		return "", ErrOtherDisk // \\server\share: the share's computer keeps no Recycle Bin for us
	}
	bin := vol + `\$Recycle.Bin`
	if st, err := os.Stat(bin); err != nil || !st.IsDir() {
		return "", ErrOtherDisk
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("can't tell whose Recycle Bin to use: %w", err)
	}
	dir := filepath.Join(bin, u.User.Sid.String())
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", ErrOtherDisk
	}
	return dir, nil
}
