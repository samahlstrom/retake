package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/stream"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// TestPushRenderWithRetakeEdit is push --render with the real editor on the demo (editor/examples/demo) at a small
// size: v1 rendered as a draft, a note on the pop, a patch answering it, then v2 rendered and pushed. It skips without
// a built retake-edit ($RETAKE_EDIT, $PATH or editor/target/release) or ffmpeg, and with -short.
func TestPushRenderWithRetakeEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("renders the demo")
	}
	testmedia.Need(t)
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	ed := bridge.Editor{Dir: repo, Exe: filepath.Join(repo, "bin", "retake")}
	bin, err := ed.Find()
	if err != nil {
		t.Skipf("no retake-edit: %v", err)
	}
	demo := filepath.Join(repo, "editor", "examples", "demo")
	root := t.TempDir()
	edit := filepath.Join(root, "edit")
	for _, f := range []string{"timeline.json", "vo.words.json"} {
		b, err := os.ReadFile(filepath.Join(demo, f))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(edit, f), string(b))
	}
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "DEMO_SIZE=160x90")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("bash", filepath.Join(demo, "make-media.sh"), filepath.Join(edit, "media"), bin)
	tl := filepath.Join(edit, "timeline.json")
	v1 := filepath.Join(root, "v1", "demo_v1.mp4")
	run(bin, "render", tl, "-o", v1, "--draft")

	p, err := Open(v1, fakeProber{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	p.Editor = bridge.Editor{Configured: bin}
	if _, err := p.Store.Sync(project.Run{Version: "v1", Comments: []project.Comment{
		{Take: "2026-10-30_12-00-00", Seg: "S1", N: 1, T0: 7.8, T1: 8.2, Text: "make the pop red", Author: "Ana"},
	}}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(edit, "pop.json"), `{"base": 0, "answers": ["v1#1"], "summary": "Pop reads FASTER! in red",
		"ops": [{"op": "set", "id": "pop/faster/text", "path": "fill", "value": "#ff3b30"}]}`)
	run(bin, "patch", "apply", tl, filepath.Join(edit, "pop.json"))

	// It streams (the default): v2 is pending at once, a playlist in v2/ with its poster; it renders when watched,
	// and the render that completes also packages the export MP4.
	var notes []string
	res, err := p.VersionPush(context.Background(), PushArgs{Render: true, Draft: true, Export: true,
		Notify: func(s string) { notes = append(notes, s) }})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "v2", "demo_v2.m3u8")
	if res.Version.Label != "v2" || res.Version.Path != dest || len(res.Version.Changelog) != 1 ||
		res.Version.Changelog[0].Summary != "Pop reads FASTER! in red" || res.Version.Changelog[0].Patches[0] != "p1" {
		t.Fatalf("push = %+v", res.Version)
	}
	if res.Render.Units != (bridge.Units{Total: 6, Reused: 5, Rendered: 1}) {
		t.Fatalf("only the pop's unit renders again: %+v", res.Render.Units)
	}
	if s := res.Render.Stream; s == nil || s.State != family.StatePending || s.Total != 6 || s.Duration != 16.1 ||
		s.Export != "demo_v2.export.mp4" || len(notes) != 1 {
		t.Fatalf("stream = %+v, notes %q", res.Render.Stream, notes)
	}
	if st, err := os.Stat(filepath.Join(root, "v2", "demo_v2.poster.jpg")); err != nil || st.Size() < 100 {
		t.Fatalf("poster: %v", err)
	}
	m0 := stream.New(stream.Options{Editor: p.Editor})
	defer m0.Close()
	if _, err := m0.Watch(stream.Target{Label: "v2", Playlist: dest, Root: root}, "test"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for st, _ := m0.State(dest); st.State != family.StateComplete; st, _ = m0.State(dest) {
		if time.Now().After(deadline) {
			t.Fatalf("not complete: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for m0.Rendering(dest) {
		time.Sleep(10 * time.Millisecond)
	}
	pl, err := family.ReadPlaylist(dest)
	if err != nil || !pl.Ended || pl.Units != 6 || pl.Seconds < 16 {
		t.Fatalf("playlist = %+v %v", pl, err)
	}
	rec, err := bridge.ReadPushRecord(dest)
	if err != nil || rec.Stream.State != family.StateComplete || rec.Stream.Renders != 1 || rec.Stream.DoneS == nil ||
		rec.Stream.FirstS == nil || *rec.Stream.FirstS > *rec.Stream.DoneS {
		t.Fatalf("record = %+v %v", rec.Stream, err)
	}
	if st, err := os.Stat(filepath.Join(root, "v2", "demo_v2.export.mp4")); err != nil || st.Size() == 0 {
		t.Fatalf("export: %v", err)
	}
	if exists(family.RenderLockPath(dest)) {
		t.Fatal("the lock is gone")
	}
	m, err := bridge.ForVideo(dest)
	if err != nil || m.Video != "demo_v2.m3u8" || m.Timeline.Rev != 1 || len(m.Units) != 6 {
		t.Fatalf("manifest = %+v %v", m, err)
	}
	// The versions agree everywhere but the pop's unit, so the map is the identity and only u5 changed.
	a, _ := bridge.ForVideo(v1)
	tm := bridge.BuildTimeMap("v1", "v2", a, m)
	if got := tm.Map(9); got != 9 {
		t.Fatalf("map 9 -> %v", got)
	}
	if got := bridge.Carry(a, m, 7.8, false, true); got != "check" {
		t.Fatalf("the pop's note: %s", got)
	}
	if got := bridge.Carry(a, m, 1, false, false); got != "unchanged" {
		t.Fatalf("the title: %s", got)
	}
}
