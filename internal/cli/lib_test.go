package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/lib"
)

func TestLibCLI(t *testing.T) {
	w := newWorld(t)
	reg, err := filepath.Abs(filepath.Join("..", "..", "library", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(w.root, "proj")
	writeFile(t, filepath.Join(proj, "edit", "timeline.json"), `{"schema": "timeline/0.1", "id": "t", "rev": 0,
		"format": {"size": [1280, 720], "fps": 30}, "assets": {}, "spine": [], "scenes": []}`)
	p := "--project=" + proj
	w.env["RETAKE_REGISTRY"] = reg
	w.env["RETAKE_EDIT"] = filepath.Join(w.root, "no-retake-edit")

	code, out, errOut := w.out("lib", "search", "pop", "--json", p)
	if code != ExitOK {
		t.Fatalf("search: %d %s %s", code, out, errOut)
	}
	if res := decode[lib.SearchResult](t, out); len(res.Hits) == 0 || res.Hits[0].Name != "pop-text" {
		t.Fatalf("search = %+v", res)
	}
	code, out, _ = w.out("lib", "add", "lower-third", p)
	if code != ExitOK || !strings.Contains(out, "added     lower-third@1.0.0") || !strings.Contains(out, "timeline.lock") {
		t.Fatalf("add: %d %s", code, out)
	}
	code, out, _ = w.out("lib", "list", p, "--json")
	if l := decode[lib.ListResult](t, out); code != ExitOK || len(l.Components) != 1 {
		t.Fatalf("list = %d %+v", code, l)
	}
	code, out, _ = w.out("lib", "describe", "lower-third", p)
	if code != ExitOK || !strings.Contains(out, "name: The big line") || !strings.Contains(out, `"use": "lower-third@1.0.0"`) {
		t.Fatalf("describe: %d %s", code, out)
	}
	code, out, _ = w.out("lib", "pack", filepath.Join(filepath.Dir(reg), "components", "counter"), p, "--json")
	if r := decode[lib.PackResult](t, out); code != ExitOK || r.Entry.Name != "counter" || r.Entry.Hash == "" {
		t.Fatalf("pack = %d %+v", code, r)
	}
	code, out, _ = w.out("lib", "recipe", "export", "-o", filepath.Join(w.root, "recipe"), p, "--json")
	if r := decode[lib.RecipeResult](t, out); code != ExitOK || len(r.Recipe.Packages) != 1 {
		t.Fatalf("recipe = %d %+v", code, r)
	}
	// Failures: typed, with --json on stdout.
	code, out, _ = w.out("lib", "add", "nothing-here", p, "--json")
	if e := decode[lib.Error](t, out); code != ExitError || e.Code != lib.CodeNotFound {
		t.Fatalf("add unknown = %d %+v", code, e)
	}
	code, out, _ = w.out("lib", "lint", p, "--json")
	if e := decode[lib.Error](t, out); code != ExitError || !strings.Contains(e.Message, "retake-edit") {
		t.Fatalf("lint without the editor = %d %+v", code, e)
	}
	if code, _, _ := w.out("lib", "frob"); code != ExitUsage {
		t.Fatalf("unknown command: %d", code)
	}
	if code, out, _ := w.out("lib"); code != ExitOK || !strings.Contains(out, "retake lib add") {
		t.Fatalf("help: %d %s", code, out)
	}
}
