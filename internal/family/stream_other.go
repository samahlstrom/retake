//go:build !unix && !windows

package family

import (
	"os"
	"sync"
)

// LockRender marks a playlist as being rendered by a streamed push. This platform has no flock, so the lock file
// only exists while the push runs (a killed push leaves it, and the version then shows as rendering until removed).
func LockRender(playlist string) (unlock func(), err error) {
	p := RenderLockPath(playlist)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644) //nolint:gosec // a lock file beside the version
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = f.Close()
			_ = os.Remove(p)
		})
	}, nil
}

// RenderLive reports whether a playlist's lock file exists.
func RenderLive(playlist string) bool {
	_, err := os.Stat(RenderLockPath(playlist))
	return err == nil
}
