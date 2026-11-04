package hub

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/server"
)

func TestFolderConcurrentCloseWaitsForStoresAndWorkers(t *testing.T) {
	started, stopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	x := newFT(t, func(o *FolderOptions) {
		o.LocalWorker = func(ctx context.Context, _ string, _ http.Handler, _ func(string)) {
			close(started)
			<-ctx.Done()
			close(stopping)
			<-release
		}
		o.Project = func(name string, s *server.Options) {
			if name != "alpha" {
				s.LocalWorker = nil
			}
		}
	})
	t.Cleanup(unblock)
	if x.f.projectNamed("alpha") == nil {
		t.Fatal("no alpha")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- x.f.Close() }()
	select {
	case <-stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not begin stopping")
	}
	go func() { second <- x.f.Close() }()
	returned := false
	select {
	case err := <-second:
		returned = true
		if err != nil {
			t.Error(err)
		}
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-first:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Close did not finish")
	}
	if returned {
		t.Fatal("second Close returned while the first still held worker/store handles")
	}
	select {
	case err := <-second:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Close did not finish")
	}
}
