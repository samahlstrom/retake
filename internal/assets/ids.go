package assets

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Scopes (spec/assets.md, "Storage and scopes").
const (
	ScopeProject = "project"
	ScopeBrand   = "brand"
	ScopeLib     = "lib"
)

// Id prefixes by scope.
const (
	prefixProject = "proj:"
	prefixBrand   = "brand:"
	prefixLib     = "lib:"
)

// nameRE is what follows an id's prefix: a valid timeline asset id once prefixed.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@/-]*$`)

// ParseID splits an asset id into its scope and name. ok is false for an id without a known prefix.
func ParseID(id string) (scope, name string, ok bool) {
	for _, p := range []struct{ prefix, scope string }{
		{prefixProject, ScopeProject}, {prefixBrand, ScopeBrand}, {prefixLib, ScopeLib},
	} {
		if rest, found := strings.CutPrefix(id, p.prefix); found {
			return p.scope, rest, true
		}
	}
	return "", id, false
}

// MakeID joins a scope and a name.
func MakeID(scope, name string) string {
	switch scope {
	case ScopeBrand:
		return prefixBrand + name
	case ScopeLib:
		return prefixLib + name
	}
	return prefixProject + name
}

// validName reports whether name can follow an id prefix.
func validName(name string) bool { return nameRE.MatchString(name) }

// ParseScope reads a scope as people write it: proj, project, brand, lib, global.
func ParseScope(s string) (string, error) {
	switch strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ":")) {
	case "proj", "project", "":
		return ScopeProject, nil
	case "brand":
		return ScopeBrand, nil
	case "lib", "global", "library":
		return ScopeLib, nil
	}
	return "", fmt.Errorf("unknown scope %q: use proj, brand or lib", s)
}

// slug turns a path (without its extension) into an id name: lowercase, path separators kept as /, runs of anything
// else outside [a-z0-9_.-] turned into -.
func slug(rel string) string {
	rel = filepath.ToSlash(rel)
	var parts []string
	for _, part := range strings.Split(rel, "/") {
		var b strings.Builder
		dash := false
		for _, r := range strings.ToLower(part) {
			switch {
			case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.'):
				b.WriteRune(r)
				dash = false
			default:
				if !dash && b.Len() > 0 {
					b.WriteByte('-')
					dash = true
				}
			}
		}
		s := strings.Trim(b.String(), "-.")
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "asset"
	}
	return strings.Join(parts, "/")
}

// Span ids: sp:<n> in the project catalog, sp:lib/<n> in the library.
const (
	spanPrefix    = "sp:"
	spanLibPrefix = "sp:lib/"
)

// SpanID formats a span id.
func SpanID(scope string, n int64) string {
	if scope == ScopeLib || scope == ScopeBrand {
		return spanLibPrefix + strconv.FormatInt(n, 10)
	}
	return spanPrefix + strconv.FormatInt(n, 10)
}

// ParseSpanID splits a span id into the catalog it lives in (ScopeProject or ScopeLib) and its row id.
func ParseSpanID(id string) (scope string, n int64, err error) {
	scope = ScopeProject
	rest, ok := strings.CutPrefix(id, spanLibPrefix)
	if ok {
		scope = ScopeLib
	} else if rest, ok = strings.CutPrefix(id, spanPrefix); !ok {
		return "", 0, fmt.Errorf("%q is not a span id (sp:<n> or sp:lib/<n>)", id)
	}
	n, err = strconv.ParseInt(rest, 10, 64)
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("%q is not a span id (sp:<n> or sp:lib/<n>)", id)
	}
	return scope, n, nil
}

// brandParts splits a brand name, <kit>[@<version>]/<key>.
func brandParts(name string) (kit, version, key string, ok bool) {
	head, key, found := strings.Cut(name, "/")
	if !found || head == "" || key == "" {
		return "", "", "", false
	}
	kit, version, _ = strings.Cut(head, "@")
	return kit, version, key, kit != ""
}

// compareVersions orders kit versions: numerically by dot-separated parts where both are numbers, else as text.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
