package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeStreamEditor is a stand-in retake-edit for a streamed push: `plan` reports three units, `frames` draws the
// first frame with ffmpeg, and `render` only leaves a mark (a streamed push never renders). $FAKE_FAIL_PLAN fails
// the plan.
const fakeStreamEditor = `#!/bin/sh
cmd="$1"
echo "$@" >> "$FAKE_LOG"
case "$cmd" in
plan)
  if [ -n "$FAKE_FAIL_PLAN" ]; then
    echo '{"ok": false, "errors": [{"code": "asset_missing", "message": "media/clip.mp4 is missing"}]}'; exit 1
  fi
  echo '{"ok": true, "size": [64, 36], "fps": 25, "range": [0, 12.5], "hits": 1, "misses": 2}' ;;
frames)
  while [ $# -gt 0 ]; do [ "$1" = -o ] && out="$2"; shift; done
  ffmpeg -nostdin -v error -y -f lavfi -i color=c=red:s=64x36 -frames:v 1 "$out/frame-0000.000.png" || exit 1
  echo '{"ok": true, "frames": [{"t": 0, "path": "'"$out/frame-0000.000.png"'"}]}' ;;
*)
  touch "$FAKE_MARK"; exit 9 ;;
esac
`

func streamProject(t *testing.T) (p *Project, mark, log string) {
	t.Helper()
	testmedia.Need(t)
	p, mark = timelineProject(t)
	bin := filepath.Join(t.TempDir(), "retake-edit")
	if err := os.WriteFile(bin, []byte(fakeStreamEditor), 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(t.TempDir(), "editor.log")
	p.Editor = bridge.Editor{Configured: bin, Env: []string{"FAKE_MARK=" + mark, "FAKE_LOG=" + log}}
	return p, mark, log
}

func versionEvents(t *testing.T, p *Project) []project.VersionEvent {
	t.Helper()
	evs, err := p.Store.Events(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []project.VersionEvent
	for _, e := range evs {
		var v project.VersionEvent
		if e.Kind == project.EventVersion && json.Unmarshal(e.Data, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// A streamed push renders nothing but the poster: the version is pending in the family at once, recorded, with its
// snapshot, plan and poster.
func TestStreamedPushIsPendingAtOnce(t *testing.T) {
	p, mark, log := streamProject(t)
	var notes []string
	res, err := p.VersionPush(context.Background(), PushArgs{Render: true, Draft: true, Export: true,
		Changes: []project.ChangeEntry{{Note: 4, Summary: "kept"}}, Notify: func(s string) { notes = append(notes, s) }})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.Root, "v8")
	pl := filepath.Join(dir, "clip_v8.m3u8")
	if res.Version.Label != "v8" || res.Version.Path != pl || res.Version.SHA256 == "" || res.Note != "" {
		t.Fatalf("version = %+v", res)
	}
	if !slices.Equal(res.Answered, []int64{1, 2, 3, 4}) {
		t.Fatalf("the push is recorded at once: answered = %v", res.Answered)
	}
	if exists(mark) {
		t.Fatal("nothing renders at push")
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "plan ") || !strings.Contains(string(b), "frames ") ||
		!strings.Contains(string(b), "--doc "+filepath.Join(dir, "clip_v8.timeline.json")) ||
		!strings.Contains(string(b), "--draft") {
		t.Fatalf("editor calls:\n%s", b)
	}

	// The snapshot is the timeline as pushed; the poster a JPEG; the playlist a header without segments.
	tl, _ := os.ReadFile(filepath.Join(p.Root, "edit", "timeline.json"))
	if snap, _ := os.ReadFile(filepath.Join(dir, "clip_v8.timeline.json")); !bytes.Equal(snap, tl) {
		t.Fatalf("snapshot = %s", snap)
	}
	if jpg, _ := os.ReadFile(filepath.Join(dir, "clip_v8.poster.jpg")); !bytes.HasPrefix(jpg, []byte{0xFF, 0xD8}) {
		t.Fatal("the poster is a JPEG")
	}
	if pl, err := family.ReadPlaylist(pl); err != nil || pl.Segments != 0 || pl.Ended {
		t.Fatalf("playlist = %+v %v", pl, err)
	}
	rec, err := bridge.ReadPushRecord(pl)
	if err != nil || rec.Stream == nil || rec.Version != "v8" || rec.Rev != 3 || len(rec.Patches) != 2 {
		t.Fatalf("record = %+v %v", rec, err)
	}
	s := rec.Stream
	if s.State != family.StatePending || s.Playlist != "clip_v8.m3u8" || s.Poster != "clip_v8.poster.jpg" ||
		s.Doc != "clip_v8.timeline.json" || s.Duration != 12.5 || s.FPS != 25 || s.Total != 3 ||
		s.Export != "clip_v8.export.mp4" || s.Renders != 0 || rec.Units != (bridge.Units{Total: 3, Reused: 1, Rendered: 2}) {
		t.Fatalf("stream = %+v, units %+v", s, rec.Units)
	}
	if res.Render == nil || res.Render.Stream == nil || res.Render.Stream.State != family.StatePending {
		t.Fatalf("result render = %+v", res.Render)
	}

	// The family lists it, pending, with its poster; the page hears the push.
	fam := p.Family()
	v8 := fam[len(fam)-1]
	if v8.Label != "v8" || v8.Stream == nil || v8.Stream.State != family.StatePending || v8.Stream.Total != 3 ||
		v8.Poster != "video/v8/clip_v8.poster.jpg" || *v8.Duration != 12.5 {
		t.Fatalf("family = %+v (stream %+v)", v8, v8.Stream)
	}
	if evs := versionEvents(t, p); len(evs) != 1 || evs[0].Why != "push" || evs[0].Label != "v8" {
		t.Fatalf("events = %+v", evs)
	}
	vl, _ := p.VersionsList(context.Background())
	if last := vl.Versions[len(vl.Versions)-1]; last.Label != "v8" || last.Stream == nil || last.Render == nil ||
		last.Render.Stream == nil {
		t.Fatalf("versions_list = %+v", last)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "v8 streams when someone opens it: 3 units (1 cached)") {
		t.Fatalf("notes = %q", notes)
	}
}

func TestStreamedPushFailures(t *testing.T) {
	ctx := context.Background()
	all := []project.ChangeEntry{{Note: 4, Summary: "kept"}}

	// The plan fails: nothing stays, nothing is recorded.
	p, _, _ := streamProject(t)
	p.Editor.Env = append(p.Editor.Env, "FAKE_FAIL_PLAN=1")
	_, err := p.VersionPush(ctx, PushArgs{Render: true, Changes: all})
	if code(err) != CodeFailed || !strings.Contains(err.Error(), "media/clip.mp4 is missing") {
		t.Fatalf("failed plan: %v", err)
	}
	if exists(filepath.Join(p.Root, "v8")) {
		left, _ := os.ReadDir(filepath.Join(p.Root, "v8"))
		t.Fatalf("a failed push left v8/: %v", left)
	}
	if rows, _ := p.Store.Versions(); len(rows) != 0 {
		t.Fatalf("versions = %+v", rows)
	}
	if evs := versionEvents(t, p); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}

	// The push rule is checked first.
	q, _, log := streamProject(t)
	if _, err := q.VersionPush(ctx, PushArgs{Render: true}); code(err) != CodePushRule {
		t.Fatalf("push rule: %v", err)
	}
	if exists(log) || exists(filepath.Join(q.Root, "v8")) {
		t.Fatal("nothing runs before the push rule holds")
	}

	// Streaming needs version folders: asked for explicitly it's refused; by default it falls back to one MP4.
	f, _ := timelineProject(t)
	for _, name := range []string{"v6/clip_v6.mp4", "v7/clip_v7.mp4"} {
		if err := os.Rename(filepath.Join(f.Root, name), filepath.Join(f.Root, filepath.Base(name))); err != nil {
			t.Fatal(err)
		}
	}
	f.Video = filepath.Join(f.Root, "clip_v7.mp4")
	if _, err := f.VersionPush(ctx, PushArgs{Render: true, Stream: true, Changes: all}); code(err) != CodeBadRequest ||
		!strings.Contains(err.Error(), "version folders") {
		t.Fatalf("explicit stream, flat family: %v", err)
	}
	res, err := f.VersionPush(ctx, PushArgs{Render: true, Changes: all})
	if err != nil || !strings.HasSuffix(res.Version.Path, "clip_v8.mp4") || !strings.Contains(res.Note, "version folders") {
		t.Fatalf("fallback = %+v %v", res, err)
	}
	if _, err := f.VersionPush(ctx, PushArgs{Render: true, Stream: true, NoStream: true}); code(err) != CodeBadRequest {
		t.Fatalf("both: %v", err)
	}
}
