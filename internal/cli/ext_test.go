package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
)

// helloTo copies the docs' hello extension (internal/ext/testdata/hello) into dir and returns that folder.
func helloTo(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join("..", "ext", "testdata", "hello")
	for _, p := range []string{"extension.toml", "ui/hello.js", "ui/hello.css"} {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type extList struct {
	Extensions []extItem `json:"extensions"`
	Warnings   []string  `json:"warnings"`
}

func (w *world) extList(t *testing.T) extList {
	t.Helper()
	var l extList
	w.runJSON(t, &l, "ext", "list", "--folder", w.root)
	return l
}

func (l extList) item(id string) *extItem {
	for i := range l.Extensions {
		if l.Extensions[i].ID == id {
			return &l.Extensions[i]
		}
	}
	return nil
}

func TestExtListEnableDisable(t *testing.T) {
	w := newWorld(t)
	cfgPath := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "config.toml")

	// Scripts is built in and off.
	l := w.extList(t)
	ig := l.item("scripts")
	if ig == nil || ig.Where != ext.WhereBundled || ig.On || ig.Enabled || ig.Version != "1.0.0" {
		t.Fatalf("scripts: %+v", ig)
	}
	w.stdout.Reset()
	if code := w.run("ext", "list", "--folder", w.root); code != ExitOK ||
		!strings.Contains(w.stdout.String(), "scripts  1.0.0    off    built in") {
		t.Errorf("list: %d\n%s", code, w.stdout)
	}

	// Enable it: config.toml gets it, and it's on (when its ui/ is built).
	w.stdout.Reset()
	if code := w.run("ext", "enable", "scripts", "--folder", w.root); code != ExitOK ||
		!strings.Contains(w.stdout.String(), "scripts 1.0.0 is on") {
		t.Fatalf("enable: %d %s %s", code, w.stdout, w.stderr)
	}
	c, _, err := config.Load(cfgPath)
	if err != nil || !slices.Equal(c.Extensions.Enabled, []string{"scripts"}) || len(c.Extensions.Pins) != 0 {
		t.Fatalf("config: %+v %v", c.Extensions, err)
	}
	if ig := w.extList(t).item("scripts"); !ig.Enabled || ig.On != (len(ig.Problems) == 0) {
		t.Errorf("enabled scripts: %+v", ig)
	}

	// A user's copy shadows the built-in one.
	user := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "extensions")
	helloTo(t, filepath.Join(user, "hello"))
	if code := w.run("ext", "enable", "hello"); code != ExitOK {
		t.Fatalf("enable hello: %d %s", code, w.stderr)
	}
	if h := w.extList(t).item("hello"); h == nil || !h.On || h.Where != ext.WhereUser {
		t.Errorf("hello: %+v", h)
	}

	// Disable: out of the list; again: already off.
	w.stdout.Reset()
	if code := w.run("ext", "disable", "scripts"); code != ExitOK || !strings.Contains(w.stdout.String(), "scripts is off") {
		t.Errorf("disable: %d %s", code, w.stdout)
	}
	w.stdout.Reset()
	if code := w.run("ext", "disable", "scripts"); code != ExitOK || !strings.Contains(w.stdout.String(), "already off") {
		t.Errorf("disable again: %d %s", code, w.stdout)
	}
	if c, _, _ := config.Load(cfgPath); !slices.Equal(c.Extensions.Enabled, []string{"hello"}) {
		t.Errorf("after disable: %v", c.Extensions.Enabled)
	}

	// Unknown ids and bad arguments.
	w.stderr.Reset()
	if code := w.run("ext", "enable", "nope"); code != ExitError || !strings.Contains(w.stderr.String(), "retake ext list") ||
		!strings.Contains(w.stderr.String(), filepath.Join(user, "nope")) {
		t.Errorf("enable nope: %d %s", code, w.stderr)
	}
	for _, args := range [][]string{{"ext"}, {"ext", "paint"}, {"ext", "enable"}, {"ext", "enable", "Bad_ID"},
		{"ext", "enable", "a", "b"}, {"ext", "disable"}, {"ext", "list", "x"}, {"ext", "list", "--nope"},
		{"ext", "check", "--nope"}} {
		if code := w.run(args...); code != ExitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// Ignition is Scripts now (spec/extensions.md, "Renamed ids"): a config that says ignition has Scripts on, and the
// commands take the old id.
func TestExtRenamedID(t *testing.T) {
	w := newWorld(t)
	cfgPath := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "config.toml")
	w.config(t, "[extensions]\nenabled = [\"ignition\"]\n")
	if sc := w.extList(t).item("scripts"); sc == nil || !sc.Enabled {
		t.Fatalf("scripts with ignition enabled: %+v", sc)
	}
	w.stdout.Reset()
	if code := w.run("ext", "enable", "ignition"); code != ExitOK || !strings.Contains(w.stdout.String(), "scripts 1.0.0 is on") {
		t.Errorf("enable ignition: %d %s %s", code, w.stdout, w.stderr)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != "[extensions]\nenabled = [\"ignition\"]\n" {
		t.Errorf("enable rewrote a working config: %q", b)
	}
	if code := w.run("ext", "check", "ignition"); code == ExitUsage {
		t.Errorf("check ignition: %d %s", code, w.stderr)
	}
	w.stdout.Reset()
	if code := w.run("ext", "disable", "ignition"); code != ExitOK || !strings.Contains(w.stdout.String(), "scripts is off") {
		t.Errorf("disable ignition: %d %s", code, w.stdout)
	}
	if c, _, _ := config.Load(cfgPath); len(c.Extensions.Enabled) != 0 {
		t.Errorf("after disable: %v", c.Extensions.Enabled)
	}
}

// A folder's own extension is pinned when enabled, and off once its files change.
func TestExtProjectPin(t *testing.T) {
	w := newWorld(t)
	dir := helloTo(t, filepath.Join(w.root, ".retake", "extensions", "hello"))
	if h := w.extList(t).item("hello"); h == nil || h.Where != ext.WhereProject || h.On {
		t.Fatalf("found: %+v", h)
	}
	w.stdout.Reset()
	if code := w.run("ext", "enable", "hello", "--folder", w.root); code != ExitOK || !strings.Contains(w.stdout.String(), "pinned") {
		t.Fatalf("enable: %d %s %s", code, w.stdout, w.stderr)
	}
	c, _, _ := config.Load(filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "config.toml"))
	h := w.extList(t).item("hello")
	if c.Extensions.Pins["hello"] != h.Hash || !h.On {
		t.Fatalf("pin %q, hello %+v", c.Extensions.Pins["hello"], h)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "hello.js"), []byte("export function page() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := w.extList(t)
	if h := l.item("hello"); h.On || !h.Enabled {
		t.Errorf("changed: %+v", h)
	}
	if !slices.Contains(l.Warnings, "hello changed since you enabled it: run retake ext enable hello again") {
		t.Errorf("warnings %v", l.Warnings)
	}
	w.stdout.Reset()
	if code := w.run("ext", "list", "--folder", w.root); code != ExitOK || !strings.Contains(w.stdout.String(), "error") ||
		!strings.Contains(w.stdout.String(), "! hello changed since you enabled it") {
		t.Errorf("list: %s", w.stdout)
	}
	if code := w.run("ext", "enable", "hello", "--folder", w.root); code != ExitOK {
		t.Fatal(code)
	}
	if h := w.extList(t).item("hello"); !h.On {
		t.Errorf("enabled again: %+v", h)
	}
	if code := w.run("ext", "disable", "hello"); code != ExitOK {
		t.Fatal(code)
	}
	if c, _, _ := config.Load(filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "config.toml")); len(c.Extensions.Pins) != 0 {
		t.Errorf("pin kept: %v", c.Extensions.Pins)
	}
}

func TestExtCheck(t *testing.T) {
	w := newWorld(t)
	hello := filepath.Join("..", "ext", "testdata", "hello")
	broken := filepath.Join("..", "ext", "testdata", "broken")
	if code := w.run("ext", "check", hello); code != ExitOK || !strings.Contains(w.stdout.String(), "ext ok: hello 0.1.0 (api 1)") {
		t.Errorf("hello: %d %s", code, w.stdout)
	}
	w.stdout.Reset()
	if code := w.run("ext", "check", broken); code != ExitError || !strings.Contains(w.stdout.String(), "ext broken 0.1.0: 11 problems") ||
		!strings.Contains(w.stdout.String(), "doesn't export sidebarTab") {
		t.Errorf("broken: %d %s", code, w.stdout)
	}
	w.stdout.Reset()
	if code := w.run("ext", "check", hello, broken, "--json"); code != ExitError {
		t.Errorf("--json: %d", code)
	}
	var reports []ext.Report
	if err := json.Unmarshal([]byte(w.stdout.String()), &reports); err != nil || len(reports) != 2 ||
		reports[0].ID != "hello" || len(reports[0].Problems) != 0 || len(reports[1].Problems) != 11 {
		t.Errorf("json: %v %s", err, w.stdout)
	}
	if !strings.Contains(w.stdout.String(), `"problems": []`) {
		t.Errorf("an empty list isn't []: %s", w.stdout)
	}
	if code := w.run("ext", "check", "nosuch"); code != ExitUsage {
		t.Errorf("unknown id: %d", code)
	}
	w.stdout.Reset()
	if code := w.run("ext", "check", "--folder", w.root); code != ExitOK || !strings.Contains(w.stdout.String(), "no extensions are enabled") {
		t.Errorf("none enabled: %d %s", code, w.stdout)
	}
	// By id: one found from the folder; with none named, every enabled one.
	helloTo(t, filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "extensions", "hello"))
	w.stdout.Reset()
	if code := w.run("ext", "check", "hello", "--folder", w.root); code != ExitOK || !strings.Contains(w.stdout.String(), "ext ok: hello") {
		t.Errorf("by id: %d %s", code, w.stdout)
	}
	w.config(t, "[extensions]\nenabled = [\"hello\", \"ghost\"]\n")
	w.stdout.Reset()
	if code := w.run("ext", "check", "--folder", w.root); code != ExitError || !strings.Contains(w.stdout.String(), "ext ok: hello") ||
		!strings.Contains(w.stdout.String(), "ghost is enabled but wasn't found") {
		t.Errorf("enabled: %d %s", code, w.stdout)
	}
}
