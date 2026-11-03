//go:build windows

package family

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// LockRender takes the render lock of a playlist (an exclusive LockFileEx on RenderLockPath) for a streamed push,
// without waiting. It's held until unlock, or until the process dies, which is how the family tells a render in
// progress from one that was killed. unlock also removes the lock file (after closing it: Windows doesn't delete an
// open file).
func LockRender(playlist string) (unlock func(), err error) {
	p := RenderLockPath(playlist)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644) //nolint:gosec // a lock file beside the version
	if err != nil {
		return nil, err
	}
	h, ol := windows.Handle(f.Fd()), new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("another push is rendering this version")
		}
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
			_ = f.Close()
			_ = os.Remove(p) // fails harmlessly when another push has just opened it
		})
	}, nil
}

// RenderLive reports whether a push holds the render lock of a playlist (from any process, this one included).
func RenderLive(playlist string) bool {
	f, err := os.Open(RenderLockPath(playlist))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h, ol := windows.Handle(f.Fd()), new(windows.Overlapped)
	err = windows.LockFileEx(h, windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol) // shared
	if err == nil {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		return false
	}
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
