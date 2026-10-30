package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// PushArgs are version_push's arguments.
type PushArgs struct {
	File    string                // the new version's video file
	Parent  string                // the version it was made from; "" = the newest
	Label   string                // v<N>; "" = the next free number
	Changes []project.ChangeEntry // what it changes, per note
	Asks    []project.PushAsk     // questions asked in the same push, per note
	Move    bool                  // move the file instead of copying it
	By      string                // who pushed; "" = agent
}

// PushResult is version_push's result.
type PushResult struct {
	Version   project.VersionRow `json:"version"`
	Answered  []int64            `json:"answered"`  // notes now answered by this version
	Questions []int64            `json:"questions"` // the question threads it asked
}

// nameRE splits a versioned stem: prefix, separator, the v, the number, the rest.
var nameRE = regexp.MustCompile(`(?i)^(.+?)([_\-])(v)(\d+)((?:[_\-].*)?)$`)

var labelRE = regexp.MustCompile(`(?i)^v(\d+)$`)

// VersionPush puts a new version into the family: it checks the push rule, copies (or moves) the file in as the
// next v<N> (into a sibling v<N>/ folder when the family uses version folders, else next to its parent), and
// records its sha256, parent and changelog. The changelog's notes become answered and the asked ones question.
func (p *Project) VersionPush(_ context.Context, a PushArgs) (PushResult, error) {
	if a.File == "" {
		return PushResult{}, errorf(CodeBadRequest, "push needs the new version's file")
	}
	src, err := filepath.Abs(a.File)
	if err != nil {
		return PushResult{}, errorf(CodeBadRequest, "%v", err)
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		return PushResult{}, errorf(CodeNotFound, "no such file: %s", src)
	}
	if !family.IsVideo(src) || family.IsPlaylist(src) {
		return PushResult{}, errorf(CodeBadRequest, "%s is not a single video file (.mp4 .m4v .mov .webm .mkv)", src)
	}
	fam := p.Family()
	if len(fam) == 0 {
		return PushResult{}, errorf(CodeNotFound, "no versions found next to %s", p.Video)
	}
	parent := fam[len(fam)-1]
	if a.Parent != "" {
		v, ok := find(fam, a.Parent)
		if !ok {
			return PushResult{}, errorf(CodeNotFound, "no parent version %s (the family has %s)", a.Parent, labels(fam))
		}
		parent = v
	}
	m := nameRE.FindStringSubmatch(family.Stem(filepath.Base(parent.Path)))
	if m == nil {
		return PushResult{}, errorf(CodeBadRequest, "%s has no _v<N> in its name, so a new version can't be named after "+
			"it; name the family's files like clip_v1.mp4", filepath.Base(parent.Path))
	}
	n, err := p.nextNumber(fam, m[1], a.Label)
	if err != nil {
		return PushResult{}, err
	}
	label := fmt.Sprintf("v%d", n)
	dir := filepath.Dir(parent.Path)
	if base := filepath.Base(dir); project.IsVersionDir(base) {
		dir = filepath.Join(filepath.Dir(dir), base[:1]+strconv.Itoa(n))
	}
	dest := filepath.Join(dir, m[1]+m[2]+m[3]+strconv.Itoa(n)+strings.ToLower(filepath.Ext(src)))
	if _, err := os.Lstat(dest); err == nil {
		return PushResult{}, errorf(CodeBadRequest, "%s already exists", dest)
	}
	by := a.By
	if by == "" {
		by = "agent"
	}
	push := project.Push{Label: label, Path: dest, Parent: parent.Label, By: by, Changelog: a.Changes, Asks: a.Asks,
		Scope: scope(parent)}
	if err := p.Store.CheckPush(push); err != nil {
		return PushResult{}, wrap(err)
	}

	placed, err := place(src, dest, a.Move)
	if err != nil {
		return PushResult{}, errorf(CodeFailed, "copying %s to %s: %v", src, dest, err)
	}
	push.SHA256 = placed.sum
	res, err := p.Store.RecordPush(push)
	if err != nil {
		placed.undo()
		return PushResult{}, wrap(err)
	}
	if a.Move && !placed.renamed {
		_ = os.Remove(src) //nolint:gosec // the caller asked to move this file
	}
	return PushResult{Version: res.Version, Answered: nonNil(res.Answered), Questions: nonNil(res.Questions)}, nil
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// nextNumber picks the new version's number: the requested label's, or one more than any version of the family
// there is (pushed, detected, or still rendering).
func (p *Project) nextNumber(fam []family.Version, prefix, want string) (int, error) {
	taken := map[int]bool{}
	top := 0
	note := func(n int) {
		taken[n] = true
		top = max(top, n)
	}
	for _, v := range fam {
		if m := labelRE.FindStringSubmatch(v.Label); m != nil {
			n, _ := strconv.Atoi(m[1])
			note(n)
		}
	}
	rows, err := p.Store.Versions()
	if err != nil {
		return 0, wrap(err)
	}
	for _, r := range rows {
		if m := labelRE.FindStringSubmatch(r.Label); m != nil {
			n, _ := strconv.Atoi(m[1])
			note(n)
		}
	}
	for _, n := range rendering(p.Video, prefix) {
		note(n)
	}
	if want == "" {
		return top + 1, nil
	}
	m := labelRE.FindStringSubmatch(want)
	if m == nil {
		return 0, errorf(CodeBadRequest, "label %q must be v<N>: the review page finds versions by the _v<N> in "+
			"their file names", want)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, errorf(CodeBadRequest, "label %q has no usable number", want)
	}
	if taken[n] {
		return 0, errorf(CodeBadRequest, "v%d is taken; the next free one is v%d", n, top+1)
	}
	return n, nil
}

// rendering returns the version numbers of every file of the family in the video's folder and its siblings,
// including the ones that don't probe yet (still rendering), so a push never takes their number.
func rendering(video, prefix string) []int {
	own := filepath.Dir(video)
	dirs := []string{own}
	if entries, err := os.ReadDir(filepath.Dir(own)); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasSuffix(e.Name(), ".review") {
				dirs = append(dirs, filepath.Join(filepath.Dir(own), e.Name()))
			}
		}
	}
	var out []int
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !family.IsVideo(e.Name()) {
				continue
			}
			if nm, ok := family.ParseStem(family.Stem(e.Name())); ok && strings.EqualFold(nm.Prefix, prefix) {
				out = append(out, nm.N)
			}
		}
	}
	return out
}

type placed struct {
	sum     string
	renamed bool   // moved by a rename: undo renames it back
	src     string // for undo
	dest    string
	newDir  string // a folder push created, removed by undo if still empty
}

// place puts src at dest (a new file; its folder is created if needed) and returns the file's sha256. With move it
// renames when it can, and otherwise copies (the caller removes src once the push is recorded).
func place(src, dest string, move bool) (placed, error) {
	pl := placed{src: src, dest: dest}
	dir := filepath.Dir(dest)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		pl.newDir = dir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a version folder others may read
		return pl, err
	}
	if move {
		if err := os.Rename(src, dest); err == nil {
			pl.renamed = true
			sum, err := hashFile(dest)
			if err != nil {
				pl.undo()
				return pl, err
			}
			pl.sum = sum
			return pl, nil
		}
	}
	tmp := filepath.Join(dir, "."+filepath.Base(dest)+".retake-tmp")
	sum, err := copyHash(src, tmp)
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		_ = os.Remove(tmp)
		pl.undo()
		return pl, err
	}
	pl.sum = sum
	return pl, nil
}

// undo takes the file back out of the family.
func (pl placed) undo() {
	if pl.renamed {
		_ = os.Rename(pl.dest, pl.src)
	} else {
		_ = os.Remove(pl.dest)
	}
	if pl.newDir != "" {
		_ = os.Remove(pl.newDir) // only if empty
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the pushed version
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyHash(src, dest string) (string, error) {
	in, err := os.Open(src) //nolint:gosec // the file the caller pushes
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a video others may read
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VersionView is one version for versions_list: the family member and what its push recorded.
type VersionView struct {
	Label     string                `json:"label"`
	N         int                   `json:"n"`
	Path      string                `json:"path"`
	Newest    bool                  `json:"newest"`
	InFamily  bool                  `json:"in_family"` // false: pushed, but not found or not playable (yet)
	Duration  *float64              `json:"duration"`
	SHA256    string                `json:"sha256,omitempty"`
	Parent    string                `json:"parent,omitempty"`
	By        string                `json:"by,omitempty"`
	Created   string                `json:"created,omitempty"`
	Changelog []project.ChangeEntry `json:"changelog"`
	Open      int                   `json:"open"` // notes open on it, which a push from it must handle
}

// VersionsResult is versions_list's result.
type VersionsResult struct {
	Versions []VersionView `json:"versions"`
}

// VersionsList returns the family's versions, oldest first, with the parent and changelog of the pushed ones.
func (p *Project) VersionsList(_ context.Context) (VersionsResult, error) {
	fam := p.Family()
	rows, err := p.Store.Versions()
	if err != nil {
		return VersionsResult{}, wrap(err)
	}
	pushed := map[string]project.VersionRow{}
	for _, r := range rows {
		pushed[r.Label] = r
	}
	out := VersionsResult{Versions: []VersionView{}}
	add := func(v VersionView, row project.VersionRow, ok bool) {
		v.Changelog = []project.ChangeEntry{}
		if ok {
			v.SHA256, v.Parent, v.By, v.Created, v.Changelog = row.SHA256, row.Parent, row.By, row.Created, row.Changelog
		}
		out.Versions = append(out.Versions, v)
	}
	for i, v := range fam {
		view := VersionView{Label: v.Label, N: v.N, Path: v.Path, Newest: i == len(fam)-1, InFamily: true}
		if v.Duration != nil {
			d := float64(*v.Duration)
			view.Duration = &d
		}
		sc := scope(v)
		open, err := p.Store.Notes(project.NoteFilter{Statuses: []project.Status{project.StatusOpen}, Scope: &sc})
		if err != nil {
			return VersionsResult{}, wrap(err)
		}
		view.Open = len(open)
		row, ok := pushed[v.Label]
		add(view, row, ok)
		delete(pushed, v.Label)
	}
	for _, r := range rows {
		if _, ok := pushed[r.Label]; ok {
			add(VersionView{Label: r.Label, Path: r.Path}, r, true)
		}
	}
	return out, nil
}
