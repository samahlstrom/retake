package lib

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
	"github.com/samahlstrom/retake/internal/userdir"
)

// Placeholder stands in for an asset a recipe doesn't carry.
type Placeholder struct {
	Kind     string   `json:"kind"`
	Duration float64  `json:"duration,omitempty"`
	Size     *[2]int  `json:"size,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Title    string   `json:"title,omitempty"`
	// Path is where the recipe's timeline looks for it; Words its transcript, when the timeline anchors on words.
	Path  string `json:"path"`
	Words string `json:"words,omitempty"`
}

// Recipe is a recipe's recipe.json.
type Recipe struct {
	Schema       string                 `json:"schema"`
	Name         string                 `json:"name"`
	Timeline     string                 `json:"timeline"`
	Rev          int64                  `json:"rev"`
	Placeholders map[string]Placeholder `json:"placeholders"`
	Packages     []string               `json:"packages"`
	Patches      int                    `json:"patches"`
	Warnings     []string               `json:"warnings"`
}

// RecipeArgs are lib recipe export's options.
type RecipeArgs struct {
	Timeline string
	Out      string
	// Catalog gives assets their kind, duration, size and tags, and the license gate; nil = probing only, and every
	// asset is "license unknown".
	Catalog *assets.Catalog
	Getenv  func(string) string
}

// RecipeResult is lib recipe export's answer.
type RecipeResult struct {
	Dir    string   `json:"dir"`
	Recipe Recipe   `json:"recipe"`
	Files  []string `json:"files"`
}

var slugRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// expandPath resolves a timeline asset path as the editor does: ~/, $VAR and ${VAR}, relative to the timeline.
func expandPath(p, dir string, getenv func(string) string) string {
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(userdir.Home(getenv), p[2:])
	}
	p = os.Expand(p, getenv)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}

// ExportRecipe packages a timeline's recipe without its media (spec/components.md, "Edit recipes"): the timeline with
// placeholders for its assets, its patch log, its lockfile and the packages it uses, plus recipe.json. It refuses a
// package without an allowed license and an asset the catalog has as blocked.
func ExportRecipe(ctx context.Context, a RecipeArgs) (RecipeResult, error) {
	if a.Getenv == nil {
		a.Getenv = os.Getenv
	}
	tl, err := filepath.Abs(a.Timeline)
	if err != nil {
		return RecipeResult{}, wrap(err)
	}
	dir := filepath.Dir(tl)
	b, err := os.ReadFile(tl) //nolint:gosec // the timeline the user named
	if err != nil {
		return RecipeResult{}, errorf(CodeNotFound, "timeline %s: %v", tl, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return RecipeResult{}, errorf(CodeInvalid, "%s isn't JSON: %v", tl, err)
	}
	if strings.TrimSpace(a.Out) == "" {
		return RecipeResult{}, errorf(CodeBadRequest, "give the folder to export into (-o DIR)")
	}
	out, err := filepath.Abs(a.Out)
	if err != nil {
		return RecipeResult{}, wrap(err)
	}
	if ents, err := os.ReadDir(out); err == nil && len(ents) > 0 && !fileExists(filepath.Join(out, "recipe.json")) {
		return RecipeResult{}, errorf(CodeConflict, "%s isn't empty: export a recipe into a new folder", out)
	}
	rc := Recipe{Schema: RecipeSchema, Timeline: "timeline.json", Placeholders: map[string]Placeholder{},
		Packages: []string{}, Warnings: []string{}}
	rc.Name, _ = doc["id"].(string)
	if rev, ok := doc["rev"].(float64); ok {
		rc.Rev = int64(rev)
	}
	var blockers []string

	// Packages: every one the lockfile pins, from where the editor finds it.
	lock, err := ReadLock(LockPath(tl))
	if err != nil {
		return RecipeResult{}, err
	}
	roots := SearchRoots(dir, a.Getenv)
	type pkgCopy struct{ ref, from string }
	var pkgs []pkgCopy
	newLock := Lock{Schema: LockSchema, Packages: map[string]LockEntry{}}
	for _, ref := range sortedKeys(anyMap(lock.Packages)) {
		e := lock.Packages[ref]
		name, version, err := ParseRef(ref)
		if err != nil {
			return RecipeResult{}, err
		}
		from := ""
		if e.Path != "" {
			if p := filepath.Join(dir, filepath.FromSlash(e.Path)); fileExists(filepath.Join(p, "component.json")) ||
				fileExists(filepath.Join(p, "kit.json")) {
				from = p
			}
		}
		if from == "" {
			p, err := FindPackage(roots, name, version)
			if err != nil {
				return RecipeResult{}, errorf(CodeNotFound, "the lockfile pins %s, which isn't on disk: %v", ref, err)
			}
			from = p.Dir
		}
		p, err := ReadPackage(from)
		if err != nil {
			return RecipeResult{}, err
		}
		if !licenseAllowed(p.License()) {
			blockers = append(blockers, fmt.Sprintf("%s is licensed %q", ref, p.License()))
		}
		if h, err := PackageHash(from); err == nil && h != e.Hash {
			rc.Warnings = append(rc.Warnings, fmt.Sprintf("%s changed since it was locked; the recipe pins it as it is now", ref))
			e.Hash = h
		}
		pkgs = append(pkgs, pkgCopy{ref, from})
		rc.Packages = append(rc.Packages, ref)
		newLock.Packages[ref] = LockEntry{Type: e.Type, Hash: e.Hash, Path: "components/" + ref,
			Source: "recipe", License: p.License()}
	}

	// Assets become placeholders.
	replace := map[string]string{} // original path strings (as written and resolved) → placeholder path
	am, _ := doc["assets"].(map[string]any)
	used := map[string]bool{}
	for _, id := range sortedKeys(am) {
		m, _ := am[id].(map[string]any)
		orig, _ := m["path"].(string)
		full := expandPath(orig, dir, a.Getenv)
		ext := strings.ToLower(filepath.Ext(full))
		base := strings.Trim(slugRE.ReplaceAllString(strings.ReplaceAll(id, ":", "-"), "-"), "-")
		name := base
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		used[name] = true
		ph := Placeholder{Kind: assets.KindOf(full), Path: "media/" + name + ext}
		if ph.Kind == "" {
			ph.Kind = assets.KindOther
		}
		status := ""
		if a.Catalog != nil {
			if e, err := a.Catalog.Get(ctx, id); err == nil {
				ph.Kind, ph.Tags, ph.Title, status = e.Kind, e.Tags, e.Title, e.License.Status
				if e.Media.Duration != nil {
					ph.Duration = *e.Media.Duration
				}
				ph.Size = e.Media.Size
			} else if fileExists(full) {
				if md, err := a.Catalog.Probe(ctx, full); err == nil {
					if md.Duration != nil {
						ph.Duration = *md.Duration
					}
					ph.Size = md.Size
				}
			}
		}
		switch status {
		case assets.LicenseBlocked:
			blockers = append(blockers, fmt.Sprintf("%s is blocked in the catalog", id))
		case assets.LicenseReviewOnly:
			rc.Warnings = append(rc.Warnings, fmt.Sprintf("%s is review-only: whoever fills its placeholder needs "+
				"their own cleared media", id))
		case "":
			rc.Warnings = append(rc.Warnings, fmt.Sprintf("%s isn't in the catalog: its license is unknown", id))
		}
		replace[orig], replace[full] = ph.Path, ph.Path
		nm := map[string]any{"path": ph.Path}
		if w, ok := m["words"].(string); ok {
			ph.Words = "media/" + name + ".words.json"
			nm["words"] = ph.Words
			replace[w], replace[expandPath(w, dir, a.Getenv)] = ph.Words, ph.Words
		}
		meta, _ := m["meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["placeholder"] = map[string]any{"kind": ph.Kind, "duration": ph.Duration, "size": ph.Size, "tags": ph.Tags}
		nm["meta"] = meta
		am[id] = nm
		rc.Placeholders[id] = ph
	}
	if fm, ok := doc["fonts"].(map[string]any); ok && len(fm) > 0 {
		for _, name := range sortedKeys(fm) {
			m, _ := fm[name].(map[string]any)
			orig, _ := m["path"].(string)
			ph := "fonts/" + name + strings.ToLower(filepath.Ext(orig))
			replace[orig], replace[expandPath(orig, dir, a.Getenv)] = ph, ph
			if m != nil {
				m["path"] = ph
			}
			rc.Warnings = append(rc.Warnings, fmt.Sprintf("font %s isn't carried: put its file at %s", name, ph))
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return RecipeResult{}, errorf(CodeLicense, "the license gate refuses this recipe: %s", strings.Join(blockers, "; "))
	}

	// The patch log, with asset paths rewritten.
	var logLines [][]byte
	logPath := strings.TrimSuffix(tl, filepath.Ext(tl)) + ".patches.jsonl"
	if f, err := os.Open(logPath); err == nil { //nolint:gosec // the timeline's patch log
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var v any
			if err := json.Unmarshal(line, &v); err != nil {
				_ = f.Close()
				return RecipeResult{}, errorf(CodeInvalid, "%s: %v", logPath, err)
			}
			v = rewrite(v, replace, &rc.Warnings)
			nb, _ := json.Marshal(v)
			logLines = append(logLines, nb)
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			return RecipeResult{}, wrap(err)
		}
	}
	rc.Patches = len(logLines)

	// Write it all.
	if err := os.MkdirAll(filepath.Join(out, "media"), 0o755); err != nil { //nolint:gosec // an export folder
		return RecipeResult{}, wrap(err)
	}
	res := RecipeResult{Dir: out, Recipe: rc}
	write := func(rel string, v any) error {
		res.Files = append(res.Files, rel)
		return writeJSON(filepath.Join(out, rel), v)
	}
	if err := write("timeline.json", doc); err != nil {
		return res, wrap(err)
	}
	if err := write("timeline.lock", newLock); err != nil {
		return res, wrap(err)
	}
	if len(logLines) > 0 {
		res.Files = append(res.Files, "timeline.patches.jsonl")
		if err := os.WriteFile(filepath.Join(out, "timeline.patches.jsonl"), append(bytes.Join(logLines, []byte("\n")), '\n'), 0o644); err != nil { //nolint:gosec // an export
			return res, wrap(err)
		}
	}
	for _, p := range pkgs {
		dst := filepath.Join(out, "components", p.ref)
		if err := os.RemoveAll(dst); err != nil {
			return res, wrap(err)
		}
		if err := copyPackage(p.from, dst); err != nil {
			return res, wrap(err)
		}
		res.Files = append(res.Files, "components/"+p.ref+"/")
	}
	if err := write("recipe.json", rc); err != nil {
		return res, wrap(err)
	}
	res.Recipe = rc
	return res, nil
}

func anyMap[V any](m map[string]V) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// rewrite replaces strings that are original asset paths; it warns about other absolute paths it leaves.
func rewrite(v any, replace map[string]string, warns *[]string) any {
	switch x := v.(type) {
	case string:
		if r, ok := replace[x]; ok {
			return r
		}
		if filepath.IsAbs(x) || strings.HasPrefix(x, "~/") {
			*warns = append(*warns, fmt.Sprintf("the patch log names a path the recipe doesn't rewrite: %s", x))
		}
		return x
	case map[string]any:
		for k, c := range x {
			x[k] = rewrite(c, replace, warns)
		}
		return x
	case []any:
		for i, c := range x {
			x[i] = rewrite(c, replace, warns)
		}
		return x
	}
	return v
}

// copyPackage copies a package's files and its previews (hidden entries skipped).
func copyPackage(src, dst string) error {
	files, err := PackageFiles(src)
	if err != nil {
		return err
	}
	for _, f := range []string{"previews/sheet.png"} {
		if fileExists(filepath.Join(src, filepath.FromSlash(f))) {
			files = append(files, f)
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f))) //nolint:gosec // a package file
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // an export folder
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // package files aren't secrets
			return err
		}
	}
	return nil
}
