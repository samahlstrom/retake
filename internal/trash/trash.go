// Package trash moves a folder to the operating system's trash and back (spec/home.md, "Move to Trash"): the
// freedesktop trash on Linux and other Unix desktops (with its info file, so a file manager can restore it),
// ~/.Trash on macOS, and the Recycle Bin on Windows (with its $I record, so Explorer lists it and can restore it). It
// only ever renames: a folder the trash can't take that way stays where it is. Nothing is deleted for real.
package trash

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
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
	// RecycleBin is, on Windows, the person's folder in the Recycle Bin of the disk a path is on
	// (<drive>:\$Recycle.Bin\<their SID>), or ErrOtherDisk when that disk has none; nil = this computer's.
	RecycleBin func(path string) (string, error)
}

// Name is what the system calls its trash, for the page's words: "Recycle Bin" on Windows, else "Trash".
func (o Options) Name() string {
	if o.GOOS == "windows" || o.GOOS == "" && goos == "windows" {
		return "Recycle Bin"
	}
	return "Trash"
}

// Moved is a folder in the trash: where it was, where it is, and its info file ("" on macOS; the $I record on
// Windows).
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
	if o.RecycleBin == nil {
		o.RecycleBin = recycleBin
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
		return moveWindows(path, o)
	}
	return moveXDG(path, o)
}

// moveWindows renames it into the Recycle Bin of its disk the way Explorer does: as $R<6 letters or digits><its
// extension>, after writing $I<the same> (its record: size, deletion time and where it was), so Explorer lists it under
// its old name and its Restore puts it back.
func moveWindows(path string, o Options) (Moved, error) {
	dir, err := o.RecycleBin(path)
	if err != nil {
		return Moved{}, err
	}
	ext := filepath.Ext(filepath.Base(path))
	record := recycleRecord(path, treeSize(path), o.Now())
	for range 100 {
		id := recycleID()
		infoPath := filepath.Join(dir, "$I"+id+ext)
		to := filepath.Join(dir, "$R"+id+ext)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // in the Recycle Bin
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Moved{}, err
		}
		_, werr := f.Write(record)
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
	return Moved{}, errors.New("the Recycle Bin has too many items")
}

// recycleID is the random part of a Recycle Bin item's name: 6 capital letters or digits, as Explorer makes them.
func recycleID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var b [6]byte
	_, _ = rand.Read(b[:])
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b[:])
}

// recycleRecord is a Recycle Bin $I file, format 2 (Windows 10 and later), little-endian: the format (2), the item's
// size in bytes, when it was deleted (a FILETIME: 100 ns steps since 1601-01-01 UTC), the length of its path in UTF-16
// units with the closing NUL, then that path in UTF-16 with the NUL.
func recycleRecord(path string, size int64, deleted time.Time) []byte {
	u := utf16.Encode([]rune(path))
	b := make([]byte, 0, 28+2*(len(u)+1))
	b = binary.LittleEndian.AppendUint64(b, 2)
	b = binary.LittleEndian.AppendUint64(b, uint64(max(size, 0)))
	b = binary.LittleEndian.AppendUint64(b, uint64(deleted.UnixNano()/100+116444736000000000))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(u)+1)) //nolint:gosec // a path's length
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return append(b, 0, 0)
}

// treeSize is the bytes of the files at or under path (links not followed).
func treeSize(path string) int64 {
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
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
