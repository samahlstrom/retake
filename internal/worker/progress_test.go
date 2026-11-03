package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// The progress endpoint takes the running attempt's secret only (a chat turn's is refused: a chat has no notes), and
// passes working on with the worker's own credentials, the attempt's lease and the notes it gave the session.
func TestProgressEndpointNeedsTheAttemptsSecret(t *testing.T) {
	var forwarded []map[string]any
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/api/worker/progress" || r.Header.Get("Authorization") != "Bearer rk_worker_secret" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		forwarded = append(forwarded, b)
		_ = json.NewEncoder(w).Encode(project.ProgressMark{Note: 1, State: "working", Target: "v3"})
	}))
	defer host.Close()
	root := t.TempDir()
	r := &runner{opts: Options{URL: host.URL, Token: "rk_worker_secret", Root: root, HTTP: host.Client(), Log: io.Discard},
		dir: filepath.Join(root, ".retake", "worker"), identity: project.EditingWorker{ID: "w", Session: "s"}}
	if e := os.MkdirAll(r.dir, 0o700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.life = ctx
	task := &project.EditingTask{ID: "task", Lease: "lease", Notes: []int64{1}, Target: "v3"}
	r.setActive(task)
	r.batch.unsent = []int64{4}
	_, _, token, url, end := r.beginAsk(project.WorkAssignment{Task: task}, "", false)
	defer end(false)
	r.chat = &chatRun{id: "turn", secret: "chat-secret"}
	post := func(secret, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, url+"/progress", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	for _, secret := range []string{"", "wrong", "rk_worker_secret"} {
		if code, _ := post(secret, `{"note":1,"state":"working"}`); code != http.StatusForbidden {
			t.Fatalf("secret %q: %d", secret, code)
		}
	}
	if code, out := post("chat-secret", `{"note":1,"state":"working"}`); code != http.StatusConflict || !strings.Contains(out, "chat") {
		t.Fatalf("a chat's secret: %d %s", code, out)
	}
	if code, _ := post(token, `{"note":1,"state":"finished"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown state: %d", code)
	}
	if len(forwarded) != 0 {
		t.Fatal("a refused mark reached the host")
	}
	if code, out := post(token, `{"note":1,"state":"working","text":"slowing the fade","task":"other","lease":"stolen"}`); code != 200 || !strings.Contains(out, `"target":"v3"`) {
		t.Fatalf("%d %s", code, out)
	}
	f := forwarded[0]
	if f["task"] != "task" || f["lease"] != "lease" || f["text"] != "slowing the fade" || f["state"] != "working" {
		t.Fatalf("forwarded %v", f)
	}
	if join, _ := f["join"].([]any); len(join) != 1 || join[0] != float64(4) || len(r.unsentJoin()) != 0 {
		t.Fatalf("the session's notes: %v, unsent %v", f["join"], r.unsentJoin())
	}
	// A done needs the attempt's record on this computer.
	if code, _ := post(token, `{"note":1,"state":"done","text":"Slowed it"}`); code != http.StatusConflict {
		t.Fatalf("a done without the attempt: %d", code)
	}
}
