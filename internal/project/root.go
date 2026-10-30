// Package project is the project store of spec/notes.md: <root>/.retake/project.db, a SQLite database (WAL) that
// holds the notes, the agent's questions and the reviewer's answers, the pushed versions, the job queue and the
// event log. Takes stay where they are; the store indexes them and holds everything that changes over time.
package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
)

// versionDirRE matches a version folder name: v7, V12.
var versionDirRE = regexp.MustCompile(`(?i)^v\d+$`)

// IsVersionDir reports whether a folder name looks like a version folder (v<N>).
func IsVersionDir(name string) bool { return versionDirRE.MatchString(name) }

// Root returns the project root of a video file (absolute, symlinks resolved). If the file's folder looks like a
// version folder (v<N>, or any folder that has sibling folders holding the same family), the root is that folder's
// parent. Otherwise it's the file's own folder. tutorial/v7/x.mp4 has the root tutorial/.
func Root(video string) string {
	dir := filepath.Dir(video)
	parent := filepath.Dir(dir)
	if parent == dir {
		return dir
	}
	if IsVersionDir(filepath.Base(dir)) {
		return parent
	}
	if name, ok := family.ParseStem(family.Stem(filepath.Base(video))); ok && siblingHolds(parent, dir, name.Prefix) {
		return parent
	}
	return dir
}

// siblingHolds reports whether a folder under parent, other than own and the .review folders, holds a video of the
// family named prefix.
func siblingHolds(parent, own, prefix string) bool {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	for _, e := range entries {
		d := filepath.Join(parent, e.Name())
		if strings.HasSuffix(e.Name(), ".review") || strings.HasPrefix(e.Name(), ".") || family.SamePath(d, own) {
			continue
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		files, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !family.IsVideo(f.Name()) {
				continue
			}
			if nm, ok := family.ParseStem(family.Stem(f.Name())); ok && strings.EqualFold(nm.Prefix, prefix) {
				return true
			}
		}
	}
	return false
}
