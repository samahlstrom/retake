package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeEditor is a stand-in retake-edit: `render <timeline> -o <out> --json [--draft]` writes a file and a manifest
// at revision $FAKE_REV beside it, and touches $FAKE_MARK; with $FAKE_FAIL set it reports a typed error instead.
const fakeEditor = `#!/bin/sh
[ "$1" = render ] || exit 9
out="$4"
touch "$FAKE_MARK"
if [ -n "$FAKE_FAIL" ]; then
  echo '{"ok": false, "errors": [{"code": "ffmpeg_failed", "message": "ffmpeg failed (encoding u3)"}]}'
  exit 1
fi
printf 'rendered rev %s %s' "$FAKE_REV" "$6" > "$out"
cat > "${out%.mp4}.manifest.json" <<EOF
{"schema": "manifest/0.1", "timeline": {"id": "t", "rev": $FAKE_REV, "hash": "b3:0"}, "video": "$(basename "$out")",
 "size": [64, 36], "fps": 10, "duration": 10, "frames": 100, "range": [0, 10], "draft": false, "units": []}
EOF
echo '{"ok": true, "output": "'"$out"'", "units": 3, "reused": 1, "rendered": 2, "frames": 100, "duration": 10}'
`

// timelineProject makes newProject a timeline project: edit/timeline.json at rev 3 and its patch log, with a fake
// retake-edit. p1 answers #1, p2 answers #2 and #3, p3 is the agent's own.
func timelineProject(t *testing.T) (*Project, string) {
	t.Helper()
	testmedia.NeedShell(t)
	p := newProject(t)
	dir := filepath.Join(p.Root, "edit")
	write(t, filepath.Join(dir, "timeline.json"), `{"schema": "timeline/0.1", "id": "t", "rev": 3}`)
	write(t, filepath.Join(dir, "timeline.patches.jsonl"), strings.Join([]string{
		`{"id": "p1", "rev": 1, "base": 0, "answers": ["v7#1"], "summary": "Title 20% bigger", "ops": [], "inverse": [], "applied": "x"}`,
		`{"id": "p2", "rev": 2, "base": 1, "answers": ["#2", "#3"], "summary": "Pause cut, music -4 dB", "ops": [], "inverse": [], "applied": "x"}`,
		`{"id": "p3", "rev": 3, "base": 2, "answers": [], "summary": "Bed crossfade follows the cut", "ops": [], "inverse": [], "applied": "x"}`,
	}, "\n")+"\n")
	bin := filepath.Join(t.TempDir(), "retake-edit")
	if err := os.WriteFile(bin, []byte(fakeEditor), 0o755); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "rendered")
	p.Editor = bridge.Editor{Configured: bin, Env: []string{"FAKE_REV=3", "FAKE_MARK=" + mark}}
	return p, mark
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestPushRenderWritesTheChangelogFromPatches(t *testing.T) {
	p, mark := timelineProject(t)
	ctx := context.Background()

	// The push rule is checked before anything renders: #4 (carried onto v7) is in no patch.
	_, err := p.VersionPush(ctx, PushArgs{Render: true})
	if code(err) != CodePushRule || !strings.Contains(err.Error(), "#4") {
		t.Fatalf("push rule: %v", err)
	}
	if exists(mark) || exists(filepath.Join(p.Root, "v8")) {
		t.Fatal("nothing renders, nothing lands in the family")
	}

	res, err := p.VersionPush(ctx, PushArgs{Render: true, Draft: true, NoStream: true,
		Changes: []project.ChangeEntry{{Note: 4, Summary: "kept as is"}, {Note: 3, Summary: "music -4 dB under the VO"}}})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(p.Root, "v8", "clip_v8.mp4")
	if res.Version.Label != "v8" || res.Version.Path != dest || res.Version.Parent != "v7" {
		t.Fatalf("version = %+v", res.Version)
	}
	if b, _ := os.ReadFile(dest); string(b) != "rendered rev 3 --draft" {
		t.Fatalf("the render is the version: %q", b)
	}
	want := []project.ChangeEntry{
		{Note: 1, Summary: "Title 20% bigger", Patches: []string{"p1"}},
		{Note: 2, Summary: "Pause cut, music -4 dB", Patches: []string{"p2"}},
		{Note: 3, Summary: "music -4 dB under the VO", Patches: []string{"p2"}}, // the explicit summary wins
		{Note: 4, Summary: "kept as is"},
	}
	if len(res.Version.Changelog) != len(want) {
		t.Fatalf("changelog = %+v", res.Version.Changelog)
	}
	for i, w := range want {
		g := res.Version.Changelog[i]
		if g.Note != w.Note || g.Summary != w.Summary || !slices.Equal(g.Patches, w.Patches) {
			t.Errorf("changelog[%d] = %+v, want %+v", i, g, w)
		}
	}
	if !slices.Equal(res.Answered, []int64{1, 2, 3, 4}) {
		t.Fatalf("answered = %v", res.Answered)
	}
	n, _ := p.Store.Note(1)
	if n.Status != project.StatusAnswered || n.AnsweredIn != "v8" || n.Change != "Title 20% bigger" {
		t.Fatalf("note 1 = %+v", n)
	}
	// The manifest is beside it, pointing at the new name; the push record lists the patches.
	m, err := bridge.ForVideo(dest)
	if err != nil || m.Video != "clip_v8.mp4" || m.Timeline.Rev != 3 || res.Manifest != bridge.ManifestPath(dest) {
		t.Fatalf("manifest = %+v %v", m, err)
	}
	rec, err := bridge.ReadPushRecord(dest)
	if err != nil || rec.Version != "v8" || rec.From != "v7" || rec.Rev != 3 || rec.Timeline != "edit/timeline.json" ||
		!rec.Draft || len(rec.Patches) != 2 || len(rec.Unrequested) != 1 || rec.Unrequested[0].ID != "p3" ||
		rec.Units != (bridge.Units{Total: 3, Reused: 1, Rendered: 2}) {
		t.Fatalf("record = %+v %v", rec, err)
	}
	if res.Render == nil || res.Render.Units.Total != 3 {
		t.Fatalf("result render = %+v", res.Render)
	}
	if left, _ := filepath.Glob(filepath.Join(p.Root, project.Dir, "renders", "*")); len(left) != 0 {
		t.Fatalf("temporary renders left: %v", left)
	}
	vl, _ := p.VersionsList(ctx)
	last := vl.Versions[len(vl.Versions)-1]
	if last.Label != "v8" || last.Render == nil || last.Render.Rev != 3 || last.Manifest == "" ||
		!slices.Equal(last.Changelog[0].Patches, []string{"p1"}) {
		t.Fatalf("versions_list = %+v", last)
	}
	js, _ := json.Marshal(last)
	if !strings.Contains(string(js), `"render":{"version":"v8"`) {
		t.Fatalf("json = %s", js)
	}

	// The next render push starts after v8's revision: nothing to answer, nothing open, so it just pushes.
	_ = os.Remove(mark)
	p.Editor.Env = []string{"FAKE_REV=3", "FAKE_MARK=" + mark}
	res, err = p.VersionPush(ctx, PushArgs{Render: true, NoStream: true})
	if err != nil || res.Version.Label != "v9" || len(res.Version.Changelog) != 0 || len(res.Render.Patches) != 0 ||
		len(res.Render.Unrequested) != 0 {
		t.Fatalf("v9 = %+v %v", res, err)
	}
}

func TestPushRenderFailures(t *testing.T) {
	ctx := context.Background()
	p, mark := timelineProject(t)
	all := []project.ChangeEntry{{Note: 4, Summary: "kept"}}

	// A failed render leaves nothing: no file, no manifest, no version, notes still open.
	p.Editor.Env = append(p.Editor.Env, "FAKE_FAIL=1")
	_, err := p.VersionPush(ctx, PushArgs{Render: true, NoStream: true, Changes: all})
	if code(err) != CodeFailed || !strings.Contains(err.Error(), "ffmpeg failed (encoding u3)") || !exists(mark) {
		t.Fatalf("failed render: %v", err)
	}
	if exists(filepath.Join(p.Root, "v8")) {
		t.Fatal("a failed render left a version folder")
	}
	if rows, _ := p.Store.Versions(); len(rows) != 0 {
		t.Fatalf("versions = %+v", rows)
	}
	if n, _ := p.Store.Note(1); n.Status != project.StatusOpen {
		t.Fatalf("note 1 = %s", n.Status)
	}

	// A file and render together, render outside a timeline project, and no retake-edit.
	if _, err := p.VersionPush(ctx, PushArgs{Render: true, File: "x.mp4"}); code(err) != CodeBadRequest {
		t.Fatalf("file and render: %v", err)
	}
	q := newProject(t)
	if _, err := q.VersionPush(ctx, PushArgs{Render: true}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "timeline project") {
		t.Fatalf("not a timeline project: %v", err)
	}
	p.Editor = bridge.Editor{Configured: filepath.Join(t.TempDir(), "missing")}
	if _, err := p.VersionPush(ctx, PushArgs{Render: true, Changes: all}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "retake-edit") {
		t.Fatalf("no editor: %v", err)
	}

	// Answers that name a note that isn't open are skipped and reported, not fatal.
	p2, _ := timelineProject(t)
	if _, err := p2.NoteAsk(ctx, AskArgs{Note: 2, Text: "which pause?"}); err != nil {
		t.Fatal(err)
	}
	res, err := p2.VersionPush(ctx, PushArgs{Render: true, NoStream: true, Changes: all})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Render.Skipped) != 1 || res.Render.Skipped[0].Answer != "#2" ||
		!strings.Contains(res.Render.Skipped[0].Why, "question") {
		t.Fatalf("skipped = %+v", res.Render.Skipped)
	}
}

// notes_list returns a note's targets (and the word, for (audio) notes) from its detail.
func TestNotesListTargets(t *testing.T) {
	p := newProject(t)
	box := [4]float64{1200, 600, 576, 324}
	c := project.Comment{Take: take7, Seg: "S9", N: 1, T0: 9, T1: 10, Text: "pip bigger", Author: "Ana",
		Detail: project.Detail{Audio: true, Unit: "u5", Word: &project.Word{T0: 8.9, T1: 9.2, Text: "corner"},
			Targets: []project.Target{{ID: "pip/v", Kind: "layer", Ptr: "/scenes/1/overlays/1/layers/1", Score: 1, Z: 2,
				Box: &box, Parent: "pip"}}}}
	if _, err := p.Store.Sync(project.Run{Version: "v7", Comments: []project.Comment{c}}); err != nil {
		t.Fatal(err)
	}
	res, err := p.NotesList(context.Background(), ListArgs{Status: []string{"open"}})
	if err != nil {
		t.Fatal(err)
	}
	var got NoteView
	for _, n := range res.Notes {
		if n.Text == "pip bigger" {
			got = n
		} else if n.Targets == nil || len(n.Targets) != 0 {
			t.Fatalf("a note without a manifest has no targets: %+v", n.Targets)
		}
	}
	if len(got.Targets) != 1 || got.Targets[0].ID != "pip/v" || got.Word == nil || got.Word.Text != "corner" {
		t.Fatalf("note = %+v", got)
	}
	js, _ := json.Marshal(got)
	if !strings.Contains(string(js), `"targets":[{"id":"pip/v","kind":"layer"`) {
		t.Fatalf("json = %s", js)
	}
}

func TestMergeChangelog(t *testing.T) {
	auto := []bridge.NoteChange{{Note: 1, Summary: "a", Patches: []string{"p1"}}, {Note: 2, Summary: "b", Patches: []string{"p2"}}}
	got := mergeChangelog(auto, []project.ChangeEntry{{Note: 3, Summary: "c"}, {Note: 1, Summary: "A"}},
		[]project.PushAsk{{Note: 2, Text: "?"}})
	if len(got) != 2 || got[0].Summary != "A" || !slices.Equal(got[0].Patches, []string{"p1"}) || got[1].Note != 3 {
		t.Fatalf("merged = %+v", got)
	}
}
