package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// fakeStreamEditor is a stand-in retake-edit for streamed renders: `render <timeline> --hls <playlist> --progress
// --json [-o export] [--draft]` publishes one segment, then waits for $FAKE_GATE (when set) before the other two, the
// ENDLIST and the manifest. With $FAKE_FAIL it fails after the first segment.
const fakeStreamEditor = `#!/bin/sh
[ "$1" = render ] && [ "$3" = --hls ] || exit 9
pl="$4"; dir=$(dirname "$pl"); stem=$(basename "$pl" .m3u8); export_=""
while [ $# -gt 0 ]; do [ "$1" = -o ] && export_="$2"; shift; done
touch "$FAKE_MARK"
echo '{"progress": "plan", "units": 3, "reused": 1, "segments": 3, "duration": 12.5, "fps": 25, "size": [64, 36]}' >&2
head='#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n'
printf ts > "$dir/seg_00000.ts"
printf "$head#EXTINF:4.000000,\nseg_00000.ts\n" > "$dir/.$stem.tmp" && mv "$dir/.$stem.tmp" "$pl"
echo '{"progress": "publish", "done": 1, "total": 3, "segments": 1, "seconds": 4.0, "elapsed": 0.2}' >&2
if [ -n "$FAKE_FAIL" ]; then
  echo '{"ok": false, "errors": [{"code": "ffmpeg_failed", "message": "ffmpeg failed (encoding u2)"}]}'
  exit 1
fi
if [ -n "$FAKE_GATE" ]; then while [ ! -e "$FAKE_GATE" ]; do sleep 0.02; done; fi
printf ts > "$dir/seg_00001.ts"; printf ts > "$dir/seg_00002.ts"
printf "$head#EXTINF:4.000000,\nseg_00000.ts\n#EXTINF:4.000000,\nseg_00001.ts\n#EXTINF:4.500000,\nseg_00002.ts\n#EXT-X-ENDLIST\n" > "$dir/.$stem.tmp" && mv "$dir/.$stem.tmp" "$pl"
echo '{"progress": "publish", "done": 3, "total": 3, "segments": 3, "seconds": 12.5, "elapsed": 0.4}' >&2
cat > "$dir/$stem.manifest.json" <<EOF
{"schema": "manifest/0.1", "timeline": {"id": "t", "rev": $FAKE_REV, "hash": "b3:0"}, "video": "$stem.m3u8",
 "size": [64, 36], "fps": 25, "duration": 12.5, "frames": 312, "range": [0, 12.5], "draft": false, "units": []}
EOF
[ -n "$export_" ] && printf mp4 > "$export_"
echo '{"progress": "end", "segments": 3, "seconds": 12.5, "elapsed": 0.4}' >&2
echo "a stray line that isn't progress" >&2
echo '{"ok": true, "units": 3, "reused": 1, "rendered": 2, "frames": 312, "duration": 12.5,
 "stream": {"playlist": "'"$pl"'", "segments": 3, "first_s": 0.2, "done_s": 0.4}}'
`

func streamProject(t *testing.T) (*Project, string) {
	t.Helper()
	p, mark := timelineProject(t)
	bin := filepath.Join(t.TempDir(), "retake-edit")
	if err := os.WriteFile(bin, []byte(fakeStreamEditor), 0o755); err != nil {
		t.Fatal(err)
	}
	p.Editor.Configured = bin
	return p, mark
}

// versionEvents returns the "version" events so far.
func versionEvents(t *testing.T, p *Project) []project.VersionEvent {
	t.Helper()
	evs, err := p.Store.Events(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []project.VersionEvent
	for _, e := range evs {
		if e.Kind != project.EventVersion {
			continue
		}
		var v project.VersionEvent
		if err := json.Unmarshal(e.Data, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func whys(evs []project.VersionEvent) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Why)
	}
	return strings.Join(s, " ")
}

func TestPushRenderStreams(t *testing.T) {
	p, mark := streamProject(t)
	gate := filepath.Join(t.TempDir(), "go")
	p.Editor.Env = append(p.Editor.Env, "FAKE_GATE="+gate)
	all := []project.ChangeEntry{{Note: 4, Summary: "kept"}}
	type result struct {
		res PushResult
		err error
	}
	done := make(chan result, 1)
	var notes []string
	go func() {
		res, err := p.VersionPush(context.Background(), PushArgs{Render: true, Changes: all, Export: true,
			Notify: func(s string) { notes = append(notes, s) }})
		done <- result{res, err}
	}()

	// The first segment is out: the version is in the family, rendering, with what the plan expects.
	dest := filepath.Join(p.Root, "v8", "clip_v8.m3u8")
	deadline := time.Now().Add(10 * time.Second)
	for whys(versionEvents(t, p)) != "rendering" {
		if time.Now().After(deadline) {
			t.Fatalf("no rendering event: %q", whys(versionEvents(t, p)))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !exists(mark) || !family.RenderLive(dest) {
		t.Fatal("the push holds the render lock while it renders")
	}
	ev := versionEvents(t, p)[0]
	if ev.Label != "v8" || ev.Parent != "v7" || ev.Path != dest || ev.Progress == nil || ev.Progress.Done != 1 ||
		ev.Progress.Total != 3 || ev.Progress.Seconds != 4 {
		t.Fatalf("rendering event = %+v", ev)
	}
	fam := p.Family()
	v8 := fam[len(fam)-1]
	if v8.Label != "v8" || v8.Name != "clip_v8.m3u8" || !v8.Rendering || v8.Duration == nil || *v8.Duration != 12.5 ||
		v8.FPS != 25 || v8.Width == nil || *v8.Width != 64 || v8.Progress == nil || v8.Progress.Done != 1 {
		t.Fatalf("family = %+v (progress %+v)", v8, v8.Progress)
	}
	rec, err := bridge.ReadPushRecord(dest)
	if err != nil || rec.Rendering == nil || rec.Rendering.Total != 3 || rec.Rendering.Duration != 12.5 ||
		rec.Rendering.Playlist != "clip_v8.m3u8" || rec.Stream != nil {
		t.Fatalf("record while rendering = %+v %v", rec, err)
	}
	vl, _ := p.VersionsList(context.Background())
	if last := vl.Versions[len(vl.Versions)-1]; last.Label != "v8" || !last.Rendering || last.Progress == nil {
		t.Fatalf("versions_list = %+v", last)
	}
	if rows, _ := p.Store.Versions(); len(rows) != 0 {
		t.Fatal("the push is recorded when the render ends, not before")
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.res.Version.Label != "v8" || r.res.Version.Path != dest || r.res.Version.SHA256 == "" || r.res.Note != "" {
		t.Fatalf("version = %+v", r.res)
	}
	rec, err = bridge.ReadPushRecord(dest)
	if err != nil || rec.Rendering != nil || rec.Stream == nil || rec.Stream.Segments != 3 || rec.Stream.FirstS != 0.2 ||
		rec.Stream.DoneS != 0.4 || rec.Export != "clip_v8.export.mp4" || rec.Units.Total != 3 || rec.Version != "v8" ||
		len(rec.Patches) != 2 {
		t.Fatalf("final record = %+v %v", rec, err)
	}
	if !exists(filepath.Join(p.Root, "v8", "clip_v8.export.mp4")) || r.res.Manifest != bridge.ManifestPath(dest) {
		t.Fatal("the export and the manifest are beside the playlist")
	}
	if exists(family.RenderLockPath(dest)) || family.RenderLive(dest) {
		t.Fatal("the lock is released and removed")
	}
	if got := whys(versionEvents(t, p)); got != "rendering progress push" {
		t.Fatalf("events = %q", got)
	}
	if n, _ := p.Store.Note(1); n.Status != project.StatusAnswered || n.AnsweredIn != "v8" {
		t.Fatalf("note 1 = %+v", n)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "v8 is playable") || !strings.Contains(notes[1], "v8 rendered") {
		t.Fatalf("notes = %q", notes)
	}
	// Ended: the family probes it like any file now.
	fam = p.Family()
	if v8 := fam[len(fam)-1]; v8.Label != "v8" || v8.Rendering || v8.Progress != nil {
		t.Fatalf("family after = %+v", v8)
	}
}

func TestPushRenderStreamFailures(t *testing.T) {
	ctx := context.Background()
	all := []project.ChangeEntry{{Note: 4, Summary: "kept"}}

	// The render fails after the first segment: everything goes, and the page hears it failed.
	p, _ := streamProject(t)
	p.Editor.Env = append(p.Editor.Env, "FAKE_FAIL=1")
	_, err := p.VersionPush(ctx, PushArgs{Render: true, Changes: all})
	if code(err) != CodeFailed || !strings.Contains(err.Error(), "ffmpeg failed (encoding u2)") {
		t.Fatalf("failed render: %v", err)
	}
	if exists(filepath.Join(p.Root, "v8")) {
		left, _ := os.ReadDir(filepath.Join(p.Root, "v8"))
		t.Fatalf("a failed render left v8/: %v", left)
	}
	if got := whys(versionEvents(t, p)); got != "rendering failed" {
		t.Fatalf("events = %q", got)
	}
	if rows, _ := p.Store.Versions(); len(rows) != 0 {
		t.Fatalf("versions = %+v", rows)
	}

	// A take recorded on it meanwhile keeps the folder (and the take).
	p2, _ := streamProject(t)
	gate := filepath.Join(t.TempDir(), "go")
	p2.Editor.Env = append(p2.Editor.Env, "FAKE_GATE="+gate)
	ctx2, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := p2.VersionPush(ctx2, PushArgs{Render: true, Changes: all})
		errc <- err
	}()
	for whys(versionEvents(t, p2)) != "rendering" {
		time.Sleep(5 * time.Millisecond)
	}
	take := filepath.Join(p2.Root, "v8", "clip_v8.review", "2026-10-30_12-00-00", "session.json")
	write(t, take, "{}")
	cancel() // the push is interrupted (Ctrl+C)
	if err := <-errc; err == nil {
		t.Fatal("an interrupted push fails")
	}
	if !exists(take) || exists(filepath.Join(p2.Root, "v8", "clip_v8.m3u8")) ||
		exists(filepath.Join(p2.Root, "v8", "seg_00000.ts")) || exists(filepath.Join(p2.Root, "v8", "clip_v8.push.json")) {
		t.Fatal("the take stays, the render goes")
	}

	// Streaming needs version folders: asked for explicitly it's refused; by default it falls back to one MP4.
	q, _ := timelineProject(t)
	for _, f := range []string{"v6/clip_v6.mp4", "v7/clip_v7.mp4"} {
		if err := os.Rename(filepath.Join(q.Root, f), filepath.Join(q.Root, filepath.Base(f))); err != nil {
			t.Fatal(err)
		}
	}
	q.Video = filepath.Join(q.Root, "clip_v7.mp4")
	if _, err := q.VersionPush(ctx, PushArgs{Render: true, Stream: true, Changes: all}); code(err) != CodeBadRequest ||
		!strings.Contains(err.Error(), "version folders") {
		t.Fatalf("explicit stream, flat family: %v", err)
	}
	res, err := q.VersionPush(ctx, PushArgs{Render: true, Changes: all})
	if err != nil || !strings.HasSuffix(res.Version.Path, "clip_v8.mp4") || !strings.Contains(res.Note, "version folders") {
		t.Fatalf("fallback = %+v %v", res, err)
	}
	if _, err := q.VersionPush(ctx, PushArgs{Render: true, Stream: true, NoStream: true}); code(err) != CodeBadRequest {
		t.Fatalf("both: %v", err)
	}
}

// A push killed outright leaves its files, but nobody holds the lock: the family skips it.
func TestKilledStreamIsSkipped(t *testing.T) {
	p := newProject(t)
	dest := filepath.Join(p.Root, "v8", "clip_v8.m3u8")
	write(t, dest, "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.0,\nseg_00000.ts\n")
	write(t, filepath.Join(p.Root, "v8", "clip_v8.push.json"), `{"version": "v8", "rendering": {"duration": 9, "done": 1, "total": 3}}`)
	// fakeProber gives every file a duration; a file named "rendering" none. Keep the playlist unprobeable too.
	if err := os.Rename(dest, filepath.Join(p.Root, "v8", "clip_v8_rendering.m3u8")); err != nil {
		t.Fatal(err)
	}
	dest = filepath.Join(p.Root, "v8", "clip_v8_rendering.m3u8")
	if err := os.Rename(filepath.Join(p.Root, "v8", "clip_v8.push.json"),
		filepath.Join(p.Root, "v8", "clip_v8_rendering.push.json")); err != nil {
		t.Fatal(err)
	}
	if fam := p.Family(); fam[len(fam)-1].Label != "v7" {
		t.Fatalf("a dead render isn't listed: %+v", fam[len(fam)-1])
	}
	unlock, err := family.LockRender(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fam := p.Family(); fam[len(fam)-1].Label != "v8" || !fam[len(fam)-1].Rendering {
		t.Fatalf("a live one is: %+v", fam[len(fam)-1])
	}
	unlock()
	if fam := p.Family(); fam[len(fam)-1].Label != "v7" {
		t.Fatal("skipped again once the lock is gone")
	}
	// Its number stays taken.
	if n, err := p.nextNumber(p.Family(), "clip", ""); err != nil || n != 9 {
		t.Fatalf("next = %d %v", n, err)
	}
}
