package ext

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// writeTree writes files (slash paths) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		f := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// copyHello copies testdata/hello into dir/<name> and returns that folder.
func copyHello(t *testing.T, dir, name string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	files := map[string]string{}
	for _, p := range []string{"extension.toml", "ui/hello.js", "ui/hello.css"} {
		b, err := os.ReadFile(filepath.Join("testdata", "hello", filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = string(b)
	}
	writeTree(t, out, files)
	return out
}

// manifestFS is an extension whose manifest is the full one below with body in place of its tables.
func manifestFS(top, body string) fstest.MapFS {
	return fstest.MapFS{
		ManifestFile:       {Data: []byte(top + body)},
		"ui/x.js":          {Data: []byte("export function page() {}\nexport function version() {}\nexport function sidebarTab() {}\n")},
		"ui/x.css":         {Data: []byte("[data-ext='x'] p { color: var(--text); }\n")},
		"ui/icon.svg":      {Data: []byte("<svg/>")},
		"agent/task.md":    {Data: []byte("Do the task.\n")},
		"agent/context.md": {Data: []byte("Context.\n")},
		"agent/big.md":     {Data: []byte(strings.Repeat("a line of context\n", 300))},
		"bin/tool":         {Data: []byte("#!/bin/sh\n")},
	}
}

const topKeys = "api = 1\nid = \"x\"\nname = \"X\"\nversion = \"1.0.0\"\ndescription = \"Test.\"\n"

const fullTables = `
[ui]
module = "ui/x.js"
style = "ui/x.css"

[nav]
title = "X"
icon = "ui/icon.svg"
list = "kind-only"
action = { title = "New", route = "new" }

[[kinds]]
id = "doc"
folder = "v0"
marker = "idea.md"
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
title = "X"
when = "has-kind"

[files]
owns = ["v0/**", "vo/*.wav"]

[agent]
instructions = "agent/task.md"
context = "agent/context.md"

[[agent.tools]]
name = "beats"
description = "Beats"
exec = ["./bin/tool", "--json"]
input_schema = { type = "object", properties = { file = { type = "string" } } }

[server]
command = ["./bin/tool"]
`

func TestLoadFull(t *testing.T) {
	e := Load(manifestFS(topKeys, fullTables), "", WhereBundled, "x")
	if len(e.Problems) != 0 || !e.OK() {
		t.Fatalf("problems: %v", e.Problems)
	}
	if e.ID != "x" || e.Name != "X" || e.Version != "1.0.0" || e.API != 1 || e.Nav.Action.Route != "new" {
		t.Errorf("manifest: %+v", e.Manifest)
	}
	if f := e.Files; f.Read != "watch" || f.Write != "record" || f.Create != "owner" {
		t.Errorf("files defaults: %+v", f)
	}
	if s := e.Server; s.RunsOn != "machine" || s.Needs != "watch" {
		t.Errorf("server defaults: %+v", s)
	}
	if got := e.Agent.Tools[0].InputSchema["type"]; got != "object" {
		t.Errorf("input_schema: %v", e.Agent.Tools[0].InputSchema)
	}
	if !strings.HasPrefix(e.Hash, "b3:") || len(e.Hash) != 3+64 {
		t.Errorf("hash %q", e.Hash)
	}
	if e.FullKind(e.Kinds[0]) != "x.doc" {
		t.Errorf("full kind %q", e.FullKind(e.Kinds[0]))
	}
	for p, want := range map[string]bool{"v0/idea.md": true, "v0/voice/a.webm": true, "vo/a.wav": true,
		"vo/a.mp3": false, "v1/idea.md": false, "v0/.x": false, "v0/../v1/a": false, "v0/clip_v0.mp4": false} {
		if e.Owns(p) != want {
			t.Errorf("Owns(%q) = %v", p, !want)
		}
	}
}

// Every value the manifest can get wrong: fatal ones disable the extension, warnings don't.
func TestLoadProblems(t *testing.T) {
	for _, tc := range []struct {
		name, top, body string
		kind, key       string // the problem expected
	}{
		{"unknown key", topKeys + "colour = 1\n", fullTables, "key", "colour"},
		{"unknown nested key", topKeys, fullTables + "extra = 1\n", "key", "server.extra"},
		{"parse error", topKeys + "id = \n", "", "manifest", ManifestFile},
		{"bad id", strings.Replace(topKeys, `"x"`, `"X_1"`, 1), fullTables, "value", "id"},
		{"no name", strings.Replace(topKeys, `name = "X"`, `name = " "`, 1), fullTables, "value", "name"},
		{"api missing", strings.Replace(topKeys, "api = 1\n", "", 1), fullTables, "value", "api"},
		{"api 2", strings.Replace(topKeys, "api = 1", "api = 2", 1), fullTables, "api", "api"},
		{"bad version", strings.Replace(topKeys, `"1.0.0"`, `"one"`, 1), fullTables, "value", "version"},
		{"bad min_retake", topKeys + "min_retake = \"soon\"\n", fullTables, "value", "min_retake"},
		{"module missing", topKeys, strings.Replace(fullTables, "ui/x.js", "ui/gone.js", 1), "file", "ui/gone.js"},
		{"module outside ui", topKeys, strings.Replace(fullTables, `module = "ui/x.js"`, `module = "x.js"`, 1), "value", "ui.module"},
		{"module html", topKeys, strings.Replace(fullTables, `module = "ui/x.js"`, `module = "ui/x.html"`, 1), "value", "ui.module"},
		{"module traversal", topKeys, strings.Replace(fullTables, `module = "ui/x.js"`, `module = "ui/../x.js"`, 1), "value", "ui.module"},
		{"no module with slots", topKeys, strings.Replace(fullTables, "module = \"ui/x.js\"\n", "", 1), "value", "ui.module"},
		{"style type", topKeys, strings.Replace(fullTables, "ui/x.css", "ui/x.scss", 1), "value", "ui.style"},
		{"icon missing", topKeys, strings.Replace(fullTables, "ui/icon.svg", "ui/gone.svg", 1), "file", "ui/gone.svg"},
		{"nav list", topKeys, strings.Replace(fullTables, `list = "kind-only"`, `list = "all"`, 1), "value", "nav.list"},
		{"action route", topKeys, strings.Replace(fullTables, `route = "new"`, `route = "a b"`, 1), "value", "nav.action"},
		{"kind folder", topKeys, strings.Replace(fullTables, `folder = "v0"`, `folder = "v1"`, 1), "value", "kinds[0].folder"},
		{"kind marker", topKeys, strings.Replace(fullTables, `"idea.md"`, `"a/idea.md"`, 1), "value", "kinds[0].marker"},
		{"two kinds on v0", topKeys, fullTables + "\n[[kinds]]\nid = \"other\"\nfolder = \"v0\"\nmarker = \"o.md\"\n", "value", "kinds[1].folder"},
		{"unknown slot", topKeys, fullTables + "\n[[slots]]\nslot = \"toolbar\"\n", "slot", "toolbar"},
		{"slot twice", topKeys, fullTables + "\n[[slots]]\nslot = \"page\"\n", "value", "slots[3]"},
		{"version slot kind", topKeys, strings.Replace(fullTables, `kind = "doc"`, `kind = "talk"`, 1), "value", "slots[1].kind"},
		{"when", topKeys, strings.Replace(fullTables, `when = "has-kind"`, `when = "sometimes"`, 1), "value", "slots[2].when"},
		{"owns .retake", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `".retake/x"`, 1), "owns", ".retake/x"},
		{"owns v1", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"v1/**"`, 1), "owns", "v1/**"},
		{"owns everything", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"**"`, 1), "owns", "**"},
		{"owns any md", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"**/*.md"`, 1), "owns", "**/*.md"},
		{"owns videos", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"*.mp4"`, 1), "owns", "*.mp4"},
		{"owns a version file", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"clip_v3.mov"`, 1), "owns", "clip_v3.mov"},
		{"owns a folder of anything", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"refs/**"`, 1), "owns", "refs/**"},
		{"owns traversal", topKeys, strings.Replace(fullTables, `"vo/*.wav"`, `"../x"`, 1), "owns", "../x"},
		{"role", topKeys, fullTables[:strings.Index(fullTables, "[agent]")] + "write = \"admin\"\n", "value", "files.write"},
		{"instructions missing", topKeys, strings.Replace(fullTables, "agent/task.md", "agent/gone.md", 1), "file", "agent/gone.md"},
		{"context over its cap", topKeys, strings.Replace(fullTables, "agent/context.md", "agent/big.md", 1), "size", "agent/big.md"},
		{"tool missing", topKeys, strings.Replace(fullTables, `exec = ["./bin/tool", "--json"]`, `exec = ["./bin/gone"]`, 1), "file", "bin/gone"},
		{"tool empty", topKeys, strings.Replace(fullTables, `exec = ["./bin/tool", "--json"]`, `exec = []`, 1), "value", "agent.tools[0].exec"},
		{"tool name", topKeys, strings.Replace(fullTables, `name = "beats"`, `name = "Beats!"`, 1), "value", "agent.tools[0].name"},
		{"runs_on", topKeys, fullTables + "runs_on = \"cloud\"\n", "value", "server.runs_on"},
		{"needs", topKeys, fullTables + "needs = \"root\"\n", "value", "server.needs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Load(manifestFS(tc.top, tc.body), "", WhereBundled, "x")
			i := slices.IndexFunc(e.Problems, func(p Problem) bool { return p.Kind == tc.kind && p.Name == tc.key })
			if i < 0 {
				t.Fatalf("no %s problem for %q in %v", tc.kind, tc.key, e.Problems)
			}
			p := e.Problems[i]
			if e.OK() == p.Fatal() {
				t.Errorf("OK() = %v with %v", e.OK(), p)
			}
			if p.Message == "" {
				t.Error("no message")
			}
		})
	}
}

func TestLoadAPIMessage(t *testing.T) {
	e := Load(manifestFS(strings.Replace(topKeys, "api = 1", "api = 3", 1), fullTables), "", WhereUser, "x")
	if e.OK() || len(e.Problems) != 1 || e.Problems[0].Message != "x 1.0.0 needs extension API 3; this retake has 1" {
		t.Errorf("%v", e.Problems)
	}
}

func TestMinRetake(t *testing.T) {
	for _, tc := range []struct {
		retake string
		ok     bool
	}{{"0.8.9", false}, {"0.9.0", true}, {"v1.2.0", true}, {"dev", true}, {"dev-abc123", true}, {"", true}} {
		e := Load(manifestFS(topKeys+"min_retake = \"0.9.0\"\n", fullTables), "", WhereUser, "x")
		e.checkRetake(tc.retake)
		if e.OK() != tc.ok {
			t.Errorf("retake %q: OK = %v (%v)", tc.retake, e.OK(), e.Problems)
		}
		if !tc.ok && e.Problems[0].Message != "x 1.0.0 needs retake 0.9.0 or newer; this retake is 0.8.9" {
			t.Errorf("message %q", e.Problems[0].Message)
		}
	}
}

func TestAgentText(t *testing.T) {
	fsys := manifestFS(topKeys, fullTables)
	long := strings.Repeat("ünïcode line\n", 2000) // over 16 KiB
	fsys["agent/task.md"] = &fstest.MapFile{Data: []byte(long)}
	e := Load(fsys, "", WhereBundled, "x")
	text, cut := e.AgentText("instructions")
	if !cut || len(text) > MaxInstructions || !strings.HasSuffix(text, "line\n") || !strings.HasPrefix(long, text) {
		t.Errorf("instructions: %d bytes, cut %v", len(text), cut)
	}
	if text, cut := e.AgentText("context"); cut || text != "Context.\n" {
		t.Errorf("context: %q %v", text, cut)
	}
	if text, _ := e.AgentText("other"); text != "" {
		t.Errorf("other: %q", text)
	}
	var nilExt *Ext
	if text, _ := nilExt.AgentText("context"); text != "" {
		t.Error("nil ext")
	}
}

func TestValidPathAndGlobs(t *testing.T) {
	for p, ok := range map[string]bool{"v0/idea.md": true, "a": true, "": false, "/a": false, "a//b": false, "a/./b": false,
		"a/../b": false, "..": false, ".retake/x": false, "v0/.hidden": false, `a\b`: false, "a\x00": false,
		strings.Repeat("a", MaxPath+1): false, "a/b c/ü.md": true, "a/\xff": false} {
		if ValidPath(p) != ok {
			t.Errorf("ValidPath(%q) = %v", p, !ok)
		}
	}
	for _, tc := range []struct {
		glob, path string
		ok         bool
	}{
		{"v0/**", "v0/idea.md", true}, {"v0/**", "v0/a/b/c.md", true}, {"v0/**", "v0", false},
		{"v0/**", "v1/idea.md", false}, {"**", "a/b", true}, {"**/*.md", "a.md", true}, {"**/*.md", "a/b/c.md", true},
		{"vo/*.wav", "vo/a.wav", true}, {"vo/*.wav", "vo/a/b.wav", false}, {"*.md", "a.md", true}, {"*.md", "a/b.md", false},
		{"a/?.md", "a/b.md", true}, {"a/[bc].md", "a/c.md", true}, {"a/[", "a/[", false}, {"a/**/z", "a/z", true},
		{"a/**/z", "a/b/c/z", true}, {"", "a", false}, {"a", "", false},
	} {
		if MatchGlob(tc.glob, tc.path) != tc.ok {
			t.Errorf("MatchGlob(%q, %q) = %v", tc.glob, tc.path, !tc.ok)
		}
	}
}
