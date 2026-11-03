package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/thumb"
)

// TestRelayedVersionsHavePosters: a relayed version's first frame is made from the start of its media, asked from the
// computer that has it (spec/home.md, "Thumbnails and the poster"): a version file's two ends, a playlist's first
// segment with its init segment, and a live revision of the agent's (a draft render), which falls back to the
// newest version before it while it has no segment yet.
func TestRelayedVersionsHavePosters(t *testing.T) {
	var runs atomic.Int32
	read := map[string][]byte{} // what each frame was made from, by the frame's body
	maker := &thumb.Maker{Run: func(_ context.Context, video, out string) error {
		runs.Add(1)
		b, err := os.ReadFile(video)
		if err != nil {
			return err
		}
		frame := "\xff\xd8\xff" + filepath.Base(video) + ":" + strconv.Itoa(len(b))
		read[frame] = b
		return os.WriteFile(out, []byte(frame), 0o600)
	}}
	h, w, headers := relayFixture(t, 1<<30, func(o *Options) { o.Thumbs, o.FollowNewest = maker, true })
	big := w.files["v1/clip_v1.mp4"]

	// A version file: its first and last chunks, in place, so the MP4's index is there whichever end it's at.
	r := h.get("/api/poster?v=v1", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("v1: %d %s", r.code, r.body)
	}
	got := read[string(r.body)]
	if len(got) != len(big) || !bytes.Equal(got[:relay.Chunk], big[:relay.Chunk]) ||
		!bytes.Equal(got[2*relay.Chunk:], big[2*relay.Chunk:]) ||
		!bytes.Equal(got[relay.Chunk:2*relay.Chunk], make([]byte, relay.Chunk)) {
		t.Fatalf("v1 was made from %d bytes, not the file's two ends", len(got))
	}
	// A playlist: its first segment.
	if r = h.get("/api/poster?v=v2", nil); r.code != 200 || string(r.body) != "\xff\xd8\xfffirst.m4s:5000" ||
		!bytes.Equal(read[string(r.body)], w.files["v2/seg_000.m4s"]) {
		t.Fatalf("v2: %d %q", r.code, r.body)
	}
	v2Frame := string(r.body)

	// The agent's live revision (a draft render) with nothing rendered yet: the newest version before it stands in.
	source := strings.Repeat("a", 64)
	render := `{"id":"rev1","worker":"machine","source":"` + source + `","duration":2,"fps":30,"width":64,"height":36,"done":0,"total":2}`
	r = h.post("/api/worker/render", render, headers)
	if r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	var rev project.LiveRender
	_ = json.Unmarshal(r.body, &rev)
	if !rev.Draft || !strings.Contains(rev.Label, "-draft") {
		t.Fatalf("the render isn't a draft: %+v", rev)
	}
	w.files["rev1/index.m3u8"] = []byte("#EXTM3U\n#EXT-X-VERSION:7\n")
	before := runs.Load()
	for _, q := range []string{"?v=" + rev.Label, ""} { // by its label, and as the current version (the home's card)
		if r = h.get("/api/poster"+q, nil); r.code != 200 || string(r.body) != v2Frame {
			t.Fatalf("no segment yet (%q): %d %q", q, r.code, r.body)
		}
	}
	if runs.Load() != before {
		t.Fatal("ffmpeg ran with nothing to read")
	}
	// Its first segment is out: its own frame, from the init segment and that segment together.
	w.files["rev1/index.m3u8"] = []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init_00000.mp4\"\n#EXTINF:1,\nseg_00000.m4s\n")
	w.files["rev1/init_00000.mp4"] = []byte("init")
	w.files["rev1/seg_00000.m4s"] = pattern(3000)
	if r = h.get("/api/poster?v="+rev.Label, nil); r.code != 200 || string(r.body) != "\xff\xd8\xfffirst.mp4:3004" ||
		!bytes.Equal(read[string(r.body)], append([]byte("init"), pattern(3000)...)) {
		t.Fatalf("the revision's own frame: %d %q", r.code, r.body)
	}
	// Made once: asked again, the worker isn't, and nothing is made.
	asked, made := w.count(), runs.Load()
	for _, q := range []string{"?v=" + rev.Label, "?v=v1", "?v=v2"} {
		if r = h.get("/api/poster"+q, nil); r.code != 200 {
			t.Fatal(q, r.code)
		}
	}
	if w.count() != asked || runs.Load() != made {
		t.Errorf("cached posters asked the worker %d more times and ran ffmpeg %d more", w.count()-asked, runs.Load()-made)
	}
	if entries, err := os.ReadDir(thumb.Dir(h.srv.root)); err != nil || len(entries) != 3 {
		t.Errorf("cache: %v %v", entries, err)
	}
}

func TestFirstMedia(t *testing.T) {
	for _, c := range []struct{ pl, init, seg string }{
		{"#EXTM3U\n#EXTINF:4,\nseg_000.ts\n", "", "seg_000.ts"},
		{"#EXTM3U\n#EXT-X-MAP:URI=\"init_00000.mp4\"\n#EXTINF:4.0,\r\nseg_00000.m4s\n#EXT-X-MAP:URI=\"init_00001.mp4\"\nseg_00001.m4s\n", "init_00000.mp4", "seg_00000.m4s"},
		{"#EXTM3U\n#EXT-X-MAP:BYTERANGE=\"1@0\",URI=\"i.mp4\"\nseg.m4s\n", "i.mp4", "seg.m4s"},
		{"#EXTM3U\n#EXT-X-VERSION:7\n", "", ""},
		{"#EXTM3U\n../secret.m4s\n", "", ""},
		{"#EXTM3U\nhttps://elsewhere/seg.ts\n", "", ""},
		{"#EXTM3U\n#EXT-X-MAP:URI=\"../init.mp4\"\nseg.m4s\n", "", ""},
	} {
		if i, s := firstMedia(c.pl); i != c.init || s != c.seg {
			t.Errorf("%q: %q %q, want %q %q", c.pl, i, s, c.init, c.seg)
		}
	}
}
