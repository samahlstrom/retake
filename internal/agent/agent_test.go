package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
)

// fakeProber gives every existing file a 10 s duration, except files whose name contains "rendering".
type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil || strings.Contains(filepath.Base(p), "rendering") {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d := 10.0
	return probe.Info{Duration: &d, FPS: 30}
}

const (
	take6 = "2026-10-28_10-00-00" // recorded on v6
	take7 = "2026-10-29_20-48-01" // recorded on v7
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newProject makes v6/clip_v6.mp4 and v7/clip_v7.mp4 with notes: #1-#3 on v7 (#1 with an image and strokes),
// #4 on a v6 take that v7 inherits for 0-10 s, #5 on the same take outside that range.
func newProject(t *testing.T) *Project {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "v6", "clip_v6.mp4"), "six")
	write(t, filepath.Join(root, "v7", "clip_v7.mp4"), "seven")
	t7 := filepath.Join(root, "v7", "clip_v7.review", take7)
	write(t, filepath.Join(t7, "images.json"), `[{"seg": "S1", "file": "img_01.png", "video_t": 1.5, "box": [0.1, 0.2, 0.3, 0.4]}]`)
	write(t, filepath.Join(t7, "img_01.png"), "png")
	t6 := filepath.Join(root, "v6", "clip_v6.review", take6)
	write(t, filepath.Join(t6, "session.json"), "{}")
	write(t, filepath.Join(root, "v7", "clip_v7.review", "inherit.json"),
		fmt.Sprintf(`{"takes": [{"dir": %q, "version": "v6", "ranges": [[0, 10]]}]}`, t6))
	p, err := Open(filepath.Join(root, "v7", "clip_v7.mp4"), fakeProber{})
	if err != nil {
		t.Fatal(err)
	}
	p.Store.Poll = 5 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	if p.Root != root {
		t.Fatalf("root = %s, want %s", p.Root, root)
	}
	c := func(take, seg string, n int, t0 float64, text, version string) project.Comment {
		return project.Comment{Take: take, Seg: seg, N: n, T0: t0, T1: t0 + 1, Text: text, Author: "Ana", Version: version}
	}
	first := c(take7, "S1", 0, 1, "make the title bigger", "")
	first.Detail = project.Detail{Strokes: []project.Stroke{{Shape: "circle", Color: "red", Box: [4]float64{0.1, 0.1, 0.2, 0.2}}},
		Snapshot: "/snap/1.png"}
	if _, err := p.Store.Sync(project.Run{Version: "v7", Comments: []project.Comment{
		first, c(take7, "S1", 1, 3, "cut the pause", ""), c(take7, "S2", 0, 6, "music too loud", ""),
		c(take6, "S1", 0, 5, "carried over", "v6"), c(take6, "S1", 1, 50, "not carried", "v6"),
	}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func code(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return fmt.Sprintf("not an *Error: %v", err)
}

func ids(notes []NoteView) []int64 {
	out := make([]int64, len(notes))
	for i, n := range notes {
		out[i] = n.ID
	}
	return out
}

func TestNotesList(t *testing.T) {
	p := newProject(t)
	ctx := context.Background()
	res, err := p.NotesList(ctx, ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(res.Notes), []int64{1, 2, 3, 4, 5}) || res.Cursor != 1 {
		t.Fatalf("all = %v cursor %d", ids(res.Notes), res.Cursor)
	}
	n := res.Notes[0]
	if n.Text != "make the title bigger" || n.Author != "Ana" || n.T0 != 1 || n.Status != project.StatusOpen ||
		len(n.Strokes) != 1 || n.Strokes[0].Shape != "circle" || n.Snapshot != "/snap/1.png" || len(n.Images) != 1 ||
		n.Images[0].Path != filepath.Join(p.Root, "v7", "clip_v7.review", take7, "img_01.png") ||
		n.Images[0].Box != [4]float64{0.1, 0.2, 0.3, 0.4} {
		t.Fatalf("note 1 = %+v", n)
	}

	// On v7: its own notes plus the carried one inside the inherited range.
	res, _ = p.NotesList(ctx, ListArgs{Version: "v7"})
	if !slices.Equal(ids(res.Notes), []int64{1, 2, 3, 4}) {
		t.Fatalf("v7 = %v", ids(res.Notes))
	}
	if _, err := p.Store.Ask(project.Ask{Note: 2, Text: "how much?"}); err != nil {
		t.Fatal(err)
	}
	res, _ = p.NotesList(ctx, ListArgs{Status: []string{"question,answered"}})
	if !slices.Equal(ids(res.Notes), []int64{2}) || len(res.Notes[0].Threads) != 1 || res.Notes[0].Threads[0].Text != "how much?" {
		t.Fatalf("question = %+v", res.Notes)
	}
	if _, err := p.NotesList(ctx, ListArgs{Status: []string{"done"}}); code(err) != CodeBadRequest {
		t.Errorf("bad status: %v", err)
	}
	if _, err := p.NotesList(ctx, ListArgs{Version: "v99"}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "the family has v6, v7") {
		t.Errorf("unknown version: %v", err)
	}
}

func TestNotesWait(t *testing.T) {
	p := newProject(t)
	ctx := context.Background()

	// From the start: the open notes, at once.
	res, err := p.NotesWait(ctx, WaitArgs{})
	if err != nil || !slices.Equal(ids(res.Notes), []int64{1, 2, 3, 4, 5}) || res.TimedOut || res.Cursor != 1 {
		t.Fatalf("since 0 = %v %+v", err, res)
	}

	// Nothing new: times out with the same cursor.
	res, err = p.NotesWait(ctx, WaitArgs{Since: res.Cursor, Timeout: 30 * time.Millisecond})
	if err != nil || !res.TimedOut || res.Cursor != 1 || len(res.Notes) != 0 {
		t.Fatalf("timeout = %v %+v", err, res)
	}

	// Things that don't need the agent (a question it asked) don't wake it; a reopened note does.
	q, err := p.Store.Ask(project.Ask{Note: 3, Text: "which track?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store.SetNote(1, project.NoteUpdate{Status: project.StatusAnswered, In: "v7", Change: "x"}); err != nil {
		t.Fatal(err)
	}
	got := make(chan WaitResult, 1)
	go func() {
		r, err := p.NotesWait(ctx, WaitArgs{Since: 1, Timeout: 10 * time.Second})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	time.Sleep(30 * time.Millisecond)
	select {
	case r := <-got:
		t.Fatalf("woke for nothing: %+v", r)
	default:
	}
	if _, err := p.Store.Reopen(1); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if !slices.Equal(ids(r.Notes), []int64{1}) || len(r.Answers) != 0 || r.TimedOut {
		t.Fatalf("reopen woke with %+v", r)
	}

	// The reviewer reopens #2; then a rebuild no longer produces #2 (retracted) and brings an answer to the
	// question on #3. The answer and #3 (open again) come back; #2, opened and then retracted, doesn't.
	if _, err := p.Store.SetNote(2, project.NoteUpdate{Status: project.StatusAnswered, In: "v7", Change: "x"}); err != nil {
		t.Fatal(err)
	}
	c0, _ := p.Store.LastSeq()
	if _, err := p.Store.Reopen(2); err != nil {
		t.Fatal(err)
	}
	ans := project.Comment{Take: "2026-10-30_08-00-00", Seg: "S1", N: 0, T0: 6, T1: 7, Text: "the calm one",
		Author: "Ana", AnswerTo: q.ID}
	keep := func(seg string, n int) project.Comment {
		return project.Comment{Take: take7, Seg: seg, N: n, T0: 1, T1: 2, Text: "same", Author: "Ana"}
	}
	res2, err := p.Store.Sync(project.Run{Version: "v7", Comments: []project.Comment{keep("S1", 0), keep("S2", 0), ans}})
	if err != nil || !slices.Equal(res2.Retracted, []int64{2}) || !slices.Equal(res2.Reopened, []int64{3}) {
		t.Fatalf("sync = %+v %v", res2, err)
	}
	r2, err := p.NotesWait(ctx, WaitArgs{Since: c0})
	if err != nil || len(r2.Answers) != 1 || r2.Answers[0].Text != "the calm one" || !slices.Equal(ids(r2.Notes), []int64{3}) {
		t.Fatalf("answer = %v %+v", err, r2)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.NotesWait(cctx, WaitArgs{Since: r2.Cursor}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	if _, err := p.NotesWait(ctx, WaitArgs{Since: -1}); code(err) != CodeBadRequest {
		t.Errorf("negative since: %v", err)
	}
}

func TestNoteAskAndSet(t *testing.T) {
	p := newProject(t)
	ctx := context.Background()
	th, err := p.NoteAsk(ctx, AskArgs{Note: 1, Text: "how big?", Options: []string{"V6", "v7"}})
	if err != nil || th.Kind != "question" || *th.Note != 1 || !slices.Equal(th.Options, []string{"v6", "v7"}) {
		t.Fatalf("ask = %+v %v", th, err)
	}
	if _, err := p.NoteAsk(ctx, AskArgs{Note: 1, Text: "again?"}); code(err) != CodeIllegal {
		t.Errorf("asking twice: %v", err)
	}
	if _, err := p.NoteAsk(ctx, AskArgs{Note: 2, Text: "x", Options: []string{"v9"}}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "push it first") {
		t.Errorf("unknown option: %v", err)
	}
	if _, err := p.NoteAsk(ctx, AskArgs{Note: 2, Text: "  "}); code(err) != CodeBadRequest {
		t.Errorf("empty question: %v", err)
	}
	if _, err := p.NoteAsk(ctx, AskArgs{Note: 99, Text: "x"}); code(err) != CodeNotFound {
		t.Errorf("unknown note: %v", err)
	}
	general, err := p.NoteAsk(ctx, AskArgs{Text: "too long overall?"})
	if err != nil || general.Note != nil || general.Version != "v7" {
		t.Fatalf("general question = %+v %v", general, err)
	}

	n, err := p.NoteSet(ctx, SetArgs{ID: 2, Status: "answered", In: "V7", Change: "pause cut"})
	if err != nil || n.Status != project.StatusAnswered || n.AnsweredIn != "v7" || n.Change != "pause cut" {
		t.Fatalf("set = %+v %v", n, err)
	}
	if _, err := p.NoteSet(ctx, SetArgs{ID: 3, Status: "answered", In: "v9", Change: "x"}); code(err) != CodeNotFound {
		t.Errorf("unknown version: %v", err)
	}
	if _, err := p.NoteSet(ctx, SetArgs{ID: 3, Status: "answered", In: "v7"}); code(err) != CodeFailed &&
		!strings.Contains(fmt.Sprint(err), "one-line change") {
		t.Errorf("no change: %v", err)
	}
	if _, err := p.NoteSet(ctx, SetArgs{ID: 2, Status: "accepted"}); code(err) != CodeIllegal ||
		!strings.Contains(err.Error(), "approving is the reviewer's action") {
		t.Errorf("accept: %v", err)
	}
	if _, err := p.NoteSet(ctx, SetArgs{ID: 2, Status: "nope"}); code(err) != CodeBadRequest {
		t.Errorf("bad status: %v", err)
	}
}

func sum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVersionPush(t *testing.T) {
	p := newProject(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "render.mp4")
	write(t, src, "eight")

	// The push rule: #1-#4 are open on v7 (#4 carried over); #5 isn't on v7.
	_, err := p.VersionPush(ctx, PushArgs{File: src, Changes: []project.ChangeEntry{{Note: 1, Summary: "bigger"}}})
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodePushRule || !slices.Equal(ae.Notes, []int64{2, 3, 4}) {
		t.Fatalf("rule = %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "v8")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a rejected push left files behind")
	}
	if code(mustErr(p.VersionPush(ctx, PushArgs{File: src, Parent: "v3"}))) != CodeNotFound {
		t.Error("unknown parent accepted")
	}
	if code(mustErr(p.VersionPush(ctx, PushArgs{File: filepath.Join(p.Root, "nope.mp4")}))) != CodeNotFound {
		t.Error("missing file accepted")
	}
	txt := filepath.Join(t.TempDir(), "notes.txt")
	write(t, txt, "x")
	if code(mustErr(p.VersionPush(ctx, PushArgs{File: txt}))) != CodeBadRequest {
		t.Error("a non-video accepted")
	}
	if err := mustErr(p.VersionPush(ctx, PushArgs{File: src, Label: "v8a"})); code(err) != CodeBadRequest ||
		!strings.Contains(err.Error(), "must be v<N>") {
		t.Errorf("label v8a: %v", err)
	}
	if err := mustErr(p.VersionPush(ctx, PushArgs{File: src, Label: "v6"})); code(err) != CodeBadRequest ||
		!strings.Contains(err.Error(), "v6 is taken; the next free one is v8") {
		t.Errorf("label v6: %v", err)
	}

	res, err := p.VersionPush(ctx, PushArgs{File: src, Parent: "v7",
		Changes: []project.ChangeEntry{{Note: 1, Summary: "title at 120%"}, {Note: 2, Summary: "pause cut"}, {Note: 4, Summary: "fixed"}},
		Asks:    []project.PushAsk{{Note: 3, Text: "which track instead?"}}})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(p.Root, "v8", "clip_v8.mp4")
	if res.Version.Label != "v8" || res.Version.Path != dest || res.Version.Parent != "v7" || res.Version.By != "agent" ||
		res.Version.SHA256 != sum(t, src) || len(res.Version.Changelog) != 3 || !slices.Equal(res.Answered, []int64{1, 2, 4}) ||
		len(res.Questions) != 1 {
		t.Fatalf("push = %+v", res)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "eight" {
		t.Fatalf("pushed file: %q %v", b, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("a copy removed the source")
	}
	entries, _ := os.ReadDir(filepath.Join(p.Root, "v8"))
	if len(entries) != 1 {
		t.Errorf("v8/ holds %d files (a temp file left?)", len(entries))
	}
	evs, _ := p.Store.Events(0, 0)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Contains(kinds, project.EventVersion) {
		t.Errorf("no version event: %v", kinds)
	}
	if v := p.Family(); len(v) != 3 || v[2].Label != "v8" {
		t.Fatalf("family after push = %+v", v)
	}

	// v9 by move; then, with a render of v10 still in progress, the next push skips 10.
	moved := filepath.Join(t.TempDir(), "next.MOV")
	write(t, moved, "nine")
	res, err = p.VersionPush(ctx, PushArgs{File: moved, Move: true, By: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version.Label != "v9" || res.Version.Parent != "v8" || res.Version.By != "codex" ||
		res.Version.Path != filepath.Join(p.Root, "v9", "clip_v9.mov") {
		t.Fatalf("moved push = %+v", res.Version)
	}
	if _, err := os.Stat(moved); !errors.Is(err, os.ErrNotExist) {
		t.Error("--move left the source")
	}
	write(t, filepath.Join(p.Root, "v10", "clip_v10_rendering.mp4"), "partial")
	next := filepath.Join(t.TempDir(), "x.mp4")
	write(t, next, "eleven")
	res, err = p.VersionPush(ctx, PushArgs{File: next})
	if err != nil || res.Version.Label != "v11" {
		t.Fatalf("push past a rendering v10 = %+v %v", res.Version, err)
	}

	// A flat family: the new version goes next to its parent.
	flat := t.TempDir()
	write(t, filepath.Join(flat, "Promo-V2.mp4"), "two")
	fp, err := Open(filepath.Join(flat, "Promo-V2.mp4"), fakeProber{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fp.Close() }()
	res, err = fp.VersionPush(ctx, PushArgs{File: src, Label: "v5"})
	if err != nil || res.Version.Path != filepath.Join(flat, "Promo-V5.mp4") || res.Version.Label != "v5" {
		t.Fatalf("flat push = %+v %v", res.Version, err)
	}
}

func mustErr[T any](_ T, err error) error { return err }

func TestVersionsList(t *testing.T) {
	p := newProject(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "r.mp4")
	write(t, src, "eight")
	if _, err := p.VersionPush(ctx, PushArgs{File: src, Asks: []project.PushAsk{
		{Note: 1, Text: "a"}, {Note: 2, Text: "b"}, {Note: 3, Text: "c"}, {Note: 4, Text: "d"},
	}}); err != nil {
		t.Fatal(err)
	}
	res, err := p.VersionsList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Versions) != 3 {
		t.Fatalf("versions = %+v", res.Versions)
	}
	v6, v7, v8 := res.Versions[0], res.Versions[1], res.Versions[2]
	if v6.Label != "v6" || v6.Open != 1 || v6.SHA256 != "" || v6.Newest || !v6.InFamily || *v6.Duration != 10 {
		t.Errorf("v6 = %+v", v6)
	}
	if v7.Open != 0 {
		t.Errorf("v7 = %+v", v7)
	}
	if v8.Label != "v8" || !v8.Newest || v8.Parent != "v7" || v8.SHA256 == "" || v8.By != "agent" || v8.Changelog == nil {
		t.Errorf("v8 = %+v", v8)
	}
	// A pushed file that disappeared still shows, out of the family.
	if err := os.Remove(filepath.Join(p.Root, "v8", "clip_v8.mp4")); err != nil {
		t.Fatal(err)
	}
	res, _ = p.VersionsList(ctx)
	if last := res.Versions[len(res.Versions)-1]; last.Label != "v8" || last.InFamily || last.Parent != "v7" {
		t.Errorf("gone v8 = %+v", last)
	}
}
