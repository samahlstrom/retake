package project

import "testing"

// TestLockJobsIsExclusive: one job runner per project, also across handles in one process (flock on Unix, LockFileEx
// on Windows); unlocking lets the next one in.
func TestLockJobsIsExclusive(t *testing.T) {
	root := t.TempDir()
	unlock, ok, err := LockJobs(root)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok2, err := LockJobs(root); err != nil || ok2 {
		t.Fatal("a second runner got the lock", ok2, err)
	}
	unlock()
	again, ok, err := LockJobs(root)
	if err != nil || !ok {
		t.Fatal("no lock after unlock", ok, err)
	}
	again()
}
