package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File is one file of a registry package.
type File struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Entry is one package version in a registry's index.json.
type Entry struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Version     string            `json:"version"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	UseAs       []string          `json:"use_as,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	License     string            `json:"license"`
	Hash        string            `json:"hash"`
	Path        string            `json:"path"`
	Files       []File            `json:"files"`
	Previews    map[string]string `json:"previews,omitempty"`
	Requires    []string          `json:"requires,omitempty"`
}

// Ref is name@version.
func (e Entry) Ref() string { return e.Name + "@" + e.Version }

// Index is a registry's index.json.
type Index struct {
	Schema   string  `json:"schema"`
	Name     string  `json:"name,omitempty"`
	Packages []Entry `json:"packages"`
}

// Registry is a loaded index and where its files are.
type Registry struct {
	Index
	// Location is the index as given: a path or an http(s) URL.
	Location string
	base     string // the folder or URL the entries' paths are relative to
	remote   bool
	client   *http.Client
}

func isURL(s string) bool { return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") }

// LoadRegistry reads an index.json from a path (a file, or a folder holding index.json) or an http(s) URL.
func LoadRegistry(ctx context.Context, loc string) (*Registry, error) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return nil, errorf(CodeBadRequest, "no registry: pass --registry (a path or a URL to an index.json) or set $RETAKE_REGISTRY")
	}
	r := &Registry{Location: loc, client: &http.Client{Timeout: 60 * time.Second}}
	var data []byte
	if isURL(loc) {
		u, err := url.Parse(loc)
		if err != nil {
			return nil, errorf(CodeBadRequest, "registry %s: %v", loc, err)
		}
		if !strings.HasSuffix(u.Path, ".json") {
			u.Path = strings.TrimSuffix(u.Path, "/") + "/index.json"
		}
		r.remote = true
		base := *u
		base.Path = path.Dir(u.Path)
		r.base = base.String()
		if data, err = r.get(ctx, u.String()); err != nil {
			return nil, err
		}
	} else {
		p, err := filepath.Abs(loc)
		if err != nil {
			return nil, wrap(err)
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			p = filepath.Join(p, "index.json")
		}
		r.base = filepath.Dir(p)
		if data, err = os.ReadFile(p); err != nil { //nolint:gosec // the registry the user named
			return nil, errorf(CodeNotFound, "registry %s: %v", loc, err)
		}
	}
	if err := json.Unmarshal(data, &r.Index); err != nil {
		return nil, errorf(CodeInvalid, "registry %s isn't an index.json: %v", loc, err)
	}
	if r.Schema != "" && r.Schema != RegistrySchema {
		return nil, errorf(CodeInvalid, "registry %s is %s; this retake reads %s", loc, r.Schema, RegistrySchema)
	}
	return r, nil
}

// get fetches a URL.
func (r *Registry) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, wrap(err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, errorf(CodeFailed, "fetching %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errorf(CodeNotFound, "fetching %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// Where is the location of a path relative to the index: a URL or an absolute path.
func (r *Registry) Where(rel string) string {
	if r.remote {
		return strings.TrimSuffix(r.base, "/") + "/" + strings.TrimPrefix(rel, "/")
	}
	return filepath.Join(r.base, filepath.FromSlash(rel))
}

// Fetch reads a file of the registry (a path relative to the index).
func (r *Registry) Fetch(ctx context.Context, rel string) ([]byte, error) {
	clean := path.Clean("/" + rel)[1:]
	if clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return nil, errorf(CodeInvalid, "registry path %q leaves the registry", rel)
	}
	if r.remote {
		return r.get(ctx, r.Where(clean))
	}
	b, err := os.ReadFile(r.Where(clean))
	if err != nil {
		return nil, errorf(CodeNotFound, "registry file %s: %v", clean, err)
	}
	return b, nil
}

// Find returns a package's entry: the given version, or the newest.
func (r *Registry) Find(name, version string) (Entry, error) {
	var best *Entry
	var versions []string
	for i, e := range r.Packages {
		if e.Name != name {
			continue
		}
		versions = append(versions, e.Version)
		if version != "" && e.Version == version {
			return e, nil
		}
		if version == "" && (best == nil || CompareVersions(e.Version, best.Version) > 0) {
			best = &r.Packages[i]
		}
	}
	if best != nil && version == "" {
		return *best, nil
	}
	if len(versions) > 0 {
		return Entry{}, errorf(CodeNotFound, "%s@%s isn't in %s (it has %s)", name, version, r.Location,
			strings.Join(versions, ", "))
	}
	var names []string
	for _, e := range r.Packages {
		if strings.Contains(e.Name, name) || strings.Contains(name, e.Name) {
			names = append(names, e.Name)
		}
	}
	msg := fmt.Sprintf("no package %s in %s", name, r.Location)
	if len(names) > 0 {
		msg += " (did you mean " + strings.Join(dedupe(names), ", ") + "?)"
	}
	return Entry{}, &Error{Code: CodeNotFound, Message: msg}
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Hit is one search result.
type Hit struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Type        string   `json:"type"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	UseAs       []string `json:"use_as,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	License     string   `json:"license,omitempty"`
	// Source is "registry" or "library" (the user library).
	Source string `json:"source"`
	// Where is the registry location or the library folder.
	Where string `json:"where"`
	// Sheet is the storybook sheet: a path, or a URL for a remote registry.
	Sheet string `json:"sheet,omitempty"`
	// Installed: the project has this version in its components/.
	Installed bool     `json:"installed"`
	Versions  []string `json:"versions,omitempty"`
}

// SearchArgs are lib search's options.
type SearchArgs struct {
	Query    string
	Registry string // a path or URL; "" = only the user library
	Type     string // component, kit or recipe; "" = all
	Timeline string // the project's timeline, for installed; "" = none
	Getenv   func(string) string
}

// SearchResult is lib search's answer.
type SearchResult struct {
	Hits     []Hit    `json:"hits"`
	Warnings []string `json:"warnings,omitempty"`
}

func matches(words []string, fields ...string) bool {
	hay := strings.ToLower(strings.Join(fields, " "))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Search finds packages in a registry and in the user library: every word of the query must appear in the name,
// title, description, category or tags. The newest version of each package, by source.
func Search(ctx context.Context, a SearchArgs) (SearchResult, error) {
	if a.Getenv == nil {
		a.Getenv = os.Getenv
	}
	if a.Type != "" && a.Type != TypeComponent && a.Type != TypeKit && a.Type != TypeRecipe {
		return SearchResult{}, errorf(CodeBadRequest, "type is component, kit or recipe, not %q", a.Type)
	}
	words := strings.Fields(strings.ToLower(a.Query))
	installed := map[string]bool{}
	if a.Timeline != "" {
		for _, p := range Available([]string{filepath.Join(filepath.Dir(a.Timeline), "components")}) {
			installed[p.Ref()] = true
		}
	}
	var res SearchResult
	best := map[string]*Hit{}
	var order []string
	add := func(h Hit) {
		key := h.Source + "\x00" + h.Name
		if cur, ok := best[key]; ok {
			cur.Versions = append(cur.Versions, h.Version)
			if CompareVersions(h.Version, cur.Version) > 0 {
				vs := cur.Versions
				*cur = h
				cur.Versions = vs
			}
			return
		}
		h.Versions = []string{h.Version}
		best[key] = &h
		order = append(order, key)
	}
	if a.Registry != "" {
		r, err := LoadRegistry(ctx, a.Registry)
		if err != nil {
			return res, err
		}
		for _, e := range r.Packages {
			if (a.Type != "" && e.Type != a.Type) ||
				!matches(words, e.Name, e.Title, e.Description, e.Category, strings.Join(e.Tags, " "), e.Type) {
				continue
			}
			h := Hit{Name: e.Name, Version: e.Version, Type: e.Type, Title: e.Title, Description: e.Description,
				Category: e.Category, UseAs: e.UseAs, Tags: e.Tags, License: e.License, Source: "registry",
				Where: r.Location, Installed: installed[e.Ref()]}
			if s := e.Previews["sheet"]; s != "" {
				h.Sheet = r.Where(s)
			}
			add(h)
		}
	}
	userDir := UserComponents(a.Getenv)
	for _, p := range Available([]string{userDir}) {
		h := Hit{Name: p.Name(), Version: p.Version(), Type: p.Type, License: p.License(), Source: "library",
			Where: userDir, Installed: installed[p.Ref()]}
		var tags []string
		if c := p.Component; c != nil {
			h.Title, h.Description, h.Category, h.UseAs, tags = c.Title, c.Description, c.Category, c.UseAs, c.Tags
		} else {
			h.Title, h.Description = p.Kit.Title, p.Kit.Description
		}
		h.Tags = tags
		if (a.Type != "" && h.Type != a.Type) ||
			!matches(words, h.Name, h.Title, h.Description, h.Category, strings.Join(tags, " "), h.Type) {
			continue
		}
		if s := filepath.Join(p.Dir, "previews", "sheet.png"); fileExists(s) {
			h.Sheet = s
		}
		add(h)
	}
	for _, k := range order {
		h := *best[k]
		sort.Slice(h.Versions, func(i, j int) bool { return CompareVersions(h.Versions[i], h.Versions[j]) > 0 })
		res.Hits = append(res.Hits, h)
	}
	sort.SliceStable(res.Hits, func(i, j int) bool {
		// Name matches first, then by name.
		ni := len(words) > 0 && strings.Contains(res.Hits[i].Name, words[0])
		nj := len(words) > 0 && strings.Contains(res.Hits[j].Name, words[0])
		if ni != nj {
			return ni
		}
		return res.Hits[i].Name < res.Hits[j].Name
	})
	if res.Hits == nil {
		res.Hits = []Hit{}
	}
	return res, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
