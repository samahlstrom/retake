package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

func TestAutomationPermissionsAndPrivateLease(t *testing.T) {
	h := startTeam(t, Options{})
	agent := makeToken(t, h, "agent", "worker", 0)
	reviewer := makeToken(t, h, "reviewer", "reviewer", 0)
	viewer := makeToken(t, h, "viewer", "viewer", 0)
	headers := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	poll := `{"worker":{"id":"machine","session":"private-session","name":"Sam's computer","agents":[{"id":"codex","name":"Codex"}]}}`
	for _, token := range []string{"", reviewer, viewer} {
		if got := h.post("/api/worker/poll", poll, headers(token)); got.code == 200 {
			t.Fatalf("non-agent connected: %s", token)
		}
	}
	if got := h.post("/api/worker/poll", poll, headers(agent)); got.code != 200 {
		t.Fatalf("worker registration: %d %s", got.code, got.body)
	}
	for _, token := range []string{reviewer, viewer, agent} {
		if got := h.post("/api/automation", `{"enabled":true,"worker":"machine","agent":"codex"}`, headers(token)); got.code != http.StatusForbidden {
			t.Fatalf("config permission: %d %s", got.code, got.body)
		}
	}
	if got := h.post("/api/automation", `{"enabled":true,"worker":"machine","agent":"codex"}`, nil); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	st, e := h.srv.store(true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "note", Text: "make blue"}}}); e != nil {
		t.Fatal(e)
	}
	if got := h.post("/api/automation/tasks", `{"notes":[1],"agent":"untrusted-command"}`, headers(reviewer)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	got := h.post("/api/worker/poll", poll, headers(agent))
	if got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	var a project.WorkAssignment
	if e = json.Unmarshal(got.body, &a); e != nil || a.Task == nil || a.Task.Agent != "codex" {
		t.Fatal(a, e)
	}
	public := h.get("/api/automation", headers(viewer))
	if public.code != 200 || strings.Contains(string(public.body), a.Task.Lease) || strings.Contains(string(public.body), "private-session") {
		t.Fatal("worker session or lease exposed", string(public.body))
	}
	if got = h.post("/api/automation/tasks/"+a.Task.ID+"/switch", `{"agent":"codex"}`, headers(reviewer)); got.code != http.StatusForbidden {
		t.Fatal("reviewer switched executable profile", got.code)
	}
	rows, e := st.Tokens()
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		if row.Role == "agent" {
			if _, e = st.RevokeToken(row.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	if got = h.post("/api/worker/poll", poll, headers(agent)); got.code == 200 {
		t.Fatal("revoked worker reconnected")
	}
}

func TestWorkerGetsOriginalAnnotationsWithoutArbitraryHostFileAccess(t *testing.T) {
	h := startTeam(t, Options{})
	token := makeToken(t, h, "agent", "worker", 0)
	headers := map[string]string{"Authorization": "Bearer " + token, "X-Retake-Worker": "machine", "X-Retake-Session": "session"}
	if got := h.post("/api/worker/poll", `{"worker":{"id":"machine","session":"session","agents":[{"id":"codex"}]}}`, headers); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	v, ok := h.srv.version("v6")
	if !ok {
		t.Fatal("fixture version missing")
	}
	dir := store.TakeDir(v.Path, take)
	if e := os.MkdirAll(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	image := []byte("\x89PNG\r\n\x1a\nfixture annotation")
	for _, name := range []string{"img_01.png", "snapshot.png"} {
		if e := os.WriteFile(filepath.Join(dir, name), image, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(dir, "images.json"), []byte(`[{"seg":"S1","file":"img_01.png","video_t":1,"box":[0.1,0.2,0.3,0.4]}]`), 0o600); e != nil {
		t.Fatal(e)
	}
	st, e := h.srv.store(true)
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{
		{Key: "inside", Take: take, Seg: "S1", T0: 1, T1: 2, Text: "Use this reference", Detail: project.Detail{Snapshot: filepath.Join(dir, "snapshot.png"), Strokes: []project.Stroke{{Shape: "circle", Color: "red"}}}},
		{Key: "outside", Text: "bad import", Detail: project.Detail{Snapshot: filepath.Join(t.TempDir(), "secret.png")}},
	}})
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/worker/notes/1/snapshot", "/api/worker/notes/1/images/img_01.png"} {
		got := h.get(path, headers)
		if got.code != 200 || string(got.body) != string(image) {
			t.Fatal(path, got.code, string(got.body))
		}
	}
	for _, path := range []string{"/api/worker/notes/2/snapshot", "/api/worker/notes/1/images/img_99.png"} {
		if got := h.get(path, headers); got.code != 404 {
			t.Fatal("unsafe artifact", path, got.code)
		}
	}
}
