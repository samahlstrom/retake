package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/assets"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssetsCLI(t *testing.T) {
	w := newWorld(t)
	proj := filepath.Join(w.root, "edit")
	media := filepath.Join(w.root, "incoming")
	writeFile(t, filepath.Join(media, "dragon-roar.bin"), "roar")
	writeFile(t, filepath.Join(media, "castle.bin"), "castle")
	p := "--project=" + proj

	code, out, errOut := w.out("assets", "import", filepath.Join(media, "dragon-roar.bin"), p, "--license", "attribution",
		"--credit", "SFX: Example (CC BY 4.0)", "--tags", "sfx, Monster", "--json")
	if code != ExitOK {
		t.Fatalf("import: %d %s %s", code, out, errOut)
	}
	imp := decode[assets.ImportResult](t, out)
	if imp.Imported != 1 || imp.Items[0].ID != "proj:dragon-roar" {
		t.Fatalf("import = %+v", imp)
	}
	// Human output; an unlicensed import is review-only.
	code, out, _ = w.out("assets", "import", filepath.Join(media, "castle.bin"), p)
	if code != ExitOK || !strings.Contains(out, "imported  proj:castle (other)") || !strings.Contains(out, "1 imported") {
		t.Fatalf("import (human): %d %s", code, out)
	}
	code, out, _ = w.out("assets", "import", filepath.Join(media, "castle.bin"), p)
	if code != ExitOK || !strings.Contains(out, "duplicate of proj:castle") {
		t.Fatalf("import again: %d %s", code, out)
	}
	// The library is the user's: here under the test's HOME.
	code, _, errOut = w.out("assets", "import", filepath.Join(media, "dragon-roar.bin"), p, "--scope", "lib", "--name", "x")
	if code != ExitOK {
		t.Fatalf("lib import: %s", errOut)
	}

	code, out, _ = w.out("assets", "search", "drag", p, "--json")
	res := decode[assets.SearchResult](t, out)
	if code != ExitOK || len(res.Results) != 1 || res.Results[0].Asset != "proj:dragon-roar" {
		t.Fatalf("search: %d %+v", code, res)
	}
	code, out, _ = w.out("assets", "search", p, "--tags", "monster")
	if code != ExitOK || !strings.Contains(out, `proj:dragon-roar  other  attribution  "dragon roar"  [sfx, monster]`) {
		t.Fatalf("search (human): %s", out)
	}
	code, out, _ = w.out("assets", "get", "proj:castle", p)
	if e := decode[assets.Entry](t, out); code != ExitOK || e.License.Status != assets.LicenseReviewOnly ||
		e.License.Note != "no license given at import" {
		t.Fatalf("get: %+v", e)
	}

	// Spans by hand.
	code, out, _ = w.out("assets", "span", "add", "proj:castle", "0:01", "2.5", "--label", "gate opens", p, "--json")
	sp := decode[assets.Span](t, out)
	if code != ExitOK || sp.Span != "sp:1" || sp.T0 != 1 || sp.T1 != 2.5 || sp.SourceOfLabel != "manual" {
		t.Fatalf("span add: %+v", sp)
	}
	code, out, _ = w.out("assets", "span", "find", "--type", "tag", p)
	if code != ExitOK || !strings.Contains(out, `sp:1     proj:castle 0:01.0-0:02.5  tag "gate opens"  (manual)`) {
		t.Fatalf("span find: %q", out)
	}

	// The resolver: one JSON object per line, exit 1 when any id fails.
	code, out, _ = w.out("assets", "resolve", "proj:castle", "proj:nope", p)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != ExitError || len(lines) != 2 {
		t.Fatalf("resolve: %d %q", code, out)
	}
	var r assets.Resolved
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil || r.Hash != assets.HashBytes([]byte("castle")) ||
		r.Kind != assets.KindOther || !strings.HasPrefix(r.Path, filepath.Join(proj, ".retake", "blobs")) {
		t.Fatalf("resolve line 1 = %+v %v", r, err)
	}
	if !strings.Contains(lines[1], `"id":"proj:nope","code":"not_found"`) {
		t.Fatalf("resolve line 2 = %s", lines[1])
	}

	// Licenses change only through set; the report reads a manifest.
	code, _, errOut = w.out("assets", "set", "proj:castle", "--credit", "x", p)
	if code != ExitUsage || !strings.Contains(errOut, "go with --license") {
		t.Fatalf("set --credit alone: %d %s", code, errOut)
	}
	manifest := filepath.Join(proj, "v1.manifest.json")
	writeFile(t, manifest, `{"schema": "manifest/0.1", "units": [{"unit": "u1", "t": [0, 4], "elements": [
		{"id": "a", "kind": "overlay", "t": [1, 2], "sources": [{"asset": "proj:castle", "in": 0, "out": 1}]},
		{"id": "b", "kind": "audio", "sources": [{"asset": "proj:dragon-roar", "in": 0, "out": 4}]}]}]}`)
	code, out, _ = w.out("assets", "license-report", manifest, p)
	if code != ExitOK || !strings.Contains(out, "NOT publishable") || !strings.Contains(out, "proj:castle: review-only") ||
		!strings.Contains(out, "SFX: Example (CC BY 4.0)") {
		t.Fatalf("license-report: %s", out)
	}
	code, out, _ = w.out("assets", "set", "proj:castle", "--license", "owned", "--title", "Castle gate", p)
	if code != ExitOK || !strings.Contains(out, `proj:castle  "Castle gate"  []  license owned`) {
		t.Fatalf("set: %d %s", code, out)
	}
	code, out, _ = w.out("assets", "license-report", manifest, p, "--json")
	if rep := decode[assets.LicenseReport](t, out); code != ExitOK || !rep.Publishable {
		t.Fatalf("license-report after set: %+v", rep)
	}

	code, out, _ = w.out("assets", "sql", "SELECT id, license FROM assets ORDER BY id", p)
	if code != ExitOK || !strings.Contains(out, "proj:castle\towned") || !strings.Contains(out, "(2 rows)") {
		t.Fatalf("sql: %s", out)
	}
	code, out, _ = w.out("assets", "sql", "UPDATE assets SET title = 'x'", p, "--json")
	if code != ExitError || !strings.Contains(out, `"code": "bad_request"`) {
		t.Fatalf("sql write: %d %s", code, out)
	}
	code, out, _ = w.out("assets", "plugins", p)
	if code != ExitOK || !strings.Contains(out, "no plug-ins") {
		t.Fatalf("plugins: %s", out)
	}

	// Usage errors exit 2; failures exit 1.
	for _, args := range [][]string{{"assets", "import", p}, {"assets", "bogus"}, {"assets", "span"},
		{"assets", "get", p}, {"assets", "import", "x", "--track", "-1", p}, {"assets", "span", "add", "proj:x", "a", "b", p}} {
		if code, _, _ := w.out(args...); code != ExitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, ExitUsage)
		}
	}
	if code, _, errOut := w.out("assets", "get", "proj:nope", p); code != ExitError || !strings.Contains(errOut, "no asset proj:nope") {
		t.Errorf("get missing: %d %s", code, errOut)
	}
	if code, out, _ := w.out("assets", "help"); code != ExitOK || !strings.Contains(out, "retake assets resolve") {
		t.Errorf("help: %s", out)
	}
}

func TestAssetsProjectFromEnv(t *testing.T) {
	w := newWorld(t)
	proj := filepath.Join(w.root, "elsewhere")
	writeFile(t, filepath.Join(proj, "a.bin"), "a")
	w.env["RETAKE_PROJECT"] = proj
	if code, _, errOut := w.out("assets", "import", filepath.Join(proj, "a.bin")); code != ExitOK {
		t.Fatalf("import: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(proj, ".retake", assets.DBName)); err != nil {
		t.Fatalf("$RETAKE_PROJECT wasn't used: %v", err)
	}
}
