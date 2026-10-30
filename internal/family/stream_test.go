package family

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/probe"
)

// probeFiles gives every .mp4 10 s and nothing else a duration (a playlist still being written doesn't probe).
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
	put(t, p, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n# a comment\n#EXTINF:4.000000,\nseg_00000.ts\n"+
		"#EXTINF:2.5,title\nseg_00001.ts\n")
	pl, err := ReadPlaylist(p)
	if err != nil || pl.Segments != 2 || pl.Seconds != 6.5 || pl.Ended {
		t.Fatalf("growing = %+v %v", pl, err)
	}
	put(t, p, "#EXTM3U\n#EXTINF:4,\r\nseg_00000.ts\r\n#EXT-X-ENDLIST\n")
	if pl, _ := ReadPlaylist(p); pl.Segments != 1 || !pl.Ended {
		t.Fatalf("ended = %+v", pl)
	}
	if _, err := ReadPlaylist(filepath.Join(t.TempDir(), "missing.m3u8")); err == nil {
		t.Fatal("a missing playlist is an error")
	}
}

// A streamed render is in the family while it renders: once it has a segment, while its push is alive, with the
// expected duration, fps and size from its record and its progress. It isn't probed.
func TestStreamingVersionInTheFamily(t *testing.T) {
	root := t.TempDir()
	v6 := filepath.Join(root, "v6", "clip_v6.mp4")
	put(t, v6, "six")
	pl := filepath.Join(root, "v7", "clip_v7.m3u8")
	rec := filepath.Join(root, "v7", "clip_v7.push.json")
	labels := func() []got { return summary(Detect(v6, nil, probeFiles{})) }
	header := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n"

	put(t, pl, header)
	put(t, rec, `{"version": "v7", "rendering": {"playlist": "clip_v7.m3u8", "duration": 182.4, "fps": 60,
		"size": [1920, 1080], "done": 0, "total": 50}}`)
	unlock, err := LockRender(pl)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := LockRender(pl); err == nil {
		t.Fatal("one push renders a version at a time")
	}
	if g := labels(); len(g) != 1 {
		t.Fatalf("no segment yet: not listed: %v", g)
	}

	put(t, pl, header+"#EXTINF:4.000000,\nseg_00000.ts\n#EXTINF:2.533333,\nseg_00001.ts\n")
	put(t, rec, `{"version": "v7", "rendering": {"playlist": "clip_v7.m3u8", "duration": 182.4, "fps": 60,
		"size": [1920, 1080], "done": 2, "total": 50}}`)
	fam := Detect(v6, nil, probeFiles{})
	if len(fam) != 2 {
		t.Fatalf("family = %v", summary(fam))
	}
	v := fam[1]
	if v.Label != "v7" || v.Name != "clip_v7.m3u8" || !v.Rendering || v.Duration == nil || *v.Duration != 182.4 ||
		v.FPS != 60 || v.Width == nil || *v.Width != 1920 || *v.Height != 1080 || v.Progress == nil ||
		v.Progress.Done != 2 || v.Progress.Total != 50 || v.Progress.Seconds != 6.533 {
		t.Fatalf("v7 = %+v (progress %+v)", v, v.Progress)
	}
	if fam[0].Rendering || fam[0].Progress != nil {
		t.Fatal("finished versions have neither field")
	}

	// The opened file can be the rendering one too.
	if f := Detect(pl, nil, probeFiles{}); len(f) != 2 || !f[1].Current || !f[1].Rendering {
		t.Fatalf("opened while rendering = %+v", f)
	}

	// Ended: probed like any file (this prober can't, so it's left out like any unfinished file).
	put(t, pl, header+"#EXTINF:4.000000,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
	if g := labels(); len(g) != 1 {
		t.Fatalf("ended: probed, not streaming: %v", g)
	}

	// Growing again but without a record, or with a record that isn't rendering: probed, so not listed here.
	put(t, pl, header+"#EXTINF:4.000000,\nseg_00000.ts\n")
	put(t, rec, `{"version": "v7"}`)
	if g := labels(); len(g) != 1 {
		t.Fatalf("no rendering state: %v", g)
	}
	_ = os.Remove(rec)
	if g := labels(); len(g) != 1 {
		t.Fatalf("no record: %v", g)
	}

	// The lock released: removed, and nobody is rendering it any more.
	put(t, rec, `{"version": "v7", "rendering": {"duration": 9, "done": 1, "total": 3}}`)
	if g := labels(); len(g) != 2 {
		t.Fatalf("locked: %v", g)
	}
	unlock()
	if RenderLive(pl) || exists(RenderLockPath(pl)) {
		t.Fatal("unlock releases and removes the lock")
	}
	if g := labels(); len(g) != 1 {
		t.Fatalf("a render nobody runs isn't listed: %v", g)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
