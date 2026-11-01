package server

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// The review page keeps each comment on the version it was made on (spec/notes.md, "Comments belong to their
// version"): it reads the family's comments once, each tagged with its version and on its own clock, including a
// newer version's, so it can count what is to do on every version.
func TestFamilyCommentsKeepTheirVersionAndClock(t *testing.T) {
	h, _ := startM2(t, Options{})
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v5", Comments: []project.Comment{{Key: "a", T0: 4, T1: 5, Text: "v5 intro"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "b", T0: 20, T1: 21, Text: "v6 title"}}}); err != nil {
		t.Fatal(err)
	}
	r := h.get("/api/notes?v=v5&scope=video", nil)
	var res sidebarResponse
	if err := json.Unmarshal(r.body, &res); err != nil || r.code != 200 {
		t.Fatalf("%d %s", r.code, r.body)
	}
	got := map[string][2]any{}
	for _, it := range res.Items {
		got[it.Text] = [2]any{it.Version, *it.T0}
	}
	want := map[string][2]any{"v5 intro": {"v5", 4.0}, "v6 title": {"v6", 20.0}}
	if res.Version != "v5" || len(got) != 2 || got["v5 intro"] != want["v5 intro"] || got["v6 title"] != want["v6 title"] {
		t.Fatalf("got %v (version %s), want %v", got, res.Version, want)
	}
}

// Send sends the shown version's comments, but the agent still gets every open comment of every version as context
// (spec/automation.md, the poll answer's `notes`).
func TestTaskContextHasEveryVersionsOpenComments(t *testing.T) {
	h := startTeam(t, Options{})
	agent := makeToken(t, h, "agent", "worker", 0)
	reviewer := makeToken(t, h, "reviewer", "reviewer", 0)
	headers := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	poll := `{"worker":{"id":"machine","session":"s","name":"computer","agents":[{"id":"codex","name":"Codex"}]}}`
	if got := h.post("/api/worker/poll", poll, headers(agent)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	if got := h.post("/api/automation", `{"worker":"machine","agent":"codex"}`, nil); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v5", Comments: []project.Comment{{Key: "old", Text: "older version, still open"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "new", Text: "this version"}}}); err != nil {
		t.Fatal(err)
	}
	ns, err := st.Notes(project.NoteFilter{})
	if err != nil || len(ns) != 2 {
		t.Fatal(ns, err)
	}
	var sent int64
	for _, n := range ns {
		if n.Version == "v6" {
			sent = n.ID
		}
	}
	body, _ := json.Marshal(map[string]any{"notes": []int64{sent}})
	if got := h.post("/api/automation/tasks", string(body), headers(reviewer)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	got := h.post("/api/worker/poll", poll, headers(agent))
	var a project.WorkAssignment
	if err := json.Unmarshal(got.body, &a); err != nil || a.Task == nil {
		t.Fatal(got.code, string(got.body), err)
	}
	if !slices.Equal(a.Task.Notes, []int64{sent}) {
		t.Fatalf("task notes %v, want [%d]", a.Task.Notes, sent)
	}
	var texts []string
	for _, n := range a.Notes {
		texts = append(texts, n.Version+": "+n.Text)
	}
	slices.Sort(texts)
	if !slices.Equal(texts, []string{"v5: older version, still open", "v6: this version"}) {
		t.Fatalf("context notes %q", texts)
	}
}

// A comment recorded on a draft stays once the family stops listing that draft (the draft became its version, or a
// newer draft replaced it): it comes with the family's comments, tagged with the draft's label, so the page keeps it
// on the version the draft headed for.
func TestCommentsOnALeftDraftStayWithTheirVersion(t *testing.T) {
	h, _ := startM2(t, Options{})
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v6-draft2", Comments: []project.Comment{{Key: "d", T0: 7, T1: 8, Text: "said on the draft"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v9-draft1", Comments: []project.Comment{{Key: "x", T0: 1, T1: 2, Text: "a draft of nothing listed"}}}); err != nil {
		t.Fatal(err)
	}
	texts := func(path string) map[string]string {
		r := h.get(path, nil)
		var res sidebarResponse
		if err := json.Unmarshal(r.body, &res); err != nil || r.code != 200 {
			t.Fatalf("%s: %d %s", path, r.code, r.body)
		}
		out := map[string]string{}
		for _, it := range res.Items {
			out[it.Text] = it.Version
		}
		return out
	}
	if got := texts("/api/notes?v=v6&scope=video"); got["said on the draft"] != "v6-draft2" || len(got) != 1 {
		t.Fatalf("video scope: %v", got)
	}
	// The integrations' review scope still leaves out what's newer than the version asked for.
	if got := texts("/api/notes?v=v5&scope=review"); len(got) != 0 {
		t.Fatalf("review scope of v5: %v", got)
	}
	if got := texts("/api/notes?v=v6&scope=review"); got["said on the draft"] != "v6-draft2" {
		t.Fatalf("review scope of v6: %v", got)
	}
}
