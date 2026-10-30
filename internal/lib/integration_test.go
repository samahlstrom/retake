package lib

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/bridge"
)

// TestSampleWithLibraryComponentsAndAKit is the shared library end to end on the CC0 sample project
// (examples/sample): two library components and the clean kit are added from the starter registry, a patch puts
// them in the timeline, the kit's house rules catch the sample's own labels outside the action-safe area, a second
// patch fixes them and lint passes, then the timeline renders and its manifest names the components. It needs
// retake-edit ($RETAKE_EDIT, $PATH or editor/target/release) and ffmpeg, and skips with -short.
func TestSampleWithLibraryComponentsAndAKit(t *testing.T) {
	if testing.Short() {
		t.Skip("renders the sample")
	}
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not installed", b)
		}
	}
	r := repo(t)
	ed := bridge.Editor{Dir: r, Exe: filepath.Join(r, "bin", "retake")}
	bin, err := ed.Find()
	if err != nil {
		t.Skipf("no retake-edit: %v", err)
	}
	ed = bridge.Editor{Configured: bin}
	ctx := context.Background()
	edit := filepath.Join(t.TempDir(), "edit")
	sample := filepath.Join(r, "examples", "sample")
	for _, f := range []string{"timeline.json", "voice/vo.ogg", "voice/vo.words.json"} {
		b, err := os.ReadFile(filepath.Join(sample, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(edit, filepath.FromSlash(f)), string(b))
	}
	run := func(name string, args ...string) []byte {
		t.Helper()
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			var ee *exec.ExitError
			if ok := asExit(err, &ee); ok {
				t.Fatalf("%s %v: %v\n%s\n%s", name, args, err, out, ee.Stderr)
			}
			t.Fatalf("%s %v: %v", name, args, err)
		}
		return out
	}
	// The sample's footage, as scripts/make-sample.sh makes it, smaller.
	if err := os.MkdirAll(filepath.Join(edit, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ name, src string }{{"rinse", "testsrc2"}, {"grind", "testsrc"}, {"pour", "smptebars"}} {
		run("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", m.src+"=size=320x180:rate=30", "-t", "8",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "30", "-pix_fmt", "yuv420p", "-an",
			filepath.Join(edit, "media", m.name+".mp4"))
	}
	tl := filepath.Join(edit, "timeline.json")
	reg := registryPath(t)

	// Two components and the kit from the registry.
	for _, ref := range []string{"title-card", "pop-text@1.0.0", "clean"} {
		if _, err := Add(ctx, AddArgs{Ref: ref, Registry: reg, Timeline: tl}); err != nil {
			t.Fatalf("add %s: %v", ref, err)
		}
	}
	// Go and the editor agree on every package hash.
	h, _ := PackageHash(filepath.Join(edit, "components", "title-card@1.0.0"))
	var hres struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(run(bin, "component", "hash", filepath.Join(edit, "components", "title-card@1.0.0"), "--json"), &hres); err != nil || hres.Hash != h {
		t.Fatalf("hashes: go %s, retake-edit %s (%v)", h, hres.Hash, err)
	}

	// A patch, as an agent writes it: the kit, the title card and the pop from the library.
	patch := func(name, body string) {
		t.Helper()
		p := filepath.Join(edit, name)
		write(t, p, body)
		run(bin, "patch", "apply", tl, p, "--json")
	}
	patch("p1.json", `{"base": 0, "answers": [], "summary": "Title card and the grams pop from the library; the clean kit",
		"ops": [
		  {"op": "set", "path": "kits", "value": ["clean@1.0.0"]},
		  {"op": "set", "id": "title/card", "path": "picture", "value": {"use": "title-card@1.0.0",
		    "props": {"title": "Pour-over coffee", "subtitle": "in a few simple steps", "bg": "#1b1410"}}},
		  {"op": "set", "id": "pop/grams", "path": "", "value": {"id": "pop/grams", "use": "pop-text@1.0.0",
		    "from": "word:c/Add", "to": "end:c", "z": 2, "props": {"text": "20 g", "x": 1590, "y": 255}}}
		]}`)

	// The house rules see the sample's hand-made step labels hugging the left edge.
	res, err := Lint(ctx, ed, tl)
	if err != nil {
		t.Fatal(err)
	}
	var safe []string
	for _, v := range res.Violations {
		if v.ID == "graphics-in-action-safe" {
			safe = append(safe, v.Elements...)
		}
	}
	if !res.Boxes || res.Clean || !slices.Equal(safe, []string{"label/1", "label/2", "label/3"}) || res.Rules != 7 {
		t.Fatalf("lint = %+v", res)
	}
	if !strings.Contains(res.Violations[0].Message, "draws outside the safe area") || res.Violations[0].Hint == "" {
		t.Fatalf("violation = %+v", res.Violations[0])
	}
	// Fixed: the labels move in; lint passes.
	var ops []string
	for i := 1; i <= 3; i++ {
		ops = append(ops,
			`{"op": "set", "id": "label/`+itoa(i)+`/bar", "path": "transform/position", "value": [64, 640]}`,
			`{"op": "set", "id": "label/`+itoa(i)+`/text", "path": "transform/position", "value": [88, 640]}`)
	}
	patch("p2.json", `{"base": 1, "answers": [], "summary": "Step labels inside the action-safe area", "ops": [`+
		strings.Join(ops, ",")+`]}`)
	if res, err = Lint(ctx, ed, tl); err != nil || !res.Clean || res.Errors != 0 {
		t.Fatalf("lint after the fix = %+v %v", res, err)
	}

	// It renders, and the manifest names the components that drew each element.
	out := filepath.Join(edit, "render", "how-to.mp4")
	var rep bridge.RenderReport
	if err := json.Unmarshal(run(bin, "render", tl, "-o", out, "--draft", "--scale", "0.25", "--json"), &rep); err != nil {
		t.Fatal(err)
	}
	m, err := bridge.ForVideo(out)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(strings.TrimSuffix(out, ".mp4") + ".manifest.json")
	var raw struct {
		Units []struct {
			Elements []struct {
				ID  string `json:"id"`
				Use string `json:"use"`
			} `json:"elements"`
		} `json:"units"`
	}
	_ = json.Unmarshal(b, &raw)
	uses := map[string]string{}
	for _, u := range raw.Units {
		for _, e := range u.Elements {
			if e.Use != "" {
				uses[e.ID] = e.Use
			}
		}
	}
	if len(m.Units) < 5 || uses["title/card"] != "title-card@1.0.0" || uses["pop/grams"] != "pop-text@1.0.0" {
		t.Fatalf("manifest: %d units, uses %v", len(m.Units), uses)
	}

	// And its recipe carries the timeline, the patches and the three packages, not the media.
	rec, err := ExportRecipe(ctx, RecipeArgs{Timeline: tl, Out: filepath.Join(edit, "..", "recipe")})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Recipe.Patches != 2 || len(rec.Recipe.Packages) != 11 || rec.Recipe.Placeholders["proj:rinse"].Path != "media/proj-rinse.mp4" {
		t.Fatalf("recipe = %+v", rec.Recipe)
	}
}

func itoa(i int) string { return string(rune('0' + i)) }

func asExit(err error, ee **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exec returns it unwrapped
	if ok {
		*ee = e
	}
	return ok
}
