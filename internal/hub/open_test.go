package hub

import (
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
)

// Requests that open a project at once share one opening; Start opens every project and its Server (its job
// runner) without waiting for a visit.
func TestProjectsOpenOnceAndAtStart(t *testing.T) {
	x := newTH(t, Config{}, nil)
	x.project("demo", false)
	x.project("empty", true)
	var wg sync.WaitGroup
	got := make([]*proj, 16)
	for i := range got {
		wg.Go(func() {
			p, err := x.h.project("demo")
			if err != nil {
				t.Error(err)
			}
			got[i] = p
		})
	}
	wg.Wait()
	for _, p := range got {
		if p != got[0] {
			t.Fatal("a project was opened twice")
		}
	}
	if _, err := x.h.project("nope"); err == nil {
		t.Fatal("an unknown project opened")
	}
	x.h.Start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, _ := x.h.project("demo")
		p.mu.Lock()
		started := p.srv != nil
		p.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start didn't open the project's Server")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v, _ := x.st.Accounts.Setting(accounts.SettingPublicURL); v != x.url {
		t.Fatalf("public URL recorded: %q", v)
	}
}
