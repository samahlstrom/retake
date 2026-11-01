package theme

import (
	"bytes"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Pages are the page's two HTML entries, which get the theme's additions.
var Pages = []string{"index.html", "home.html"}

// Prefix is where a theme's files are served, relative to the page: theme/theme.css.
const Prefix = "theme/"

// CSP is the Content-Security-Policy of every theme file: an SVG opened on its own can't run a script.
const CSP = "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; sandbox"

// Page wraps the page's files (the build in the binary, or --ui-dir's folder) with the theme folder dir: index.html
// and home.html get the theme's additions (Inject), and theme/<path> is a file of the folder (Open's rules are
// spec/theme.md's "Serving theme files"). The folder is read on every open, so an edit shows on the next reload and a
// folder made later is picked up. With dir "" (theming off) or no page, it returns page itself.
func Page(page fs.FS, dir string) fs.FS {
	if page == nil || dir == "" {
		return page
	}
	return themed{page: page, dir: dir}
}

type themed struct {
	page fs.FS
	dir  string
}

func (t themed) Open(name string) (fs.File, error) {
	if rest, ok := strings.CutPrefix(name, Prefix); ok {
		return openAsset(t.dir, rest)
	}
	if name == "theme" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	isPage := false
	for _, p := range Pages {
		isPage = isPage || name == p
	}
	if !isPage {
		return t.page.Open(name)
	}
	th := Load(t.dir)
	if th == nil {
		return t.page.Open(name) // no theme folder: the page byte for byte
	}
	f, err := t.page.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return &memFile{Reader: bytes.NewReader(Inject(b, th)), name: name, mod: st.ModTime()}, nil
}

// openAsset opens a file of the theme folder for serving: an allowed type, no hidden segment, inside the folder
// (os.Root refuses .. and symlinks that leave it), a regular file of at most MaxFile bytes.
func openAsset(dir, name string) (fs.File, error) {
	notFound := &fs.PathError{Op: "open", Path: Prefix + name, Err: fs.ErrNotExist}
	clean, ok := servable(name)
	if !ok || clean != name {
		return nil, notFound
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, notFound
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(clean)
	if err != nil {
		return nil, notFound
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > MaxFile {
		_ = f.Close()
		return nil, notFound
	}
	return f, nil
}

// Headers sets a theme file's headers when name (a path relative to the page) is one: its Content-Type, nosniff, the
// CSP and no-cache. It reports whether it was one. The servers' static handlers call it after setting their own.
func Headers(h http.Header, name string) bool {
	rest, ok := strings.CutPrefix(name, Prefix)
	if !ok {
		return false
	}
	if ct := ContentType(rest); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", CSP)
	h.Set("Cache-Control", "no-cache")
	return true
}

var (
	headClose = regexp.MustCompile(`(?i)</head\s*>`)
	htmlOpen  = regexp.MustCompile(`(?i)<html\b[^>]*>`)
	titleTag  = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
)

// Inject adds the theme to a page's HTML (spec/theme.md, "How the page loads it"): before </head>, the theme's
// stylesheet, favicon and the brand metas, and data-theme on <html>; the name goes into <title>. HTML without a
// </head> comes back unchanged. t nil: unchanged.
func Inject(page []byte, t *Theme) []byte {
	if t == nil {
		return page
	}
	loc := headClose.FindIndex(page)
	if loc == nil {
		return page
	}
	var tags strings.Builder
	scheme := map[string]string{ModeDark: "dark", ModeLight: "light", ModeAuto: "light dark"}[t.Settings.Mode]
	tags.WriteString(`<meta name="color-scheme" content="` + scheme + `">`)
	if t.Settings.Name != "" {
		tags.WriteString(`<meta name="retake-brand" content="` + html.EscapeString(t.Settings.Name) + `">`)
	}
	if t.Settings.Logo != "" {
		tags.WriteString(`<meta name="retake-logo" content="` + html.EscapeString(AssetURL(t.Settings.Logo)) + `">`)
	}
	if t.Settings.Favicon != "" {
		tags.WriteString(`<link rel="icon" href="` + html.EscapeString(AssetURL(t.Settings.Favicon)) + `">`)
	}
	if t.CSS {
		tags.WriteString(`<link rel="stylesheet" href="` + Prefix + CSSFile + `">`)
	}
	out := make([]byte, 0, len(page)+tags.Len()+64)
	out = append(out, page[:loc[0]]...)
	out = append(out, tags.String()...)
	out = append(out, page[loc[0]:]...)
	if m := htmlOpen.FindIndex(out); m != nil && !bytes.Contains(out[m[0]:m[1]], []byte("data-theme")) {
		attr := []byte(` data-theme="` + t.Settings.Mode + `"`)
		out = append(out[:m[1]-1], append(attr, out[m[1]-1:]...)...)
	}
	if name := t.Settings.Name; name != "" {
		out = titleTag.ReplaceAllFunc(out, func(tag []byte) []byte {
			inner := titleTag.FindSubmatch(tag)[1]
			text := html.UnescapeString(string(inner))
			if strings.Contains(text, "Retake") {
				text = strings.ReplaceAll(text, "Retake", name)
			} else {
				text += " · " + name
			}
			return []byte("<title>" + html.EscapeString(text) + "</title>")
		})
	}
	return out
}

// AssetURL is a theme file's URL relative to the page: theme/<path>, each segment escaped.
func AssetURL(name string) string {
	segs := strings.Split(name, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return Prefix + strings.Join(segs, "/")
}

// memFile is a page with the theme's additions.
type memFile struct {
	*bytes.Reader
	name string
	mod  time.Time
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct{ f *memFile }

func (i memInfo) Name() string       { return i.f.name }
func (i memInfo) Size() int64        { return i.f.Size() }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return i.f.mod }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
