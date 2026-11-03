package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// POST /api/notes/<id>/edit (spec/notes.md, "Editing a comment"): the comment's author, approvers and the owner;
// origin-checked; the same words again change nothing; a comment that isn't to do (Done, a question, deleted) can't
// change; the sidebar says which comments this request may edit, and which a person edited.
func TestEditCommentRoute(t *testing.T) {
	h, f := startM2(t, Options{})
	st := seed(t, f) // #1 and #2 answered, #3 asked about: Ana's
	ana, err := st.AddTyped(project.TypedNote{Version: "v6", Text: "louder music", Author: "Ana", Video: true, At: 7,
		Wants: project.WantsVersion})
	if err != nil {
		t.Fatal(err)
	}
	ben, err := st.AddTyped(project.TypedNote{Version: "v6", Text: "shorter intro", Author: "Ben", Video: true, At: 8,
		Wants: project.WantsVersion})
	if err != nil {
		t.Fatal(err)
	}
	bearer := func(role, label string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + makeToken(t, h, role, label, 0)}
	}
	path := func(id int64) string { return "/api/notes/" + strconv.FormatInt(id, 10) + "/edit" }
	body := func(text string) string { b, _ := json.Marshal(map[string]string{"text": text}); return string(b) }
	words := func(id int64) string { n, _ := st.Note(id); return n.Text }

	for _, who := range []map[string]string{bearer("viewer", "Ana"), bearer("agent", "Ana"), bearer("reviewer", "Ben")} {
		if r := h.post(path(ana.ID), body("x"), who); r.code != http.StatusForbidden {
			t.Fatalf("someone else: %d %s", r.code, r.body)
		}
	}
	if r := h.post(path(ana.ID), body("x"), map[string]string{"Origin": "http://elsewhere.example"}); r.code != http.StatusForbidden {
		t.Fatal("origin:", r.code)
	}
	if words(ana.ID) != "louder music" {
		t.Fatal("a refused edit changed it:", words(ana.ID))
	}
	// Its author, under her own name.
	r := h.post(path(ana.ID), body("  a little louder music "), bearer("reviewer", "Ana"))
	if r.code != 200 || r.json(t)["text"] != "a little louder music" || words(ana.ID) != "a little louder music" {
		t.Fatalf("the author: %d %s", r.code, r.body)
	}
	// The same words: 200, nothing changes.
	before, _ := st.Note(ana.ID)
	if r = h.post(path(ana.ID), body("a little louder music"), h.origin()); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	if after, _ := st.Note(ana.ID); after.Updated != before.Updated {
		t.Fatal("the same words changed the note")
	}
	// An approver and the owner edit anyone's.
	if r = h.post(path(ben.ID), body("cut the intro to 3 s"), bearer("approver", "Zed")); r.code != 200 {
		t.Fatal("an approver:", r.code, string(r.body))
	}
	if r = h.post(path(ben.ID), body("cut the intro to 2 s"), h.origin()); r.code != 200 || words(ben.ID) != "cut the intro to 2 s" {
		t.Fatal("the owner:", r.code, string(r.body))
	}
	// Not to do any more, unknown, bad bodies.
	for id, code := range map[int64]int{1: 409, 3: 409, 99: 404} {
		if r = h.post(path(id), body("x"), h.origin()); r.code != code {
			t.Fatalf("#%d: %d %s", id, r.code, r.body)
		}
	}
	for _, b := range []string{"", "{", `{"words":"x"}`, `{"text":7}`} {
		if r = h.post(path(ana.ID), b, h.origin()); r.code != 400 {
			t.Fatalf("body %q: %d", b, r.code)
		}
	}
	if r = h.post(path(ana.ID), body(""), h.origin()); r.code != 400 || !strings.Contains(string(r.body), "needs words") {
		t.Fatal("no words, no drawing:", r.code, string(r.body))
	}
	if r = h.post(path(ana.ID), body(strings.Repeat("x", project.MaxDocNote+1)), h.origin()); r.code != 400 {
		t.Fatal("too long:", r.code)
	}

	// The sidebar: edited, and editable for whoever may.
	items := func(hdr map[string]string) map[int64]project.Item {
		var resp sidebarResponse
		if r := h.get("/api/notes?v=v6&scope=video", hdr); r.code != 200 || json.Unmarshal(r.body, &resp) != nil {
			t.Fatal(r.code, string(r.body))
		}
		out := map[int64]project.Item{}
		for _, it := range resp.Items {
			if it.Kind == "note" {
				out[it.ID] = it
			}
		}
		return out
	}
	mine, owner, viewer := items(bearer("reviewer", "Ana")), items(nil), items(bearer("viewer", "Ana"))
	if !mine[ana.ID].Editable || mine[ben.ID].Editable || !mine[ana.ID].Edited || mine[ana.ID].Text != "a little louder music" {
		t.Fatalf("Ana's view: %+v %+v", mine[ana.ID], mine[ben.ID])
	}
	if !owner[ana.ID].Editable || !owner[ben.ID].Editable || owner[1].Editable || owner[3].Editable {
		t.Fatalf("the owner's view: %+v", owner)
	}
	if viewer[ana.ID].Editable {
		t.Fatal("a viewer may edit")
	}

	// Deleted: not editable, and the words stay.
	if r = h.post("/api/notes/"+strconv.FormatInt(ben.ID, 10)+"/delete", "", h.origin()); r.code != 200 {
		t.Fatal(r.code)
	}
	if r = h.post(path(ben.ID), body("again"), h.origin()); r.code != 409 || !strings.Contains(string(r.body), "deleted") {
		t.Fatal("a deleted comment:", r.code, string(r.body))
	}
	if words(ben.ID) != "cut the intro to 2 s" {
		t.Fatal(words(ben.ID))
	}
}

// The agent reads the edited words: a worker that picks up a task gets them in its notes (its context), and so does
// every later poll while it runs, so a note it hasn't reached yet carries the new words.
func TestWorkerGetsEditedWords(t *testing.T) {
	h, _ := startM2(t, Options{})
	agent := map[string]string{"Authorization": "Bearer " + makeToken(t, h, "agent", "worker", 0)}
	poll := `{"worker":{"id":"machine","session":"session","name":"Test","agents":[{"id":"fake","name":"Fake"}]}}`
	if r := h.post("/api/worker/poll", poll, agent); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	if r := h.post("/api/automation", `{"enabled":true,"worker":"machine","agent":"fake"}`, nil); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "a", Text: "make it blue"},
		{Key: "b", Text: "trim the pause"}}}); err != nil {
		t.Fatal(err)
	}
	if r := h.post("/api/notes/1/edit", `{"text":"make the title blue"}`, h.origin()); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	if r := h.post("/api/automation/tasks", `{"notes":[1,2]}`, nil); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	var task, lease string
	words := func() map[int64]string {
		body := poll
		if task != "" { // the running attempt's heartbeat
			body = strings.TrimSuffix(poll, "}") + `,"task":"` + task + `","lease":"` + lease + `"}`
		}
		r := h.post("/api/worker/poll", body, agent)
		var a project.WorkAssignment
		if r.code != 200 || json.Unmarshal(r.body, &a) != nil || a.Task == nil || !slices.Equal(a.Task.Notes, []int64{1, 2}) {
			t.Fatal(r.code, string(r.body))
		}
		task, lease = a.Task.ID, a.Task.Lease
		out := map[int64]string{}
		for _, n := range a.Notes {
			out[n.ID] = n.Text
			if n.ID == 1 && !n.Edited {
				t.Fatal("an edited note isn't marked edited")
			}
		}
		return out
	}
	if got := words(); got[1] != "make the title blue" || got[2] != "trim the pause" {
		t.Fatal("picked up:", got)
	}
	// While it runs, a note the agent hasn't reached changes: the next poll has the new words.
	if r := h.post("/api/notes/2/edit", `{"text":"trim the pause to half a second"}`, h.origin()); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	if got := words(); got[2] != "trim the pause to half a second" {
		t.Fatal("while running:", got)
	}
}
