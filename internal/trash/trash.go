// Package trash moves a folder to the operating system's trash and back (spec/home.md, "Move to Trash"): the
// freedesktop trash on Linux and other Unix desktops (with its info file, so a file manager can restore it), and
// ~/.Trash on macOS. It only ever renames: a folder the trash can't take that way stays where it is. Nothing is
// deleted for real.
package trash

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrOtherDisk is a folder on a disk whose trash can't take it by a rename.
var ErrOtherDisk = errors.New("can't move it to the Trash from this disk")

// Options say where the trash is. The zero value is this computer's, from the environment.
type Options struct {
	GOOS     string           // "" = the running system's
	Home     string           // the home folder; "" = os.UserHomeDir
	DataHome string           // $XDG_DATA_HOME; "" = its value, else <Home>/.local/share
	UID      int              // for <mount>/.Trash-<uid>; -1 = os.Getuid
	Now      func() time.Time // nil = time.Now
	// SameDisk reports whether two paths are on the same disk; nil = by their device numbers.
	SameDisk func(a, b string) bool
	// MountOf finds the mount point a path is on; nil = by walking up while the device stays the same.
	MountOf func(path string) string
}

// Moved is a folder in the trash: where it was, where it is, and its info file ("" on macOS).
type Moved struct {
	From string `json:"from"`
	To   string `json:"to"`
	Info string `json:"info,omitempty"`
}

func (o Options) fill() (Options, error) {
	if o.GOOS == "" {
		o.GOOS = goos
	}
	if o.Home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return o, fmt.Errorf("no home folder: %w", err)
		}
		o.Home = h
	}
	if o.DataHome == "" {
		o.DataHome = os.Getenv("XDG_DATA_HOME")
	}
	if o.DataHome == "" || !filepath.IsAbs(o.DataHome) {
		o.DataHome = filepath.Join(o.Home, ".local", "share")
	}
	if o.UID < 0 {
		o.UID = os.Getuid()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SameDisk == nil {
		o.SameDisk = sameDisk
	}
	if o.MountOf == nil {
		o.MountOf = mountOf
	}
	return o, nil
}

// Move moves the folder (or file) at path to the trash. path must be absolute and exist.
func Move(path string, o Options) (Moved, error) {
	o, err := o.fill()
	if err != nil {
		return Moved{}, err
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return Moved{}, fmt.Errorf("%s isn't an absolute path", path)
	}
	if _, err := os.Lstat(path); err != nil {
		return Moved{}, err
	}
	switch o.GOOS {
	case "darwin":
		return moveMac(path, o)
	case "windows":
		return Moved{}, errors.New("moving to the Recycle Bin isn't supported yet")
	}
	return moveXDG(path, o)
}

// moveMac renames it into ~/.Trash, as "<name>", else "<name> 2", "<name> 3", ….
func moveMac(path string, o Options) (Moved, error) {
	dir := filepath.Join(o.Home, ".Trash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Moved{}, err
	}
	if !o.SameDisk(path, dir) {
		return Moved{}, ErrOtherDisk
	}
	base := filepath.Base(path)
	for n := 1; n < 10000; n++ {
		name := base
		if n > 1 {
			name = base + " " + strconv.Itoa(n)
		}
		to := filepath.Join(dir, name)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		if err := os.Rename(path, to); err != nil {
			return Moved{}, err
		}
		return Moved{From: path, To: to}, nil
	}
	return Moved{}, errors.New("the trash has too many items of that name")
}

// trashDirs are the freedesktop trash folders that may take path, in order: the home trash when path is on its disk,
// else that disk's <mount>/.Trash-<uid>.
func trashDirs(path string, o Options) ([]string, error) {
	home := filepath.Join(o.DataHome, "Trash")
	if err := os.MkdirAll(filepath.Join(home, "files"), 0o700); err != nil {
		return nil, err
	}
	if o.SameDisk(path, home) {
		return []string{home}, nil
	}
	mount := o.MountOf(path)
	if mount == "" {
		return nil, ErrOtherDisk
	}
	return []string{filepath.Join(mount, ".Trash-"+strconv.Itoa(o.UID))}, nil
}

// moveXDG writes the info file first (claiming the name), then renames the folder into files/ (the Trash
// specification's order). A rename that fails takes the info file back.
func moveXDG(path string, o Options) (Moved, error) {
	dirs, err := trashDirs(path, o)
	if err != nil {
		return Moved{}, err
	}
	dir := dirs[0]
	for _, sub := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return Moved{}, ErrOtherDisk
		}
	}
	base := filepath.Base(path)
	info := infoText(path, o.Now())
	for n := 1; n < 10000; n++ {
		name := base
		if n > 1 {
			name = base + "." + strconv.Itoa(n)
		}
		infoPath := filepath.Join(dir, "info", name+".trashinfo")
		to := filepath.Join(dir, "files", name)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // in the trash's info/
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Moved{}, err
		}
		_, werr := f.WriteString(info)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(infoPath)
			return Moved{}, werr
		}
		if err := os.Rename(path, to); err != nil {
			_ = os.Remove(infoPath)
			if isCrossDevice(err) {
				return Moved{}, ErrOtherDisk
			}
			return Moved{}, err
		}
		return Moved{From: path, To: to, Info: infoPath}, nil
	}
	return Moved{}, errors.New("the trash has too many items of that name")
}

// infoText is a .trashinfo file: the original path, URL-escaped by segment, and the local deletion time.
func infoText(path string, now time.Time) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "[Trash Info]\nPath=" + strings.Join(parts, "/") + "\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
}

// Restore puts a moved folder back where it was. It refuses when something is at the old place now, or when the
// folder is no longer in the trash.
func Restore(m Moved) error {
	if _, err := os.Lstat(m.To); err != nil {
		return fmt.Errorf("it isn't in the trash any more: %w", err)
	}
	if _, err := os.Lstat(m.From); err == nil {
		return fmt.Errorf("%s: %w", m.From, os.ErrExist)
	}
	if err := os.MkdirAll(filepath.Dir(m.From), 0o755); err != nil { //nolint:gosec // the folder it was in
		return err
	}
	if err := os.Rename(m.To, m.From); err != nil {
		return err
	}
	if m.Info != "" {
		_ = os.Remove(m.Info)
	}
	return nil
}
