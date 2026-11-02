package ext

import (
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lukechampine.com/blake3"
)

// EnvPath is the variable that adds extension folders for a run, after --ext (":"-separated, like PATH).
const EnvPath = "RETAKE_EXT_PATH"

// Sources are the places extensions are found, in spec/extensions.md's order ("Where extensions are found").
type Sources struct {
	// Flags are --ext's folders, then RETAKE_EXT_PATH's (FlagDirs): each an extension folder or a folder of them.
	Flags []string
	// Project is the opened folder: its .retake/extensions/<id>/ are read (a path that already ends in
	// .retake/extensions is read as is). "" = none; a host never sets it.
	Project string
	// User is the folder of the user's extensions (UserDir), or of a server's (DataDir); "" = none.
	User string
	// UserWhere is how User's are listed: user (the default) or data.
	UserWhere string
	// Bundled holds the extensions built into retake, one folder per id (extensions.FS); nil = none.
	Bundled fs.FS
	// Retake is this binary's version, for min_retake; "" or a dev build skips that check.
	Retake string
}

// FlagDirs is --ext's folders followed by RETAKE_EXT_PATH's.
func FlagDirs(flags []string, getenv func(string) string) []string {
	out := append([]string(nil), flags...)
	for d := range strings.SplitSeq(getenv(EnvPath), string(os.PathListSeparator)) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// UserDir is the user's extensions folder: $XDG_CONFIG_HOME/retake/extensions, else ~/.config/retake/extensions
// (next to the theme and config.toml).
func UserDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "retake", "extensions"), nil
	}
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home folder for extensions: %w", err)
		}
		home = h
	}
	return filepath.Join(home, ".config", "retake", "extensions"), nil
}

// DataDir is retake serve's extensions folder: <data dir>/extensions.
func DataDir(data string) string { return filepath.Join(data, "extensions") }

// ProjectDir is the folder of an opened folder's own extensions: <folder>/.retake/extensions.
func ProjectDir(folder string) string {
	if filepath.Base(folder) == "extensions" && filepath.Base(filepath.Dir(folder)) == ".retake" {
		return folder
	}
	return filepath.Join(folder, ".retake", "extensions")
}

// Discover finds the extensions of src in spec order. The first folder that has an id wins; the folders of the same
// id found after it are in its Shadowed. Being found never turns one on (Resolve does).
func Discover(src Sources) []*Ext {
	var out []*Ext
	byID := map[string]*Ext{}
	add := func(e *Ext) {
		if w, ok := byID[e.ID]; ok {
			w.Shadowed = append(w.Shadowed, e.Display())
			return
		}
		e.checkRetake(src.Retake)
		byID[e.ID] = e
		out = append(out, e)
	}
	for _, d := range src.Flags {
		for _, e := range flagExts(d) {
			add(e)
		}
	}
	if src.Project != "" {
		for _, e := range folderExts(ProjectDir(src.Project), WhereProject) {
			add(e)
		}
	}
	if src.User != "" {
		where := src.UserWhere
		if where == "" {
			where = WhereUser
		}
		for _, e := range folderExts(src.User, where) {
			add(e)
		}
	}
	if src.Bundled != nil {
		entries, _ := fs.ReadDir(src.Bundled, ".")
		for _, en := range entries {
			if !en.IsDir() || strings.HasPrefix(en.Name(), ".") {
				continue
			}
			sub, err := fs.Sub(src.Bundled, en.Name())
			if err != nil {
				continue
			}
			if _, err := fs.Stat(sub, ManifestFile); err != nil {
				continue
			}
			add(Load(sub, "", WhereBundled, en.Name()))
		}
	}
	return out
}

// LoadDir reads the extension in the folder dir, found by --ext.
func LoadDir(dir string) *Ext {
	if a, err := filepath.Abs(dir); err == nil {
		dir = a
	}
	return Load(DirFS(dir), dir, WhereFlag, filepath.Base(dir))
}

// flagExts is a --ext folder's extensions: itself when it has an extension.toml, else its folders that have one.
// A folder with neither is an extension with one problem, so the run says so.
func flagExts(dir string) []*Ext {
	if a, err := filepath.Abs(dir); err == nil {
		dir = a
	}
	if isFile(filepath.Join(dir, ManifestFile)) {
		return []*Ext{LoadDir(dir)}
	}
	if found := folderExts(dir, WhereFlag); len(found) > 0 {
		return found
	}
	e := &Ext{Dir: dir, Where: WhereFlag, FS: DirFS(dir)}
	e.ID = filepath.Base(dir)
	msg := fmt.Sprintf("no %s in %s or its folders", ManifestFile, dir)
	if !isDir(dir) {
		msg = "no folder at " + dir
	}
	e.problem("manifest", dir, msg)
	return []*Ext{e}
}

// folderExts is the extensions in a folder of them: each folder (not hidden) that has an extension.toml, by name.
func folderExts(dir, where string) []*Ext {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Ext
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".") {
			continue
		}
		d := filepath.Join(dir, en.Name())
		if !isDir(d) || !isFile(filepath.Join(d, ManifestFile)) {
			continue
		}
		out = append(out, Load(DirFS(d), d, where, en.Name()))
	}
	return out
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// DirFS is a folder on disk as an fs.FS whose every open goes through os.Root, so .. and symlinks never leave it.
// Unlike os.DirFS it holds nothing open between calls.
type DirFS string

// Open opens a file of the folder.
func (d DirFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	r, err := os.OpenRoot(string(d))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	defer func() { _ = r.Close() }()
	return r.Open(filepath.FromSlash(name))
}

// hashFS is "b3:<hex>", the blake3 of an extension's files: each regular file not under a hidden segment, sorted by
// path, as its path, its size and its bytes. A project extension's pin holds it.
func hashFS(fsys fs.FS) string {
	var files []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is left out; the rest still counts
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	h := blake3.New(32, nil)
	for _, p := range files {
		f, err := fsys.Open(p)
		if err != nil {
			continue
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			_ = f.Close()
			continue
		}
		_, _ = io.WriteString(h, p+"\x00"+strconv.FormatInt(st.Size(), 10)+"\x00")
		_, _ = io.Copy(h, f)
		_ = f.Close()
	}
	return "b3:" + hex.EncodeToString(h.Sum(nil))
}
