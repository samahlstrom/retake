package home

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// Found is a video in a folder's tree (spec/home.md, "Folders, projects and videos"): a version family with its
// project root. The routes and the cards call it a project: it has one review page, one store and one agent.
type Found struct {
	Name   string // URL-safe and unique in the folder: its address is /p/<Name>/
	Title  string // its folder's name as it is
	Root   string // the project root (the folder that holds it), absolute
	Anchor string // the video its family is detected from; "" for a timeline project without a version yet
	// Path is Root relative to the scanned folder, with "/" between names; "" for the folder itself.
	Path string
	// Project is the folder of the project it belongs to, relative like Path: the folder above it when its folder
	// holds just this video (deadlock for deadlock/item-shop), else its own folder.
	Project string
	// Kind is the extension kind of its v0/ folder (spec/extensions.md, "Version kinds"), "" without one. A video's
	// folder may hold only that: an idea, with Anchor "".
	Kind string
}

// KindAt says which enabled extension kind a folder's v0/ is ("ignition.doc"), or "" (ext.Set.KindAt).
type KindAt func(dir string) string

// The scan's limits (spec/home.md).
const (
	MaxDepth     = 6     // folders below the scanned one where projects are looked for
	ProjectDepth = 2     // folders below a video's folder where more videos are looked for
	MaxDirs      = 20000 // folders one scan reads at most
)

// dirLimit is MaxDirs; tests lower it.
var dirLimit = MaxDirs

// Scan is one look at a folder's tree.
type Scan struct {
	// Videos are the videos, in name order: the folder's own first, then the shallowest, then by path.
	Videos []Found
	// Empty are the projects with a native timeline and no version yet (Anchor ""), named after the videos.
	Empty  []Found
	Dirs   int           // folders read
	Capped bool          // the scan stopped at MaxDirs
	Took   time.Duration // how long it took
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

// skipDir reports whether a folder is never a candidate nor searched: hidden ones (.retake among them), takes'
// .review folders, and the package and cache folders tools leave in project folders.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".review") || name == "node_modules" ||
		name == "__pycache__"
}

// scanner reads each folder once per scan.
type scanner struct {
	top    string // the scanned folder, absolute, symlinks resolved
	lists  map[string][]os.DirEntry
	subs   map[string][]subdir
	fams   map[string]int // how many families have their root at a folder
	dirs   int
	capped bool
	linked map[string]bool // the real folders reached through links, so each is entered once
	videos []Found
	empty  []Found
	kindAt KindAt // nil: no kinds
}

// kind is the kind of dir's v0/ folder, asked only when dir lists one.
func (s *scanner) kind(dir string) string {
	if s.kindAt == nil {
		return ""
	}
	for _, e := range s.list(dir) {
		if e.Name() == "v0" && e.IsDir() {
			return s.kindAt(dir)
		}
	}
	return ""
}

func newScanner(top string) *scanner {
	return &scanner{top: filepath.Clean(top), lists: map[string][]os.DirEntry{}, subs: map[string][]subdir{},
		fams: map[string]int{}, linked: map[string]bool{}}
}

// list returns a folder's entries, reading it at most once and at most MaxDirs folders in all.
func (s *scanner) list(dir string) []os.DirEntry {
	if l, ok := s.lists[dir]; ok {
		return l
	}
	if s.dirs >= dirLimit {
		s.capped = true
		return nil
	}
	s.dirs++
	l, err := os.ReadDir(dir)
	if err != nil {
		l = nil
	}
	s.lists[dir] = l
	return l
}

type subdir struct{ name, path string }

// subdirs lists the folders directly in dir that aren't skipped, by name. A link to a folder counts when the folder
// is outside the scanned one (the scan finds the others anyway), once per scan.
func (s *scanner) subdirs(dir string) []subdir {
	if out, ok := s.subs[dir]; ok {
		return out
	}
	out := []subdir{}
	for _, e := range s.list(dir) {
		name := e.Name()
		if skipDir(name) {
			continue
		}
		p := filepath.Join(dir, name)
		switch {
		case e.IsDir():
		case e.Type()&os.ModeSymlink != 0:
			target, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			st, err := os.Stat(target) //nolint:gosec // a link in the scanned tree, only looked at
			if err != nil || !st.IsDir() || inside(target, s.top) || s.linked[target] {
				continue
			}
			s.linked[target] = true
		default:
			continue
		}
		out = append(out, subdir{name, p})
	}
	s.subs[dir] = out
	return out
}

// inside reports whether p is dir or a folder in it.
func inside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

// timeline reports whether dir is a native timeline project (spec/bridge.md). Only folders that list a retake.toml
// or an edit folder are asked.
func (s *scanner) timeline(dir string) bool {
	maybe := false
	for _, e := range s.list(dir) {
		if e.Name() == bridge.ProjectConfig || e.Name() == "edit" {
			maybe = true
			break
		}
	}
	if !maybe {
		return false
	}
	_, err := bridge.FindTimeline(dir)
	return err == nil
}

// anchor returns the video the family held by the folder root is detected from: of the versioned videos in root and
// in its own subfolders (one level) whose project root is root, the highest version number, a plain name before one
// with more after the number, then the newest. "" when root holds no video. It also counts the families whose root
// is root (fams).
func (s *scanner) anchor(root string) string {
	families := map[string]bool{}
	defer func() { s.fams[root] = len(families) }()
	type pick struct {
		path  string
		n     int
		plain bool
		mtime int64
	}
	var best *pick
	rootOf := map[string]string{} // folder + family prefix -> project root
	scan := func(d string) {
		for _, e := range s.list(d) {
			name := e.Name()
			if strings.HasPrefix(name, ".") || !family.IsVideo(name) {
				continue
			}
			nm, ok := family.ParseStem(family.Stem(name))
			if !ok {
				continue
			}
			p := filepath.Join(d, name)
			st, err := os.Stat(p) //nolint:gosec // a file of the scanned tree, only looked at
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
			families[strings.ToLower(nm.Prefix)] = true
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
	for _, d := range s.subdirs(root) {
		scan(d.path)
	}
	if best == nil {
		return ""
	}
	return best.path
}

func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

func (s *scanner) add(dir, rel, anchor, kind string) {
	f := Found{Root: dir, Anchor: anchor, Path: rel, Kind: kind}
	if anchor == "" && kind == "" {
		s.empty = append(s.empty, f)
	} else {
		s.videos = append(s.videos, f)
	}
}

// walk looks for videos in dir, depth folders below the scanned one. In a video's folder it only looks
// ProjectDepth folders further (inner): the rest of it is the video's own (assets, work, renders).
func (s *scanner) walk(dir, rel string, depth int) {
	if anchor, kind := s.anchor(dir), s.kind(dir); anchor != "" || kind != "" || s.timeline(dir) {
		s.add(dir, rel, anchor, kind)
		if depth > 0 {
			s.inner(dir, rel, 1, depth+1)
			return
		}
	}
	if depth >= MaxDepth {
		return
	}
	for _, d := range s.subdirs(dir) {
		s.walk(d.path, joinRel(rel, d.name), depth+1)
	}
}

// inner adds the videos held by the folders in a video's folder, up to ProjectDepth folders below it and MaxDepth
// below the scanned folder, version folders left out (their videos are the family of the folder above them). in is
// how far below the video's folder the folders in dir are, at how far below the scanned folder.
func (s *scanner) inner(dir, rel string, in, at int) {
	if at > MaxDepth {
		return
	}
	for _, d := range s.subdirs(dir) {
		if project.IsVersionDir(d.name) {
			continue
		}
		r := joinRel(rel, d.name)
		if a, k := s.anchor(d.path), s.kind(d.path); a != "" || k != "" {
			s.add(d.path, r, a, k)
		}
		if in < ProjectDepth {
			s.inner(d.path, r, in+1, at+1)
		}
	}
}

// place sets each video's project (spec/home.md): a folder that holds just one video (its family, directly or in
// vN/ folders, and no other video inside it) is that video, and the folder above it is the project; a folder with
// more (two families side by side, or videos in folders inside it) is a project itself, holding its own video. The
// scanned folder's own video is at the top.
func place(list []Found, fams map[string]int, all []Found) {
	for i := range list {
		f := &list[i]
		f.Project = parentOf(f.Path)
		if f.Path == "" || fams[f.Root] > 1 {
			f.Project = f.Path
			continue
		}
		for _, o := range all {
			if strings.HasPrefix(o.Path, f.Path+"/") {
				f.Project = f.Path
				break
			}
		}
	}
}

// parentOf is the folder above a relative path ("" above a top-level one).
func parentOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// ScanDir scans a folder's tree for its folders, projects and videos (spec/home.md). dir must be absolute with
// symlinks resolved. It reads folders and stats the files named like versioned videos; it never opens a video.
func ScanDir(dir string, kinds ...KindAt) Scan {
	start := time.Now()
	s := newScanner(dir)
	for _, k := range kinds {
		if k != nil {
			s.kindAt = k
		}
	}
	if st, err := os.Stat(s.top); err == nil && st.IsDir() { //nolint:gosec // the folder being opened
		s.walk(s.top, "", 0)
	}
	all := slices.Concat(s.videos, s.empty)
	place(s.videos, s.fams, all)
	place(s.empty, s.fams, all)
	used := map[string]bool{}
	name := func(list []Found) []Found {
		slices.SortStableFunc(list, func(a, b Found) int {
			return cmp.Or(cmp.Compare(depthOf(a.Path), depthOf(b.Path)), strings.Compare(a.Path, b.Path))
		})
		for i := range list {
			f := &list[i]
			f.Title, f.Name = filepath.Base(f.Root), Slug(f.Path)
			if f.Path == "" {
				f.Name = Slug(f.Title)
			}
			f.Name = unique(f.Name, used)
			used[f.Name] = true
		}
		return list
	}
	return Scan{Videos: name(s.videos), Empty: name(s.empty), Dirs: s.dirs, Capped: s.capped, Took: time.Since(start)}
}

func depthOf(rel string) int {
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

// Discover lists the videos in dir's tree (ScanDir's Videos): the folder's own first, then the shallowest, then by
// path. dir must be absolute with symlinks resolved.
func Discover(dir string) []Found {
	return ScanDir(dir).Videos
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

// Anchor returns the video the family held by the folder root is detected from: of the versioned videos in root and
// in its own subfolders (one level) whose project root is root, the highest version number, a plain name before one
// with more after the number, then the newest. "" when root holds no video.
func Anchor(root string) string {
	return newScanner(root).anchor(root)
}

// ValidPath reports whether p can be a video's or a project's place in a home's tree: "" or folder names joined by
// "/", none empty, "." or "..", none with "\" or control characters, at most 1024 bytes in all.
func ValidPath(p string) bool {
	if p == "" {
		return true
	}
	if len(p) > 1024 {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f || r == '\\' {
				return false
			}
		}
	}
	return true
}

// Within reports whether the folder proj is path or a folder above it (both as ValidPath).
func Within(proj, path string) bool {
	return proj == path || proj == "" || strings.HasPrefix(path, proj+"/")
}
