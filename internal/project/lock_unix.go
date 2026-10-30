//go:build unix

package project

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// LockJobs takes the project's job-runner lock (<root>/.retake/jobs.lock) without waiting, so only one process
// runs a project's jobs at a time. ok is false while another process holds it. unlock releases it.
func LockJobs(root string) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(root, Dir, "jobs.lock"), os.O_RDWR|os.O_CREATE, 0o644) //nolint:gosec // a fixed name under the project
	if err != nil {
		return nil, false, err
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
