package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
)

// PackArgs are lib pack's options.
type PackArgs struct {
	Dir string
	// Index is an index.json to write the entry into (created when missing); the package must be inside its folder.
	// "" = only print the entry.
	Index string
	// Catalog checks asset ids the package names (props and examples): review-only and blocked are refused. nil = the
	// check is skipped.
	Catalog *assets.Catalog
}

// PackResult is lib pack's answer.
type PackResult struct {
	Entry    Entry    `json:"entry"`
	Index    string   `json:"index,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

var (
	fontExts  = map[string]bool{".ttf": true, ".otf": true, ".ttc": true, ".otc": true, ".woff": true, ".woff2": true}
	licenseRE = regexp.MustCompile(`(?i)^(ofl|license|licence|copying)([._-].*)?(\.(txt|md))?$`)
)

// MaxSheetBytes is the size a committed preview sheet should stay under.
const MaxSheetBytes = 300 << 10

// Validate checks a package folder: its manifest, its license and the license gate. It returns the package and
// warnings; problems are one CodeInvalid or CodeLicense error listing them all.
func Validate(ctx context.Context, dir string, catalog *assets.Catalog) (Package, []string, error) {
	p, err := ReadPackage(dir)
	if err != nil {
		return p, nil, err
	}
	var probs, lic, warns []string
	bad := func(f string, a ...any) { probs = append(probs, fmt.Sprintf(f, a...)) }
	if !nameRE.MatchString(p.Name()) {
		bad("name %q: use a-z, 0-9 and -", p.Name())
	}
	if !semverRE.MatchString(p.Version()) {
		bad("version %q isn't semver (1.0.0)", p.Version())
	}
	switch spdx := p.License(); {
	case spdx == "":
		lic = append(lic, "no license: set \"license\" to an SPDX id ("+strings.Join(AllowedLicenses, ", ")+")")
	case !licenseAllowed(spdx):
		lic = append(lic, fmt.Sprintf("license %q isn't one a shared package may carry (%s)", spdx,
			strings.Join(AllowedLicenses, ", ")))
	}
	files, err := PackageFiles(dir)
	if err != nil {
		return p, nil, wrap(err)
	}
	// Fonts travel with their license.
	for _, f := range files {
		if !fontExts[strings.ToLower(filepath.Ext(f))] {
			continue
		}
		folder := filepath.Dir(filepath.Join(dir, filepath.FromSlash(f)))
		ents, _ := os.ReadDir(folder)
		ok := false
		for _, e := range ents {
			if !e.IsDir() && licenseRE.MatchString(e.Name()) {
				ok = true
			}
		}
		if !ok {
			lic = append(lic, fmt.Sprintf("font %s has no license file beside it (OFL.txt, LICENSE…)", f))
		}
	}
	var ids []string // asset ids the package names
	if c := p.Component; c != nil {
		if c.Schema != "" && c.Schema != ComponentSchema {
			bad("schema %q; this retake reads %s", c.Schema, ComponentSchema)
		}
		if strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Description) == "" {
			bad("give it a title and a description: agents choose components by them")
		}
		for _, u := range c.UseAs {
			if u != "overlay" && u != "picture" {
				bad("use_as %q: overlay or picture", u)
			}
		}
		var raw struct {
			Layers  []json.RawMessage `json:"layers"`
			Backend *struct {
				Command []string `json:"command"`
			} `json:"backend"`
		}
		if b, err := os.ReadFile(filepath.Join(dir, "component.json")); err == nil { //nolint:gosec // the package
			_ = json.Unmarshal(b, &raw)
		}
		// A component draws with layers, or with a program (a backend, spec/components.md).
		switch {
		case raw.Backend != nil && len(raw.Layers) > 0:
			bad("layers or a backend, not both")
		case raw.Backend != nil && len(raw.Backend.Command) == 0:
			bad("backend.command is the argv to run, e.g. [\"python3\", \"draw.py\"]")
		case raw.Backend == nil && len(raw.Layers) == 0:
			bad("no layers")
		}
		if raw.Backend != nil {
			warns = append(warns, fmt.Sprintf("it draws by running a program (%s): whoever adds it runs that code",
				strings.Join(raw.Backend.Command, " ")))
		}
		props, _ := c.Props["properties"].(map[string]any)
		required := map[string]bool{}
		if rs, ok := c.Props["required"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok {
					required[s] = true
				}
			}
		}
		var undocumented []string
		inputs := map[string]bool{}
		for _, k := range sortedKeys(props) {
			m, _ := props[k].(map[string]any)
			if d, _ := m["description"].(string); strings.TrimSpace(d) == "" {
				undocumented = append(undocumented, k)
			}
			if _, ok := m["x-retake-input"]; ok {
				inputs[k] = true
				if s, ok := m["default"].(string); ok {
					ids = append(ids, s)
				}
			}
		}
		if len(undocumented) > 0 {
			bad("props without a description: %s", strings.Join(undocumented, ", "))
		}
		if len(c.Examples) == 0 {
			bad("no examples: previews and agents need at least one")
		}
		for i, ex := range c.Examples {
			if strings.TrimSpace(ex.Name) == "" {
				bad("examples/%d has no name", i)
			}
			for r := range required {
				if _, ok := ex.Props[r]; !ok {
					bad("example %q misses the required prop %s", ex.Name, r)
				}
			}
			for k, v := range ex.Props {
				if _, ok := props[k]; !ok && props != nil {
					bad("example %q has a prop %s the schema doesn't list", ex.Name, k)
				}
				if s, ok := v.(string); ok && inputs[k] {
					ids = append(ids, s)
				}
			}
		}
		if len(c.Changelog) == 0 {
			warns = append(warns, "no changelog")
		}
	}
	if k := p.Kit; k != nil {
		if k.Schema != "" && k.Schema != KitSchema {
			bad("schema %q; this retake reads %s", k.Schema, KitSchema)
		}
		for _, r := range k.Components {
			if _, v, err := ParseRef(r); err != nil || v == "" {
				bad("components: %q isn't name@version", r)
			}
		}
		for i, r := range k.Rules {
			if s, _ := r["rule"].(string); s == "" {
				bad("rules/%d has no rule", i)
			}
		}
		for _, a := range k.Assets {
			if !fileExists(filepath.Join(dir, filepath.FromSlash(a.Path))) {
				bad("asset %s: no file %s", a.Key, a.Path)
			}
			if a.License != nil && (a.License.Status == assets.LicenseReviewOnly || a.License.Status == assets.LicenseBlocked) {
				lic = append(lic, fmt.Sprintf("asset %s is %s", a.Key, a.License.Status))
			}
		}
	}
	// The catalog's gate on the asset ids it names.
	if catalog != nil {
		for _, id := range dedupe(ids) {
			e, err := catalog.Get(ctx, id)
			if err != nil {
				continue // not a catalog asset (a placeholder id): nothing to check
			}
			if e.License.Status == assets.LicenseReviewOnly || e.License.Status == assets.LicenseBlocked {
				lic = append(lic, fmt.Sprintf("it names %s, which the catalog has as %s", id, e.License.Status))
			}
		}
	}
	if s, err := os.Stat(filepath.Join(dir, "previews", "sheet.png")); err == nil && s.Size() > MaxSheetBytes {
		warns = append(warns, fmt.Sprintf("previews/sheet.png is %d KB; keep it under %d KB", s.Size()>>10,
			MaxSheetBytes>>10))
	} else if err != nil && p.Type == TypeComponent {
		warns = append(warns, "no previews/sheet.png: run retake-edit component preview "+dir)
	}
	if len(lic) > 0 {
		return p, warns, errorf(CodeLicense, "%s: the license gate refuses it: %s", p.Ref(), strings.Join(lic, "; "))
	}
	if len(probs) > 0 {
		return p, warns, errorf(CodeInvalid, "%s: %s", p.Ref(), strings.Join(probs, "; "))
	}
	return p, warns, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Pack validates a package and builds its registry entry; with an index, it writes the entry into it.
func Pack(ctx context.Context, a PackArgs) (PackResult, error) {
	dir, err := filepath.Abs(a.Dir)
	if err != nil {
		return PackResult{}, wrap(err)
	}
	p, warns, err := Validate(ctx, dir, a.Catalog)
	if err != nil {
		return PackResult{Warnings: warns}, err
	}
	e, err := entryFor(p)
	if err != nil {
		return PackResult{Warnings: warns}, err
	}
	e.Path = filepath.ToSlash(dir)
	res := PackResult{Warnings: warns}
	if a.Index == "" {
		if fileExists(filepath.Join(dir, "previews", "sheet.png")) {
			e.Previews = map[string]string{"sheet": filepath.ToSlash(filepath.Join(dir, "previews", "sheet.png"))}
		}
		res.Entry = e
		return res, nil
	}
	idx, err := filepath.Abs(a.Index)
	if err != nil {
		return res, wrap(err)
	}
	rel, err := filepath.Rel(filepath.Dir(idx), dir)
	if err != nil || !filepath.IsLocal(rel) {
		return res, errorf(CodeBadRequest, "%s isn't inside %s: a registry serves its packages from its folder", dir,
			filepath.Dir(idx))
	}
	e.Path = filepath.ToSlash(rel)
	if fileExists(filepath.Join(dir, "previews", "sheet.png")) {
		e.Previews = map[string]string{"sheet": e.Path + "/previews/sheet.png"}
	}
	var ix Index
	if b, err := os.ReadFile(idx); err == nil { //nolint:gosec // the index the user named
		if err := json.Unmarshal(b, &ix); err != nil {
			return res, errorf(CodeInvalid, "%s isn't an index.json: %v", idx, err)
		}
	}
	ix.Schema = RegistrySchema
	if ix.Name == "" {
		ix.Name = filepath.Base(filepath.Dir(idx))
	}
	kept := ix.Packages[:0]
	for _, x := range ix.Packages {
		if x.Name != e.Name || x.Version != e.Version {
			kept = append(kept, x)
		}
	}
	ix.Packages = append(kept, e)
	sort.SliceStable(ix.Packages, func(i, j int) bool {
		if ix.Packages[i].Name != ix.Packages[j].Name {
			return ix.Packages[i].Name < ix.Packages[j].Name
		}
		return CompareVersions(ix.Packages[i].Version, ix.Packages[j].Version) < 0
	})
	if err := writeJSON(idx, ix); err != nil {
		return res, wrap(err)
	}
	res.Entry, res.Index = e, idx
	return res, nil
}

// entryFor builds a package's registry entry (without its path and previews).
func entryFor(p Package) (Entry, error) {
	files, err := PackageFiles(p.Dir)
	if err != nil {
		return Entry{}, wrap(err)
	}
	e := Entry{Name: p.Name(), Type: p.Type, Version: p.Version(), License: p.License()}
	for _, f := range files {
		h, n, err := FileHash(filepath.Join(p.Dir, filepath.FromSlash(f)))
		if err != nil {
			return Entry{}, wrap(err)
		}
		e.Files = append(e.Files, File{Path: f, Hash: h, Size: n})
	}
	if e.Hash, err = PackageHash(p.Dir); err != nil {
		return Entry{}, wrap(err)
	}
	if c := p.Component; c != nil {
		e.Title, e.Description, e.Category, e.UseAs, e.Tags = c.Title, c.Description, c.Category, c.UseAs, c.Tags
	} else {
		e.Title, e.Description, e.Requires = p.Kit.Title, p.Kit.Description, p.Kit.Components
	}
	return e, nil
}
