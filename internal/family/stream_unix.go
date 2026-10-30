//go:build unix

package family

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

// LockRender takes the render lock of a playlist (an exclusive flock on RenderLockPath) for a streamed push, without
// waiting. It's held until unlock, or until the process dies, which is how the family tells a render in progress
// from one that was killed. unlock also removes the lock file.
func LockRender(playlist string) (unlock func(), err error) {
	p := RenderLockPath(playlist)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644) //nolint:gosec // a lock file beside the version
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another push is rendering this version")
		}
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = os.Remove(p)
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
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
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}
