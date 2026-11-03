package download

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// The fixtures are generated (internal/testmedia): 4 s of testsrc2 at 160x90 and a 440 Hz tone, in the container and
// codecs each test needs. x264 only: tests never touch a GPU.

var (
	h264High  = testmedia.H264High
	bt709Tags = testmedia.BT709
	aacStereo = testmedia.AACStereo
)

func fixture(t testing.TB, path string, video []string, gainDB float64, audio []string) {
	t.Helper()
	testmedia.Clip(t, path, gainDB, append(append([]string{}, video...), audio...)...)
}

func atTarget(t testing.TB, path string) { testmedia.YouTube(t, path) }

func youTubeReady(t testing.TB, path string) { testmedia.YouTubeReady(t, path) }

func manager() *Manager {
	return &Manager{Getenv: func(k string) string {
		if k == "RETAKE_ENCODER" {
			return "x264"
		}
		return ""
	}}
}

func wait(t testing.TB, m *Manager, s Source) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return m.Wait(ctx, s)
}

func TestAReadyMP4IsServedAsItIs(t *testing.T) {
	root := t.TempDir()
	clip := filepath.Join(root, "clip_v1.mp4")
	atTarget(t, clip)
	m := manager()
	s := Source{Root: root, Path: clip}
	if st := m.Status(context.Background(), s); st.State != StateNone {
		t.Fatalf("before: %+v", st)
	}
	st := wait(t, m, s)
	fi, _ := os.Stat(clip)
	if st.State != StateReady || !st.AsIs || st.File != clip || st.Size != fi.Size() {
		t.Fatalf("ready: %+v", st)
	}
	if made, _ := filepath.Glob(filepath.Join(Dir(root), "*.mp4")); len(made) != 0 {
		t.Fatalf("made a copy: %v", made)
	}
	// The check is kept: a new Manager (a restart) knows at once.
	if st := manager().Status(context.Background(), s); st.State != StateReady || !st.AsIs {
		t.Fatalf("after a restart: %+v", st)
	}
}

func TestOtherFilesAreMadeOnceAndReused(t *testing.T) {
	root := t.TempDir()
	cases := []struct{ name, why string }{
		{"loud.mkv", "an MKV, untagged H.264, a tone 10 LU too loud: video copied and tagged, audio levelled"},
		{"quiet.mp4", "an MP4 at the end of its file, 12 LU too quiet"},
		{"mpeg4.mov", "a QuickTime movie with MPEG-4 Part 2 video and 44.1 kHz mono: everything encoded"},
	}
	fixture(t, filepath.Join(root, "loud.mkv"), h264High, -4, aacStereo)
	fixture(t, filepath.Join(root, "quiet.mp4"), append(append([]string{}, h264High...), bt709Tags...), -26, aacStereo)
	fixture(t, filepath.Join(root, "mpeg4.mov"), []string{"-c:v", "mpeg4", "-q:v", "3"}, -18,
		[]string{"-c:a", "aac", "-ar", "44100", "-ac", "1"})
	m := manager()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(root, c.name)
			s := Source{Root: root, Path: src}
			st := wait(t, m, s)
			if st.State != StateReady || st.AsIs || filepath.Dir(st.File) != Dir(root) {
				t.Fatalf("%s: %+v", c.why, st)
			}
			youTubeReady(t, st.File)
			made, _ := os.Stat(st.File)
			// Again: the same file, not made again.
			again := m.Start(context.Background(), s)
			after, _ := os.Stat(st.File)
			if again.State != StateReady || again.File != st.File || !after.ModTime().Equal(made.ModTime()) {
				t.Fatalf("not reused: %+v", again)
			}
		})
	}
	// The video was copied where it could be: same packets, tagged.
	var rec record
	loud, _ := os.Stat(filepath.Join(root, "loud.mkv"))
	b, err := os.ReadFile(filepath.Join(Dir(root), keyOf(filepath.Join(root, "loud.mkv"), loud, false)+".json"))
	if err != nil || json.Unmarshal(b, &rec) != nil {
		t.Fatal(err)
	}
	if rec.AsIs || !rec.Check.Plan.CopyVideo || !rec.Check.Plan.TagVideo || rec.Check.Plan.CopyAudio || !rec.Check.Plan.Level {
		t.Fatalf("loud.mkv's plan: %+v", rec.Check.Plan)
	}
}

func TestAChangedFileIsANewDownload(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "clip_v2.mkv")
	fixture(t, src, h264High, -6, aacStereo)
	m := manager()
	s := Source{Root: root, Path: src}
	first := wait(t, m, s)
	if first.State != StateReady {
		t.Fatal(first)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(context.Background(), s); st.State != StateNone {
		t.Fatalf("a changed file still has the old download: %+v", st)
	}
	second := wait(t, m, s)
	if second.State != StateReady || second.File == first.File {
		t.Fatalf("second: %+v (first %s)", second, first.File)
	}
}

func TestFailuresAreOneSentenceAndTriedAgain(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "clip_v3.mp4")
	testmedia.Garbage(t, src)
	m := manager()
	s := Source{Root: root, Path: src}
	st := wait(t, m, s)
	if st.State != StateFailed || !strings.HasPrefix(st.Error, "Couldn't make the MP4: ") || strings.Contains(st.Error, "\n") {
		t.Fatalf("garbage: %+v", st)
	}
	fixture(t, src, h264High, -6, aacStereo)
	if st := wait(t, m, s); st.State != StateReady {
		t.Fatalf("tried again: %+v", st)
	}
	if st := m.Status(context.Background(), Source{Root: root, Path: src, Unavailable: "v8 is still rendering"}); st.State != StateUnavailable ||
		st.Error != "v8 is still rendering" {
		t.Fatalf("unavailable: %+v", st)
	}
}

func TestTheTimelinesOwnLevelIsKept(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "clip_v4.mp4")
	fixture(t, src, append(append([]string{}, h264High...), bt709Tags...), -26,
		append(append([]string{}, aacStereo...), "-movflags", "+faststart"))
	tl := filepath.Join(root, "edit", "timeline.json")
	if err := os.MkdirAll(filepath.Dir(tl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tl, []byte(`{"master": {"loudness": -26}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := jsonx.Float(4)
	s := ForVersion(root, family.Version{Label: "v4", Path: src, Duration: &d}, bridge.Editor{})
	if !s.Keep {
		t.Fatal("the master's loudness isn't seen")
	}
	if st := wait(t, manager(), s); st.State != StateReady || !st.AsIs {
		t.Fatalf("a -26 LUFS master is a download as it is: %+v", st)
	}
}

// A rendered version downloads the renderer's export: packaged with the export's level when it is missing, reused
// when it passes, packaged again when an older one misses the level.
func TestARenderedVersionDownloadsItsExport(t *testing.T) {
	testmedia.NeedShell(t)
	root := t.TempDir()
	dir := filepath.Join(root, "v8")
	ready := filepath.Join(root, "ready.mp4")
	atTarget(t, ready)
	bin := filepath.Join(t.TempDir(), "retake-edit")
	// The fake renderer copies the ready file to -o, writes a manifest beside it, and keeps its arguments.
	script := `#!/bin/sh
echo "$@" >> "$ARGS"
out=""
prev=""
for a in "$@"; do [ "$prev" = "-o" ] && out="$a"; prev="$a"; done
cp "$READY" "$out"
echo '{}' > "${out%.mp4}.manifest.json"
echo '{"ok": true, "units": 1, "reused": 1, "rendered": 0}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	ed := bridge.Editor{Configured: bin, Env: []string{"ARGS=" + argsFile, "READY=" + ready}}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(dir, "clip_v8.export.mp4")
	s := Source{Root: root, Path: filepath.Join(dir, "clip_v8.m3u8"), Render: &Render{Editor: ed,
		Timeline: filepath.Join(root, "edit", "timeline.json"), Doc: filepath.Join(dir, "clip_v8.timeline.json"),
		Export: export}}
	m := manager()
	st := wait(t, m, s)
	if st.State != StateReady || st.File != export || st.AsIs {
		t.Fatalf("packaged: %+v", st)
	}
	youTubeReady(t, export)
	if _, err := os.Stat(filepath.Join(dir, "clip_v8.export.manifest.json")); err != nil {
		t.Fatal("the export's manifest isn't moved in beside it")
	}
	b, _ := os.ReadFile(argsFile)
	got := strings.Fields(string(b))
	if len(got) != 8 || strings.Join(got[:3], " ") != "render "+s.Render.Timeline+" -o" ||
		!strings.HasPrefix(got[3], filepath.Join(dir, ".download-")) || filepath.Base(got[3]) != "clip_v8.export.mp4" ||
		strings.Join(got[4:], " ") != "--export-loudness=-14 --json --doc "+s.Render.Doc {
		t.Fatalf("renderer args: %s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".download-*")); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	// Reused: nothing runs again.
	_ = os.Remove(argsFile)
	if again := wait(t, manager(), s); again.State != StateReady {
		t.Fatalf("reused: %+v", again)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Fatal("the export was packaged again")
	}
	// An export made before the loudness rule (too loud) is packaged again.
	loud := filepath.Join(root, "loud.mp4")
	fixture(t, loud, append(append([]string{}, h264High...), bt709Tags...), -3,
		append(append([]string{}, aacStereo...), "-movflags", "+faststart"))
	b, _ = os.ReadFile(loud)
	if err := os.WriteFile(export, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if st := wait(t, manager(), s); st.State != StateReady {
		t.Fatalf("again: %+v", st)
	}
	if _, err := os.Stat(argsFile); err != nil {
		t.Fatal("a loud export wasn't packaged again")
	}
	youTubeReady(t, export)
}

func TestLicenseLine(t *testing.T) {
	for _, c := range []struct {
		review, blocked int
		want            string
	}{
		{0, 0, ""},
		{1, 0, "Has 1 asset marked review only"},
		{2, 0, "Has 2 assets marked review only"},
		{0, 1, "Has 1 asset marked blocked"},
		{2, 1, "Has 3 assets marked review only or blocked"},
	} {
		if got := LicenseLine(c.review, c.blocked); got != c.want {
			t.Errorf("%d, %d: %q, want %q", c.review, c.blocked, got, c.want)
		}
	}
}

func TestFastStartReadsTheBoxes(t *testing.T) {
	dir := t.TempDir()
	box := func(kind string, n int) []byte {
		b := make([]byte, 8+n)
		b[3] = byte(8 + n)
		copy(b[4:], kind)
		return b
	}
	write := func(name string, boxes ...[]byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, bytes.Join(boxes, nil), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if !fastStart(write("a.mp4", box("ftyp", 8), box("moov", 4), box("mdat", 16))) {
		t.Error("moov first")
	}
	if fastStart(write("b.mp4", box("ftyp", 8), box("mdat", 16), box("moov", 4))) {
		t.Error("mdat first")
	}
	if fastStart(write("c.mp4", []byte("not a box"))) {
		t.Error("garbage")
	}
}
