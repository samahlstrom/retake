package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// slowEditor stands in for retake-edit's streamed render: it resumes after the playlist's last unit mark and
// publishes a unit every 0.1 s, up to 30.
const slowEditor = `#!/bin/sh
shift 2
while [ $# -gt 0 ]; do [ "$1" = --hls ] && pl="$2"; shift; done
dir=$(dirname "$pl")
start=$(sed -n 's/^# unit //p' "$pl" | tail -1); i=${start:-0}
while [ $i -lt 30 ]; do
  i=$((i+1)); sleep 0.1
  printf seg > "$dir/seg_$i.m4s"
  cp "$pl" "$pl.tmp"; printf '#EXTINF:2.000000,\nseg_%s.m4s\n# unit %s\n' $i $i >> "$pl.tmp"; mv "$pl.tmp" "$pl"
  echo '{"progress": "publish", "done": '$i', "total": 30, "segments": '$i', "seconds": '$((i*2))', "elapsed": 0.1}' >&2
done
echo '{"ok": true}'
`

// A streamed version (spec/bridge.md, "Streaming"): pending in /api/info with its poster; watching it renders it,
// which the page hears about; unwatching stops it.
func TestStreamedVersionWatch(t *testing.T) {
	testmedia.NeedShell(t)
	root := t.TempDir()
	v6 := filepath.Join(root, "v6", "clip_v6.mp4")
	writeFile(t, v6, "six")
	writeFile(t, filepath.Join(root, "edit", "timeline.json"), `{"rev": 2}`)
	pl := filepath.Join(root, "v7", "clip_v7.m3u8")
	writeFile(t, pl, "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n")
	writeFile(t, filepath.Join(root, "v7", "clip_v7.poster.jpg"), "jpeg")
	writeFile(t, filepath.Join(root, "v7", "clip_v7.timeline.json"), `{"rev": 2}`)
	if err := bridge.WritePushRecord(pl, bridge.PushRecord{Version: "v7", From: "v6", Timeline: "edit/timeline.json",
		Stream: &family.StreamRecord{State: family.StatePending, Playlist: "clip_v7.m3u8", Poster: "clip_v7.poster.jpg",
			Doc: "clip_v7.timeline.json", Duration: 60, FPS: 25, Size: []int{64, 36}, Total: 30}}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "tools", "retake-edit")
	writeFile(t, bin, slowEditor)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Video: v6, Prober: fakeProber{}, Editor: bridge.Editor{Configured: bin}})

	type ver struct {
		Label    string              `json:"label"`
		Duration float64             `json:"duration"`
		Poster   string              `json:"poster"`
		Stream   *family.StreamState `json:"stream"`
	}
	info := func() ver {
		t.Helper()
		var i struct {
			Versions []ver `json:"versions"`
		}
		r := h.get("/api/info", nil)
		if err := json.Unmarshal(r.body, &i); err != nil || len(i.Versions) != 2 {
			t.Fatalf("info: %s", r.body)
		}
		if i.Versions[0].Stream != nil || i.Versions[0].Poster != "" {
			t.Fatalf("an MP4 version has neither field: %+v", i.Versions[0])
		}
		return i.Versions[1]
	}
	v := info()
	if v.Label != "v7" || v.Duration != 60 || v.Poster != "video/v7/clip_v7.poster.jpg" || v.Stream == nil ||
		*v.Stream != (family.StreamState{State: "pending", Total: 30}) {
		t.Fatalf("pending: %+v", v)
	}
	if r := h.get("/"+v.Poster, nil); r.code != 200 || string(r.body) != "jpeg" {
		t.Fatalf("poster: %d", r.code)
	}

	// Bad requests: no viewer, a version that isn't streamed, an unknown one.
	for path, code := range map[string]int{
		"/api/versions/v7/watch":           400,
		"/api/versions/v6/watch?viewer=a":  400,
		"/api/versions/v9/watch?viewer=a":  404,
		"/api/versions/v7/frob?viewer=a":   404,
		"/api/versions/v7/unwatch?viewer=": 400,
	} {
		if r := h.post(path, "", nil); r.code != code {
			t.Errorf("%s: %d %s, want %d", path, r.code, r.body, code)
		}
	}

	// Watched: it renders, and says so.
	r := h.post("/api/versions/v7/watch?viewer=page1", "", nil)
	var w struct {
		Label  string             `json:"label"`
		Stream family.StreamState `json:"stream"`
	}
	if err := json.Unmarshal(r.body, &w); r.code != 200 || err != nil || w.Label != "v7" {
		t.Fatalf("watch: %d %s", r.code, r.body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for v = info(); v.Stream.Done < 2; v = info() {
		if time.Now().After(deadline) {
			t.Fatalf("no progress: %+v", v.Stream)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v.Stream.State != "rendering" || v.Stream.Seconds < 4 {
		t.Fatalf("rendering: %+v", v.Stream)
	}
	st, err := project.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	events := func() string {
		evs, err := st.Events(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		var whys []string
		for _, e := range evs {
			var ve project.VersionEvent
			if e.Kind == project.EventVersion && json.Unmarshal(e.Data, &ve) == nil &&
				(len(whys) == 0 || whys[len(whys)-1] != ve.Why) {
				whys = append(whys, ve.Why)
			}
		}
		return strings.Join(whys, " ")
	}
	if got := events(); !strings.HasPrefix(got, "rendering progress") {
		t.Fatalf("events: %q", got)
	}

	// Unwatched (the page's beacon): it stops, pending, keeping what it rendered.
	if r := h.post("/api/versions/v7/unwatch?viewer=page1", "x", map[string]string{"Content-Type": "text/plain"}); r.code != 200 ||
		!bytes.Contains(r.body, []byte(`"label":"v7"`)) {
		t.Fatalf("unwatch: %d %s", r.code, r.body)
	}
	deadline = time.Now().Add(10 * time.Second)
	for v = info(); v.Stream.State != "pending"; v = info() {
		if time.Now().After(deadline) {
			t.Fatalf("still rendering: %+v", v.Stream)
		}
		time.Sleep(20 * time.Millisecond)
	}
	done := v.Stream.Done
	time.Sleep(300 * time.Millisecond)
	if v = info(); v.Stream.Done != done || done == 0 || done == 30 {
		t.Fatalf("stopped at %d, now %+v", done, v.Stream)
	}
	if got := events(); !strings.HasSuffix(got, "stopped") {
		t.Fatalf("events: %q", got)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
