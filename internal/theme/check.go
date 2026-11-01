package theme

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/samahlstrom/retake/web"
)

// Token is one token of this version (web/src/theme/tokens.css).
type Token struct {
	Name    string
	Default string // its built-in value
	Wide    string // its value on wide screens, when that's different
	Group   string // the comment above it in tokens.css ("surfaces")
}

// Deprecated is a name a major release renamed: what to use instead, and since when.
type Deprecated struct {
	Use   string `json:"use"`
	Since string `json:"since"`
}

// Spec is this version's Theme API: its tokens, its parts and the deprecated names.
type Spec struct {
	API    int
	Tokens []Token
	Parts  []Part
	// DeprecatedTokens and DeprecatedParts still work for one major release (spec/theme.md, "Versioning").
	DeprecatedTokens map[string]Deprecated
	DeprecatedParts  map[string]Deprecated
}

// Part is one data-part name and what it is.
type Part struct {
	Name string
	What string
}

// HasToken reports whether name is a token of this version.
func (s *Spec) HasToken(name string) bool {
	return slices.ContainsFunc(s.Tokens, func(t Token) bool { return t.Name == name })
}

// HasPart reports whether name is a part of this version.
func (s *Spec) HasPart(name string) bool {
	return slices.ContainsFunc(s.Parts, func(p Part) bool { return p.Name == name })
}

var (
	specOnce sync.Once
	specVal  *Spec
	specErr  error
)

// Current is the Theme API of this binary, read from the page's own files (web.ThemeTokens, web.ThemeAPI).
func Current() (*Spec, error) {
	specOnce.Do(func() { specVal, specErr = parseSpec(web.ThemeTokens, web.ThemeAPI) })
	return specVal, specErr
}

var (
	tokenDecl = regexp.MustCompile(`^\s*(--[a-z0-9-]+):\s*([^;]+);`)
	groupLine = regexp.MustCompile(`^\s*/\*\s*(.+?)\s*\*/\s*$`)
	partKey   = regexp.MustCompile(`^"([a-z-]+)"\s*:`)
)

func parseSpec(tokensCSS string, apiJSON []byte) (*Spec, error) {
	var api struct {
		API        int               `json:"api"`
		Parts      map[string]string `json:"parts"`
		Deprecated struct {
			Tokens map[string]Deprecated `json:"tokens"`
			Parts  map[string]Deprecated `json:"parts"`
		} `json:"deprecated"`
	}
	if err := json.Unmarshal(apiJSON, &api); err != nil {
		return nil, fmt.Errorf("theme api.json: %w", err)
	}
	s := &Spec{API: api.API, DeprecatedTokens: api.Deprecated.Tokens, DeprecatedParts: api.Deprecated.Parts}
	// api.json's parts in the file's order: the order spec/theme.md lists them in.
	for _, name := range partOrder(apiJSON, api.Parts) {
		s.Parts = append(s.Parts, Part{Name: name, What: api.Parts[name]})
	}
	seen := map[string]int{}
	group, wide := "", false
	for line := range strings.SplitSeq(tokensCSS, "\n") {
		if strings.Contains(line, "@media") {
			wide = true
			continue
		}
		if m := groupLine.FindStringSubmatch(line); m != nil && !wide {
			group = m[1]
			continue
		}
		m := tokenDecl.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if i, ok := seen[m[1]]; ok {
			if wide {
				s.Tokens[i].Wide = strings.TrimSpace(m[2])
			}
			continue
		}
		seen[m[1]] = len(s.Tokens)
		s.Tokens = append(s.Tokens, Token{Name: m[1], Default: strings.TrimSpace(m[2]), Group: group})
	}
	if len(s.Tokens) == 0 || len(s.Parts) == 0 {
		return nil, fmt.Errorf("theme api: %d tokens and %d parts", len(s.Tokens), len(s.Parts))
	}
	return s, nil
}

// partOrder is api.json's part names in the file's order (a map loses it), one per line inside "parts", then any
// the lines missed, sorted.
func partOrder(apiJSON []byte, parts map[string]string) []string {
	var out []string
	in := false
	for line := range strings.SplitSeq(string(apiJSON), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, `"parts": {`):
			in = true
		case in && strings.HasPrefix(t, "}"):
			in = false
		case in:
			if m := partKey.FindStringSubmatch(t); m != nil {
				if _, ok := parts[m[1]]; ok && !slices.Contains(out, m[1]) {
					out = append(out, m[1])
				}
			}
		}
	}
	var rest []string
	for name := range parts {
		if !slices.Contains(out, name) {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// Report is what `retake theme check` found.
type Report struct {
	Dir      string    `json:"dir"`
	API      int       `json:"api"`
	Tokens   []string  `json:"tokens"`   // this version's tokens the theme sets
	Parts    []string  `json:"parts"`    // this version's parts the theme selects
	Settings *Settings `json:"settings"` // theme.toml, as it applies
	Problems []Problem `json:"problems"`
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	declared   = regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:`)
	referenced = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)`)
	partSel    = regexp.MustCompile(`\[\s*data-part\s*[~|^$*]?=\s*(?:"([^"]*)"|'([^']*)'|([^\]\s]+))\s*[is]?\s*\]`)
	scriptExt  = []string{".js", ".mjs", ".cjs", ".html", ".htm", ".xhtml", ".wasm"}
)

// Check reads the theme folder dir and reports what this version won't use (spec/theme.md, "Commands").
func Check(dir string) Report {
	r := Report{Dir: dir, API: API, Tokens: []string{}, Parts: []string{}, Problems: []Problem{}}
	spec, err := Current()
	if err != nil {
		r.Problems = append(r.Problems, Problem{Kind: "folder", Message: err.Error()})
		return r
	}
	t := Load(dir)
	if t == nil {
		r.Problems = append(r.Problems, Problem{Kind: "folder", Message: "no theme folder at " + dir})
		return r
	}
	r.Settings = &t.Settings
	r.Problems = append(r.Problems, t.Problems...)
	if t.CSS {
		r.checkCSS(spec, dir)
	}
	r.checkFiles(dir)
	return r
}

func (r *Report) checkCSS(spec *Spec, dir string) {
	b, err := os.ReadFile(filepath.Join(dir, CSSFile)) //nolint:gosec // the theme folder the user named
	if err != nil {
		r.Problems = append(r.Problems, Problem{Kind: "file", Name: CSSFile, Message: fmt.Sprintf("%s: %v", CSSFile, err)})
		return
	}
	css := cssComment.ReplaceAllString(string(b), "")
	sets, reads := names(declared, css), names(referenced, css)
	for _, n := range union(sets, reads) {
		_, set := sets[n]
		_, read := reads[n]
		switch {
		case spec.HasToken(n):
			if set {
				r.Tokens = append(r.Tokens, n)
			}
		case spec.DeprecatedTokens[n].Use != "":
			d := spec.DeprecatedTokens[n]
			r.Problems = append(r.Problems, Problem{Kind: "deprecated-token", Name: n, Message: fmt.Sprintf(
				"%s: %s is deprecated since %s: use %s (the old name works until the next major release)",
				CSSFile, n, d.Since, d.Use)})
		case set && read:
			// the theme's own custom property: it sets it and reads it itself
		default:
			r.Problems = append(r.Problems, Problem{Kind: "unknown-token", Name: n, Message: fmt.Sprintf(
				"%s: %s isn't a token in this version of Retake (removed, renamed or misspelled), so it changes nothing",
				CSSFile, n)})
		}
	}
	parts := map[string]struct{}{}
	for _, m := range partSel.FindAllStringSubmatch(css, -1) {
		for p := range strings.FieldsSeq(m[1] + m[2] + m[3]) {
			parts[p] = struct{}{}
		}
	}
	for _, p := range sortedKeys(parts) {
		switch {
		case spec.HasPart(p):
			r.Parts = append(r.Parts, p)
		case spec.DeprecatedParts[p].Use != "":
			d := spec.DeprecatedParts[p]
			r.Problems = append(r.Problems, Problem{Kind: "deprecated-part", Name: p, Message: fmt.Sprintf(
				"%s: the part %q is deprecated since %s: use %q (the old name works until the next major release)",
				CSSFile, p, d.Since, d.Use)})
		default:
			r.Problems = append(r.Problems, Problem{Kind: "unknown-part", Name: p, Message: fmt.Sprintf(
				"%s: no element has data-part %q in this version of Retake, so those rules match nothing", CSSFile, p)})
		}
	}
}

// checkFiles reports files a theme never serves because they could run: scripts and pages.
func (r *Report) checkFiles(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, the rest is still checked
		}
		if p != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !slices.Contains(scriptExt, strings.ToLower(path.Ext(d.Name()))) {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		r.Problems = append(r.Problems, Problem{Kind: "file", Name: filepath.ToSlash(rel), Message: fmt.Sprintf(
			"%s is never served: a theme is CSS, fonts and images, never a script or a page", filepath.ToSlash(rel))})
		return nil
	})
}

func names(re *regexp.Regexp, css string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

func union(a, b map[string]struct{}) []string {
	all := map[string]struct{}{}
	for k := range a {
		all[k] = struct{}{}
	}
	for k := range b {
		all[k] = struct{}{}
	}
	return sortedKeys(all)
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
