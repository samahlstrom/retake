package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// scriptsManifest is spec/scripts.md's manifest.
const scriptsManifest = `api = 1
id = "scripts"
name = "Scripts"
version = "1.0.0"
description = "Talk out an idea, shape it into a script, and start the video."

[ui]
module = "ui/scripts.js"
style = "ui/scripts.css"

[nav]
title = "Scripts"
icon = "ui/script.svg"
list = "kind-only"
action = { title = "New idea", route = "new" }

[[kinds]]
id = "doc"
folder = "v0"
marker = ["*_v0.md", "idea.md"]
label = "Script"
status = "Idea"
record = true

[[slots]]
slot = "page"

[[slots]]
slot = "version"
kind = "doc"

[[slots]]
slot = "sidebar-tab"
title = "Script"
when = "has-kind"

[files]
owns = ["v0/**"]
read = "watch"
write = "record"
create = "owner"

[agent]
instructions = "agent/task.md"
context = "agent/context.md"
`

// scriptsExts is the Scripts extension (its manifest, with stand-in UI files) turned on.
func scriptsExts(t *testing.T) *ext.Set {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "scripts")
	files := map[string]string{
		"extension.toml":   scriptsManifest,
		"ui/scripts.js":    "export function page(el) {}\nexport function version(el) {}\nexport function sidebarTab(el) {}\n",
		"ui/scripts.css":   "[data-ext='scripts'] p { color: var(--text); }\n",
		"ui/script.svg":    `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"agent/task.md":    "Shape the talk-outs into outline.md and script.md.\n",
		"agent/context.md": "Follow v0/script.md.\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := ext.LoadDir(dir)
	if !e.OK() {
		t.Fatalf("the test extension doesn't load: %v", e.Problems)
	}
	set, warnings := ext.Resolve([]*ext.Ext{e}, []string{"scripts"}, nil)
	if len(warnings) > 0 || set.Get("scripts") == nil {
		t.Fatalf("not on: %v", warnings)
	}
	return set
}

// fixedWords is a transcriber that hears the same words in every file.
type fixedWords struct{}

func (fixedWords) Transcribe(context.Context, string, transcribe.Options) ([]transcribe.Word, error) {
	return []transcribe.Word{{Text: " So", Start: 0, End: 0.4, Prob: 0.9}, {Text: " the", Start: 0.4, End: 0.6, Prob: 0.9},
		{Text: " idea", Start: 0.6, End: 1, Prob: 0.9}}, nil
}
func (fixedWords) Name() string { return "fixed" }

func extFT(t *testing.T) *ft {
	t.Helper()
	set := scriptsExts(t)
	return newFT(t, func(o *FolderOptions) {
		o.Exts = set
		o.FileTranscriber = func() (transcribe.Transcriber, error) { return fixedWords{}, nil }
		prev := o.Project
		o.Project = func(name string, so *server.Options) {
			if prev != nil {
				prev(name, so)
			}
			so.ExtWatchEvery = 50 * time.Millisecond
		}
	})
}

func jsonBody(t *testing.T, r res, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.body), v); err != nil {
		t.Fatalf("%d %s: %v", r.code, r.body, err)
	}
}

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// waitFor polls cond for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHomeExtensions(t *testing.T) {
	x := extFT(t)
	// The list, the styles and the UI files at the home.
	r := x.get("/api/ext")
	var list struct {
		API        int `json:"api"`
		Extensions []struct {
			ID    string `json:"id"`
			Kinds []struct {
				ID string `json:"id"`
			} `json:"kinds"`
		} `json:"extensions"`
	}
	jsonBody(t, r, &list)
	if list.API != 1 || len(list.Extensions) != 1 || list.Extensions[0].ID != "scripts" ||
		len(list.Extensions[0].Kinds) != 1 || list.Extensions[0].Kinds[0].ID != "scripts.doc" {
		t.Fatalf("api/ext: %s", r.body)
	}
	if r = x.get("/ext/styles.css"); r.code != 200 || !strings.Contains(r.body, "layer(ext.scripts)") {
		t.Fatalf("styles: %d %s", r.code, r.body)
	}
	if r = x.get("/ext/scripts/ui/scripts.js"); r.code != 200 || !strings.Contains(r.body, "export function page") {
		t.Fatalf("module: %d %s", r.code, r.body)
	}
	if r = x.get("/ext/scripts/extension.toml"); r.code != 404 {
		t.Fatalf("only ui/ is served: %d", r.code)
	}

	// New idea: alpha/ is a video, so the project is the top; a new folder there becomes an idea.
	body := `{"project": "", "name": "jump-souls", "files": {"v0/jump-souls_v0.md": "# Jump souls\n\n"}}`
	r = x.do(http.MethodPost, "/api/ext/scripts/videos", body)
	if r.code != 201 {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	var made struct{ Name, URL string }
	jsonBody(t, r, &made)
	if made.Name != "jump-souls" || made.URL != "p/jump-souls/" {
		t.Fatalf("made %+v", made)
	}
	if r = x.do(http.MethodPost, "/api/ext/scripts/videos", body); r.code != 409 {
		t.Fatalf("the same folder again: %d %s", r.code, r.body)
	}
	for name, bad := range map[string]string{
		"not owned":  `{"project": "", "name": "other", "files": {"notes.md": "x"}}`,
		"bad name":   `{"project": "", "name": "Has Spaces", "files": {"v0/jump-souls_v0.md": "x"}}`,
		"no project": `{"project": "nope", "name": "other", "files": {"v0/jump-souls_v0.md": "x"}}`,
		"traversal":  `{"project": "..", "name": "other", "files": {"v0/jump-souls_v0.md": "x"}}`,
	} {
		if r = x.do(http.MethodPost, "/api/ext/scripts/videos", bad); r.code != 400 {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	if r = x.do(http.MethodPost, "/api/ext/scripts/videos", body, withHeader("Origin", "https://evil.test")); r.code != 403 {
		t.Fatalf("a foreign origin: %d", r.code)
	}

	// The home lists it as an idea: no cut, the kind, v0.
	h := x.home(t)
	var idea *struct{}
	for _, c := range h.Projects {
		if c.Name == "jump-souls" {
			idea = &struct{}{}
			if c.Status != "no_cut" || c.Kind != "scripts.doc" || c.Version != "v0" || c.Thumb != "" || c.Changed == 0 {
				t.Fatalf("idea card: %+v", c)
			}
		}
	}
	if idea == nil {
		t.Fatalf("no idea card: %v", summary(h))
	}
	// Its page: api/info has v0 with its kind, current.
	r = x.get("/p/jump-souls/api/info")
	var info struct {
		Label    string `json:"label"`
		Kind     string `json:"kind"`
		Versions []struct {
			Label    string   `json:"label"`
			Kind     string   `json:"kind"`
			Duration *float64 `json:"duration"`
		} `json:"versions"`
	}
	jsonBody(t, r, &info)
	if info.Label != "v0" || info.Kind != "scripts.doc" || len(info.Versions) != 1 || info.Versions[0].Duration != nil {
		t.Fatalf("info: %s", r.body)
	}
	// Its link cards (spec/api.md, "Link preview"): the video's server reads them, and never a local address.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if r = x.get("/p/jump-souls/api/link-preview?url=" + url.QueryEscape("http://127.0.0.1:1/")); r.code != 400 ||
		!strings.Contains(r.body, "not a public web address") {
		t.Fatalf("link preview of a local address: %d %s", r.code, r.body)
	}
}

func TestVideoExtensionFiles(t *testing.T) {
	x := extFT(t)
	idea := filepath.Join(x.dir, "jump-souls")
	file(t, filepath.Join(idea, "v0", "jump-souls_v0.md"), time.Now())
	if err := os.WriteFile(filepath.Join(idea, "v0", "jump-souls_v0.md"), []byte("# Jump souls\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := "/p/jump-souls/api/ext/scripts/files"
	r := x.get(base)
	var list struct {
		Files []ext.FileInfo `json:"files"`
	}
	jsonBody(t, r, &list)
	if len(list.Files) != 1 || list.Files[0].Path != "v0/jump-souls_v0.md" || list.Files[0].SHA256 == "" {
		t.Fatalf("list: %s", r.body)
	}
	sha := list.Files[0].SHA256
	if r = x.get(base + "/v0/jump-souls_v0.md"); r.code != 200 || r.body != "# Jump souls\n" || r.hdr.Get("ETag") != `"`+sha+`"` {
		t.Fatalf("read: %d %q %v", r.code, r.body, r.hdr)
	}
	// The watch: an outside edit comes as an ext.files event.
	if r = x.do(http.MethodPost, "/p/jump-souls/api/ext/scripts/watch?viewer=a", ""); r.code != 200 {
		t.Fatalf("watch: %d %s", r.code, r.body)
	}
	time.Sleep(150 * time.Millisecond) // the first look
	// A write with the sha read: 200; with an old one: 409 with the new sha.
	r = x.do(http.MethodPut, base+"/v0/jump-souls_v0.md", "# Jump souls\n\nThe trick.\n", withHeader("If-Match", `"`+sha+`"`))
	var fi ext.FileInfo
	jsonBody(t, r, &fi)
	if r.code != 200 || fi.SHA256 == sha {
		t.Fatalf("write: %d %s", r.code, r.body)
	}
	r = x.do(http.MethodPut, base+"/v0/jump-souls_v0.md", "stale", withHeader("If-Match", `"`+sha+`"`))
	if r.code != 409 || !strings.Contains(r.body, fi.SHA256) {
		t.Fatalf("stale write: %d %s", r.code, r.body)
	}
	if r = x.do(http.MethodPut, base+"/v0/outline.md", "## Cold open\n", withHeader("If-Match", "*")); r.code != 200 {
		t.Fatalf("new file: %d %s", r.code, r.body)
	}
	if r = x.do(http.MethodPut, base+"/v1/x.md", "no"); r.code != 404 {
		t.Fatalf("not owned: %d", r.code)
	}
	if r = x.do(http.MethodPut, base+"/v0/../v1/x.md", "no"); r.code == 200 {
		t.Fatalf("traversal: %d", r.code)
	}
	waitFor(t, "the ext.files event", func() bool {
		st, err := project.OpenExisting(idea)
		if err != nil {
			return false
		}
		defer func() { _ = st.Close() }()
		evs, _ := st.Events(0, 0)
		for _, e := range evs {
			if e.Kind == "ext.files" && strings.Contains(string(e.Data), "v0/outline.md") {
				return true
			}
		}
		return false
	})
	if r = x.do(http.MethodDelete, base+"/v0/outline.md", ""); r.code != 204 {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}

	// A typed comment on v0, anchored; on a video version one needs its time (spec/notes.md).
	note := `{"version": "v0", "text": "Work this talk-out in", "anchor": {"file": "v0/jump-souls_v0.md", "line": 3, "heading": "Jump souls", "quote": "The trick."}}`
	if r = x.do(http.MethodPost, "/p/jump-souls/api/notes", note); r.code != 201 {
		t.Fatalf("typed comment: %d %s", r.code, r.body)
	}
	if r = x.do(http.MethodPost, "/p/alpha/api/notes", `{"version": "v2", "text": "x"}`); r.code != 400 {
		t.Fatalf("a typed comment on a video without its time: %d %s", r.code, r.body)
	}
	r = x.get("/p/jump-souls/api/notes?v=v0")
	if !strings.Contains(r.body, `"key":"doc/1"`) || !strings.Contains(r.body, `"heading":"Jump souls"`) {
		t.Fatalf("api/notes: %s", r.body)
	}

	// A talk-out's transcription: the job finishes and the words file is there.
	if err := os.MkdirAll(filepath.Join(idea, "v0", "voice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idea, "v0", "voice", "20261101-190211.webm"), []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = x.do(http.MethodPost, "/p/jump-souls/api/ext/scripts/transcribe", `{"path": "v0/voice/20261101-190211.webm"}`)
	var job struct{ Job string }
	jsonBody(t, r, &job)
	if r.code != 202 || job.Job == "" {
		t.Fatalf("transcribe: %d %s", r.code, r.body)
	}
	waitFor(t, "the transcription", func() bool {
		r := x.get("/p/jump-souls/api/ext/scripts/jobs/" + job.Job)
		return strings.Contains(r.body, `"state":"done"`)
	})
	b, err := os.ReadFile(filepath.Join(idea, "v0", "voice", "20261101-190211.words.json"))
	if err != nil || !strings.Contains(string(b), `"w":" idea"`) {
		t.Fatalf("words: %s %v", b, err)
	}

	// History: an agent's edit, undone.
	snap, err := ext.Snapshot(idea, []string{"v0/**"}, "task1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idea, "v0", "jump-souls_v0.md"), []byte("agent's words\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snap.Finish(); err != nil {
		t.Fatal(err)
	}
	r = x.get("/p/jump-souls/api/ext/scripts/history?path=v0/jump-souls_v0.md")
	var hist struct {
		Edits []ext.Edit `json:"edits"`
	}
	jsonBody(t, r, &hist)
	if len(hist.Edits) != 1 || hist.Edits[0].Path != "v0/jump-souls_v0.md" {
		t.Fatalf("history: %s", r.body)
	}
	undo, _ := json.Marshal(map[string]string{"id": hist.Edits[0].ID, "path": "v0/jump-souls_v0.md"})
	if r = x.do(http.MethodPost, "/p/jump-souls/api/ext/scripts/history/undo", string(undo)); r.code != 200 {
		t.Fatalf("undo: %d %s", r.code, r.body)
	}
	if b, _ := os.ReadFile(filepath.Join(idea, "v0", "jump-souls_v0.md")); string(b) != "# Jump souls\n\nThe trick.\n" {
		t.Fatalf("after undo: %q", b)
	}
}
