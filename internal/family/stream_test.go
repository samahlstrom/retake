package family

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/probe"
)

// probeFiles gives every .mp4 10 s and nothing else a duration (so a streamed version is only listed by its record).
type probeFiles struct{}

func (probeFiles) Probe(p string) probe.Info {
	if filepath.Ext(p) != ".mp4" {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d := 10.0
	return probe.Info{Duration: &d, FPS: 30}
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadPlaylist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.m3u8")
	put(t, p, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MAP:URI=\"init_00000.mp4\"\n"+
		"#EXTINF:4.000000,\nseg_00000.m4s\n# unit 1\n#EXTINF:2.5,title\nseg_00001.m4s\n# unit 2\n")
	pl, err := ReadPlaylist(p)
	if err != nil || pl.Segments != 2 || pl.Seconds != 6.5 || pl.Units != 2 || pl.Ended {
		t.Fatalf("growing = %+v %v", pl, err)
	}
	put(t, p, "#EXTM3U\n#EXTINF:4,\r\nseg_00000.ts\r\n#EXT-X-ENDLIST\n")
	if pl, _ := ReadPlaylist(p); pl.Segments != 1 || !pl.Ended || pl.Units != 0 {
		t.Fatalf("ended = %+v", pl)
	}
	if _, err := ReadPlaylist(filepath.Join(t.TempDir(), "missing.m3u8")); err == nil {
		t.Fatal("a missing playlist is an error")
	}
}

// A streamed version is in the family in every state, unprobed, with what its record expects, its state and its
// poster's URL.
func TestStreamedVersionInTheFamily(t *testing.T) {
	root := t.TempDir()
	v6 := filepath.Join(root, "v6", "clip_v6.mp4")
	put(t, v6, "six")
	pl := filepath.Join(root, "v7", "clip_v7.m3u8")
	rec := filepath.Join(root, "v7", "clip_v7.push.json")
	header := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n"
	record := `{"version": "v7", "stream": {"state": "pending", "playlist": "clip_v7.m3u8", "poster": "clip_v7.poster.jpg",
		"doc": "clip_v7.timeline.json", "duration": 182.4, "fps": 60, "size": [1920, 1080], "total": 50}}`
	v7 := func() Version {
		t.Helper()
		fam := Detect(v6, nil, probeFiles{})
		if len(fam) != 2 {
			t.Fatalf("family = %v", summary(fam))
		}
		return fam[1]
	}

	// Pending, nothing rendered yet: listed with its poster.
	put(t, pl, header)
	put(t, rec, record)
	v := v7()
	if v.Label != "v7" || v.Name != "clip_v7.m3u8" || v.Duration == nil || *v.Duration != 182.4 || v.FPS != 60 ||
		v.Width == nil || *v.Width != 1920 || *v.Height != 1080 || v.Poster != "video/v7/clip_v7.poster.jpg" ||
		v.Stream == nil || *v.Stream != (StreamState{State: StatePending, Total: 50}) {
		t.Fatalf("pending = %+v (stream %+v)", v, v.Stream)
	}

	// Rendering: a render holds the lock; done and seconds come from the playlist.
	put(t, pl, header+"#EXTINF:4.000000,\nseg_00000.m4s\n#EXTINF:2.533333,\nseg_00001.m4s\n# unit 2\n")
	unlock, err := LockRender(pl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockRender(pl); err == nil {
		t.Fatal("one render at a time")
	}
	if s := v7().Stream; *s != (StreamState{State: StateRendering, Done: 2, Total: 50, Seconds: 6.533}) {
		t.Fatalf("rendering = %+v", s)
	}
	// The opened file can be the streamed one too.
	if f := Detect(pl, nil, probeFiles{}); len(f) != 2 || !f[1].Current || f[1].Stream == nil {
		t.Fatalf("opened = %+v", f)
	}

	// Stopped (nobody holds the lock, whatever the record says): pending, with what it has.
	unlock()
	if RenderLive(pl) || exists(RenderLockPath(pl)) {
		t.Fatal("unlock releases and removes the lock")
	}
	if s := v7().Stream; *s != (StreamState{State: StatePending, Done: 2, Total: 50, Seconds: 6.533}) {
		t.Fatalf("stopped = %+v", s)
	}

	// Complete: the playlist ended.
	put(t, pl, header+"#EXTINF:4.000000,\nseg_00000.m4s\n# unit 50\n#EXT-X-ENDLIST\n")
	if s := v7().Stream; s.State != StateComplete || s.Done != 50 {
		t.Fatalf("complete = %+v", s)
	}

	// No stream in the record (or no record): an ordinary playlist, probed (this prober can't, so it's left out).
	put(t, rec, `{"version": "v7"}`)
	if f := Detect(v6, nil, probeFiles{}); len(f) != 1 {
		t.Fatalf("no stream record: %v", summary(f))
	}
	_ = os.Remove(rec)
	if f := Detect(v6, nil, probeFiles{}); len(f) != 1 {
		t.Fatalf("no record: %v", summary(f))
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
