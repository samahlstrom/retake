package relay

import (
	"context"
	"testing"
	"time"
)

func TestRelayCorrelatesResponsesAndDropsCancelledRequests(t *testing.T) {
	var h Hub
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan Response, 1)
	go func() { v, _ := h.Fetch(ctx, "sam", Request{Render: "revision", File: "index.m3u8"}); done <- v }()
	r, e := h.Next(ctx, "sam")
	if e != nil {
		t.Fatal(e)
	}
	if h.Reply("clark", r.ID, Response{Status: 200}) {
		t.Fatal("another worker answered")
	}
	if !h.Reply("sam", r.ID, Response{Status: 200, Body: []byte("playlist")}) {
		t.Fatal("response not delivered")
	}
	v := <-done
	if string(v.Body) != "playlist" {
		t.Fatal(v)
	}
	if h.Reply("sam", r.ID, Response{Status: 200}) {
		t.Fatal("reply after request finished")
	}
	cancelled, c := context.WithCancel(context.Background())
	c()
	if _, e = h.Fetch(cancelled, "sam", Request{}); e == nil {
		t.Fatal("cancellation ignored")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) != 0 {
		t.Fatal("queue slots leaked")
	}
}

func TestMediaNamesCannotReadSourceOrEscape(t *testing.T) {
	for _, name := range []string{"../file.mp4", "a/b.mp4", "a\\b.mp4", "source.json", "http://elsewhere/a.mp4", ".env", "index.m3u8?secret"} {
		if ValidFile(name) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"index.m3u8", "init_00000.mp4", "seg_00000.m4s"} {
		if !ValidFile(name) {
			t.Errorf("rejected %q", name)
		}
	}
}
