package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := &clock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	s.Now = c.now
	s.Poll = 5 * time.Millisecond
	return s, c
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoot(t *testing.T) {
	base := t.TempDir()
	for _, f := range []string{
		"item-shop/v7/item_shop_v7.mp4", "item-shop/v6/item_shop_v6.mp4",
		"flat/clip_v1.mp4", "flat/clip_v2.mp4",
		"nested/renders/clip_v3.mp4", "nested/old/clip_v2.mov",
		"lonely/renders/clip_v3.mp4", "lonely/other/unrelated_v1.mp4",
		"plain/folder/movie.mp4",
		"upper/V12/a.mp4",
	} {
		touch(t, filepath.Join(base, f))
	}
	for _, tc := range []struct{ video, root string }{
		{"item-shop/v7/item_shop_v7.mp4", "item-shop"},       // a v<N> folder
		{"upper/V12/a.mp4", "upper"},                         // v<N> in any case, even for an unversioned name
		{"flat/clip_v1.mp4", "flat"},                         // no version folders: the file's own folder
		{"nested/renders/clip_v3.mp4", "nested"},             // a sibling folder holds the same family
		{"lonely/renders/clip_v3.mp4", "lonely/renders"},     // siblings hold another family only
		{"plain/folder/movie.mp4", "plain/folder"},           // no version number at all
		{"item-shop/v6/item_shop_v6.mp4", "item-shop"},       // every member finds the same root
		{"nested/old/clip_v2.mov", "nested"},                 // ... from either side
		{"plain/folder/../folder/movie.mp4", "plain/folder"}, // (callers pass cleaned paths; Dir cleans anyway)
	} {
		got := Root(filepath.Join(base, tc.video))
		if want := filepath.Join(base, tc.root); got != want {
			t.Errorf("Root(%s) = %s, want %s", tc.video, got, want)
		}
	}
}

func TestOpenCreatesWALStore(t *testing.T) {
	root := t.TempDir()
	if _, err := OpenExisting(root); !errors.Is(err, ErrNoProject) {
		t.Fatalf("OpenExisting on an empty folder: %v, want ErrNoProject", err)
	}
	if _, err := os.Stat(filepath.Join(root, Dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("OpenExisting created the store folder")
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.Path() != filepath.Join(root, ".retake", "project.db") {
		t.Errorf("Path = %s", s.Path())
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", mode, err)
	}
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion {
		t.Errorf("user_version = %d (%v), want %d", v, err, SchemaVersion)
	}
	for _, table := range []string{"notes", "threads", "versions", "jobs", "events"} {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
	s2, err := OpenExisting(root)
	if err != nil {
		t.Fatalf("OpenExisting after Open: %v", err)
	}
	_ = s2.Close()
}

func TestMigrationsAreVersionedAndForwardOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	m1 := `CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT);`
	m2 := `ALTER TABLE things ADD COLUMN size INTEGER NOT NULL DEFAULT 7; CREATE TABLE more (x TEXT);`
	userVersion := func(s *Store) int {
		var v int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	s, err := open(root, []string{m1})
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(s); v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	if _, err := s.db.Exec(`INSERT INTO things (name) VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// A newer build runs only the new step, once, and keeps the data.
	for range 2 {
		s, err = open(root, []string{m1, m2})
		if err != nil {
			t.Fatalf("migrating to 2: %v", err)
		}
		if v := userVersion(s); v != 2 {
			t.Fatalf("user_version = %d, want 2", v)
		}
		var name string
		var size int
		if err := s.db.QueryRow(`SELECT name, size FROM things`).Scan(&name, &size); err != nil || name != "kept" ||
			size != 7 {
			t.Fatalf("row after migrating: %q %d %v", name, size, err)
		}
		_ = s.Close()
	}

	// An older build refuses a newer store instead of touching it.
	_, err = open(root, []string{m1})
	if err == nil || !strings.Contains(err.Error(), "newer than this retake knows") {
		t.Fatalf("old build on a new store: %v", err)
	}

	// A failing step leaves the store at the last good version.
	_, err = open(root, []string{m1, m2, `CREATE TABLE broken (`})
	if err == nil || !strings.Contains(err.Error(), "schema version 3") {
		t.Fatalf("broken step: %v", err)
	}
	s, err = open(root, []string{m1, m2})
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(s); v != 2 {
		t.Errorf("after a failed step user_version = %d, want 2", v)
	}
	_ = s.Close()
}

func TestTransitions(t *testing.T) {
	legal := map[string]bool{
		"open>answered/push":         true,
		"open>answered/agent":        true,
		"open>question/agent":        true,
		"question>open/reviewer":     true,
		"answered>accepted/reviewer": true,
		"answered>open/reviewer":     true,
		"open>retracted/engine":      true,
		"retracted>open/engine":      true,
	}
	actors := []Actor{ByEngine, ByAgent, ByPush, ByReviewer}
	n := 0
	for _, from := range Statuses {
		for _, to := range Statuses {
			for _, by := range actors {
				key := fmt.Sprintf("%s>%s/%s", from, to, by)
				want := legal[key]
				if got := Legal(from, to, by); got != want {
					t.Errorf("Legal(%s) = %v, want %v", key, got, want)
				}
				err := checkTransition(3, from, to, by)
				if want != (err == nil) {
					t.Errorf("checkTransition(%s) = %v", key, err)
				}
				if err != nil {
					var te *TransitionError
					if !errors.As(err, &te) || !strings.HasPrefix(err.Error(), fmt.Sprintf("note #3 is %s; it can't become %s", from, to)) {
						t.Errorf("checkTransition(%s) error = %q", key, err)
					}
				}
				n++
			}
		}
	}
	if n != 100 {
		t.Fatalf("checked %d cases", n)
	}
	for _, tc := range []struct {
		from, to Status
		by       Actor
		hint     string
	}{
		{StatusOpen, StatusQuestion, ByPush, "note_ask"},
		{StatusAnswered, StatusAccepted, ByAgent, "approving is the reviewer's action"},
		{StatusAnswered, StatusOpen, ByAgent, "reopening is the reviewer's action"},
		{StatusQuestion, StatusOpen, ByAgent, "when the reviewer answers"},
		{StatusOpen, StatusRetracted, ByAgent, "notes engine"},
		{StatusQuestion, StatusAnswered, ByPush, "waits for the reviewer's answer"},
		{StatusOpen, StatusOpen, ByAgent, "it already is"},
		{StatusAccepted, StatusRetracted, ByEngine, "isn't a legal change"},
	} {
		if err := checkTransition(1, tc.from, tc.to, tc.by); err == nil || !strings.Contains(err.Error(), tc.hint) {
			t.Errorf("%s>%s by %s: %v, want a hint with %q", tc.from, tc.to, tc.by, err, tc.hint)
		}
	}
	for _, s := range []string{"open", " Answered ", "RETRACTED"} {
		if _, err := ParseStatus(s); err != nil {
			t.Errorf("ParseStatus(%q): %v", s, err)
		}
	}
	if _, err := ParseStatus("done"); err == nil {
		t.Error("ParseStatus(done) succeeded")
	}
}

func comment(take, seg string, n int, t0 float64, text string) Comment {
	return Comment{Take: take, Seg: seg, N: n, T0: t0, T1: t0 + 1, Text: text, Author: "Sam"}
}

const (
	take1 = "2026-10-29_20-48-01"
	take2 = "2026-10-30_09-00-00"
)

func notesByKey(t *testing.T, s *Store) map[string]Note {
	t.Helper()
	all, err := s.Notes(NoteFilter{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Note{}
	for _, n := range all {
		out[n.Key] = n
	}
	return out
}

func events(t *testing.T, s *Store, since int64) []Event {
	t.Helper()
	evs, err := s.Events(since, 0)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func notesEvent(t *testing.T, e Event) NotesEvent {
	t.Helper()
	if e.Kind != EventNotes {
		t.Fatalf("event %d is %s, want notes", e.Seq, e.Kind)
	}
	var ne NotesEvent
	if err := json.Unmarshal(e.Data, &ne); err != nil {
		t.Fatal(err)
	}
	return ne
}

func TestSyncKeepsIDsAndStates(t *testing.T) {
	s, _ := newStore(t)
	run := Run{Version: "v7", Comments: []Comment{
		comment(take1, "S1", 0, 1, "make the title bigger"),
		comment(take1, "S1", 1, 3, "cut the pause"),
		comment(take1, "S2", 0, 9, "music too loud"),
	}}
	run.Comments[0].Detail = Detail{Strokes: []Stroke{{Shape: "circle", Color: "red", Box: [4]float64{0.1, 0.2, 0.3, 0.4}}},
		Snapshot: "/x/snap.png"}
	res, err := s.Sync(run)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Added, []int64{1, 2, 3}) {
		t.Fatalf("Added = %v", res.Added)
	}
	byKey := notesByKey(t, s)
	n1 := byKey[take1+"/S1/0"]
	if n1.ID != 1 || n1.Status != StatusOpen || n1.Version != "v7" || n1.Text != "make the title bigger" ||
		n1.Author != "Sam" || n1.T0 != 1 || n1.T1 != 2 || n1.Detail.Strokes[0].Shape != "circle" ||
		n1.Detail.Snapshot != "/x/snap.png" {
		t.Fatalf("note 1 = %+v", n1)
	}
	evs := events(t, s, 0)
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if ne := notesEvent(t, evs[0]); ne.Why != "sync" || !slices.Equal(ne.Opened, []int64{1, 2, 3}) {
		t.Fatalf("event = %+v", ne)
	}

	// The same run again changes nothing and appends no event.
	res, err = s.Sync(run)
	if err != nil || !res.empty() {
		t.Fatalf("resync: %+v %v", res, err)
	}
	if n := len(events(t, s, 0)); n != 1 {
		t.Fatalf("a no-op sync appended an event (%d)", n)
	}

	// Note 2 gets answered, note 3 asked about; then a rebuild: note 1's text changes, note 2 is gone (but
	// answered, so it stays), note 3 is gone (question, stays), a new comment arrives, and #1 is kept.
	if _, err := s.SetNote(2, NoteUpdate{Status: StatusAnswered, In: "v8", Change: "cut"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(Ask{Note: 3, Text: "which track?"}); err != nil {
		t.Fatal(err)
	}
	seq, _ := s.LastSeq()
	run2 := Run{Version: "v7", Comments: []Comment{
		comment(take1, "S1", 0, 1, "make the title much bigger"),
		comment(take2, "S1", 0, 5, "new thought"),
	}}
	res, err = s.Sync(run2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Updated, []int64{1}) || !slices.Equal(res.Added, []int64{4}) || len(res.Retracted) != 0 {
		t.Fatalf("rebuild = %+v", res)
	}
	byKey = notesByKey(t, s)
	if n := byKey[take1+"/S1/0"]; n.ID != 1 || n.Text != "make the title much bigger" || n.Status != StatusOpen {
		t.Errorf("note 1 = %+v", n)
	}
	if n := byKey[take1+"/S1/1"]; n.Status != StatusAnswered || n.AnsweredIn != "v8" {
		t.Errorf("answered note vanished from the engine but should keep its state: %+v", n)
	}
	if n := byKey[take1+"/S2/0"]; n.Status != StatusQuestion {
		t.Errorf("asked note: %+v", n)
	}
	if n := byKey[take2+"/S1/0"]; n.ID != 4 || n.Status != StatusOpen {
		t.Errorf("new note: %+v", n)
	}

	// Note 1 disappears: retracted, never deleted. A "scrap that" flag on note 4 retracts it too.
	run3 := Run{Version: "v7", Comments: []Comment{comment(take2, "S1", 0, 5, "new thought")}}
	run3.Comments[0].Retracted = true
	res, err = s.Sync(run3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unique(res.Retracted), []int64{1, 4}) {
		t.Fatalf("retracted = %v", res.Retracted)
	}
	if n := notesByKey(t, s)[take1+"/S1/0"]; n.ID != 1 || n.Status != StatusRetracted {
		t.Errorf("vanished note: %+v", n)
	}
	// Both come back: reinstated, same ids.
	res, err = s.Sync(run2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unique(res.Reinstated), []int64{1, 4}) {
		t.Fatalf("reinstated = %v", res.Reinstated)
	}
	evs = events(t, s, seq)
	last := notesEvent(t, evs[len(evs)-1])
	if !slices.Equal(last.Opened, []int64{1, 4}) {
		t.Errorf("reinstatement event = %+v", last)
	}

	// Another version's run never retracts v7's notes, and a comment born retracted isn't "opened".
	res, err = s.Sync(Run{Version: "v8", Comments: []Comment{{Take: take2, Seg: "S9", N: 0, Text: "scrap", Retracted: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retracted) != 0 || len(res.Added) != 1 {
		t.Fatalf("v8 run = %+v", res)
	}
	evs = events(t, s, 0)
	if ne := notesEvent(t, evs[len(evs)-1]); len(ne.Opened) != 0 {
		t.Errorf("a note born retracted was announced as opened: %+v", ne)
	}
}

// The engine's own keys: a comment carried over to v7 keeps the key it has on v6, so it stays one note.
func TestSyncCarriedKeys(t *testing.T) {
	s, _ := newStore(t)
	own := Comment{Key: take1 + "/S1/1", Take: take1, Seg: "S1", Version: "v6", T0: 2, T1: 3, Text: "carried"}
	if _, err := s.Sync(Run{Version: "v6", Comments: []Comment{own}}); err != nil {
		t.Fatal(err)
	}
	v7 := Comment{Key: take2 + "/S1/1", Take: take2, Seg: "S1", T0: 9, T1: 10, Text: "new on v7"}
	res, err := s.Sync(Run{Version: "v7", Comments: []Comment{own, v7, own}})
	if err != nil || !slices.Equal(res.Added, []int64{2}) || len(res.Updated) != 0 {
		t.Fatalf("v7 run = %+v %v", res, err)
	}
	byKey := notesByKey(t, s)
	if n := byKey[take1+"/S1/1"]; n.ID != 1 || n.Version != "v6" {
		t.Errorf("carried note = %+v", n)
	}
	if n := byKey[take2+"/S1/1"]; n.Version != "v7" {
		t.Errorf("v7's note = %+v", n)
	}
	// v7's next run drops the carried comment (its range changed): v6's note isn't v7's to retract.
	res, err = s.Sync(Run{Version: "v7", Comments: []Comment{v7}})
	if err != nil || len(res.Retracted) != 0 {
		t.Fatalf("v7 run without the carried note = %+v %v", res, err)
	}
}

func TestSyncAnswers(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Sync(Run{Version: "v7", Comments: []Comment{comment(take1, "S1", 0, 4, "swap the clip")}}); err != nil {
		t.Fatal(err)
	}
	q, err := s.Ask(Ask{Note: 1, Text: "which clip?", Options: []string{"v8", "v9"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Kind != KindQuestion || q.By != "agent" || q.Version != "v7" || *q.T != 4 || *q.Note != 1 ||
		!slices.Equal(q.Options, []string{"v8", "v9"}) {
		t.Fatalf("question = %+v", q)
	}
	general, err := s.Ask(Ask{Version: "v8", Text: "is the pacing right overall?"})
	if err != nil || general.Note != nil || general.T != nil {
		t.Fatalf("general question = %+v %v", general, err)
	}
	if _, err := s.Ask(Ask{Text: "about nothing"}); err == nil {
		t.Error("a question about no note and no version was accepted")
	}
	if _, err := s.Ask(Ask{Note: 1, Text: "again?"}); err == nil {
		t.Error("asked twice about a note that is already a question")
	}
	un, err := s.Unanswered()
	if err != nil || len(un) != 2 {
		t.Fatalf("unanswered = %v %v", un, err)
	}
	seq, _ := s.LastSeq()

	ans := comment(take2, "S1", 0, 4, "the second one")
	ans.AnswerTo = q.ID
	ans.Version = "v8"
	bad := comment(take2, "S2", 0, 6, "answer to nothing")
	bad.AnswerTo = 999
	res, err := s.Sync(Run{Version: "v8", Comments: []Comment{ans, bad}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Answers) != 1 || !slices.Equal(res.Reopened, []int64{1}) || len(res.Added) != 1 {
		t.Fatalf("sync = %+v", res)
	}
	n, _ := s.Note(1)
	if n.Status != StatusOpen {
		t.Errorf("answered question's note is %s, want open", n.Status)
	}
	th, err := s.Thread(res.Answers[0])
	if err != nil || th.Kind != KindAnswer || *th.AnswerTo != q.ID || *th.Note != 1 || th.By != "Sam" ||
		th.Text != "the second one" {
		t.Fatalf("answer = %+v %v", th, err)
	}
	ne := notesEvent(t, events(t, s, seq)[0])
	if !slices.Equal(ne.Answers, res.Answers) || !slices.Contains(ne.Opened, 1) {
		t.Errorf("event = %+v", ne)
	}
	un, _ = s.Unanswered()
	if len(un) != 1 || un[0].ID != general.ID {
		t.Errorf("unanswered after the answer = %+v", un)
	}
	// Re-syncing doesn't duplicate the answer.
	res, err = s.Sync(Run{Version: "v8", Comments: []Comment{ans, bad}})
	if err != nil || !res.empty() {
		t.Fatalf("resync = %+v %v", res, err)
	}
}

func seedNotes(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Sync(Run{Version: "v7", Comments: []Comment{
		comment(take1, "S1", 0, 1, "one"),
		comment(take1, "S1", 1, 2, "two"),
		comment(take1, "S2", 0, 3, "three"),
	}}); err != nil {
		t.Fatal(err)
	}
	// An older version's take, carried over to v7 by inherit.json for 0-10 s: note 4 (t=5) is on v7, note 5 (t=50)
	// isn't.
	if _, err := s.Sync(Run{Version: "v6", Comments: []Comment{
		comment("2026-10-01_10-00-00", "S1", 0, 5, "four"),
		comment("2026-10-01_10-00-00", "S1", 1, 50, "five"),
	}}); err != nil {
		t.Fatal(err)
	}
}

var v7Scope = Scope{Version: "v7", Carried: []Carried{{Take: "2026-10-01_10-00-00", Ranges: [][2]float64{{0, 10}}}}}

func TestPushRule(t *testing.T) {
	s, _ := newStore(t)
	seedNotes(t, s)
	push := Push{Label: "v8", Path: "/p/v8/clip_v8.mp4", SHA256: "abc", Parent: "v7", By: "agent", Scope: v7Scope}

	err := s.CheckPush(push)
	var pr *PushRuleError
	if !errors.As(err, &pr) || !slices.Equal(pr.Notes, []int64{1, 2, 3, 4}) || pr.Parent != "v7" {
		t.Fatalf("empty push: %v", err)
	}
	if !strings.Contains(err.Error(), "notes #1, #2, #3, #4 are open on v7") {
		t.Errorf("message = %q", err)
	}

	push.Changelog = []ChangeEntry{{1, "bigger title"}, {2, "pause cut"}, {4, "carried note fixed"}}
	err = s.CheckPush(push)
	if !errors.As(err, &pr) || !slices.Equal(pr.Notes, []int64{3}) || !strings.Contains(err.Error(), "note #3 is open on v7") {
		t.Fatalf("one missing: %v", err)
	}
	if _, err := s.RecordPush(push); !errors.As(err, &pr) {
		t.Fatalf("RecordPush ignored the rule: %v", err)
	}
	if vs, _ := s.Versions(); len(vs) != 0 {
		t.Fatal("a rejected push was recorded")
	}

	for _, tc := range []struct {
		name string
		mod  func(p *Push)
		want string
	}{
		{"twice", func(p *Push) { p.Asks = []PushAsk{{1, "why?"}, {3, "which?"}} }, "listed twice"},
		{"unknown note", func(p *Push) { p.Asks = []PushAsk{{3, "which?"}, {99, "?"}} }, "note #99: not found"},
		{"empty summary", func(p *Push) { p.Changelog[0].Summary = " "; p.Asks = []PushAsk{{3, "q"}} }, "summary"},
		{"empty question", func(p *Push) { p.Asks = []PushAsk{{3, ""}} }, "needs its text"},
		{"no label", func(p *Push) { p.Label = ""; p.Asks = []PushAsk{{3, "q"}} }, "needs a label"},
	} {
		p := push
		p.Changelog = slices.Clone(push.Changelog)
		tc.mod(&p)
		if err := s.CheckPush(p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}

	seq, _ := s.LastSeq()
	push.Asks = []PushAsk{{3, "which track?"}}
	res, err := s.RecordPush(push)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version.Label != "v8" || res.Version.SHA256 != "abc" || res.Version.Parent != "v7" ||
		len(res.Version.Changelog) != 3 || !slices.Equal(res.Answered, []int64{1, 2, 4}) || len(res.Questions) != 1 {
		t.Fatalf("push = %+v", res)
	}
	for id, want := range map[int64]Status{1: StatusAnswered, 2: StatusAnswered, 3: StatusQuestion, 4: StatusAnswered, 5: StatusOpen} {
		n, _ := s.Note(id)
		if n.Status != want {
			t.Errorf("note %d is %s, want %s", id, n.Status, want)
		}
		if want == StatusAnswered && (n.AnsweredIn != "v8" || n.Change == "") {
			t.Errorf("note %d: answered_in %q change %q", id, n.AnsweredIn, n.Change)
		}
	}
	q, _ := s.Thread(res.Questions[0])
	if q.Version != "v8" || q.By != "agent" || *q.Note != 3 {
		t.Errorf("pushed question = %+v", q)
	}
	evs := events(t, s, seq)
	if len(evs) != 2 || evs[0].Kind != EventVersion || evs[1].Kind != EventNotes {
		t.Fatalf("push events = %+v", evs)
	}
	if _, err := s.RecordPush(push); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Errorf("pushing a label twice: %v", err)
	}

	// v9 from v8: nothing recorded on v8 yet, so an empty push passes. Note 3 (question) can't be answered by a
	// push until the reviewer answers.
	if err := s.CheckPush(Push{Label: "v9", Parent: "v8", Scope: Scope{Version: "v8"}}); err != nil {
		t.Errorf("empty push on a clean parent: %v", err)
	}
	err = s.CheckPush(Push{Label: "v9", Parent: "v8", Scope: Scope{Version: "v8"}, Changelog: []ChangeEntry{{3, "x"}}})
	var te *TransitionError
	if !errors.As(err, &te) || te.From != StatusQuestion || !strings.Contains(err.Error(), "waits for the reviewer's answer") {
		t.Errorf("answering a question note: %v", err)
	}
}

func TestReviewerActionsAndNoteSet(t *testing.T) {
	s, _ := newStore(t)
	seedNotes(t, s)
	var te *TransitionError
	if _, err := s.SetNote(1, NoteUpdate{Status: StatusAnswered, In: "v8"}); err == nil || !strings.Contains(err.Error(), "one-line change") {
		t.Errorf("answer without change: %v", err)
	}
	n, err := s.SetNote(1, NoteUpdate{Status: StatusAnswered, In: "v8", Change: " cut "})
	if err != nil || n.Status != StatusAnswered || n.Change != "cut" || n.AnsweredIn != "v8" {
		t.Fatalf("set answered: %+v %v", n, err)
	}
	if n, err = s.SetNote(1, NoteUpdate{Status: StatusAnswered, In: "v9", Change: "cut harder"}); err != nil || n.AnsweredIn != "v9" {
		t.Fatalf("correcting an answer: %+v %v", n, err)
	}
	for _, st := range []Status{StatusAccepted, StatusOpen, StatusRetracted, StatusQuestion} {
		if _, err := s.SetNote(1, NoteUpdate{Status: st}); !errors.As(err, &te) {
			t.Errorf("agent set answered -> %s: %v", st, err)
		}
	}
	if _, err := s.SetNote(99, NoteUpdate{Status: StatusAnswered, In: "v8", Change: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown note: %v", err)
	}

	if _, err := s.Reopen(2); !errors.As(err, &te) {
		t.Errorf("reopening an open note: %v", err)
	}
	seq, _ := s.LastSeq()
	n, err = s.Reopen(1)
	if err != nil || n.Status != StatusOpen {
		t.Fatalf("reopen: %+v %v", n, err)
	}
	if ne := notesEvent(t, events(t, s, seq)[0]); ne.Why != "reopen" || !slices.Equal(ne.Opened, []int64{1}) {
		t.Errorf("reopen event = %+v", ne)
	}

	if _, err := s.SetNote(1, NoteUpdate{Status: StatusAnswered, In: "v9", Change: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNote(2, NoteUpdate{Status: StatusAnswered, In: "v8", Change: "y"}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.Approve("v9")
	if err != nil || !slices.Equal(ids, []int64{1}) {
		t.Fatalf("approve v9 = %v %v", ids, err)
	}
	if n, _ := s.Note(2); n.Status != StatusAnswered {
		t.Errorf("approving v9 touched v8's note: %s", n.Status)
	}
	seq, _ = s.LastSeq()
	if ids, err := s.Approve("v9"); err != nil || len(ids) != 0 {
		t.Errorf("approving again = %v %v", ids, err)
	}
	if len(events(t, s, seq)) != 0 {
		t.Error("a no-op approve appended an event")
	}
	if _, err := s.Reopen(1); !errors.As(err, &te) {
		t.Errorf("reopening an accepted note: %v", err)
	}
}

func TestJobsQueueClaimFinish(t *testing.T) {
	s, c := newStore(t)
	jobs, err := s.QueueTake("v7", take1, "/x/"+take1)
	if err != nil || len(jobs) != 2 || jobs[0].Kind != JobTranscribe || jobs[1].Kind != JobNotes ||
		!slices.Equal(jobs[1].Args.After, []int64{jobs[0].ID}) || jobs[0].Args.Dir != "/x/"+take1 {
		t.Fatalf("QueueTake = %+v %v", jobs, err)
	}
	again, err := s.QueueTake("v7", take1, "/x/"+take1)
	if err != nil || again[0].ID != jobs[0].ID || again[1].ID != jobs[1].ID {
		t.Fatalf("QueueTake is not idempotent: %+v %v", again, err)
	}
	// Another take of the same version joins the queued notes run.
	other, err := s.QueueTake("v7", take2, "/x/"+take2)
	if err != nil || other[1].ID != jobs[1].ID {
		t.Fatalf("second take = %+v %v", other, err)
	}
	nj, _ := s.Job(jobs[1].ID)
	if !slices.Equal(nj.Args.After, []int64{jobs[0].ID, other[0].ID}) {
		t.Fatalf("notes job waits for %v", nj.Args.After)
	}

	// The notes job waits for both transcriptions.
	if _, ok, err := s.Claim(JobNotes, nil); ok || err != nil {
		t.Fatalf("claimed notes before its transcriptions: %v", err)
	}
	backoff := func(n int) time.Duration { return time.Duration(n) * time.Minute }
	j, ok, err := s.Claim(JobTranscribe, backoff)
	if !ok || err != nil || j.ID != jobs[0].ID || j.State != JobRunning || j.Attempts != 1 {
		t.Fatalf("claim = %+v %v %v", j, ok, err)
	}
	if j, err = s.Finish(j.ID, Failed, "boom", 3); err != nil || j.State != JobQueued || j.Error != "boom" {
		t.Fatalf("first failure = %+v %v", j, err)
	}
	// Backing off: the other take's job goes first, then nothing until a minute has passed.
	j2, ok, _ := s.Claim(JobTranscribe, backoff)
	if !ok || j2.ID != other[0].ID {
		t.Fatalf("claim during backoff = %+v %v", j2, ok)
	}
	if _, err := s.Finish(j2.ID, Succeeded, "", 3); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Claim(JobTranscribe, backoff); ok {
		t.Fatal("claimed during backoff")
	}
	c.add(61 * time.Second)
	j, ok, _ = s.Claim(JobTranscribe, backoff)
	if !ok || j.ID != jobs[0].ID || j.Attempts != 2 {
		t.Fatalf("retry = %+v %v", j, ok)
	}
	// Interrupted (shutdown): back in the queue, the attempt doesn't count.
	if j, err = s.Finish(j.ID, Interrupted, "stopping", 3); err != nil || j.State != JobQueued || j.Attempts != 1 {
		t.Fatalf("interrupted = %+v %v", j, err)
	}
	c.add(time.Hour)
	j, _, _ = s.Claim(JobTranscribe, backoff)
	if j.Attempts != 2 {
		t.Fatalf("attempts after interrupt = %d", j.Attempts)
	}
	if j, err = s.Finish(j.ID, Fatal, "no transcriber installed", 3); err != nil || j.State != JobFailed {
		t.Fatalf("fatal = %+v %v", j, err)
	}

	// Its notes job fails at once with the reason.
	if _, ok, _ := s.Claim(JobNotes, backoff); ok {
		t.Fatal("ran notes after a failed transcription")
	}
	nj, _ = s.Job(jobs[1].ID)
	if nj.State != JobFailed || !strings.Contains(nj.Error, "transcribing take "+take1+" failed: no transcriber installed") {
		t.Fatalf("notes after failed dep = %+v", nj)
	}

	// Done again retries: a new transcription and a new notes run.
	retry, err := s.QueueTake("v7", take1, "/x/"+take1)
	if err != nil || retry[0].ID == jobs[0].ID || retry[1].ID == jobs[1].ID {
		t.Fatalf("retry after failure = %+v %v", retry, err)
	}

	// Job events carry {id, kind, state}.
	var states []string
	for _, e := range events(t, s, 0) {
		if e.Kind != EventJob {
			continue
		}
		var je JobEvent
		if err := json.Unmarshal(e.Data, &je); err != nil {
			t.Fatal(err)
		}
		if je.ID == jobs[0].ID {
			states = append(states, je.State)
		}
	}
	want := []string{"queued", "running", "queued", "running", "queued", "running", "failed"}
	if !slices.Equal(states, want) {
		t.Errorf("job %d states = %v, want %v", jobs[0].ID, states, want)
	}
}

func TestRequeueRunning(t *testing.T) {
	s, _ := newStore(t)
	a, _ := s.QueueNotes("v7")
	if again, _ := s.QueueNotes("v7"); again.ID != a.ID {
		t.Fatal("QueueNotes queued a duplicate")
	}
	b, _ := s.QueueNotes("v8")
	for range 3 {
		if _, err := s.db.Exec(`UPDATE jobs SET state = 'running', attempts = attempts + 1 WHERE id = ?`, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE jobs SET state = 'running', attempts = 1 WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.RequeueRunning(3)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("requeue = %+v %v", jobs, err)
	}
	if jobs[0].State != JobQueued || !strings.Contains(jobs[0].Error, "interrupted") {
		t.Errorf("job a = %+v", jobs[0])
	}
	if jobs[1].State != JobFailed || !strings.Contains(jobs[1].Error, "last attempt") {
		t.Errorf("job b = %+v", jobs[1])
	}
}

func TestWaitEvents(t *testing.T) {
	s, _ := newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.WaitEvents(ctx, 0, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait on nothing: %v", err)
	}

	// A write by this Store wakes the waiter at once; the poll is long so the bell must do it.
	s.Poll = time.Hour
	got := make(chan []Event, 1)
	go func() {
		evs, _ := s.WaitEvents(context.Background(), 0, 0)
		got <- evs
	}()
	time.Sleep(10 * time.Millisecond)
	if _, err := s.QueueNotes("v7"); err != nil {
		t.Fatal(err)
	}
	select {
	case evs := <-got:
		if len(evs) != 1 || evs[0].Kind != EventJob || evs[0].Seq != 1 {
			t.Fatalf("woke with %+v", evs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bell didn't wake the waiter")
	}

	// Another process's write (here another Store on the same file) is found by polling.
	other, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	s.Poll = 5 * time.Millisecond
	go func() {
		evs, _ := s.WaitEvents(context.Background(), 1, 0)
		got <- evs
	}()
	time.Sleep(10 * time.Millisecond)
	if _, err := other.QueueNotes("v8"); err != nil {
		t.Fatal(err)
	}
	select {
	case evs := <-got:
		if len(evs) != 1 || evs[0].Seq != 2 {
			t.Fatalf("woke with %+v", evs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("polling didn't find the other store's write")
	}
}

func TestConcurrentWritersTakeTurns(t *testing.T) {
	s, _ := newStore(t)
	other, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			if i%2 == 1 {
				st = other
			}
			if _, err := st.QueueNotes(fmt.Sprintf("v%d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 20 {
		t.Fatalf("jobs = %d %v", n, err)
	}
	seqs := events(t, s, 0)
	for i, e := range seqs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event seqs are not 1..n: %v", seqs)
		}
	}
}

func TestSidebar(t *testing.T) {
	s, _ := newStore(t)
	seedNotes(t, s) // v7: 1 2 3; v6: 4 5
	// Take 1 has two images in segment S1: one at 1.5 s (note 1's span) and one at 2.9 s (nearest to note 2).
	dir := t.TempDir()
	images := `[{"seg": "S1", "file": "img_01.png", "video_t": 1.5, "box": [0, 0, 1, 1]},
		{"seg": "S1", "file": "img_02.jpg", "video_t": 2.9, "box": [0.5, 0.5, 0.1, 0.1]}]`
	if err := os.WriteFile(filepath.Join(dir, "images.json"), []byte(images), 0o600); err != nil {
		t.Fatal(err)
	}
	q := SidebarQuery{Version: "v7", Newest: true,
		TakeDir: func(n Note) string {
			if n.Take == take1 {
				return dir
			}
			return ""
		},
		ImageURL: func(n Note, img TakeImage) string { return n.Version + "/" + img.File },
	}
	// v7 newest, never pushed: every open note on it.
	items, err := s.Sidebar(q)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(items); !slices.Equal(got, []string{"note 1", "note 2", "note 3"}) {
		t.Fatalf("v7 newest = %v", got)
	}
	if len(items[0].Images) != 1 || items[0].Images[0].URL != "v7/img_01.png" || items[0].By != "Sam" || *items[0].T0 != 1 {
		t.Errorf("item 1 = %+v", items[0])
	}
	if len(items[1].Images) != 1 || items[1].Images[0].Box != [4]float64{0.5, 0.5, 0.1, 0.1} {
		t.Errorf("item 2 = %+v", items[1])
	}
	img := q

	if _, err := s.RecordPush(Push{Label: "v8", Parent: "v7", Scope: Scope{Version: "v7"},
		Changelog: []ChangeEntry{{1, "title bigger"}, {2, "pause cut"}}, Asks: []PushAsk{{3, "which track?"}}}); err != nil {
		t.Fatal(err)
	}
	general, _ := s.Ask(Ask{Version: "v7", Text: "too long overall?"})
	// v8 newest: only the unanswered questions; nothing answered.
	img.Version = "v8"
	items, err = s.Sidebar(img)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(items); !slices.Equal(got, []string{fmt.Sprintf("question %d", general.ID), "question 1"}) {
		t.Fatalf("v8 newest = %v", got)
	}
	qi := items[1]
	if qi.Text != "which track?" || qi.Note == nil || qi.Note.ID != 3 || qi.Note.Text != "three" ||
		qi.Status != StatusQuestion || qi.By != "agent" {
		t.Errorf("question item = %+v", qi)
	}
	// Notes recorded on v8 show only from takes after its push.
	s.Now = func() time.Time { return time.Now() }
	if _, err := s.Sync(Run{Version: "v8", Comments: []Comment{
		comment("2000-01-01_00-00-00", "S1", 0, 1, "before the push"),
		comment("2999-01-01_00-00-00", "S1", 0, 2, "after the push"),
	}}); err != nil {
		t.Fatal(err)
	}
	v8, _ := s.Version("v8")
	pushed, _ := ParseTime(v8.Created)
	items, _ = s.Sidebar(SidebarQuery{Version: "v8", Newest: true, PushedAt: pushed})
	var texts []string
	for _, it := range items {
		if it.Kind == "note" {
			texts = append(texts, it.Text)
		}
	}
	if !slices.Equal(texts, []string{"after the push"}) {
		t.Errorf("v8 newest notes = %v", texts)
	}

	// v7 older: its notes in their final state, plus the question about v7 as a whole.
	items, err = s.Sidebar(SidebarQuery{Version: "v7"})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(items); !slices.Equal(got, []string{fmt.Sprintf("question %d", general.ID), "note 1", "note 2", "note 3"}) {
		t.Fatalf("v7 older = %v", got)
	}
	if it := items[1]; it.Status != StatusAnswered || it.Change != "title bigger" || it.AnsweredIn != "v8" {
		t.Errorf("answered item = %+v", it)
	}
	if it := items[3]; it.Status != StatusQuestion || it.Question != "which track?" {
		t.Errorf("asked item = %+v", it)
	}
	b, _ := json.Marshal(items[3])
	if strings.Contains(string(b), "change") || strings.Contains(string(b), "images") {
		t.Errorf("asked item JSON has answer fields: %s", b)
	}
}

func itemIDs(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = fmt.Sprintf("%s %d", it.Kind, it.ID)
	}
	return out
}

func TestAttachImages(t *testing.T) {
	dir := t.TempDir()
	images := `[
	  {"id": 1, "seg": "S1", "file": "img_01.png", "video_t": 1.5, "box": [0.1, 0.1, 0.2, 0.2]},
	  {"id": 2, "seg": "S1", "file": "img_02.webp", "video_t": 9, "box": [0, 0, 1, 1]},
	  {"id": 3, "seg": "S2", "file": "img_03.gif", "video_t": 3},
	  {"id": 4, "seg": "S9", "file": "img_04.png", "video_t": 3},
	  {"id": 5, "seg": "S1", "file": "../escape.png", "video_t": 1}
	]`
	if err := os.WriteFile(filepath.Join(dir, "images.json"), []byte(images), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := []Note{
		{ID: 1, Take: "t", Seg: "S1", T0: 1, T1: 2},
		{ID: 2, Take: "t", Seg: "S1", T0: 4, T1: 6},
		{ID: 3, Take: "t", Seg: "S2", T0: 0, T1: 1},
		{ID: 4, Take: "other", Seg: "S1", T0: 0, T1: 1},
	}
	got := AttachImages(notes, func(n Note) string {
		if n.Take == "t" {
			return dir
		}
		return ""
	})
	files := func(id int64) []string {
		var out []string
		for _, img := range got[id] {
			out = append(out, img.File)
		}
		return out
	}
	if !slices.Equal(files(1), []string{"img_01.png"}) || !slices.Equal(files(2), []string{"img_02.webp"}) ||
		!slices.Equal(files(3), []string{"img_03.gif"}) || len(got[4]) != 0 {
		t.Fatalf("images = %+v", got)
	}
	if got[1][0].Box != [4]float64{0.1, 0.1, 0.2, 0.2} {
		t.Errorf("box = %v", got[1][0].Box)
	}
	for _, name := range []string{"img_01.png", "img_123.jpg", "img_07.webp", "img_99.gif"} {
		if !ValidImageName(name) {
			t.Errorf("%s should be valid", name)
		}
	}
	for _, name := range []string{"img_1.png", "img_01.jpeg", "img_01.png.exe", "x/img_01.png", "img_01.PNG"} {
		if ValidImageName(name) {
			t.Errorf("%s should be invalid", name)
		}
	}
}
