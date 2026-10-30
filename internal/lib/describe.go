package lib

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Listed is a package the project can use.
type Listed struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	UseAs       []string `json:"use_as,omitempty"`
	License     string   `json:"license,omitempty"`
	Path        string   `json:"path"`
	Sheet       string   `json:"sheet,omitempty"`
	// Used lists the timeline's elements using it (a kit: ["kits"] when the timeline lists it).
	Used []string `json:"used,omitempty"`
	// Locked: the lockfile pins this exact copy.
	Locked bool `json:"locked"`
}

// ListArgs are lib list's options.
type ListArgs struct {
	Timeline string // "" = no project: only $RETAKE_COMPONENT_PATH and the user library
	Query    string // words that must all appear in the name, title, description, category or tags
	Getenv   func(string) string
}

// ListResult is lib list's answer.
type ListResult struct {
	Timeline   string   `json:"timeline,omitempty"`
	Components []Listed `json:"components"`
	Kits       []Listed `json:"kits"`
}

// timelineUses reads which elements of a timeline use which component, and the kits it lists.
func timelineUses(timeline string) (map[string][]string, []string) {
	uses := map[string][]string{}
	b, err := os.ReadFile(timeline) //nolint:gosec // the project's timeline
	if err != nil {
		return uses, nil
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return uses, nil
	}
	var walk func(v any, owner string)
	walk = func(v any, owner string) {
		switch x := v.(type) {
		case map[string]any:
			if id, ok := x["id"].(string); ok {
				owner = id
			}
			if u, ok := x["use"].(string); ok {
				uses[u] = append(uses[u], owner)
			}
			for k, c := range x {
				if k != "meta" && k != "props" {
					walk(c, owner)
				}
			}
		case []any:
			for _, c := range x {
				walk(c, owner)
			}
		}
	}
	walk(doc["scenes"], "")
	walk(doc["spine"], "")
	var kits []string
	if ks, ok := doc["kits"].([]any); ok {
		for _, k := range ks {
			if s, ok := k.(string); ok {
				kits = append(kits, strings.TrimPrefix(s, "brand:"))
			}
		}
	}
	for k := range uses {
		sort.Strings(uses[k])
	}
	return uses, kits
}

// List lists the components and kits a project can use: its vendored packages, $RETAKE_COMPONENT_PATH and the user
// library, with which the timeline uses and whether the lockfile pins them.
func List(a ListArgs) (ListResult, error) {
	dir := ""
	if a.Timeline != "" {
		abs, err := filepath.Abs(a.Timeline)
		if err != nil {
			return ListResult{}, wrap(err)
		}
		a.Timeline, dir = abs, filepath.Dir(abs)
	}
	res := ListResult{Timeline: a.Timeline, Components: []Listed{}, Kits: []Listed{}}
	uses, kits := map[string][]string{}, []string(nil)
	lock := Lock{Packages: map[string]LockEntry{}}
	if a.Timeline != "" {
		uses, kits = timelineUses(a.Timeline)
		var err error
		if lock, err = ReadLock(LockPath(a.Timeline)); err != nil {
			return res, err
		}
	}
	words := strings.Fields(strings.ToLower(a.Query))
	for _, p := range Available(SearchRoots(dir, a.Getenv)) {
		l := Listed{Ref: p.Ref(), Name: p.Name(), Version: p.Version(), Type: p.Type, License: p.License(), Path: p.Dir}
		var tags []string
		if c := p.Component; c != nil {
			l.Title, l.Description, l.Category, l.UseAs, tags = c.Title, c.Description, c.Category, c.UseAs, c.Tags
			l.Used = uses[p.Ref()]
		} else {
			l.Title, l.Description = p.Kit.Title, p.Kit.Description
			for _, k := range kits {
				if k == p.Ref() {
					l.Used = []string{"kits"}
				}
			}
		}
		if !matches(words, l.Name, l.Title, l.Description, l.Category, strings.Join(tags, " ")) {
			continue
		}
		if s := filepath.Join(p.Dir, "previews", "sheet.png"); fileExists(s) {
			l.Sheet = s
		}
		if e, ok := lock.Packages[p.Ref()]; ok {
			h, _ := PackageHash(p.Dir)
			l.Locked = h == e.Hash
		}
		if p.Type == TypeKit {
			res.Kits = append(res.Kits, l)
		} else {
			res.Components = append(res.Components, l)
		}
	}
	return res, nil
}

// Description is everything an agent needs to use a component (or a kit).
type Description struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	UseAs       []string `json:"use_as,omitempty"`
	Canvas      []int    `json:"canvas,omitempty"`
	Duration    float64  `json:"duration,omitempty"`
	License     string   `json:"license"`
	Tags        []string `json:"tags,omitempty"`
	// Source is project, path ($RETAKE_COMPONENT_PATH), library or registry.
	Source string `json:"source"`
	Path   string `json:"path"`
	Hash   string `json:"hash,omitempty"`
	// Props is the props JSON Schema; Required and Defaults are read from it.
	Props    map[string]any    `json:"props,omitempty"`
	Required []string          `json:"required,omitempty"`
	Defaults map[string]any    `json:"defaults,omitempty"`
	Inputs   map[string]string `json:"inputs,omitempty"` // props that take an asset id → video, image or audio
	Examples []Example         `json:"examples,omitempty"`
	Previews map[string]string `json:"previews"`
	// Changelog is newest first.
	Changelog []map[string]any `json:"changelog,omitempty"`
	// Use is a use to patch in, with the first example's props.
	Use      map[string]any `json:"use,omitempty"`
	Versions []string       `json:"versions,omitempty"`
	Used     []string       `json:"used,omitempty"`
	// Kits only.
	Tokens     map[string]any   `json:"tokens,omitempty"`
	Components []string         `json:"components,omitempty"`
	Rules      []map[string]any `json:"rules,omitempty"`
}

// DescribeArgs are lib describe's options.
type DescribeArgs struct {
	Ref      string // name or name@version
	Timeline string
	Registry string // looked in when the package isn't local
	Getenv   func(string) string
}

// Describe describes a component or kit: from the project's packages, $RETAKE_COMPONENT_PATH or the user library,
// else from the registry.
func Describe(ctx context.Context, a DescribeArgs) (Description, error) {
	name, version, err := ParseRef(a.Ref)
	if err != nil {
		return Description{}, err
	}
	dir := ""
	if a.Timeline != "" {
		if abs, err := filepath.Abs(a.Timeline); err == nil {
			a.Timeline, dir = abs, filepath.Dir(abs)
		}
	}
	roots := SearchRoots(dir, a.Getenv)
	var versions []string
	for _, p := range Available(roots) {
		if p.Name() == name {
			versions = append(versions, p.Version())
		}
	}
	p, err := FindPackage(roots, name, version)
	if err == nil {
		d := describe(p)
		d.Source = "library"
		switch {
		case dir != "" && strings.HasPrefix(p.Dir, filepath.Join(dir, "components")+string(filepath.Separator)):
			d.Source = "project"
		case !strings.HasPrefix(p.Dir, UserComponents(a.Getenv)):
			d.Source = "path"
		}
		d.Hash, _ = PackageHash(p.Dir)
		for _, f := range []struct{ k, f string }{{"sheet", "sheet.png"}, {"webp", "preview.webp"}} {
			if s := filepath.Join(p.Dir, "previews", f.f); fileExists(s) {
				d.Previews[f.k] = s
			}
		}
		sort.Slice(versions, func(i, j int) bool { return CompareVersions(versions[i], versions[j]) > 0 })
		d.Versions = versions
		if a.Timeline != "" {
			uses, _ := timelineUses(a.Timeline)
			d.Used = uses[d.Ref]
		}
		return d, nil
	}
	if a.Registry == "" {
		return Description{}, err
	}
	r, rerr := LoadRegistry(ctx, a.Registry)
	if rerr != nil {
		return Description{}, rerr
	}
	e, rerr := r.Find(name, version)
	if rerr != nil {
		return Description{}, rerr
	}
	file := "component.json"
	if e.Type == TypeKit {
		file = "kit.json"
	}
	b, rerr := r.Fetch(ctx, e.Path+"/"+file)
	if rerr != nil {
		return Description{}, rerr
	}
	tmp, rerr := os.MkdirTemp("", "retake-describe-")
	if rerr != nil {
		return Description{}, wrap(rerr)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if rerr := os.WriteFile(filepath.Join(tmp, file), b, 0o600); rerr != nil {
		return Description{}, wrap(rerr)
	}
	rp, rerr := ReadPackage(tmp)
	if rerr != nil {
		return Description{}, rerr
	}
	d := describe(rp)
	d.Source, d.Path, d.Hash = "registry", r.Where(e.Path), e.Hash
	if s := e.Previews["sheet"]; s != "" {
		d.Previews["sheet"] = r.Where(s)
	}
	for _, x := range r.Packages {
		if x.Name == name {
			d.Versions = append(d.Versions, x.Version)
		}
	}
	return d, nil
}

func describe(p Package) Description {
	d := Description{Ref: p.Ref(), Name: p.Name(), Version: p.Version(), Type: p.Type, License: p.License(),
		Path: p.Dir, Previews: map[string]string{}}
	if k := p.Kit; k != nil {
		d.Title, d.Description, d.Tokens, d.Components, d.Rules = k.Title, k.Description, k.Tokens, k.Components, k.Rules
		return d
	}
	c := p.Component
	d.Title, d.Description, d.Category, d.UseAs, d.Canvas = c.Title, c.Description, c.Category, c.UseAs, c.Canvas
	d.Duration, d.Tags, d.Props, d.Examples, d.Changelog = c.Duration, c.Tags, c.Props, c.Examples, c.Changelog
	if rs, ok := c.Props["required"].([]any); ok {
		for _, r := range rs {
			if s, ok := r.(string); ok {
				d.Required = append(d.Required, s)
			}
		}
	}
	if ps, ok := c.Props["properties"].(map[string]any); ok {
		d.Defaults = map[string]any{}
		d.Inputs = map[string]string{}
		for k, v := range ps {
			m, _ := v.(map[string]any)
			if def, ok := m["default"]; ok {
				d.Defaults[k] = def
			}
			if in, ok := m["x-retake-input"].(string); ok {
				d.Inputs[k] = in
			}
		}
	}
	props := map[string]any{}
	if len(c.Examples) > 0 && c.Examples[0].Props != nil {
		props = c.Examples[0].Props
	}
	if len(c.UseAs) > 0 && c.UseAs[0] == "picture" {
		d.Use = map[string]any{"id": "<shot id>", "at": "<anchor, e.g. start:a>",
			"picture": map[string]any{"use": d.Ref, "props": props}}
	} else {
		d.Use = map[string]any{"id": "<overlay id>", "use": d.Ref, "from": "<anchor, e.g. word:b/faster>",
			"to": "<anchor, e.g. word:b/faster+1.20>", "props": props}
	}
	return d
}
