//go:build !unix && !windows

package project

import (
	"os"
	"path/filepath"
)

// LockJobs takes the project's job-runner lock. This platform has no flock, so it only makes sure the folder
// exists and always succeeds: run one server per project there.
func LockJobs(root string) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return nil, false, err
	}
	return func() {}, true, nil
}
