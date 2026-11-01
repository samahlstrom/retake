package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// A question from inside a task over the worker routes (hosted, with a machine token): it is stored with its
// visuals, its images are served to viewers, the comment carries it under itself in the sidebar (not as its own
// item), and a reviewer's typed answer goes back to the task.
func TestWorkerAskWithVisualsReachesThePageAndTheAnswerTheTask(t *testing.T) {
	h := startTeam(t, Options{})
	agent := makeToken(t, h, "agent", "worker", 0)
	reviewer := makeToken(t, h, "reviewer", "reviewer", 0)
	viewer := makeToken(t, h, "viewer", "viewer", 0)
	as := func(token string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + token, "X-Retake-Worker": "machine", "X-Retake-Session": "session"}
	}
	poll := `{"worker":{"id":"machine","session":"session","agents":[{"id":"codex"}]}}`
	if got := h.post("/api/worker/poll", poll, as(agent)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	st, e := h.srv.store(true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "note", Text: "make the title pop", T0: 2, T1: 3}}}); e != nil {
		t.Fatal(e)
	}
	if got := h.post("/api/automation/tasks", `{"notes":[1]}`, as(reviewer)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	got := h.post("/api/worker/poll", poll, as(agent))
	var a project.WorkAssignment
	if e = json.Unmarshal(got.body, &a); e != nil || a.Task == nil {
		t.Fatal(string(got.body), e)
	}
	var pic bytes.Buffer
	if e = png.Encode(&pic, image.NewGray(image.Rect(0, 0, 3, 2))); e != nil {
		t.Fatal(e)
	}
	b64 := base64.StdEncoding.EncodeToString(pic.Bytes())
	body := fmt.Sprintf(`{"task":%q,"lease":%q,"at":2.5,"text":"Which title?","boxes":[{"box":[0.1,0.1,0.4,0.2],"label":"this"}],
		"arrows":[{"from":[0.9,0.9],"to":[0.5,0.3]}],"options":[{"text":"Bold","image":{"name":"bold.png","data":%q}},{"text":"Thin"}]}`,
		a.Task.ID, a.Task.Lease, b64)
	if got = h.post("/api/worker/ask", body, as(reviewer)); got.code == 200 {
		t.Fatal("a reviewer asked as the worker")
	}
	if got = h.post("/api/worker/ask", strings.Replace(body, a.Task.Lease, "stale", 1), as(agent)); got.code != 409 {
		t.Fatal("stale lease:", got.code, string(got.body))
	}
	if got = h.post("/api/worker/ask", strings.Replace(body, `"label":"this"`, `"label":"`+strings.Repeat("x", 50)+`"`, 1), as(agent)); got.code != 409 ||
		!strings.Contains(string(got.body), "label") {
		t.Fatal("long label:", got.code, string(got.body))
	}
	got = h.post("/api/worker/ask", body, as(agent))
	var q project.TaskQuestion
	if e = json.Unmarshal(got.body, &q); got.code != 200 || e != nil || q.Note != 1 || q.Version != "v6" || q.At != 2.5 {
		t.Fatal(got.code, string(got.body), e)
	}
	// The poll gives the worker the task's questions, with their visuals.
	got = h.post("/api/worker/poll", fmt.Sprintf(`{"worker":{"id":"machine","session":"session","agents":[{"id":"codex"}]},"task":%q,"lease":%q}`, a.Task.ID, a.Task.Lease), as(agent))
	if e = json.Unmarshal(got.body, &a); e != nil || len(a.Questions) != 1 || a.Questions[0].Visuals == nil || len(a.Questions[0].Visuals.Options) != 2 {
		t.Fatal(string(got.body), e)
	}
	// The page: the comment carries the question; its images are served to anyone who can watch.
	notes := h.get("/api/notes?v=v6&scope=review", as(viewer))
	var res struct {
		Items []project.Item `json:"items"`
	}
	if e = json.Unmarshal(notes.body, &res); e != nil || len(res.Items) != 1 {
		t.Fatal(string(notes.body), e)
	}
	it := res.Items[0]
	if it.Kind != "note" || it.Status != project.StatusQuestion || it.Ask == nil || it.Ask.ID != q.ID || it.Ask.Task != a.Task.ID ||
		it.Ask.T != 2.5 || it.Ask.Visuals == nil || it.Ask.Visuals.Boxes[0].Label != "this" || it.Ask.Visuals.Options[0].Image == nil {
		t.Fatal(string(notes.body))
	}
	url := it.Ask.Visuals.Options[0].Image.URL
	if img := h.get(url, as(viewer)); img.code != 200 || !bytes.Equal(img.body, pic.Bytes()) {
		t.Fatal(url, img.code)
	}
	for _, bad := range []string{fmt.Sprintf("/api/questions/%d/visuals.json", q.ID), fmt.Sprintf("/api/questions/%d/..%%2fproject.db", q.ID), "/api/questions/0/frame.jpg"} {
		if got := h.get(bad, as(viewer)); got.code != 404 {
			t.Fatal(bad, got.code)
		}
	}
	// A typed answer (a tapped choice sends its text) goes to the same task; the worker hears of it in its poll.
	if got = h.post("/api/automation/tasks/"+a.Task.ID+"/answer", fmt.Sprintf(`{"question":%d,"message":"Bold"}`, q.ID), as(viewer)); got.code != http.StatusForbidden {
		t.Fatal("a viewer answered", got.code)
	}
	if got = h.post("/api/automation/tasks/"+a.Task.ID+"/answer", fmt.Sprintf(`{"question":%d,"message":"Bold"}`, q.ID), as(reviewer)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	got = h.post("/api/worker/poll", fmt.Sprintf(`{"worker":{"id":"machine","session":"session","agents":[{"id":"codex"}]},"task":%q,"lease":%q}`, a.Task.ID, a.Task.Lease), as(agent))
	if e = json.Unmarshal(got.body, &a); e != nil || a.Questions[0].Answer == nil || a.Questions[0].Answer.Text != "Bold" || a.Task.State != "running" {
		t.Fatal(string(got.body), e)
	}
	if n, _ := st.Note(1); n.Status != project.StatusOpen {
		t.Fatalf("answered note is %s", n.Status)
	}
}
