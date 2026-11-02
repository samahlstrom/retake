package ext

import (
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/extensions"
)

// The hello extension of docs/extensions.md, as the docs show it, has nothing to report.
func TestCheckHello(t *testing.T) {
	r := CheckDir(filepath.Join("testdata", "hello"), "1.0.0")
	if len(r.Problems) != 0 {
		t.Fatalf("problems: %v", r.Problems)
	}
	if r.ID != "hello" || r.Version != "0.1.0" || r.API != 1 || !filepath.IsAbs(r.Dir) {
		t.Errorf("report %+v", r)
	}
}

// Every finding of `retake ext check`, from one extension that has each.
func TestCheckBroken(t *testing.T) {
	r := CheckDir(filepath.Join("testdata", "broken"), "1.0.0")
	got := map[string]bool{}
	for _, p := range r.Problems {
		got[p.Kind+" "+p.Name] = true
		if p.Message == "" {
			t.Errorf("no message: %+v", p)
		}
	}
	want := []string{
		"key colour",
		"slot toolbar",
		"owns .retake/notes.json",
		"owns **/*.md",
		"owns clip_v2.mp4",
		"size agent/context.md",
		"export sidebarTab",
		"css :root",
		"css li",
		"css .loose",
		"token --no-such-token",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %q", w)
		}
	}
	if len(r.Problems) != len(want) {
		t.Errorf("%d problems, want %d: %v", len(r.Problems), len(want), slices.Sorted(maps.Keys(got)))
	}
}

func TestCheckManifestProblems(t *testing.T) {
	r := CheckDir(filepath.Join("testdata", "bad-toml"), "")
	if len(r.Problems) != 1 || r.Problems[0].Kind != "manifest" || !strings.Contains(r.Problems[0].Message, "doesn't parse") {
		t.Errorf("bad toml: %v", r.Problems)
	}
	r = CheckDir(filepath.Join(t.TempDir(), "nowhere"), "")
	if len(r.Problems) != 1 || !strings.Contains(r.Problems[0].Message, "no folder at") {
		t.Errorf("missing folder: %v", r.Problems)
	}
	dir := copyHello(t, t.TempDir(), "hello")
	writeTree(t, dir, map[string]string{ManifestFile: strings.Replace(manifestOf("hello"), "api = 1", "api = 4", 1)})
	r = CheckDir(dir, "")
	if len(r.Problems) != 1 || r.Problems[0].Message != "hello 1.0.0 needs extension API 4; this retake has 1" {
		t.Errorf("api: %v", r.Problems)
	}
	writeTree(t, dir, map[string]string{ManifestFile: manifestOf("hello") + "min_retake = \"9.0.0\"\n"})
	if r := CheckDir(dir, "1.0.0"); len(r.Problems) != 1 || r.Problems[0].Kind != "retake" {
		t.Errorf("min_retake: %v", r.Problems)
	}
}

// Ignition, as bundled: its manifest and agent text are right; ui/ is the web build's output and may not be there.
func TestCheckIgnition(t *testing.T) {
	sub, err := fs.Sub(extensions.FS, "ignition")
	if err != nil {
		t.Fatal(err)
	}
	e := Load(sub, "", WhereBundled, "ignition")
	r := Check(e)
	for _, p := range r.Problems {
		if p.Kind == "file" && (p.Name == "ui/ignition.js" || p.Name == "ui/ignition.css") {
			continue // not built yet
		}
		t.Errorf("ignition: %+v", p)
	}
	if e.ID != "ignition" || e.Version != "1.0.0" || len(e.Kinds) != 1 ||
		!slices.Equal([]string(e.Kinds[0].Marker), []string{"*_v0.md", "idea.md"}) ||
		!slices.Equal(e.Files.Owns, []string{"v0/**"}) || e.Nav.Icon != "ui/spark.svg" {
		t.Errorf("manifest %+v", e.Manifest)
	}
	for _, which := range []string{"instructions", "context"} {
		text, cut := e.AgentText(which)
		if text == "" || cut {
			t.Errorf("%s: %d bytes, cut %v", which, len(text), cut)
		}
		if !strings.Contains(text, "v0/cuts/") {
			t.Errorf("%s doesn't say to write the cuts file", which)
		}
	}
	if svg, err := fs.ReadFile(sub, "ui/spark.svg"); err != nil || !strings.Contains(string(svg), "currentColor") ||
		strings.Contains(string(svg), "href") {
		t.Errorf("spark.svg: %v", err)
	}
}

func TestExports(t *testing.T) {
	js := `
export function page(el, ctx) {}
export async function version(el) {}
export const sidebarTab = (el) => {};
// export function reviewPanel() {}
/* export function hidden() {} */
const a = 1, b = 2;
export { a as first, b };
export{c as "quoted",d}
export let counter = 0;
export class Thing {}
`
	got := Exports(js)
	for _, name := range []string{"page", "version", "sidebarTab", "first", "b", "quoted", "d", "counter", "Thing"} {
		if !got[name] {
			t.Errorf("missing %s", name)
		}
	}
	for _, name := range []string{"reviewPanel", "hidden", "a", "c"} {
		if got[name] {
			t.Errorf("%s isn't exported", name)
		}
	}
	// A bundler's output.
	if got := Exports(`function q(e,t){}function r(e){}export{q as page,r as version};`); !got["page"] || !got["version"] {
		t.Errorf("minified: %v", got)
	}
}

func TestSelectors(t *testing.T) {
	css := `
@charset "utf-8";
@import url("x.css") layer(base);
[data-ext='x'] a, [data-ext="x"] b,
  c > d { color: red; }
@media (min-width: 1px) and (max-width: 9px) {
  [data-ext=x] e { x: y }
  @supports (display: grid) { f:hover, [data-ext='x'] g { x: y } }
}
@layer ext { h { x: y } }
@font-face { font-family: "a{b"; }
@keyframes k { from { x: y } 50% { x: z } }
[data-ext='x'] i { &:hover { x: y } .nested { x: y } }
j[title="a,b{c}"] { content: "}"; }
:is(k, l) { x: y }
`
	got := Selectors(css)
	want := []string{"[data-ext='x'] a", `[data-ext="x"] b`, "c > d", "[data-ext=x] e", "f:hover", "[data-ext='x'] g", "h",
		"[data-ext='x'] i", `j[title="a,b{c}"]`, ":is(k, l)"}
	if !slices.Equal(got, want) {
		t.Errorf("Selectors =\n%q\nwant\n%q", got, want)
	}
	for sel, ok := range map[string]bool{"[data-ext='x'] a": true, `[data-ext="x"]`: true, "[data-ext=x]>b": true,
		"[ data-ext = 'x' ] a": true, "[data-ext='xy'] a": false, "a [data-ext='x']": false, ":root": false,
		"[data-ext]": false} {
		if scoped(sel, "x") != ok {
			t.Errorf("scoped(%q) = %v", sel, !ok)
		}
	}
}
