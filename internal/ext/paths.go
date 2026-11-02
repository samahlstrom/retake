package ext

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/family"
)

// MaxPath is the longest owned or ui/ path, in bytes.
const MaxPath = 1024

// ValidPath reports whether p is a path an extension may name: slash-separated and relative, no empty, "." or ".."
// segment, no hidden segment (so never .retake/), no "\" or NUL, valid UTF-8, at most MaxPath bytes.
func ValidPath(p string) bool {
	if p == "" || len(p) > MaxPath || !utf8.ValidString(p) || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}

// MatchGlob reports whether the slash path p matches glob: * and ? within a segment, [...] classes, and ** for any
// number of segments (none included, except at the end: v0/** is what's inside v0/). A malformed glob matches
// nothing.
func MatchGlob(glob, p string) bool {
	if glob == "" || p == "" {
		return false
	}
	return matchSegs(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegs(g, p []string) bool {
	for len(g) > 0 {
		if g[0] == "**" {
			if len(g) == 1 {
				return len(p) > 0 // a trailing ** is what's inside, not the folder itself
			}
			for i := 0; i <= len(p); i++ {
				if matchSegs(g[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		if ok, err := path.Match(g[0], p[0]); err != nil || !ok {
			return false
		}
		g, p = g[1:], p[1:]
	}
	return len(p) == 0
}

// mayHold reports whether a path inside the folder dir (slash segments) could match glob: whether a walk should go
// into it.
func mayHold(glob string, dir []string) bool {
	g := strings.Split(glob, "/")
	for _, seg := range dir {
		if len(g) == 0 {
			return false
		}
		if g[0] == "**" {
			return true
		}
		if ok, err := path.Match(g[0], seg); err != nil || !ok {
			return false
		}
		g = g[1:]
	}
	return len(g) > 0
}

// isVersionDir reports whether a folder name is a version folder (v<N>).
func isVersionDir(name string) bool { return versionRE.MatchString(name) }

// isFamilyVideo reports whether a file name is a versioned video, a version of a family (clip_v3.mp4).
func isFamilyVideo(name string) bool {
	if !family.IsVideo(name) {
		return false
	}
	_, ok := family.ParseStem(family.Stem(name))
	return ok
}

// allowed reports whether an extension may ever touch p, whatever it owns: a valid path, not inside a version folder
// other than v0 (the only kind folder API 1 has), and not a family video file (spec/extensions.md, "Files it owns").
// Hidden segments, so .retake/, are already refused by ValidPath.
func allowed(p string) bool {
	if !ValidPath(p) {
		return false
	}
	segs := strings.Split(p, "/")
	if len(segs) > 1 && isVersionDir(segs[0]) && segs[0] != "v0" {
		return false
	}
	return !isFamilyVideo(segs[len(segs)-1])
}

// Probes for the owns check: names a version folder or a family video file can have.
var (
	versionProbes = []string{"v1", "v2", "v7", "v10", "v42", "v100", "V3"}
	videoProbes   = []string{"clip_v1.mp4", "clip-v2.mov", "clip_v3.webm", "clip_v4.mkv", "clip_v5.m4v", "clip_v6.m3u8",
		"CLIP_V7.MP4", "a_v1_preview.mp4"}
)

// globProblem says why an owns glob may not be used (spec/extensions.md, "Files it owns"), or "" when it may.
// kindFolders are the version folders the extension's own kinds claim.
func globProblem(glob string, kindFolders []string) string {
	if glob == "" || strings.HasPrefix(glob, "/") || strings.ContainsAny(glob, "\\\x00") {
		return "isn't a relative slash path"
	}
	segs := strings.Split(glob, "/")
	for _, s := range segs {
		switch {
		case s == "" || s == "." || s == "..":
			return "isn't a relative slash path"
		case s == ".retake":
			return "reaches .retake/"
		case strings.HasPrefix(s, "."):
			return "names a hidden file or folder, which is never served"
		}
		if _, err := path.Match(s, "x"); err != nil {
			return "isn't a valid glob"
		}
	}
	own := func(s string) bool {
		for _, k := range kindFolders {
			if s == k {
				return true
			}
		}
		return false
	}
	first := segs[0]
	if len(segs) > 1 || first == "**" {
		if isVersionDir(first) && !own(first) {
			return "reaches " + first + "/, another version's folder"
		}
		if !own(first) {
			for _, v := range versionProbes {
				if mayHold(glob, []string{v}) {
					return "reaches version folders (" + v + "/)"
				}
			}
		}
	}
	if own(first) {
		return "" // inside its own kind's folder
	}
	if last := segs[len(segs)-1]; isFamilyVideo(last) {
		return "reaches a family video file (" + last + ")"
	}
	dir := strings.Join(segs[:len(segs)-1], "/")
	for _, v := range videoProbes {
		for _, p := range []string{v, "footage/" + v, dir + "/" + v} {
			if MatchGlob(glob, strings.TrimPrefix(p, "/")) {
				return "reaches family video files (" + v + ")"
			}
		}
	}
	return ""
}
