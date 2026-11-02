package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// A typed comment on a video (spec/notes.md, "Typed comments on a video"): its time, its pen lines and a still, no
// recording. It is a note like any other: in the sidebar with its lines, its still served, and it wants a version.
func TestTypedCommentOnAVideo(t *testing.T) {
	h, f := startM2(t, Options{})
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte("a still")...)
	still := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
	circle := make([][2]float64, 0, 300)
	for i := range 300 {
		circle = append(circle, [2]float64{0.5 + 0.1*math.Cos(2*math.Pi*float64(i)/299), 0.5 + 0.1*math.Sin(2*math.Pi*float64(i)/299)})
	}
	pts, _ := json.Marshal(circle)
	body := fmt.Sprintf(`{"version": "v6", "t": 4.25, "text": "  Hold this shot longer  ", "frame": [1920, 1080],
		"strokes": [{"color": "yellow", "points": %s, "t": 4.25}], "snapshot": %q}`, pts, still)
	r := h.post("/api/notes", body, h.origin())
	if r.code != 201 {
		t.Fatalf("typed comment: %d %s", r.code, r.body)
	}
	var item project.Item
	if err := json.Unmarshal(r.body, &item); err != nil {
		t.Fatal(err)
	}
	if item.Key != "doc/1" || item.Text != "Hold this shot longer" || *item.T0 != 4.25 || *item.T1 != 4.25 ||
		item.Version != "v6" || item.Wants != project.WantsVersion || item.Anchor != nil {
		t.Fatalf("item: %s", r.body)
	}
	if len(item.Strokes) != 1 || item.Strokes[0].Color != "yellow" || item.Strokes[0].Shape != "circle" ||
		len(item.Strokes[0].Points) != project.MaxStrokePoints || item.Strokes[0].T != 4.25 {
		t.Fatalf("its line: %+v", item.Strokes)
	}
	if b := item.Strokes[0].Box; b[0] < 0.39 || b[0] > 0.41 || b[2] < 0.19 || b[2] > 0.21 {
		t.Fatalf("its box: %v", b)
	}
	// Its still is in the project, and the sidebar serves it with the comment and its lines.
	if b, err := os.ReadFile(filepath.Join(f.root, ".retake", "typed", "doc-1.jpg")); err != nil || string(b) != string(jpeg) {
		t.Fatalf("the still: %q %v", b, err)
	}
	var side sidebarResponse
	if err := json.Unmarshal(h.get("/api/notes?v=v6&scope=video", nil).body, &side); err != nil || len(side.Items) != 1 {
		t.Fatalf("sidebar: %+v %v", side, err)
	}
	got := side.Items[0]
	if got.Snapshot == "" || len(got.Strokes) != 1 || got.Drawings[0] != "yellow" || got.Status != project.StatusOpen {
		t.Fatalf("sidebar item: %+v", got)
	}
	if s := h.get(got.Snapshot, nil); s.code != 200 || s.header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("snapshot: %d %s", s.code, s.header.Get("Content-Type"))
	}
	// A drawing alone is a comment; a second one gets the next key, and no still is fine.
	r = h.post("/api/notes", `{"version": "v6", "t": 1, "strokes": [{"color": "red", "points": [[0.1, 0.1], [0.3, 0.2]], "t": 1}]}`, h.origin())
	if r.code != 201 || !strings.Contains(string(r.body), `"key":"doc/2"`) || strings.Contains(string(r.body), "snapshot") {
		t.Fatalf("a drawing alone: %d %s", r.code, r.body)
	}
	st, err := h.srv.store(false)
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.Note(1)
	if err != nil || !n.WantsVersion() || n.Detail.Snapshot != ".retake/typed/doc-1.jpg" {
		t.Fatalf("stored note: %+v %v", n, err)
	}

	// Everything invalid is refused with the reason.
	for _, c := range []struct{ body, want string }{
		{`{"version": "v6", "text": "x"}`, "needs its time"},
		{`{"version": "v6", "t": 99, "text": "x"}`, "past the end"},
		{`{"version": "v6", "t": -1, "text": "x"}`, "0 or more"},
		{`{"version": "v6", "t": 1}`, "needs words or a drawing"},
		{`{"version": "v6", "t": 1, "text": "   "}`, "needs words or a drawing"},
		{`{"version": "v6", "t": 1, "text": "x", "strokes": [{"color": "blue", "points": [[0.1, 0.1]]}]}`, "isn't red"},
		{`{"version": "v6", "t": 1, "text": "x", "strokes": [{"color": "red", "points": [[1.5, 0.1]]}]}`, "0 to 1"},
		{`{"version": "v6", "t": 1, "text": "x", "strokes": [{"color": "red", "points": []}]}`, "1 to 2000 points"},
		{`{"version": "v6", "t": 1, "text": "x", "snapshot": "data:image/gif;base64,R0lG"}`, "data:image/jpeg"},
		{`{"version": "v6", "t": 1, "text": "x", "snapshot": "data:image/jpeg;base64,iVBORw0KGgo="}`, "data:image/jpeg"},
		{`{"version": "v6", "t": 1, "text": "x", "anchor": {"file": "v0/a.md", "line": 1}}`, "an anchor is for"},
		{`{"version": "v9", "t": 1, "text": "x"}`, "no such version"},
		{fmt.Sprintf(`{"version": "v6", "t": 1, "text": %q}`, strings.Repeat("x", 4001)), "up to 4000"},
		{`not json`, "bad json"},
	} {
		if r := h.post("/api/notes", c.body, h.origin()); r.code != 400 || !strings.Contains(string(r.body), c.want) {
			t.Errorf("%s: %d %s (want %q)", c.body, r.code, r.body, c.want)
		}
	}
	if r := h.post("/api/notes", `{"version": "v6", "t": 1, "text": "x"}`, map[string]string{"Origin": "http://evil.example"}); r.code != 403 {
		t.Fatalf("another origin: %d", r.code)
	}
}

// A send may pick the agent (spec/automation.md, "Choosing the agent for one send"): one the selected computer
// offers goes with the task, anything else falls back to the configured agent.
func TestASendPicksItsAgent(t *testing.T) {
	h, _ := startM2(t, Options{})
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PollWork(project.EditingWorker{ID: "w1", Session: "s", Name: "studio",
		Agents: []project.AgentProfile{{ID: "claude", Name: "Claude Code"}, {ID: "codex", Name: "Codex"}}}, "", ""); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 3 {
		r := h.post("/api/notes", fmt.Sprintf(`{"version": "v6", "t": %d, "text": "note %d"}`, i, i), h.origin())
		var it project.Item
		if r.code != 201 || json.Unmarshal(r.body, &it) != nil {
			t.Fatalf("note: %d %s", r.code, r.body)
		}
		ids = append(ids, it.ID)
	}
	for i, c := range []struct{ agent, want string }{{"codex", "codex"}, {"untrusted", "claude"}, {"", "claude"}} {
		body := fmt.Sprintf(`{"notes": [%d], "agent": %q}`, ids[i], c.agent)
		if r := h.post("/api/automation/tasks", body, h.origin()); r.code != 200 {
			t.Fatalf("send: %d %s", r.code, r.body)
		}
		a, _ := st.Automation()
		if got := a.Tasks[len(a.Tasks)-1]; got.Agent != c.want {
			t.Fatalf("agent %q: the task runs %q", c.agent, got.Agent)
		}
	}
	// A take finished with a picked agent keeps it for when its notes are ready.
	h.saveTake(take, "v6")
	if r := h.post("/api/session/"+take+"/done?v=v6", `{"send": true, "agent": "codex"}`, h.origin()); r.code != 200 {
		t.Fatalf("done: %d %s", r.code, r.body)
	}
	a, _ := st.Automation()
	if len(a.Submissions) != 1 || a.Submissions[0].Agent != "codex" || !a.Submissions[0].Send {
		t.Fatalf("submission: %+v", a.Submissions)
	}
}
