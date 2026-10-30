// Package family finds a video's version family: the files that are versions of the same edit (clip_v5.mp4,
// v6/clip_v6.mp4, v7/clip_v7_0-010_preview.mp4). It is a rule-for-rule port of the legacy review.py
// version_family, plus .m3u8 playlists. See spec/api.md, "Versions".
package family

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/probe"
)

// verRE splits a file stem into prefix, version number and the rest ("clip", "7", "_0-010_preview").
var verRE = regexp.MustCompile(`(?i)^(.+?)[_\-]v(\d+)((?:[_\-].*)?)$`)

// ContentTypes maps the video extensions a family may contain to their HTTP content type.
var ContentTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".m3u8": "application/vnd.apple.mpegurl",
}

// IsVideo reports whether name has a family video extension (case-insensitive).
func IsVideo(name string) bool {
	_, ok := ContentTypes[strings.ToLower(Suffix(name))]
	return ok
}

// IsPlaylist reports whether path is an HLS playlist.
func IsPlaylist(path string) bool {
	return strings.EqualFold(Suffix(filepath.Base(path)), ".m3u8")
}

// Suffix returns the extension of a file name the way Python's PurePath.suffix does: ".hidden" and "name." have none.
func Suffix(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// Stem returns the file name without its Suffix, like Python's PurePath.stem.
func Stem(name string) string {
	return strings.TrimSuffix(name, Suffix(name))
}

// Name is a parsed versioned file name.
type Name struct {
	Prefix string // "clip"
	N      int    // 7
	Rest   string // "_0-010_preview"; empty for the plain file
}

// ParseStem parses a file stem such as "clip_v7_0-010_preview". ok is false if it has no version number.
func ParseStem(stem string) (Name, bool) {
	m := verRE.FindStringSubmatch(stem)
	if m == nil {
		return Name{}, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return Name{}, false // more digits than an int holds: not a version number we can use
	}
	return Name{Prefix: m[1], N: n, Rest: m[3]}, true
}

// Version is one member of the family, in the shape of the /api/info version object.
type Version struct {
	Label    string       `json:"label"`
	N        int          `json:"n"`
	Path     string       `json:"path"`
	Name     string       `json:"name"`
	Current  bool         `json:"current"`
	Duration *jsonx.Float `json:"duration"`
	FPS      jsonx.Float  `json:"fps"`
	Width    *int         `json:"width"`
	Height   *int         `json:"height"`
}

// Prober is what Detect needs from probe.Prober.
type Prober interface {
	Probe(path string) probe.Info
}

// SamePath reports whether two cleaned absolute paths name the same file (case-insensitive on Windows).
func SamePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

type pick struct {
	label string
	path  string
	n     int
}

// picks is an insertion-ordered label -> pick map, like the Python dict the legacy code used.
type picks struct {
	order []string
	byKey map[string]pick
}

func (p *picks) set(label, path string, n int) {
	if p.byKey == nil {
		p.byKey = map[string]pick{}
	}
	if _, ok := p.byKey[label]; !ok {
		p.order = append(p.order, label)
	}
	p.byKey[label] = pick{label: label, path: path, n: n}
}

func (p *picks) has(label string) bool {
	_, ok := p.byKey[label]
	return ok
}

// Detect returns the family of video, sorted by version number. video and explicit must be absolute paths with
// symlinks resolved. With explicit files the family is exactly those (plus video); otherwise it's detected from the
// file names in video's folder and its sibling folders. Versions other than video that don't probe with a duration
// are left out (still rendering).
func Detect(video string, explicit []string, pr Prober) []Version {
	var ps picks
	name0, ok0 := ParseStem(Stem(filepath.Base(video)))
	switch {
	case len(explicit) > 0:
		files := []string{video}
		for _, x := range explicit {
			if !SamePath(x, video) {
				files = append(files, x)
			}
		}
		for i, p := range files {
			nm, ok := ParseStem(Stem(filepath.Base(p)))
			label := Stem(filepath.Base(p))
			n := 1000 + i
			if ok {
				label, n = fmt.Sprintf("v%d", nm.N), nm.N
			}
			if ps.has(label) {
				label = fmt.Sprintf("%s-%d", label, i)
			}
			ps.set(label, p, n)
		}
	case ok0:
		detect(&ps, video, name0, pr)
	}
	if len(ps.order) == 0 {
		label := "current"
		if ok0 {
			label = fmt.Sprintf("v%d", name0.N)
		}
		ps.set(label, video, 0)
	}
	list := make([]pick, 0, len(ps.order))
	for _, l := range ps.order {
		list = append(list, ps.byKey[l])
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].n < list[j].n })
	out := make([]Version, 0, len(list))
	for _, p := range list {
		info := pr.Probe(p.path)
		cur := SamePath(p.path, video)
		if !info.HasDuration() && !cur {
			continue // still rendering or broken: skipped until it probes
		}
		out = append(out, Version{
			Label: p.label, N: p.n, Path: p.path, Name: filepath.Base(p.path), Current: cur,
			Duration: jsonx.FloatPtr(info.Duration), FPS: jsonx.Float(info.FPS), Width: info.Width, Height: info.Height,
		})
	}
	return out
}

type candidate struct {
	path  string
	plain bool
	mtime int64
}

// detect fills ps from the files named <prefix>_v<N>... in video's folder and every sibling folder (folders under
// its parent), skipping folders that end in ".review".
func detect(ps *picks, video string, name0 Name, pr Prober) {
	prefix := strings.ToLower(name0.Prefix)
	own := filepath.Dir(video)
	dirs := []string{own}
	if entries, err := os.ReadDir(filepath.Dir(own)); err == nil {
		for _, e := range entries {
			d := filepath.Join(filepath.Dir(own), e.Name())
			if strings.HasSuffix(e.Name(), ".review") || SamePath(d, own) {
				continue
			}
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				dirs = append(dirs, d)
			}
		}
	}
	found := map[int][]candidate{}
	var order []int
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !IsVideo(name) {
				continue
			}
			nm, ok := ParseStem(Stem(name))
			if !ok || strings.ToLower(nm.Prefix) != prefix {
				continue
			}
			f := filepath.Join(d, name)
			st, err := os.Stat(f)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			if _, seen := found[nm.N]; !seen {
				order = append(order, nm.N)
			}
			found[nm.N] = append(found[nm.N], candidate{path: f, plain: nm.Rest == "", mtime: st.ModTime().UnixNano()})
		}
	}
	if _, ok := found[name0.N]; !ok {
		// The opened file always is its version, even with an extension the scan doesn't list.
		order = append(order, name0.N)
		found[name0.N] = []candidate{{path: video, plain: true}}
	}
	for _, n := range order {
		best := ""
		if n == name0.N {
			best = video // the opened file is that version
		} else {
			fs := found[n]
			// A plain <name>_vN wins over <name>_vN_<anything>; then the newest; then the first that probes.
			sort.SliceStable(fs, func(i, j int) bool {
				if fs[i].plain != fs[j].plain {
					return fs[i].plain
				}
				return fs[i].mtime > fs[j].mtime
			})
			for _, c := range fs {
				if pr.Probe(c.path).HasDuration() {
					best = c.path
					break
				}
			}
		}
		if best != "" {
			ps.set(fmt.Sprintf("v%d", n), best, n)
		}
	}
}
