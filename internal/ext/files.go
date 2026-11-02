package ext

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// FileInfo is one owned file (spec/extensions.md, "Files it owns").
type FileInfo struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix ms
	SHA256   string `json:"sha256"`
	Deleted  bool   `json:"deleted,omitempty"` // in the ext.files event: it's gone
}

// The files API's errors. A path that isn't owned or isn't a regular file inside the folder is ErrNotOwned or
// ErrNotFound (both answer 404).
var (
	ErrNotFound = errors.New("not found")
	ErrNotOwned = errors.New("not one of the extension's files")
	ErrTooLarge = fmt.Errorf("over %d MiB", MaxWrite>>20)
)

// ChangedError is an If-Match that doesn't hold: the file changed (or appeared, or is gone). SHA256 is its sha now,
// "" when there is no file. It answers 409 {"error": "changed", "sha256"}.
type ChangedError struct{ SHA256 string }

func (e *ChangedError) Error() string { return "changed" }

// Folder is one extension's files in a video's folder: Root is the folder (absolute) and Owns the extension's globs.
// Every operation goes through os.Root, so .. and symlinks never leave the folder, and only reaches paths that are
// owned, allowed (never .retake/, a version folder other than v0, or a family video file) and, under v0/, only while
// v0/ is no video version.
type Folder struct {
	Root string
	Owns []string
}

// writeMu serializes the If-Match check and the rename of writes and removes, process-wide.
var writeMu sync.Mutex

// owned reports whether p is one of the folder's owned paths, as far as the name goes.
func (f Folder) owned(p string) bool {
	return allowed(p) && slices.ContainsFunc(f.Owns, func(g string) bool { return MatchGlob(g, p) })
}

// open opens the folder and checks p: owned, and not under a v0/ that is a video version.
func (f Folder) open(p string) (*os.Root, error) {
	if !f.owned(p) {
		return nil, ErrNotOwned
	}
	r, err := os.OpenRoot(f.Root)
	if err != nil {
		return nil, ErrNotFound
	}
	if first, _, deep := strings.Cut(p, "/"); deep && first == "v0" && rootHoldsVideo(r, first) {
		_ = r.Close()
		return nil, ErrNotOwned
	}
	return r, nil
}

func rootHoldsVideo(r *os.Root, dir string) bool {
	d, err := r.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(en os.DirEntry) bool { return !en.IsDir() && isFamilyVideo(en.Name()) })
}

// List is the owned regular files, sorted by path; glob narrows them ("" = all).
func (f Folder) List(glob string) ([]FileInfo, error) {
	r, err := os.OpenRoot(f.Root)
	if err != nil {
		return nil, ErrNotFound
	}
	defer func() { _ = r.Close() }()
	video0 := rootHoldsVideo(r, "v0")
	out := []FileInfo{}
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is left out; the rest is still listed
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			segs := strings.Split(p, "/")
			if strings.HasPrefix(d.Name(), ".") || (isVersionDir(segs[0]) && (segs[0] != "v0" || video0)) ||
				!slices.ContainsFunc(f.Owns, func(g string) bool { return mayHold(g, segs) }) {
				return fs.SkipDir
			}
			return nil
		}
		if !f.owned(p) || (glob != "" && !MatchGlob(glob, p)) {
			return nil
		}
		if first, _, deep := strings.Cut(p, "/"); deep && first == "v0" && video0 {
			return nil
		}
		info, err := stat(r, p)
		if err != nil {
			return nil //nolint:nilerr // a symlink out, or a file that just went: not listed
		}
		out = append(out, info)
		return nil
	})
	slices.SortFunc(out, func(a, b FileInfo) int { return strings.Compare(a.Path, b.Path) })
	return out, err
}

// Stat is an owned file's FileInfo.
func (f Folder) Stat(p string) (FileInfo, error) {
	r, err := f.open(p)
	if err != nil {
		return FileInfo{}, err
	}
	defer func() { _ = r.Close() }()
	return stat(r, p)
}

// Open opens an owned regular file for reading, with its FileInfo (sha256 for the ETag).
func (f Folder) Open(p string) (*os.File, FileInfo, error) {
	r, err := f.open(p)
	if err != nil {
		return nil, FileInfo{}, err
	}
	defer func() { _ = r.Close() }()
	file, err := r.Open(filepath.FromSlash(p))
	if err != nil {
		return nil, FileInfo{}, ErrNotFound
	}
	info, err := fileInfo(file, p)
	if err != nil {
		_ = file.Close()
		return nil, FileInfo{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, FileInfo{}, err
	}
	return file, info, nil
}

// stat opens p in r and returns its FileInfo.
func stat(r *os.Root, p string) (FileInfo, error) {
	file, err := r.Open(filepath.FromSlash(p))
	if err != nil {
		return FileInfo{}, ErrNotFound
	}
	defer func() { _ = file.Close() }()
	return fileInfo(file, p)
}

// fileInfo is an open file's FileInfo; it reads the whole file for its sha256.
func fileInfo(file *os.File, p string) (FileInfo, error) {
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return FileInfo{}, ErrNotFound
	}
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Path: p, Size: n, Modified: st.ModTime().UnixMilli(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// current is p's sha256, "" when there's no file. A folder or other non-file there is an error.
func current(r *os.Root, p string) (string, error) {
	st, err := r.Lstat(filepath.FromSlash(p))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() && st.Mode()&fs.ModeSymlink == 0 {
		return "", fmt.Errorf("%s isn't a file", p)
	}
	info, err := stat(r, p)
	if err != nil {
		return "", err
	}
	return info.SHA256, nil
}

// cleanMatch is an If-Match value without quotes or W/: "" (no check), "*" or a sha256.
func cleanMatch(m string) string {
	m = strings.TrimSpace(m)
	m = strings.TrimPrefix(m, "W/")
	return strings.Trim(m, `"`)
}

// holds reports whether an If-Match holds for a file whose sha is now cur ("" = no file). For a write "*" means the
// file must not exist yet; for a remove it means it must.
func holds(ifMatch, cur string, write bool) bool {
	switch ifMatch {
	case "":
		return true
	case "*":
		return (cur == "") == write
	}
	return cur != "" && ifMatch == cur
}

// Write writes an owned file from body (spec/extensions.md: folders made, a temp file then a rename, at most MaxWrite
// bytes). ifMatch "" doesn't check, "*" needs the file not to exist, and a sha256 needs the file to be that one; when
// it doesn't hold the error is a *ChangedError.
func (f Folder) Write(p string, body io.Reader, ifMatch string) (FileInfo, error) {
	r, err := f.open(p)
	if err != nil {
		return FileInfo{}, err
	}
	defer func() { _ = r.Close() }()
	ifMatch = cleanMatch(ifMatch)
	dir := path.Dir(p)
	if dir != "." {
		if err := r.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
			return FileInfo{}, fmt.Errorf("make %s: %w", dir, err)
		}
	}
	tmp, err := tempName(p)
	if err != nil {
		return FileInfo{}, err
	}
	out, err := r.OpenFile(filepath.FromSlash(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return FileInfo{}, err
	}
	n, err := io.Copy(out, io.LimitReader(body, MaxWrite+1))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxWrite {
		err = ErrTooLarge
	}
	if err != nil {
		_ = r.Remove(filepath.FromSlash(tmp))
		return FileInfo{}, err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	cur, err := current(r, p)
	if err == nil && !holds(ifMatch, cur, true) {
		err = &ChangedError{SHA256: cur}
	}
	if err == nil {
		err = r.Rename(filepath.FromSlash(tmp), filepath.FromSlash(p))
	}
	if err != nil {
		_ = r.Remove(filepath.FromSlash(tmp))
		return FileInfo{}, err
	}
	return stat(r, p)
}

// tempName is a hidden temp file next to p, never listed or owned.
func tempName(p string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	name := "." + path.Base(p) + "." + hex.EncodeToString(b) + ".tmp"
	if dir := path.Dir(p); dir != "." {
		return dir + "/" + name, nil
	}
	return name, nil
}

// Remove deletes an owned file. ifMatch "" doesn't check, "*" needs it to exist, a sha256 needs it to be that one
// (else a *ChangedError). No file is ErrNotFound.
func (f Folder) Remove(p, ifMatch string) error {
	r, err := f.open(p)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	writeMu.Lock()
	defer writeMu.Unlock()
	cur, err := current(r, p)
	if err != nil {
		return err
	}
	if cur == "" {
		return ErrNotFound
	}
	if !holds(cleanMatch(ifMatch), cur, false) {
		return &ChangedError{SHA256: cur}
	}
	return r.Remove(filepath.FromSlash(p))
}
