package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/project"
)

// takeNotes is a notes engine that makes one comment per finished take of the version, once gate is closed.
type takeNotes struct {
	mu    sync.Mutex
	takes []string
	gate  chan struct{}
}

func (n *takeNotes) add(take string) {
	n.mu.Lock()
	n.takes = append(n.takes, take)
	n.mu.Unlock()
}

func (n *takeNotes) Build(ctx context.Context, v family.Version, _ []family.Version) (project.Run, error) {
	select {
	case <-n.gate:
	case <-ctx.Done():
		return project.Run{}, ctx.Err()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	run := project.Run{Version: v.Label}
	for i, tk := range n.takes {
		run.Comments = append(run.Comments, project.Comment{Take: tk, Seg: "S1", N: 1, T0: float64(i), T1: float64(i) + 1,
			Text: "note from " + tk})
	}
	return run, nil
}

// sender is one way into the page that can record: who, the Host it comes through and its headers.
type sender struct {
	name, host string
	hdr        map[string]string
}

func (h *harness) as(s sender, method, path, body string) resp {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Host = s.host
	for k, v := range s.hdr {
		req.Header.Set(k, v)
	}
	res, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out resp
	out.code, out.header = res.StatusCode, res.Header
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, e := res.Body.Read(buf)
		b.Write(buf[:n])
		if e != nil {
			break
		}
	}
	out.body = []byte(b.String())
	return out
}

// Everyone who can record sends (spec/automation.md): the owner, a reviewer's and an approver's token, and a
// reviewer who came through a share link. With no computer online the comments queue, show as being sent while
// they're processed, and start when a computer connects. A viewer can't send.
func TestEveryRecordingRoleSendsWithNoComputerOnline(t *testing.T) {
	notes := &takeNotes{gate: make(chan struct{})}
	engines := &jobs.Engines{
		Transcriber: func() (jobs.TakeTranscriber, error) { return takeFunc(func(string) error { return nil }), nil },
		Notes:       notes,
	}
	h := startTeam(t, Options{Jobs: engines, EventPoll: 5 * time.Millisecond})
	local := fmt.Sprintf("127.0.0.1:%d", h.port())
	origin := "http://" + local
	bearer := func(role string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + makeToken(t, h, role, role, time.Hour), "Origin": origin}
	}
	// The share link sets its cookie; the page then sends with it, from the share's origin.
	link := makeToken(t, h, "reviewer", "alex", time.Hour)
	got := h.req("GET", "/s/"+link, "share.test", nil)
	cookies := (&http.Response{Header: got.header}).Cookies()
	if got.code != 200 || len(cookies) != 1 {
		t.Fatalf("share link: %d %q", got.code, got.header.Values("Set-Cookie"))
	}
	senders := []sender{
		{"owner", local, map[string]string{"Origin": origin}},
		{"reviewer token", local, bearer("reviewer")},
		{"approver token", local, bearer("approver")},
		{"share-link reviewer", "share.test", map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value,
			"Origin": "https://share.test"}},
	}
	viewer := sender{"viewer", local, bearer("viewer")}

	takes := map[string]string{}
	for i, s := range senders {
		tk := fmt.Sprintf("2026-11-01_10-00-0%d", i)
		takes[s.name] = tk
		body := `{"session": {"format": 3}, "segments": [{"id": "S1"}], "strokes": []}`
		if r := h.as(s, "POST", "/api/session/"+tk+"/data?v=v6", body); r.code != 200 {
			t.Fatalf("%s saving a take: %d %s", s.name, r.code, r.body)
		}
		notes.add(tk)
		if r := h.as(s, "POST", "/api/session/"+tk+"/done?v=v6", `{"send":true}`); r.code != 200 {
			t.Fatalf("%s sending: %d %s", s.name, r.code, r.body)
		}
	}
	if r := h.as(viewer, "POST", "/api/session/"+takes["owner"]+"/done?v=v6", `{"send":true}`); r.code != http.StatusForbidden {
		t.Fatalf("a viewer finished a take: %d", r.code)
	}
	if r := h.as(viewer, "POST", "/api/automation/tasks", `{"notes":[1]}`); r.code != http.StatusForbidden {
		t.Fatalf("a viewer sent: %d", r.code)
	}

	// While their notes are processed, every sender's take is on its way: the page shows its comments Working.
	var view struct {
		Local   bool              `json:"local"`
		Workers []json.RawMessage `json:"workers"`
		Sending []project.TakeRef `json:"sending"`
		Tasks   []project.EditingTask
	}
	for _, s := range senders {
		r := h.as(s, "GET", "/api/automation", "")
		if r.code != 200 || json.Unmarshal(r.body, &view) != nil {
			t.Fatalf("%s reading the agent: %d %s", s.name, r.code, r.body)
		}
		if view.Local || len(view.Workers) != 0 || len(view.Sending) != len(senders) {
			t.Fatalf("%s sees %s", s.name, r.body)
		}
		for _, other := range senders {
			if !slices.Contains(view.Sending, project.TakeRef{Version: "v6", Take: takes[other.name]}) {
				t.Fatalf("%s's take isn't sending: %s", other.name, r.body)
			}
		}
	}

	// The notes are made, and they queue for the agent with no computer to poll: one batch, however many sent.
	close(notes.gate)
	st, err := h.srv.store(false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sent notes to queue", func() bool {
		a, e := st.Automation()
		return e == nil && len(a.Tasks) == 1 && len(a.Tasks[0].Notes) == len(senders)
	})
	ns, err := st.Notes(project.NoteFilter{})
	if err != nil || len(ns) != len(senders) {
		t.Fatalf("notes: %+v %v", ns, err)
	}
	a, _ := st.Automation()
	for _, n := range ns {
		if !slices.ContainsFunc(a.Tasks, func(j project.EditingTask) bool { return j.State == "queued" && slices.Contains(j.Notes, n.ID) }) {
			t.Fatalf("note %d (%s) isn't queued: %+v", n.ID, n.Take, a.Tasks)
		}
	}
	if r := h.get("/api/automation", nil); !strings.Contains(string(r.body), `"sending":[]`) {
		t.Fatalf("processed takes still sending: %s", r.body)
	}

	// A paused task can be retried by anyone who records, with its own agent; switching and pausing stay the owner's.
	first := a.Tasks[0].ID
	if r := h.as(senders[0], "POST", "/api/automation/tasks/"+first+"/pause", `{}`); r.code != 200 {
		t.Fatalf("owner pause: %d %s", r.code, r.body)
	}
	for _, s := range senders[1:] {
		for _, action := range []string{"pause", "switch"} {
			if r := h.as(s, "POST", "/api/automation/tasks/"+first+"/"+action, `{"agent":"codex"}`); r.code != http.StatusForbidden {
				t.Fatalf("%s could %s: %d", s.name, action, r.code)
			}
		}
	}
	if r := h.as(viewer, "POST", "/api/automation/tasks/"+first+"/retry", `{}`); r.code != http.StatusForbidden {
		t.Fatalf("a viewer retried: %d", r.code)
	}
	share := senders[3]
	if r := h.as(share, "POST", "/api/automation/tasks/"+first+"/retry", `{"agent":"untrusted"}`); r.code != 200 {
		t.Fatalf("share-link reviewer retry: %d %s", r.code, r.body)
	}
	if a, _ = st.Automation(); a.Task(first).State != "queued" || a.Task(first).Agent != "" {
		t.Fatalf("retry: %+v", a.Task(first))
	}
	// The owner's Send of a comment that is already queued changes nothing.
	if r := h.as(senders[0], "POST", "/api/automation/tasks", fmt.Sprintf(`{"notes":[%d]}`, ns[0].ID)); r.code != 200 {
		t.Fatalf("send again: %d %s", r.code, r.body)
	}
	if a, _ = st.Automation(); len(a.Tasks) != 1 || len(a.Tasks[0].Notes) != len(senders) {
		t.Fatalf("a queued note was queued twice: %+v", a.Tasks)
	}

	// A computer connects: the first queued task starts, with its first agent, and nobody pressed anything.
	agent := map[string]string{"Authorization": "Bearer " + makeToken(t, h, "agent", "worker", 0)}
	poll := `{"worker":{"id":"machine","session":"session","name":"studio","agents":[{"id":"claude","name":"Claude Code"}]}}`
	got = h.post("/api/worker/poll", poll, agent)
	var work project.WorkAssignment
	if got.code != 200 || json.Unmarshal(got.body, &work) != nil || work.Task == nil || work.Task.Agent != "claude" {
		t.Fatalf("the queue didn't start when the computer connected: %d %s", got.code, got.body)
	}
}
