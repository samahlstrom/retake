package home

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// Found is a project in a folder (spec/home.md, "Which folders are projects").
type Found struct {
	Name   string // URL-safe and unique in the folder: its address is /p/<Name>/
	Title  string // the folder's name as it is
	Root   string // the project root, absolute
	Anchor string // the video its family is detected from
}

// MaxName is the longest project name, as on the team server.
const MaxName = 63

// Slug makes a folder name URL-safe: lower case, every run of other characters becomes "-", at most MaxName
// characters, and "video" when nothing is left.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > MaxName {
		out = strings.TrimRight(out[:MaxName], "-")
	}
	if out == "" {
		out = "video"
	}
	return out
}

// skipDir reports whether a folder is never a candidate nor searched: hidden ones and takes' .review folders.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".review")
}

// subdirs lists the folders directly in dir that aren't skipped, by name.
func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if skipDir(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if st, err := os.Stat(p); err == nil && st.IsDir() { // follows a link to a folder
			out = append(out, p)
		}
	}
	return out
}

// Discover lists the projects in dir: the folder itself and its immediate subfolders, each when it holds a versioned
// video whose project root it is. dir must be absolute with symlinks resolved. They come in name order, the folder
// itself first.
func Discover(dir string) []Found {
	dir = filepath.Clean(dir)
	var out []Found
	used := map[string]bool{}
	for _, c := range append([]string{dir}, subdirs(dir)...) {
		anchor := Anchor(c)
		if anchor == "" {
			continue
		}
		title := filepath.Base(c)
		name := unique(Slug(title), used)
		used[name] = true
		out = append(out, Found{Name: name, Title: title, Root: c, Anchor: anchor})
	}
	return out
}

// unique returns name, or name-2, name-3, ... when it's taken, within MaxName characters.
func unique(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		base := name
		if len(base)+len(suffix) > MaxName {
			base = strings.TrimRight(base[:MaxName-len(suffix)], "-")
		}
		if !used[base+suffix] {
			return base + suffix
		}
	}
}

// Anchor returns the video the family of the project rooted at root is detected from: of the versioned videos in
// root and in its own subfolders (one level) whose project root is root, the highest version number, a plain name
// before one with more after the number, then the newest. "" when root isn't a project.
func Anchor(root string) string {
	type pick struct {
		path  string
		n     int
		plain bool
		mtime int64
	}
	var best *pick
	rootOf := map[string]string{} // folder + family prefix -> project root
	scan := func(d string) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || !family.IsVideo(name) {
				continue
			}
			nm, ok := family.ParseStem(family.Stem(name))
			if !ok {
				continue
			}
			p := filepath.Join(d, name)
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			k := d + "\x00" + strings.ToLower(nm.Prefix)
			r, seen := rootOf[k]
			if !seen {
				r = project.Root(p)
				rootOf[k] = r
			}
			if !family.SamePath(r, root) {
				continue
			}
			c := pick{path: p, n: nm.N, plain: nm.Rest == "", mtime: st.ModTime().UnixNano()}
			switch {
			case best == nil, c.n > best.n,
				c.n == best.n && c.plain && !best.plain,
				c.n == best.n && c.plain == best.plain && c.mtime > best.mtime:
				best = &c
			}
		}
	}
	scan(root)
	for _, d := range subdirs(root) {
		scan(d)
	}
	if best == nil {
		return ""
	}
	return best.path
}
