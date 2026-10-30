package lib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"lukechampine.com/blake3"

	"github.com/samahlstrom/retake/internal/assets"
)

// TestMain keeps the tests away from the user's library, registry and component path.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "retake-lib-test-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{"RETAKE_LIBRARY": filepath.Join(dir, "library"), "RETAKE_COMPONENT_PATH": "",
		"RETAKE_REGISTRY": "", "RETAKE_PROJECT": "", "XDG_CONFIG_HOME": filepath.Join(dir, "config")} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func repo(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func registryPath(t *testing.T) string { return filepath.Join(repo(t), "library", "index.json") }

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return fmt.Sprintf("not a lib error: %v", err)
}

// project is a timeline in a fresh folder.
func project(t *testing.T) string {
	t.Helper()
	tl := filepath.Join(t.TempDir(), "edit", "timeline.json")
	write(t, tl, `{"schema": "timeline/0.1", "id": "t", "rev": 0, "format": {"size": [1280, 720], "fps": 30},
		"assets": {}, "spine": [], "scenes": []}`)
	return tl
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := copyPackage(src, dst); err != nil {
		t.Fatal(err)
	}
}

func TestPackageHashIsTheSpecsAndTheIndexIsCurrent(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "component.json"), `{"name": "x"}`)
	write(t, filepath.Join(d, "fonts", "OFL.txt"), "ofl")
	write(t, filepath.Join(d, "previews", "sheet.png"), "not hashed")
	write(t, filepath.Join(d, ".hidden"), "not hashed")
	h := blake3.New(32, nil)
	for _, f := range [][2]string{{"component.json", `{"name": "x"}`}, {"fonts/OFL.txt", "ofl"}} {
		s := blake3.Sum256([]byte(f[1]))
		_, _ = fmt.Fprintf(h, "%s\x00%x\n", f[0], s)
	}
	got, err := PackageHash(d)
	if err != nil || got != fmt.Sprintf("b3:%x", h.Sum(nil)) {
		t.Fatalf("PackageHash = %s, %v", got, err)
	}
	// library/index.json is built from library/ (scripts/build-registry.sh): it must match the packages.
	r, err := LoadRegistry(context.Background(), registryPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Packages) < 9 {
		t.Fatalf("%d packages in the starter registry", len(r.Packages))
	}
	for _, e := range r.Packages {
		h, err := PackageHash(r.Where(e.Path))
		if err != nil || h != e.Hash {
			t.Errorf("%s: %s on disk, %s in library/index.json: run scripts/build-registry.sh", e.Ref(), h, e.Hash)
		}
		if e.License != "CC0-1.0" || (e.Type == TypeComponent && e.Previews["sheet"] == "") {
			t.Errorf("%s: license %s, previews %v", e.Ref(), e.License, e.Previews)
		}
	}
}

func TestAddCopiesChecksAndLocks(t *testing.T) {
	ctx := context.Background()
	tl := project(t)
	reg := registryPath(t)
	res, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: reg, Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(filepath.Dir(tl), "components", "pop-text@1.0.0")
	if len(res.Packages) != 1 || res.Packages[0].Status != "added" || res.Packages[0].Path != "components/pop-text@1.0.0" {
		t.Fatalf("add = %+v", res)
	}
	if !fileExists(filepath.Join(dest, "component.json")) || !fileExists(filepath.Join(dest, "previews", "sheet.png")) {
		t.Fatal("the package and its sheet are in components/")
	}
	if !strings.Contains(res.Use, `"use": "pop-text@1.0.0"`) {
		t.Fatalf("use = %s", res.Use)
	}
	lock, err := ReadLock(LockPath(tl))
	if err != nil {
		t.Fatal(err)
	}
	e := lock.Packages["pop-text@1.0.0"]
	h, _ := PackageHash(dest)
	if e.Hash != h || e.Type != TypeComponent || e.License != "CC0-1.0" || !strings.HasPrefix(e.Source, "registry:") ||
		strings.Contains(e.Source, os.Getenv("HOME")+"/") && filepath.IsAbs(strings.TrimPrefix(e.Source, "registry:")) {
		t.Fatalf("lock entry = %+v (hash on disk %s)", e, h)
	}
	if lock.Schema != LockSchema || !strings.HasSuffix(LockPath(tl), "timeline.lock") {
		t.Fatalf("lock = %+v at %s", lock, LockPath(tl))
	}

	// Again: unchanged. A changed project copy is a conflict, unless --force.
	if res, err := Add(ctx, AddArgs{Ref: "pop-text@1.0.0", Registry: reg, Timeline: tl}); err != nil ||
		res.Packages[0].Status != "unchanged" {
		t.Fatalf("again: %+v %v", res, err)
	}
	write(t, filepath.Join(dest, "component.json"), "{}")
	if _, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: reg, Timeline: tl}); code(err) != CodeConflict {
		t.Fatalf("a changed copy: %v", err)
	}
	if res, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: reg, Timeline: tl, Force: true}); err != nil ||
		res.Packages[0].Status != "replaced" {
		t.Fatalf("--force: %+v %v", res, err)
	}

	// A kit brings its components.
	res, err = Add(ctx, AddArgs{Ref: "clean", Registry: reg, Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	if res.Packages[0].Ref != "clean@1.0.0" || res.Packages[0].Type != TypeKit || len(res.Packages) != 11 {
		t.Fatalf("kit add = %+v", res.Packages)
	}
	for _, p := range res.Packages[1:] {
		if p.Because != "clean@1.0.0" {
			t.Fatalf("%+v", p)
		}
	}
	if !strings.Contains(res.Use, `"kits"`) {
		t.Fatalf("use = %s", res.Use)
	}
	lock, _ = ReadLock(LockPath(tl))
	if len(lock.Packages) != 11 {
		t.Fatalf("%d locked", len(lock.Packages))
	}

	// Unknown names and versions.
	if _, err := Add(ctx, AddArgs{Ref: "pop-text@9.0.0", Registry: reg, Timeline: tl}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "it has 1.0.0") {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := Add(ctx, AddArgs{Ref: "Pop", Registry: reg, Timeline: tl}); code(err) != CodeBadRequest {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := Add(ctx, AddArgs{Ref: "pop-text", Timeline: tl}); code(err) != CodeBadRequest {
		t.Fatalf("no registry: %v", err)
	}
}

// fakeRegistry copies the starter registry somewhere it can be broken.
func fakeRegistry(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	src := filepath.Join(repo(t), "library")
	for _, p := range []string{"components/pop-text", "components/caption", "kits/clean"} {
		copyDir(t, filepath.Join(src, p), filepath.Join(d, p))
	}
	b, err := os.ReadFile(filepath.Join(src, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ix Index
	if err := json.Unmarshal(b, &ix); err != nil {
		t.Fatal(err)
	}
	ix.Packages = slices.DeleteFunc(ix.Packages, func(e Entry) bool {
		return e.Name != "pop-text" && e.Name != "caption" && e.Name != "clean"
	})
	if err := writeJSON(filepath.Join(d, "index.json"), ix); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAddRefusesBrokenPackagesAndLeavesNothing(t *testing.T) {
	ctx := context.Background()
	reg := fakeRegistry(t)
	tl := project(t)
	write(t, filepath.Join(reg, "components", "pop-text", "component.json"), `{"tampered": true}`)
	if _, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: reg, Timeline: tl}); code(err) != CodeHash {
		t.Fatalf("a tampered file: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(filepath.Dir(tl), "components"))
	if len(ents) != 0 {
		t.Fatalf("left behind: %v", ents)
	}
	if fileExists(LockPath(tl)) {
		t.Fatal("no lock written for a failed add")
	}
	// A kit that requires what the registry doesn't have.
	if _, err := Add(ctx, AddArgs{Ref: "clean", Registry: reg, Timeline: tl}); code(err) != CodeNotFound ||
		!strings.Contains(err.Error(), "clean@1.0.0 requires") {
		t.Fatalf("missing requirement: %v", err)
	}
	// A package whose license isn't open.
	var ix Index
	b, _ := os.ReadFile(filepath.Join(reg, "index.json"))
	_ = json.Unmarshal(b, &ix)
	for i := range ix.Packages {
		ix.Packages[i].License = "LicenseRef-proprietary"
	}
	if err := writeJSON(filepath.Join(reg, "index.json"), ix); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, AddArgs{Ref: "caption", Registry: reg, Timeline: tl}); code(err) != CodeLicense {
		t.Fatalf("a proprietary package: %v", err)
	}
}

func TestAddFromAURLRegistry(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(repo(t), "library"))))
	defer srv.Close()
	tl := project(t)
	res, err := Add(context.Background(), AddArgs{Ref: "caption@1.0.0", Registry: srv.URL, Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	lock, _ := ReadLock(LockPath(tl))
	if res.Packages[0].Status != "added" || lock.Packages["caption@1.0.0"].Source != "registry:"+srv.URL {
		t.Fatalf("%+v %+v", res, lock)
	}
	sr, err := Search(context.Background(), SearchArgs{Query: "karaoke", Registry: srv.URL + "/index.json"})
	if err != nil || len(sr.Hits) != 1 || !strings.HasPrefix(sr.Hits[0].Sheet, srv.URL+"/components/caption/") {
		t.Fatalf("search over HTTP: %+v %v", sr, err)
	}
}

func TestSearchRegistryAndUserLibrary(t *testing.T) {
	ctx := context.Background()
	reg := registryPath(t)
	tl := project(t)
	if _, err := Add(ctx, AddArgs{Ref: "caption", Registry: reg, Timeline: tl}); err != nil {
		t.Fatal(err)
	}
	res, err := Search(ctx, SearchArgs{Query: "captions words", Registry: reg, Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Name != "caption" || !res.Hits[0].Installed || res.Hits[0].Source != "registry" ||
		!fileExists(res.Hits[0].Sheet) {
		t.Fatalf("search = %+v", res.Hits)
	}
	kits, _ := Search(ctx, SearchArgs{Registry: reg, Type: TypeKit})
	if len(kits.Hits) != 1 || kits.Hits[0].Name != "clean" {
		t.Fatalf("kits = %+v", kits.Hits)
	}
	all, _ := Search(ctx, SearchArgs{Registry: reg})
	if len(all.Hits) != 11 {
		t.Fatalf("%d hits for everything", len(all.Hits))
	}
	// The user library: a component of one's own, two versions.
	ul := UserComponents(os.Getenv)
	for _, v := range []string{"0.9.0", "1.2.0"} {
		write(t, filepath.Join(ul, "shout@"+v, "component.json"), `{"name": "shout", "version": "`+v+`",
			"title": "Shout", "description": "My own big text", "license": "MIT", "tags": ["text"], "use_as": ["overlay"]}`)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ul) })
	res, _ = Search(ctx, SearchArgs{Query: "text", Registry: reg})
	var mine *Hit
	for i, h := range res.Hits {
		if h.Name == "shout" {
			mine = &res.Hits[i]
		}
	}
	if mine == nil || mine.Source != "library" || mine.Version != "1.2.0" || !slices.Equal(mine.Versions, []string{"1.2.0", "0.9.0"}) {
		t.Fatalf("the user library: %+v", res.Hits)
	}
	if _, err := Search(ctx, SearchArgs{Registry: reg, Type: "font"}); code(err) != CodeBadRequest {
		t.Fatalf("bad type: %v", err)
	}
}

// component writes a component package from the library's pop-text with changes to its JSON.
func component(t *testing.T, change func(m map[string]any)) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "pkg")
	copyDir(t, filepath.Join(repo(t), "library", "components", "pop-text"), d)
	b, _ := os.ReadFile(filepath.Join(d, "component.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	nb, _ := json.Marshal(m)
	write(t, filepath.Join(d, "component.json"), string(nb))
	return d
}

func pngFile(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestPackValidatesAndGatesLicenses(t *testing.T) {
	ctx := context.Background()
	ok := component(t, func(map[string]any) {})
	res, err := Pack(ctx, PackArgs{Dir: ok})
	if err != nil || res.Entry.Name != "pop-text" || res.Entry.Hash == "" || len(res.Entry.Files) != 1 {
		t.Fatalf("pack = %+v %v", res, err)
	}
	cases := []struct {
		name, code string
		change     func(m map[string]any)
	}{
		{"GPL", CodeLicense, func(m map[string]any) { m["license"] = "GPL-3.0-only" }},
		{"no license", CodeLicense, func(m map[string]any) { delete(m, "license") }},
		{"bad version", CodeInvalid, func(m map[string]any) { m["version"] = "1.0" }},
		{"no examples", CodeInvalid, func(m map[string]any) { m["examples"] = []any{} }},
		{"undocumented prop", CodeInvalid, func(m map[string]any) {
			m["props"].(map[string]any)["properties"].(map[string]any)["x"] = map[string]any{"type": "number"}
		}},
		{"example misses a required prop", CodeInvalid, func(m map[string]any) {
			m["examples"] = []any{map[string]any{"name": "e", "props": map[string]any{"size": 80}}}
		}},
	}
	for _, c := range cases {
		if _, err := Pack(ctx, PackArgs{Dir: component(t, c.change)}); code(err) != c.code {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// A font travels with its license file.
	withFont := component(t, func(map[string]any) {})
	write(t, filepath.Join(withFont, "fonts", "Display.ttf"), "not really a font")
	if _, err := Pack(ctx, PackArgs{Dir: withFont}); code(err) != CodeLicense || !strings.Contains(err.Error(), "fonts/Display.ttf") {
		t.Fatalf("font without a license: %v", err)
	}
	write(t, filepath.Join(withFont, "fonts", "OFL.txt"), "SIL Open Font License")
	if _, err := Pack(ctx, PackArgs{Dir: withFont}); err != nil {
		t.Fatalf("font with OFL.txt: %v", err)
	}

	// The catalog's gate: a prop's asset that is review-only or blocked.
	root := t.TempDir()
	logo := filepath.Join(root, "logo.png")
	pngFile(t, logo)
	cat, err := assets.Open(assets.Options{Root: root, Library: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cat.Close() }()
	if _, err := cat.Import(ctx, assets.ImportArgs{Paths: []string{logo}, Name: "logo"}); err != nil {
		t.Fatal(err)
	}
	withInput := component(t, func(m map[string]any) {
		m["props"].(map[string]any)["properties"].(map[string]any)["logo"] = map[string]any{"type": "string",
			"x-retake-input": "image", "default": "proj:logo", "description": "A logo."}
	})
	if _, err := Pack(ctx, PackArgs{Dir: withInput, Catalog: cat}); code(err) != CodeLicense ||
		!strings.Contains(err.Error(), "proj:logo, which the catalog has as review-only") {
		t.Fatalf("review-only input: %v", err)
	}
	if _, err := cat.Set(ctx, assets.SetArgs{ID: "proj:logo", License: &assets.LicenseArgs{Status: assets.LicenseOwned}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(ctx, PackArgs{Dir: withInput, Catalog: cat}); err != nil {
		t.Fatalf("owned input: %v", err)
	}

	// Into an index: paths relative to it; a package outside its folder is refused.
	regDir := t.TempDir()
	inside := filepath.Join(regDir, "components", "pop-text")
	copyDir(t, ok, inside)
	idx := filepath.Join(regDir, "index.json")
	res, err = Pack(ctx, PackArgs{Dir: inside, Index: idx})
	if err != nil || res.Entry.Path != "components/pop-text" || res.Index != idx {
		t.Fatalf("pack --index = %+v %v", res, err)
	}
	if _, err := Pack(ctx, PackArgs{Dir: ok, Index: idx}); code(err) != CodeBadRequest {
		t.Fatalf("outside the index: %v", err)
	}
	r, err := LoadRegistry(ctx, regDir)
	if err != nil || len(r.Packages) != 1 || r.Packages[0].Hash != res.Entry.Hash {
		t.Fatalf("registry = %+v %v", r, err)
	}
}

func TestListAndDescribe(t *testing.T) {
	ctx := context.Background()
	tl := project(t)
	reg := registryPath(t)
	if _, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: reg, Timeline: tl}); err != nil {
		t.Fatal(err)
	}
	write(t, tl, `{"schema": "timeline/0.1", "id": "t", "rev": 0, "format": {"size": [1280, 720], "fps": 30},
		"kits": ["clean@1.0.0"], "assets": {}, "spine": [], "scenes": [{"id": "s", "shots": [], "overlays": [
		{"id": "pop/a", "use": "pop-text@1.0.0", "from": "t:0", "to": "t:1", "props": {"text": "A"}}]}]}`)
	l, err := List(ListArgs{Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Components) != 1 || l.Components[0].Ref != "pop-text@1.0.0" || !l.Components[0].Locked ||
		!slices.Equal(l.Components[0].Used, []string{"pop/a"}) || l.Components[0].Sheet == "" {
		t.Fatalf("list = %+v", l)
	}
	if l, _ := List(ListArgs{Timeline: tl, Query: "lower third"}); len(l.Components) != 0 {
		t.Fatalf("query: %+v", l)
	}
	d, err := Describe(ctx, DescribeArgs{Ref: "pop-text", Timeline: tl})
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != "project" || d.Defaults["size"] != float64(132) || !slices.Equal(d.Required, []string{"text"}) ||
		d.Previews["sheet"] == "" || d.Use["use"] != "pop-text@1.0.0" || !slices.Equal(d.Used, []string{"pop/a"}) ||
		len(d.Examples) != 3 || d.Hash == "" {
		t.Fatalf("describe = %+v", d)
	}
	// Not in the project: from the registry.
	d, err = Describe(ctx, DescribeArgs{Ref: "zoom-punch@1.0.0", Timeline: tl, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if d.Source != "registry" || d.Inputs["asset"] != "video" || d.Use["picture"] == nil || !fileExists(d.Previews["sheet"]) {
		t.Fatalf("describe from the registry = %+v", d)
	}
	k, err := Describe(ctx, DescribeArgs{Ref: "clean", Registry: reg})
	if err != nil || k.Type != TypeKit || len(k.Rules) < 6 || len(k.Components) != 10 || k.Tokens["color"] == nil {
		t.Fatalf("describe a kit = %+v %v", k, err)
	}
	if _, err := Describe(ctx, DescribeArgs{Ref: "nope", Timeline: tl}); code(err) != CodeNotFound {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRecipeExportWithPlaceholdersAndTheLicenseGate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	media := filepath.Join(root, "media-elsewhere")
	logo := filepath.Join(media, "logo.png")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	pngFile(t, logo)
	cat, err := assets.Open(assets.Options{Root: root, Library: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cat.Close() }()
	if _, err := cat.Import(ctx, assets.ImportArgs{Paths: []string{logo}, Name: "logo", Tags: []string{"brand"},
		License: assets.LicenseArgs{Status: assets.LicenseOwned}}); err != nil {
		t.Fatal(err)
	}
	tl := filepath.Join(root, "edit", "timeline.json")
	write(t, tl, `{"schema": "timeline/0.1", "id": "how-to", "rev": 1, "format": {"size": [1280, 720], "fps": 30},
		"assets": {"proj:logo": {"path": "`+logo+`"}, "proj:vo": {"path": "../voice/vo.ogg", "words": "../voice/vo.words.json"}},
		"spine": [], "scenes": []}`)
	write(t, strings.TrimSuffix(tl, ".json")+".patches.jsonl",
		`{"id": "p1", "rev": 1, "base": 0, "answers": [], "summary": "logo", "ops": [{"op": "set", "path": "assets/proj:logo", "value": {"path": "`+logo+`"}}], "inverse": [], "applied": "2026-10-30T00:00:00Z"}`+"\n")
	if _, err := Add(ctx, AddArgs{Ref: "pop-text", Registry: registryPath(t), Timeline: tl}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "recipe")
	res, err := ExportRecipe(ctx, RecipeArgs{Timeline: tl, Out: out, Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	ph := res.Recipe.Placeholders["proj:logo"]
	if ph.Kind != assets.KindImage || ph.Size == nil || ph.Size[0] != 8 || ph.Path != "media/proj-logo.png" ||
		!slices.Contains(ph.Tags, "brand") {
		t.Fatalf("logo placeholder = %+v", ph)
	}
	vo := res.Recipe.Placeholders["proj:vo"]
	if vo.Kind != assets.KindAudio || vo.Words != "media/proj-vo.words.json" || vo.Path != "media/proj-vo.ogg" {
		t.Fatalf("vo placeholder = %+v", vo)
	}
	if !slices.Equal(res.Recipe.Packages, []string{"pop-text@1.0.0"}) || res.Recipe.Patches != 1 {
		t.Fatalf("recipe = %+v", res.Recipe)
	}
	if !slices.ContainsFunc(res.Recipe.Warnings, func(w string) bool { return strings.Contains(w, "proj:vo isn't in the catalog") }) {
		t.Fatalf("warnings = %v", res.Recipe.Warnings)
	}
	// Nothing in the export points at the original media.
	for _, f := range []string{"timeline.json", "timeline.patches.jsonl", "timeline.lock", "recipe.json"} {
		b, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), media) || strings.Contains(string(b), root) {
			t.Fatalf("%s names a local path:\n%s", f, b)
		}
	}
	if !fileExists(filepath.Join(out, "components", "pop-text@1.0.0", "component.json")) {
		t.Fatal("the package is in the recipe")
	}
	lock, _ := ReadLock(filepath.Join(out, "timeline.lock"))
	if lock.Packages["pop-text@1.0.0"].Path != "components/pop-text@1.0.0" {
		t.Fatalf("lock = %+v", lock)
	}

	// Blocked media: the export refuses, and writes nothing.
	if _, err := cat.Set(ctx, assets.SetArgs{ID: "proj:logo", License: &assets.LicenseArgs{Status: assets.LicenseBlocked}}); err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(root, "recipe2")
	if _, err := ExportRecipe(ctx, RecipeArgs{Timeline: tl, Out: out2, Catalog: cat}); code(err) != CodeLicense ||
		!strings.Contains(err.Error(), "proj:logo is blocked") {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := os.Stat(out2); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused export writes nothing")
	}
	// A package without an open license.
	if _, err := cat.Set(ctx, assets.SetArgs{ID: "proj:logo", License: &assets.LicenseArgs{Status: assets.LicenseOwned}}); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(filepath.Dir(tl), "components", "pop-text@1.0.0", "component.json")
	b, _ := os.ReadFile(pkg)
	write(t, pkg, strings.Replace(string(b), `"license": "CC0-1.0"`, `"license": "LicenseRef-mine"`, 1))
	if _, err := ExportRecipe(ctx, RecipeArgs{Timeline: tl, Out: out2, Catalog: cat}); code(err) != CodeLicense {
		t.Fatalf("a proprietary package: %v", err)
	}
	// A folder with other things in it isn't overwritten.
	write(t, filepath.Join(root, "busy", "notes.txt"), "mine")
	if _, err := ExportRecipe(ctx, RecipeArgs{Timeline: tl, Out: filepath.Join(root, "busy"), Catalog: cat}); code(err) != CodeConflict {
		t.Fatalf("busy folder: %v", err)
	}
}

func TestVersions(t *testing.T) {
	if CompareVersions("1.10.0", "1.9.0") <= 0 || CompareVersions("1.0.0-rc.1", "1.0.0") >= 0 || CompareVersions("2.0.0", "2.0.0") != 0 {
		t.Fatal("semver order")
	}
	if n, v, err := ParseRef("pop-text@1.4.0"); err != nil || n != "pop-text" || v != "1.4.0" {
		t.Fatal(n, v, err)
	}
	if _, _, err := ParseRef("pop-text@1.4"); err == nil {
		t.Fatal("1.4 isn't semver")
	}
	if s, c := CatalogStatus("CC-BY-4.0"); s != assets.LicenseAttribution || c == "" {
		t.Fatal(s, c)
	}
}
