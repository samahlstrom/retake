package hub

import (
	"sync/atomic"
	"testing"
	"time"
)

type pausedLibraryScanLog struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (w *pausedLibraryScanLog) Write(p []byte) (int, error) {
	if w.armed.CompareAndSwap(true, false) {
		close(w.entered)
		<-w.release
	}
	return len(p), nil
}

func TestLibraryRemovalCannotBeUndoneByOlderScan(t *testing.T) {
	log := &pausedLibraryScanLog{entered: make(chan struct{}), release: make(chan struct{})}
	x := newFT(t, func(o *FolderOptions) { o.Log = log; o.Verbose = true; o.RescanEvery = time.Hour })
	initial := libraryView(t, x.get("/api/library"))
	revision := libraryRevision(t, initial)
	log.armed.Store(true)
	scanned := make(chan struct{})
	go func() { x.f.rescan(true); close(scanned) }()
	select {
	case <-log.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach publication")
	}
	changed := make(chan res, 1)
	go func() { changed <- replaceLibrary(t, x, revision, []map[string]any{}) }()
	var result res
	completed := false
	select {
	case result = <-changed:
		completed = true
	case <-time.After(100 * time.Millisecond):
	}
	close(log.release)
	select {
	case <-scanned:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not finish")
	}
	if !completed {
		select {
		case result = <-changed:
		case <-time.After(5 * time.Second):
			t.Fatal("change did not finish")
		}
	}
	if result.code != 200 {
		t.Fatalf("remove: %d %s", result.code, result.body)
	}
	if cards := x.home(t).Projects; len(cards) != 0 {
		t.Fatalf("older scan restored removed videos: %+v", cards)
	}
}
