package project

import (
	"strings"
	"testing"
)

// Typed comments on an extension's version (spec/extensions.md, "Comments and questions on an extension's version").

func docFixture(t *testing.T) (*Store, EditingWorker) {
	t.Helper()
	s, _ := newStore(t)
	w := EditingWorker{ID: "machine", Session: "session", Agents: []AgentProfile{{ID: "claude"}}}
	if _, err := s.PollWork(w, "", ""); err != nil {
		t.Fatal(err)
	}
	return s, w
}

func TestAddDocNote(t *testing.T) {
	s, _ := docFixture(t)
	anchor := &Anchor{File: "v0/script.md", Line: 4, Heading: "Cold open", Quote: "So the idea"}
	n, err := s.AddDocNote("v0", "  Work this talk-out in  ", "Sam", anchor, "")
	if err != nil {
		t.Fatal(err)
	}
	if n.Key != "doc/1" || n.Version != "v0" || n.Status != StatusOpen || n.Text != "Work this talk-out in" ||
		n.Detail.Anchor == nil || n.Detail.Anchor.Heading != "Cold open" || !n.IsDoc() || n.WantsVersion() {
		t.Fatalf("%+v", n)
	}
	n2, err := s.AddDocNote("v0", "Build the first cut (v1) from script.md.", "Sam", nil, WantsVersion)
	if err != nil || n2.Key != "doc/2" || !n2.WantsVersion() {
		t.Fatalf("%+v %v", n2, err)
	}
	it := ItemOf(n)
	if it.Key != "doc/1" || it.Anchor == nil || it.Wants != "" || ItemOf(n2).Wants != "version" {
		t.Fatalf("item %+v", it)
	}
	for name, c := range map[string]struct {
		text, wants string
		anchor      *Anchor
	}{
		"empty":       {text: " "},
		"bad wants":   {text: "x", wants: "render"},
		"traversal":   {text: "x", anchor: &Anchor{File: "../x.md", Line: 1}},
		"hidden":      {text: "x", anchor: &Anchor{File: "v0/.history/x.md", Line: 1}},
		"line 0":      {text: "x", anchor: &Anchor{File: "v0/x.md"}},
		"long quote":  {text: "x", anchor: &Anchor{File: "v0/x.md", Line: 1, Quote: strings.Repeat("q", MaxQuote+1)}},
		"long note":   {text: strings.Repeat("n", MaxDocNote+1)},
		"absolute":    {text: "x", anchor: &Anchor{File: "/etc/passwd", Line: 1}},
		"backslashes": {text: "x", anchor: &Anchor{File: `v0\x.md`, Line: 1}},
	} {
		if _, err := s.AddDocNote("v0", c.text, "", c.anchor, c.wants); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A notes sync of v0 (no takes make these) never retracts them.
	if _, err := s.Sync(Run{Version: "v0"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Note(n.ID); got.Status != StatusOpen {
		t.Fatalf("sync retracted a typed comment: %s", got.Status)
	}
}

func TestDocNotesCompleteWithoutARender(t *testing.T) {
	s, w := docFixture(t)
	a, _ := s.AddDocNote("v0", "Shape it", "Sam", nil, "")
	b, _ := s.AddDocNote("v0", "And this", "Sam", nil, "")
	if _, err := s.QueueFeedback([]int64{a.ID, b.ID}, ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		j, err := s.PollWork(w, "", "")
		if err != nil || j == nil {
			t.Fatal(j, err)
		}
		if ok, _ := s.DocOnly(*j); !ok {
			t.Fatal("a task of typed comments is doc-only")
		}
		if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed"}, 0); err == nil {
			t.Fatal("a completion without a message")
		}
		if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed",
			Message: "Outline and script written"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{a.ID, b.ID} {
		n, _ := s.Note(id)
		if n.Status != StatusAnswered || n.AnsweredIn != "v0" || n.Change != "Outline and script written" {
			t.Fatalf("%+v", n)
		}
	}
}

func TestWantsVersionNeedsAVersion(t *testing.T) {
	s, w := docFixture(t)
	n, _ := s.AddDocNote("v0", "Build the first cut (v1) from script.md.", "Sam", nil, WantsVersion)
	if _, err := s.QueueFeedback([]int64{n.ID}, ""); err != nil {
		t.Fatal(err)
	}
	j, err := s.PollWork(w, "", "")
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	if ok, _ := s.DocOnly(*j); ok {
		t.Fatal("wants a version: not doc-only")
	}
	if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed",
		Message: "done"}, 0); err == nil {
		t.Fatal("completed without a version")
	}
	if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "v1 cut",
		Version: "v1", VersionChecked: true}, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Note(n.ID); got.Status != StatusAnswered || got.AnsweredIn != "v1" {
		t.Fatalf("%+v", got)
	}
}

func TestAskWithAnAnchor(t *testing.T) {
	s, w := docFixture(t)
	n, _ := s.AddDocNote("v0", "Shape it", "Sam", nil, "")
	if _, err := s.QueueFeedback([]int64{n.ID}, ""); err != nil {
		t.Fatal(err)
	}
	j, _ := s.PollWork(w, "", "")
	anchor := &Anchor{File: "v0/script.md", Line: 3, Heading: "Cold open"}
	at := 2.0
	if _, err := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which opening?", Anchor: anchor,
		At: &at}); err == nil {
		t.Fatal("a document question with a time")
	}
	q, err := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which opening?", Anchor: anchor,
		Options: []AgentOption{{Text: "Start on the jump"}, {Text: "Start on the shop"}}})
	if err != nil || q.Anchor == nil || q.Anchor.Heading != "Cold open" {
		t.Fatalf("%+v %v", q, err)
	}
	j2, _ := s.PollWork(w, j.ID, j.Lease)
	qs, err := s.TaskQuestions(*j2, func(int64, string) string { return "" })
	if err != nil || len(qs) != 1 || qs[0].Anchor == nil || qs[0].Anchor.File != "v0/script.md" || qs[0].Visuals == nil ||
		len(qs[0].Visuals.Options) != 2 {
		t.Fatalf("%+v %v", qs, err)
	}
	if got, _ := s.Note(n.ID); got.Status != StatusQuestion {
		t.Fatalf("%s", got.Status)
	}
}
