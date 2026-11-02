package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The team server's extensions: on for everyone the admin allows, each person can turn one off, and a new video's
// folder goes to the editing computer that holds its project (the machine channel).
func TestHostedExtensions(t *testing.T) {
	set := scriptsExts(t)
	x := newTH(t, Config{}, func(o *Options) { o.Exts = set })
	x.h.createWait = 50 * time.Millisecond
	admin, member := x.user("admin@example.com", true), x.user("member@example.com", false)
	as, ms := x.session(admin), x.session(member)

	if r := x.do(http.MethodGet, "/api/ext", ""); r.code != 401 {
		t.Fatalf("signed out: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/api/ext", "", cookie(SessionCookie, ms)); r.code != 200 ||
		!strings.Contains(r.body, `"id":"scripts"`) {
		t.Fatalf("member: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/ext/scripts/ui/scripts.js", "", cookie(SessionCookie, ms)); r.code != 200 {
		t.Fatalf("module: %d", r.code)
	}
	// The member turns it off for themselves; the admin still has it.
	if r := x.do(http.MethodPut, "/api/me/extensions", `{"scripts": false}`, cookie(SessionCookie, ms), origin(x.url)); r.code != 200 ||
		!strings.Contains(r.body, `"scripts":true`) {
		t.Fatalf("turn off: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/api/ext", "", cookie(SessionCookie, ms)); strings.Contains(r.body, "scripts") {
		t.Fatalf("off for the member: %s", r.body)
	}
	if r := x.do(http.MethodGet, "/ext/styles.css", "", cookie(SessionCookie, ms)); strings.Contains(r.body, "scripts") {
		t.Fatalf("styles still layer it: %s", r.body)
	}
	if r := x.do(http.MethodGet, "/api/ext", "", cookie(SessionCookie, as)); !strings.Contains(r.body, "scripts") {
		t.Fatalf("on for the admin: %s", r.body)
	}
	if r := x.do(http.MethodPut, "/api/me/extensions", `{"nope": false}`, cookie(SessionCookie, ms), origin(x.url)); r.code != 400 {
		t.Fatalf("an extension the server doesn't allow: %d", r.code)
	}
	if r := x.do(http.MethodPut, "/api/me/extensions", `{"scripts": true}`, cookie(SessionCookie, ms), origin(x.url)); r.code != 200 {
		t.Fatalf("turn back on: %d", r.code)
	}

	// Ignition is Scripts now (spec/extensions.md, "Renamed ids"): the old id turns it off, a choice kept under the old
	// id still holds, and its files answer under the old id too.
	if r := x.do(http.MethodPut, "/api/me/extensions", `{"ignition": false}`, cookie(SessionCookie, ms), origin(x.url)); r.code != 200 ||
		r.body != `{"off":{"scripts":true}}` {
		t.Fatalf("turn off by the old id: %d %q", r.code, r.body)
	}
	if err := x.st.Accounts.SetSetting(extOffKey(member.ID), `{"ignition": true}`); err != nil {
		t.Fatal(err)
	}
	if r := x.do(http.MethodGet, "/api/me/extensions", "", cookie(SessionCookie, ms)); !strings.Contains(r.body, `{"scripts":true}`) {
		t.Fatalf("kept under the old id: %s", r.body)
	}
	if r := x.do(http.MethodGet, "/api/ext", "", cookie(SessionCookie, ms)); strings.Contains(r.body, "scripts") {
		t.Fatalf("off under the old id: %s", r.body)
	}
	if r := x.do(http.MethodPut, "/api/me/extensions", `{"scripts": true}`, cookie(SessionCookie, ms), origin(x.url)); r.code != 200 ||
		strings.Contains(r.body, "ignition") || strings.Contains(r.body, "scripts") {
		t.Fatalf("turn back on again: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/ext/ignition/ui/scripts.js", "", cookie(SessionCookie, ms)); r.code != 200 {
		t.Fatalf("module under the old id: %d", r.code)
	}

	// New idea with no computer connected: offline.
	body := `{"project": "deadlock", "name": "jump-souls", "files": {"v0/jump-souls_v0.md": "# Jump souls\n\n"}}`
	if r := x.do(http.MethodPost, "/api/ext/scripts/videos", body, cookie(SessionCookie, as), origin(x.url)); r.code != 503 ||
		!strings.Contains(r.body, "offline") {
		t.Fatalf("offline: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/api/ext/scripts/videos", body, cookie(SessionCookie, ms), origin(x.url)); r.code != 403 {
		t.Fatalf("a member makes ideas: %d %s", r.code, r.body)
	}
	// A computer polls the machine channel; it registered a video in deadlock/.
	tok := x.connect(as)
	if r := x.post("/api/machine/projects", `{"name": "item-shop", "title": "item-shop", "path": "deadlock/item-shop", "project": "deadlock"}`,
		bearerTok(tok)); r.code != 201 {
		t.Fatalf("register: %d %s", r.code, r.body)
	}
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, "deadlock"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan string, 1)
	go func() {
		// A stand-in for retake connect: take one request, write the folder, register it, answer.
		for ctx.Err() == nil {
			r := x.do(http.MethodGet, "/api/machine/requests", "", bearerTok(tok))
			if r.code == 204 {
				continue
			}
			var req struct {
				ID, Op, Ext string
				NewVideo
			}
			if r.code != 200 || json.Unmarshal([]byte(r.body), &req) != nil {
				served <- "bad request " + r.body
				return
			}
			root, err := WriteVideo(folder, set.Get(req.Ext), req.NewVideo)
			reply := `{"status": 201, "name": "jump-souls"}`
			if err != nil {
				reply = `{"status": 400, "error": "` + err.Error() + `"}`
			}
			x.post("/api/machine/projects", `{"name": "jump-souls", "title": "jump-souls", "path": "deadlock/jump-souls", "project": "deadlock"}`,
				bearerTok(tok))
			if a := x.post("/api/machine/requests/"+req.ID, reply, bearerTok(tok)); a.code != 200 {
				served <- "answer " + a.body
				return
			}
			served <- root
			return
		}
	}()
	time.Sleep(100 * time.Millisecond) // the computer is polling
	start := time.Now()
	r := x.do(http.MethodPost, "/api/ext/scripts/videos", body, cookie(SessionCookie, as), origin(x.url))
	if r.code != 201 || !strings.Contains(r.body, `"name":"jump-souls"`) {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("too slow")
	}
	root := <-served
	if b, err := os.ReadFile(filepath.Join(root, "v0", "jump-souls_v0.md")); err != nil || string(b) != "# Jump souls\n\n" {
		t.Fatalf("on the computer: %q %v (%s)", b, err, root)
	}
	if !strings.HasSuffix(root, filepath.Join("deadlock", "jump-souls")) {
		t.Fatalf("where: %s", root)
	}
}
