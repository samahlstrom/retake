//go:build windows

package project

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// LockJobs takes the project's job-runner lock (<root>/.retake/jobs.lock) without waiting, so only one process
// runs a project's jobs at a time: an exclusive LockFileEx, released when the process ends. ok is false while another
// process holds it. unlock releases it.
func LockJobs(root string) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(root, Dir, "jobs.lock"), os.O_RDWR|os.O_CREATE, 0o644) //nolint:gosec // a fixed name under the project
	if err != nil {
		return nil, false, err
	}
	h, ol := windows.Handle(f.Fd()), new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		_ = f.Close()
	}, true, nil
}
