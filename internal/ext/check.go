package ext

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/theme"
)

// Report is what `retake ext check` found in one extension (spec/extensions.md, "Commands").
type Report struct {
	Dir      string    `json:"dir"`
	ID       string    `json:"id"`
	Version  string    `json:"version"`
	API      int       `json:"api"`
	Problems []Problem `json:"problems"`
}

// CheckDir reads the extension in the folder dir and checks it. retake is this binary's version, for min_retake.
func CheckDir(dir, retake string) Report {
	e := LoadDir(dir)
	if e.FS != nil && !isDir(e.Dir) {
		e.Problems = []Problem{{Kind: "manifest", Name: dir, Message: "no folder at " + e.Dir}}
	} else {
		e.checkRetake(retake)
	}
	return Check(e)
}

// Check is everything `retake ext check` reports about e: its load problems (the manifest, missing files, owns
// globs, agent Markdown over its cap), the module's exports against its slots, and its CSS: selectors outside
// [data-ext='<id>'] and tokens the Theme API doesn't have.
func Check(e *Ext) Report {
	r := Report{Dir: e.Display(), ID: e.ID, Version: e.Version, API: e.API, Problems: append([]Problem{}, e.Problems...)}
	if r.API == 0 {
		r.API = API
	}
	if slices.ContainsFunc(e.Problems, func(p Problem) bool { return p.Kind == "manifest" }) {
		return r
	}
	r.checkModule(e)
	r.checkStyle(e)
	return r
}

func (r *Report) add(kind, name, format string, a ...any) {
	r.Problems = append(r.Problems, Problem{Kind: kind, Name: name, Message: fmt.Sprintf(format, a...)})
}

var (
	jsComment  = regexp.MustCompile(`(?s:/\*.*?\*/)|(?m:^[ \t]*//[^\n]*)`)
	exportDecl = regexp.MustCompile(`\bexport\s+(?:async\s+)?(?:function\s*\*?|const|let|var|class)\s*([A-Za-z_$][\w$]*)`)
	exportList = regexp.MustCompile(`\bexport\s*\{([^}]*)\}`)
)

// Exports is the names a module exports, from a static scan: export function|const|let|var|class <name> and
// export { a, b as c }.
func Exports(js string) map[string]bool {
	js = jsComment.ReplaceAllString(js, "")
	out := map[string]bool{}
	for _, m := range exportDecl.FindAllStringSubmatch(js, -1) {
		out[m[1]] = true
	}
	for _, m := range exportList.FindAllStringSubmatch(js, -1) {
		for item := range strings.SplitSeq(m[1], ",") {
			f := strings.Fields(item)
			switch {
			case len(f) == 1:
				out[f[0]] = true
			case len(f) == 3 && f[1] == "as":
				out[strings.Trim(f[2], `"'`)] = true
			}
		}
	}
	return out
}

// checkModule reports slots whose function the module doesn't export.
func (r *Report) checkModule(e *Ext) {
	if e.UI.Module == "" || !ValidPath(e.UI.Module) {
		return
	}
	b, err := readFile(e.FS, e.UI.Module, MaxUIFile)
	if err != nil {
		return // reported as a missing file
	}
	exports := Exports(string(b))
	for _, s := range e.Slots {
		name, ok := Slots[s.Slot]
		if ok && !exports[name] {
			r.add("export", name, "%s doesn't export %s, which the %s slot needs", e.UI.Module, name, s.Slot)
		}
	}
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	declared   = regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:`)
	referenced = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)`)
)

// checkStyle reports the style's selectors outside [data-ext='<id>'] and the tokens it reads that the Theme API
// doesn't have (its own custom properties, set and read in the file, are fine).
func (r *Report) checkStyle(e *Ext) {
	if e.UI.Style == "" || !ValidPath(e.UI.Style) {
		return
	}
	b, err := readFile(e.FS, e.UI.Style, MaxUIFile)
	if err != nil {
		return
	}
	css := cssComment.ReplaceAllString(string(b), "")
	for _, sel := range Selectors(css) {
		if !scoped(sel, e.ID) {
			r.add("css", sel, "%s: %q isn't under [data-ext='%s'], so it would style the rest of the page",
				e.UI.Style, sel, e.ID)
		}
	}
	spec, err := theme.Current()
	if err != nil {
		r.add("token", "", "%s: can't read this retake's tokens: %v", e.UI.Style, err)
		return
	}
	sets := map[string]bool{}
	for _, m := range declared.FindAllStringSubmatch(css, -1) {
		sets[m[1]] = true
	}
	reads := map[string]bool{}
	for _, m := range referenced.FindAllStringSubmatch(css, -1) {
		reads[m[1]] = true
	}
	names := make([]string, 0, len(reads))
	for n := range reads {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch {
		case spec.HasToken(n), sets[n]:
		case spec.DeprecatedTokens[n].Use != "":
			d := spec.DeprecatedTokens[n]
			r.add("deprecated-token", n, "%s: %s is deprecated since %s: use %s (the old name works until the next "+
				"major release)", e.UI.Style, n, d.Since, d.Use)
		default:
			r.add("token", n, "%s: %s isn't a Theme API token in this version of Retake (removed, renamed or misspelled)",
				e.UI.Style, n)
		}
	}
}

var scopeRE = regexp.MustCompile(`^\[\s*data-ext\s*=\s*(?:'([^']*)'|"([^"]*)"|([A-Za-z0-9_-]+))\s*\]`)

// scoped reports whether a selector starts with [data-ext='<id>'].
func scoped(sel, id string) bool {
	m := scopeRE.FindStringSubmatch(strings.TrimSpace(sel))
	return m != nil && m[1]+m[2]+m[3] == id
}

// groupRules are the at-rules whose block holds style rules, which are checked like top-level ones.
var groupRules = []string{"media", "supports", "layer", "container", "document", "-moz-document", "starting-style"}

// Selectors is every selector of every style rule in css (comments already removed): the comma lists split, rules
// inside @media, @supports, @layer and @container included, other at-rules' blocks (@font-face, @keyframes) skipped.
// Rules nested inside a rule's block are relative to it and aren't listed.
func Selectors(css string) []string {
	var out []string
	sc := cssScanner{s: css}
	sc.block(&out, true)
	return out
}

type cssScanner struct {
	s string
	i int
}

// block reads rules until the end, or the } that closes the block when it isn't the top.
func (c *cssScanner) block(out *[]string, top bool) {
	for c.i < len(c.s) {
		prelude, end := c.prelude()
		p := strings.TrimSpace(prelude)
		switch end {
		case '}':
			if !top {
				return
			}
		case '{':
			if strings.HasPrefix(p, "@") {
				name := strings.ToLower(strings.TrimPrefix(strings.FieldsFunc(p, func(r rune) bool {
					return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
				})[0], "@"))
				if slices.Contains(groupRules, name) {
					c.block(out, false)
				} else {
					c.skip()
				}
				continue
			}
			for _, sel := range splitTop(p) {
				if sel = strings.TrimSpace(sel); sel != "" {
					*out = append(*out, strings.Join(strings.Fields(sel), " "))
				}
			}
			c.skip()
		}
	}
}

// prelude reads up to an unquoted, unbracketed {, ; or } (or the end), and returns the text before it and that
// character (0 at the end). It leaves the scanner after the character.
func (c *cssScanner) prelude() (string, byte) {
	start, depth := c.i, 0
	for c.i < len(c.s) {
		ch := c.s[c.i]
		switch {
		case ch == '"' || ch == '\'':
			c.str(ch)
			continue
		case ch == '\\':
			c.i += 2
			continue
		case ch == '(' || ch == '[':
			depth++
		case (ch == ')' || ch == ']') && depth > 0:
			depth--
		case depth == 0 && (ch == '{' || ch == ';' || ch == '}'):
			c.i++
			return c.s[start : c.i-1], ch
		}
		c.i++
	}
	return c.s[start:], 0
}

// str skips a quoted string starting at the quote q.
func (c *cssScanner) str(q byte) {
	c.i++
	for c.i < len(c.s) {
		switch c.s[c.i] {
		case '\\':
			c.i += 2
			continue
		case q, '\n':
			c.i++
			return
		}
		c.i++
	}
}

// skip skips the rest of a block whose { was just read, nested blocks included.
func (c *cssScanner) skip() {
	depth := 1
	for c.i < len(c.s) && depth > 0 {
		switch ch := c.s[c.i]; ch {
		case '"', '\'':
			c.str(ch)
			continue
		case '\\':
			c.i += 2
			continue
		case '{':
			depth++
		case '}':
			depth--
		}
		c.i++
	}
}

// splitTop splits a selector list at the commas outside parentheses, brackets and strings.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0 && ch == '\\':
			i++
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '(' || ch == '[':
			depth++
		case ch == ')' || ch == ']':
			depth--
		case ch == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
