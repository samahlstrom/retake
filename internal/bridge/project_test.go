package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func TestFindTimeline(t *testing.T) {
	root := t.TempDir()
	if _, err := FindTimeline(root); !errors.Is(err, ErrNoTimeline) {
		t.Fatalf("empty project: %v", err)
	}
	write(t, filepath.Join(root, "edit", "timeline.json"), `{"rev": 3}`, 0o600)
	got, err := FindTimeline(root)
	if err != nil || got != filepath.Join(root, "edit", "timeline.json") {
		t.Fatalf("default = %s %v", got, err)
	}
	// retake.toml names another one, relative to the root.
	write(t, filepath.Join(root, "cut", "main.json"), `{"rev": 7}`, 0o600)
	write(t, filepath.Join(root, ProjectConfig), "[edit]\ntimeline = \"cut/main.json\"\n", 0o600)
	if got, err = FindTimeline(root); err != nil || got != filepath.Join(root, "cut", "main.json") {
		t.Fatalf("retake.toml = %s %v", got, err)
	}
	if rev, err := TimelineRev(got); err != nil || rev != 7 {
		t.Fatalf("rev = %d %v", rev, err)
	}
	write(t, filepath.Join(root, ProjectConfig), "[edit]\ntimeline = \"cut/missing.json\"\n", 0o600)
	if _, err := FindTimeline(root); err == nil || errors.Is(err, ErrNoTimeline) || !strings.Contains(err.Error(), "missing.json") {
		t.Fatalf("a named timeline that's missing is an error, not a fallback: %v", err)
	}
	write(t, filepath.Join(root, ProjectConfig), "[edit\n", 0o600)
	if _, err := FindTimeline(root); err == nil {
		t.Fatal("bad TOML")
	}
	write(t, filepath.Join(root, ProjectConfig), "name = \"x\"\n", 0o600)
	if got, err = FindTimeline(root); err != nil || got != filepath.Join(root, "edit", "timeline.json") {
		t.Fatalf("no [edit] timeline: the default: %s %v", got, err)
	}
	write(t, filepath.Join(root, "x.json"), `{"id": "x"}`, 0o600)
	if _, err := TimelineRev(filepath.Join(root, "x.json")); err == nil {
		t.Fatal("a timeline without rev")
	}
}

func TestPatchLogAndAnswers(t *testing.T) {
	dir := t.TempDir()
	tl := filepath.Join(dir, "timeline.json")
	if log, err := ReadPatchLog(tl); err != nil || len(log) != 0 {
		t.Fatalf("no log = %v %v", log, err)
	}
	if PatchLogPath(tl) != filepath.Join(dir, "timeline.patches.jsonl") {
		t.Fatal(PatchLogPath(tl))
	}
	write(t, PatchLogPath(tl), `{"id": "p1", "rev": 1, "base": 0, "answers": ["v1#3"], "summary": "pop", "ops": [], "inverse": [], "applied": "x"}

{"id": "p2", "rev": 2, "base": 1, "answers": [], "summary": "trim", "ops": [{"op": "set"}], "inverse": [], "applied": "y"}
`, 0o600)
	log, err := ReadPatchLog(tl)
	if err != nil || len(log) != 2 || log[0].ID != "p1" || log[1].Rev != 2 || !slices.Equal(log[0].Answers, []string{"v1#3"}) {
		t.Fatalf("log = %+v %v", log, err)
	}
	write(t, PatchLogPath(tl), "{nope\n", 0o600)
	if _, err := ReadPatchLog(tl); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("bad line: %v", err)
	}
	for in, want := range map[string]int64{"#5": 5, "5": 5, "v7#5": 5, " v12#40 ": 40, "item-shop.v7#2": 2} {
		if got, ok := NoteID(in); !ok || got != want {
			t.Errorf("NoteID(%q) = %d %v", in, got, ok)
		}
	}
	for _, in := range []string{"v7", "", "#", "#0", "note", "5a", "#-1"} {
		if got, ok := NoteID(in); ok {
			t.Errorf("NoteID(%q) = %d, want none", in, got)
		}
	}
}

func TestChangelog(t *testing.T) {
	log := []Patch{
		{ID: "p1", Rev: 1, Answers: []string{"#9"}, Summary: "old, already pushed"},
		{ID: "p2", Rev: 2, Answers: []string{"v1#1"}, Summary: "pop says FASTER"},
		{ID: "p3", Rev: 3, Answers: []string{}, Summary: "bed crossfade follows the trim"},
		{ID: "p4", Rev: 4, Answers: []string{"#1", "#2"}, Summary: "pop in red"},
		{ID: "p5", Rev: 5, Answers: []string{"#7", "huh"}, Summary: "x"},
		{ID: "p6", Rev: 6, Answers: []string{"#3"}, Summary: ""},
		{ID: "p7", Rev: 7, Answers: []string{"#1"}, Summary: "not rendered yet"},
	}
	open := func(id int64) (bool, string) {
		if id == 7 {
			return false, "note #7 is answered, not open"
		}
		return true, ""
	}
	got, rec := Changelog(log, 1, 6, open)
	want := []NoteChange{
		{Note: 1, Summary: "pop says FASTER; pop in red", Patches: []string{"p2", "p4"}},
		{Note: 2, Summary: "pop in red", Patches: []string{"p4"}},
		{Note: 3, Summary: "changed by p6", Patches: []string{"p6"}},
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v", got)
	}
	for i := range want {
		if got[i].Note != want[i].Note || got[i].Summary != want[i].Summary || !slices.Equal(got[i].Patches, want[i].Patches) {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	var pids []string
	for _, p := range rec.Patches {
		pids = append(pids, p.ID)
	}
	if !slices.Equal(pids, []string{"p2", "p4", "p5", "p6"}) || len(rec.Unrequested) != 1 || rec.Unrequested[0].ID != "p3" {
		t.Fatalf("patches %v unrequested %+v", pids, rec.Unrequested)
	}
	if len(rec.Skipped) != 2 || rec.Skipped[0].Answer != "#7" || !strings.Contains(rec.Skipped[0].Why, "answered") ||
		rec.Skipped[1].Answer != "huh" {
		t.Fatalf("skipped = %+v", rec.Skipped)
	}
	// No base (the parent has no manifest): every patch up to the revision.
	all, _ := Changelog(log, -1, 2, open)
	if len(all) != 2 || all[0].Note != 9 {
		t.Fatalf("all = %+v", all)
	}
}

func TestReadPushRecord(t *testing.T) {
	dir := t.TempDir()
	v := filepath.Join(dir, "clip_v2.mp4")
	if r, err := ReadPushRecord(v); r != nil || err != nil {
		t.Fatalf("none = %v %v", r, err)
	}
	write(t, PushRecordPath(v), `{"version": "v2", "from": "v1", "rev": 4, "units": {"total": 6, "reused": 5, "rendered": 1}}`, 0o600)
	r, err := ReadPushRecord(v)
	if err != nil || r.From != "v1" || r.Units.Reused != 5 {
		t.Fatalf("record = %+v %v", r, err)
	}
	write(t, PushRecordPath(v), `{`, 0o600)
	if _, err := ReadPushRecord(v); err == nil {
		t.Fatal("broken record")
	}
}

func TestEditorFind(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "retake-edit")
	write(t, bin, "#!/bin/sh\n", 0o755)
	notExec := filepath.Join(dir, "plain")
	write(t, notExec, "x", 0o644)
	env := map[string]string{"PATH": filepath.Join(dir, "empty")}
	getenv := func(k string) string { return env[k] }
	t.Setenv("PATH", filepath.Join(dir, "empty"))

	if p, err := (Editor{Configured: bin, Getenv: getenv}).Find(); err != nil || p != bin {
		t.Fatalf("configured = %s %v", p, err)
	}
	if _, err := (Editor{Configured: notExec, Getenv: getenv}).Find(); !errors.Is(err, ErrNoEditor) {
		t.Fatalf("configured, not executable: %v", err)
	}
	env["RETAKE_EDIT"] = bin
	if p, err := (Editor{Getenv: getenv}).Find(); err != nil || p != bin {
		t.Fatalf("$RETAKE_EDIT = %s %v", p, err)
	}
	delete(env, "RETAKE_EDIT")
	t.Setenv("PATH", filepath.Join(dir, "bin"))
	if p, err := (Editor{Getenv: getenv}).Find(); err != nil || p != bin {
		t.Fatalf("$PATH = %s %v", p, err)
	}
	t.Setenv("PATH", filepath.Join(dir, "empty"))
	// <repo>/bin/retake -> <repo>/editor/target/release/retake-edit
	repo := filepath.Join(dir, "repo")
	built := filepath.Join(repo, "editor", "target", "release", "retake-edit")
	write(t, built, "#!/bin/sh\n", 0o755)
	if p, err := (Editor{Getenv: getenv, Exe: filepath.Join(repo, "bin", "retake"), Dir: dir}).Find(); err != nil || p != built {
		t.Fatalf("beside the retake binary = %s %v", p, err)
	}
	if p, err := (Editor{Getenv: getenv, Exe: filepath.Join(dir, "x", "retake"), Dir: repo}).Find(); err != nil || p != built {
		t.Fatalf("under the current folder = %s %v", p, err)
	}
	if _, err := (Editor{Getenv: getenv, Exe: filepath.Join(dir, "x", "retake"), Dir: dir}).Find(); !errors.Is(err, ErrNoEditor) ||
		!strings.Contains(err.Error(), "cargo build --release") {
		t.Fatalf("nowhere: %v", err)
	}
}

func TestEditorRender(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	ok := filepath.Join(dir, "ok.sh")
	write(t, ok, `#!/bin/sh
echo "$@" > `+args+`
echo "$RETAKE_LIB" >> `+args+`
cat <<EOF
{"ok": true, "output": "$4", "units": 6, "reused": 5, "rendered": 1, "frames": 483, "duration": 16.1}
EOF
`, 0o755)
	e := Editor{Env: []string{"RETAKE_LIB=/lib/x"}}
	rep, err := e.Render(context.Background(), ok, "/p/timeline.json", "/p/out.mp4", true)
	if err != nil || rep.Units != 6 || rep.Reused != 5 || rep.Output != "/p/out.mp4" {
		t.Fatalf("report = %+v %v", rep, err)
	}
	b, _ := os.ReadFile(args)
	if got := string(b); got != "render /p/timeline.json -o /p/out.mp4 --json --draft\n/lib/x\n" {
		t.Fatalf("args = %q", got)
	}
	bad := filepath.Join(dir, "bad.sh")
	write(t, bad, `#!/bin/sh
echo '{"ok": false, "errors": [{"code": "anchor_word_not_found", "message": "anchor word:b/fastr not found", "hint": "did you mean faster?"}]}'
exit 1
`, 0o755)
	_, err = e.Render(context.Background(), bad, "t.json", "o.mp4", false)
	var ee *EditError
	if !errors.As(err, &ee) || ee.Code != "anchor_word_not_found" || !strings.Contains(err.Error(), "did you mean faster?") {
		t.Fatalf("typed error = %v", err)
	}
	crash := filepath.Join(dir, "crash.sh")
	write(t, crash, "#!/bin/sh\necho boom >&2\nexit 3\n", 0o755)
	if _, err := e.Render(context.Background(), crash, "t.json", "o.mp4", false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("crash = %v", err)
	}
}
