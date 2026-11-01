package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// call sends one request to an in-process worker handler, as the worker package's transport does: no token.
func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Retake-Worker", "machine")
	r.Header.Set("X-Retake-Session", "session")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// localServer starts a server whose LocalWorker hands over its worker handler and says it is blocked by why.
func localServer(t *testing.T, why string) (*harness, http.Handler, <-chan struct{}) {
	t.Helper()
	handler := make(chan http.Handler, 1)
	stopped := make(chan struct{})
	h := startTeam(t, Options{LocalWorker: func(ctx context.Context, root string, wh http.Handler, blocked func(string)) {
		defer close(stopped)
		if root == "" {
			t.Error("no project root")
		}
		blocked(why)
		handler <- wh
		<-ctx.Done()
	}})
	select {
	case wh := <-handler:
		return h, wh, stopped
	case <-time.After(5 * time.Second):
		t.Fatal("the local worker never started")
	}
	return nil, nil, nil
}

func getAutomation(t *testing.T, h *harness) map[string]any {
	t.Helper()
	got := h.get("/api/automation", nil)
	var v map[string]any
	if got.code != 200 || json.Unmarshal(got.body, &v) != nil {
		t.Fatalf("GET /api/automation: %d %s", got.code, got.body)
	}
	return v
}

func TestLocalWorkerRunsInProcessWithoutAToken(t *testing.T) {
	h, wh, stopped := localServer(t, "No agent found. Install Claude Code or Codex.")
	v := getAutomation(t, h)
	if v["local"] != true || v["blocked"] != "No agent found. Install Claude Code or Codex." {
		t.Fatalf("local view: %v", v)
	}
	h.srv.inproc.setReason("")
	if v = getAutomation(t, h); v["local"] != true || v["blocked"] != nil {
		t.Fatalf("running local worker still blocked: %v", v)
	}
	// The handler is the agent, without a token, and serves nothing but the worker routes.
	poll := `{"worker":{"id":"machine","session":"session","name":"sam-pc","agents":[{"id":"claude","name":"Claude Code"}]}}`
	if got := call(t, wh, http.MethodPost, "/api/worker/poll", poll); got.Code != 200 {
		t.Fatalf("in-process poll: %d %s", got.Code, got.Body)
	}
	for _, path := range []string{"/api/info", "/api/automation", "/mcp"} {
		if got := call(t, wh, http.MethodGet, path, ""); got.Code != 404 {
			t.Fatalf("worker handler served %s: %d", path, got.Code)
		}
	}
	a, err := h.srv.proj.Automation()
	if err != nil || a.Config.Worker != "machine" || a.Config.Agent != "claude" || a.Workers[0].Name != "sam-pc" {
		t.Fatalf("first poll didn't select the local machine: %+v %v", a, err)
	}
	// Over HTTP the worker routes still need a token, even from this machine.
	if got := h.post("/api/worker/poll", poll, nil); got.code != http.StatusUnauthorized {
		t.Fatalf("tokenless HTTP poll: %d", got.code)
	}
	if err = h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before the local worker stopped")
	}
}

func TestHostedAutomationIsNotLocal(t *testing.T) {
	h := startTeam(t, Options{})
	if v := getAutomation(t, h); v["local"] != false || v["blocked"] != nil {
		t.Fatalf("hosted view: %v", v)
	}
	if h.srv.Busy() {
		t.Fatal("a server without a local worker is busy")
	}
}

func TestSendAndFinishWithSendIgnoreTheAutoSendSwitch(t *testing.T) {
	h, wh, _ := localServer(t, "")
	h.saveTake(take, "v6")
	if got := h.post("/api/session/"+take+"/done?v=v6", `{"send":true}`, h.origin()); got.code != 200 {
		t.Fatalf("done with send: %d %s", got.code, got.body)
	}
	st := h.srv.proj
	a, err := st.Automation()
	if err != nil || len(a.Submissions) != 1 || !a.Submissions[0].Send || a.Config.Enabled {
		t.Fatalf("finish with send: %+v %v", a.Submissions, err)
	}
	// Send on a comment queues and runs with automation off.
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "note", Text: "make it blue"}}}); err != nil {
		t.Fatal(err)
	}
	poll := `{"worker":{"id":"machine","session":"session","agents":[{"id":"claude"}]}}`
	call(t, wh, http.MethodPost, "/api/worker/poll", poll)
	if got := h.post("/api/automation/tasks", `{"notes":[1]}`, h.origin()); got.code != 200 {
		t.Fatalf("send: %d %s", got.code, got.body)
	}
	got := call(t, wh, http.MethodPost, "/api/worker/poll", poll)
	var w project.WorkAssignment
	if err = json.Unmarshal(got.Body.Bytes(), &w); err != nil || w.Task == nil || w.Task.Notes[0] != 1 {
		t.Fatalf("manual send didn't dispatch with automation off: %s %v", got.Body, err)
	}
	if !h.srv.Busy() {
		t.Fatal("a running local task doesn't keep the server busy")
	}
	// Turning auto-send off stops nothing.
	if got := h.post("/api/automation", `{"enabled":false,"worker":"machine","agent":"claude"}`, h.origin()); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	if a, _ = st.Automation(); a.Tasks[0].State != "running" || a.Tasks[0].Stop {
		t.Fatalf("disabling auto-send stopped the task: %+v", a.Tasks[0])
	}
}

func TestRendersAreDraftsUntilATaskCompletes(t *testing.T) {
	h, wh, _ := localServer(t, "")
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	disk := 0
	for _, v := range h.srv.Family() {
		disk = max(disk, v.N)
	}
	next := disk + 1
	poll := `{"worker":{"id":"machine","session":"session","agents":[{"id":"claude"}]}}`
	pollOnce := func() project.WorkAssignment {
		t.Helper()
		var w project.WorkAssignment
		got := call(t, wh, http.MethodPost, "/api/worker/poll", poll)
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &w) != nil {
			t.Fatalf("poll: %d %s", got.Code, got.Body)
		}
		return w
	}
	if w := pollOnce(); w.Versions != len(h.srv.Family()) || w.Versions == 0 {
		t.Fatalf("poll counts %d versions, family has %d", w.Versions, len(h.srv.Family()))
	}
	source := strings.Repeat("a", 64)
	register := func(id, task, lease string, complete bool) project.LiveRender {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": id, "worker": "machine", "task": task, "lease": lease, "source": source,
			"duration": 2, "fps": 12, "width": 320, "height": 180, "complete": complete, "done": 1, "total": 1})
		got := call(t, wh, http.MethodPost, "/api/worker/render", string(body))
		var v project.LiveRender
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &v) != nil {
			t.Fatalf("render %s: %d %s", id, got.Code, got.Body)
		}
		return v
	}
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "note", Text: "make it blue"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.QueueFeedback([]int64{1}, ""); err != nil {
		t.Fatal(err)
	}
	task := pollOnce().Task
	if task == nil {
		t.Fatal("no task")
	}
	first := register("render-1", task.ID, task.Lease, true)
	second := register("render-2", task.ID, task.Lease, false)
	if first.Label != fmt.Sprintf("v%d-draft1", next) || !first.Draft || second.Label != fmt.Sprintf("v%d-draft2", next) {
		t.Fatalf("draft labels: %q %q", first.Label, second.Label)
	}
	// The family follows the work: the newest draft and the newest complete draft.
	labels := func() string {
		var out []string
		for _, v := range h.srv.Rescan() {
			out = append(out, v.Label)
		}
		return strings.Join(out, " ")
	}
	if got := labels(); !strings.HasSuffix(got, first.Label+" "+second.Label) {
		t.Fatalf("family while working: %s", got)
	}
	third := register("render-3", task.ID, task.Lease, true)
	if got := labels(); strings.Contains(got, first.Label) || strings.Contains(got, second.Label) || !strings.HasSuffix(got, third.Label) {
		t.Fatalf("superseded drafts still listed: %s", got)
	}
	if _, ok := h.srv.version(first.Label); !ok {
		t.Fatal("an unlisted draft no longer takes takes")
	}
	report, _ := json.Marshal(project.WorkReport{Worker: "machine", Task: task.ID, Lease: task.Lease, State: "completed",
		Message: "The title is blue", Result: third.ID})
	if got := call(t, wh, http.MethodPost, "/api/worker/report", string(report)); got.Code != 200 {
		t.Fatalf("report: %d %s", got.Code, got.Body)
	}
	want := fmt.Sprintf("v%d", next)
	if got := labels(); !strings.HasSuffix(got, " "+want) || strings.Contains(got, "draft") {
		t.Fatalf("family after completion: %s", got)
	}
	a, _ := st.Automation()
	if a.Tasks[0].Result != want {
		t.Fatalf("task result %q", a.Tasks[0].Result)
	}
	if n, _ := st.Note(1); n.AnsweredIn != want {
		t.Fatalf("note answered in %q", n.AnsweredIn)
	}
	if v, ok := h.srv.version(third.Label); !ok || v.Label != want {
		t.Fatalf("the promoted draft's old label: %+v %v", v, ok)
	}
	if w := pollOnce(); w.Versions != len(h.srv.Family()) {
		t.Fatalf("poll counts %d versions, family has %d", w.Versions, len(h.srv.Family()))
	}
	// A render outside a task, in a project that has versions, is a draft of the next one.
	if v := register("render-4", "", "", true); v.Label != fmt.Sprintf("v%d-draft1", next+1) {
		t.Fatalf("plain save made %q", v.Label)
	}
}
