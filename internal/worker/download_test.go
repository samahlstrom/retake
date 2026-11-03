package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/download"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/testmedia"
)

func downloadState(t *testing.T, m media) download.Status {
	t.Helper()
	if m.Status != 200 {
		t.Fatalf("status %d", m.Status)
	}
	var st download.Status
	if err := json.Unmarshal([]byte(body(t, m)), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// The relay's download request: the worker gets its version's download ready itself and reads it in chunks.
func TestWorkerDownloads(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".retake", "worker")
	clip := filepath.Join(root, "v1", "clip_v1.mkv")
	testmedia.Clip(t, clip, -4, append(append([]string{}, testmedia.H264High...), testmedia.AACStereo...)...)
	r := &runner{opts: Options{Root: root, Prober: fakeProber{}, Editor: bridge.Editor{Env: []string{"RETAKE_ENCODER=x264"}}},
		dir: dir}
	ctx := context.Background()
	if st := downloadState(t, r.readMedia(ctx, relay.Request{Op: "download", Version: "v1"})); st.State != download.StateNone {
		t.Fatalf("before: %+v", st)
	}
	if m := r.readMedia(ctx, relay.Request{Version: "v1", Download: true}); m.Status != 404 {
		t.Fatal("read before it's ready", m.Status)
	}
	st := downloadState(t, r.readMedia(ctx, relay.Request{Op: "download", Version: "v1", Start: true}))
	for deadline := time.Now().Add(time.Minute); st.State == download.StateChecking || st.State == download.StateMaking; {
		if time.Now().After(deadline) {
			t.Fatal("never ready")
		}
		time.Sleep(50 * time.Millisecond)
		st = downloadState(t, r.readMedia(ctx, relay.Request{Op: "download", Version: "v1"}))
	}
	if st.State != download.StateReady || st.Size == 0 || st.Modified == 0 || st.File != "" {
		t.Fatalf("ready: %+v", st)
	}
	m := r.readMedia(ctx, relay.Request{Version: "v1", Download: true, Range: relay.ChunkRange(0)})
	got := body(t, m)
	if m.Status != 200 && m.Status != 206 || int64(len(got)) != st.Size || m.Modified != st.Modified {
		t.Fatalf("read: %d, %d bytes of %d", m.Status, len(got), st.Size)
	}
	out := filepath.Join(t.TempDir(), "clip_v1.mp4")
	if err := os.WriteFile(out, []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}
	testmedia.YouTubeReady(t, out)

	// A render that's still rendering has nothing to download; one that isn't this worker's isn't there.
	render := filepath.Join(dir, "renders", "rendering1")
	if err := os.MkdirAll(render, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(render, "state.json"), project.LiveRender{ID: "rendering1", Label: "v3"}); err != nil {
		t.Fatal(err)
	}
	if st := downloadState(t, r.readMedia(ctx, relay.Request{Op: "download", Render: "rendering1", Start: true})); st.State != download.StateUnavailable || st.Error != "v3 is still rendering" {
		t.Fatalf("rendering: %+v", st)
	}
	for _, req := range []relay.Request{{Op: "download", Render: "../rendering1"}, {Op: "download", Render: "nope"},
		{Op: "download", Version: "v9"}, {Op: "download"}, {Download: true, Render: "rendering1"}} {
		if m := r.readMedia(ctx, req); m.Status != 404 {
			body(t, m)
			t.Errorf("%+v: %d", req, m.Status)
		}
	}
}
