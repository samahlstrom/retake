package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
)

// chatSet is the Scripts extension with its chat on (spec/chat.md).
func chatSet(t *testing.T, chat bool) *ext.Set {
	t.Helper()
	manifest := `api = 1
id = "scripts"
name = "Scripts"
version = "1.0.0"
[ui]
module = "ui/scripts.js"
[[kinds]]
id = "doc"
folder = "v0"
marker = "*_v0.md"
label = "Script"
record = true
chat = ` + map[bool]string{true: "true", false: "false"}[chat] + `
[files]
owns = ["v0/**"]
read = "watch"
write = "record"
`
	e := ext.Load(fstest.MapFS{ext.ManifestFile: {Data: []byte(manifest)}, "ui/scripts.js": {Data: []byte("export function version() {}\n")}},
		"", ext.WhereBundled, "scripts")
	set, warnings := ext.Resolve([]*ext.Ext{e}, []string{"scripts"}, nil)
	if set.Get("scripts") == nil {
		t.Fatalf("not on: %v %v", warnings, e.Problems)
	}
	return set
}

// An idea with a chat: who may read and write it, what it refuses, and Send on it.
func TestChatRoutes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jump")
	if err := os.MkdirAll(filepath.Join(root, "v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v0", "jump_v0.md"), []byte("# Jump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Root: root, Exts: chatSet(t, true), FollowNewest: true, AllowHosts: []string{"share.test"}})
	t.Cleanup(func() { _ = h.srv.Close() })
	viewer := makeToken(t, h, "viewer", "", time.Hour)
	reviewer := makeToken(t, h, "reviewer", "", time.Hour)
	as := func(tok, method, path, body string) resp {
		t.Helper()
		hdr := map[string]string{"Authorization": "Bearer " + tok, "Origin": "https://share.test", "Content-Type": "application/json"}
		req, _ := http.NewRequest(method, h.url+path, strings.NewReader(body))
		req.Host = "share.test"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := h.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := res.Body.Read(buf)
			b.Write(buf[:n])
			if e != nil {
				break
			}
		}
		return resp{code: res.StatusCode, body: []byte(b.String())}
	}
	if r := as(viewer, "GET", "/api/chat", ""); r.code != 200 || !strings.Contains(string(r.body), `"messages":[]`) {
		t.Fatalf("a viewer reads: %d %s", r.code, r.body)
	}
	if r := as(viewer, "POST", "/api/chat", `{"text":"hi"}`); r.code != 403 {
		t.Fatalf("a viewer writes: %d", r.code)
	}
	if r := as(viewer, "POST", "/api/chat/stop", ``); r.code != 403 {
		t.Fatalf("a viewer stops: %d", r.code)
	}
	r := as(reviewer, "POST", "/api/chat", `{"text":"Make the hook shorter","images":["v0/images/a.png"]}`)
	if r.code != 201 {
		t.Fatalf("a reviewer writes: %d %s", r.code, r.body)
	}
	var posted struct{ Message project.ChatMessage }
	if json.Unmarshal(r.body, &posted) != nil || posted.Message.Turn == "" {
		t.Fatalf("%s", r.body)
	}
	for _, bad := range []string{`{"text":""}`, `{"text":"x","images":["footage/a.png"]}`, `{"text":"x","voice":"../a.webm"}`,
		`{"text":"x","answer":1}`, `nope`} {
		if r := as(reviewer, "POST", "/api/chat", bad); r.code != 400 {
			t.Errorf("%s: %d %s", bad, r.code, r.body)
		}
	}
	// No computer has taken the turn: it waits, and says the editing computer is offline.
	var v project.ChatView
	if json.Unmarshal(as(viewer, "GET", "/api/chat", "").body, &v) != nil || v.Turn == nil || v.Turn.State != "queued" || !v.Offline {
		t.Fatalf("%+v", v)
	}
	if r := as(reviewer, "POST", "/api/chat/stop", ""); r.code != 200 {
		t.Fatalf("stop: %d %s", r.code, r.body)
	}
	if r := as(reviewer, "POST", "/api/chat/stop", ""); r.code != 409 {
		t.Fatalf("stop again: %d", r.code)
	}
	for _, p := range []string{"/api/chat/files/1/option_1.png", "/api/chat/files/x/option_1.png", "/api/chat/files/1/frame.jpg",
		"/api/chat/files/1/..%2fproject.db", "/api/chat/nope"} {
		if r := as(viewer, "GET", p, ""); r.code != 404 {
			t.Errorf("%s: %d", p, r.code)
		}
	}
	// Send: v1 from the script; the same script again is refused with the reason, and is never a silent no-op.
	if r := as(reviewer, "POST", "/api/chat/send", `{"file":"v0/elsewhere.md"}`); r.code != 404 {
		t.Fatalf("a missing document: %d %s", r.code, r.body)
	}
	r = as(reviewer, "POST", "/api/chat/send", `{"file":"v0/jump_v0.md"}`)
	if r.code != 200 || !strings.Contains(string(r.body), `"target":"v1"`) {
		t.Fatalf("send: %d %s", r.code, r.body)
	}
	if r = as(reviewer, "POST", "/api/chat/send", `{"file":"v0/jump_v0.md"}`); r.code != 409 ||
		!strings.Contains(string(r.body), "Nothing changed since the last Send") {
		t.Fatalf("send again: %d %s", r.code, r.body)
	}
	st, err := h.srv.store(false)
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.Note(1)
	if err != nil || n.Version != "v0" || !n.WantsVersion() || n.Text != "Build the first cut (v1) from the script." {
		t.Fatalf("the comment: %+v %v", n, err)
	}
	// A changed script sends again: here it joins the same queued batch for v1.
	if err = os.WriteFile(filepath.Join(root, "v0", "jump_v0.md"), []byte("# Jump\n\nMore.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r = as(reviewer, "POST", "/api/chat/send", `{"file":"v0/jump_v0.md"}`); r.code != 200 {
		t.Fatalf("send a change: %d %s", r.code, r.body)
	}
	a, _ := st.Automation()
	if len(a.Tasks) != 1 || len(a.Tasks[0].Notes) != 2 {
		t.Fatalf("one batch for v1: %+v", a.Tasks)
	}
}

// A kind without a chat keeps its comments: the chat's routes say so.
func TestNoChatWithoutTheKindsChat(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jump")
	if err := os.MkdirAll(filepath.Join(root, "v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v0", "jump_v0.md"), []byte("# Jump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Root: root, Exts: chatSet(t, false), FollowNewest: true})
	t.Cleanup(func() { _ = h.srv.Close() })
	if r := h.post("/api/chat", `{"text":"hi"}`, h.origin()); r.code != 404 {
		t.Fatalf("%d %s", r.code, r.body)
	}
	if r := h.post("/api/chat/send", `{"file":"v0/jump_v0.md"}`, h.origin()); r.code != 404 {
		t.Fatalf("%d %s", r.code, r.body)
	}
}

// Once cuts exist, Send brings the next version in line with the script: a comment on the newest cut at 0:00, which
// goes into the version after it by the batch rule.
func TestChatSendAfterCuts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jump")
	for p, body := range map[string]string{"v0/jump_v0.md": "# Jump\n", "v1/jump_v1.mp4": "video", "v2/jump_v2.mp4": "video"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := start(t, Options{Root: root, Video: filepath.Join(root, "v2", "jump_v2.mp4"), Exts: chatSet(t, true),
		FollowNewest: true})
	t.Cleanup(func() { _ = h.srv.Close() })
	r := h.post("/api/chat/send", `{"file":"v0/jump_v0.md"}`, h.origin())
	if r.code != 200 || !strings.Contains(string(r.body), `"target":"v3"`) {
		t.Fatalf("send: %d %s", r.code, r.body)
	}
	st, err := h.srv.store(false)
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.Note(1)
	if err != nil || n.Version != "v2" || !n.WantsVersion() || n.T0 != 0 ||
		n.Text != "Bring the video in line with the script's changes (v0/jump_v0.md)." {
		t.Fatalf("the comment: %+v %v", n, err)
	}
	a, _ := st.Automation()
	if len(a.Tasks) != 1 || a.Tasks[0].Target != "v3" || a.Tasks[0].State != "queued" {
		t.Fatalf("its batch: %+v", a.Tasks)
	}
	var v struct {
		Sent *project.ChatSentView `json:"sent"`
	}
	if json.Unmarshal(h.get("/api/chat", nil).body, &v) != nil || v.Sent == nil || v.Sent.Target != "v3" || v.Sent.Open {
		t.Fatalf("sent: %+v", v.Sent)
	}
}
