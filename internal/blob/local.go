package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Modes of what Local writes: the media of a project, which other local users may read, as `retake open` writes it.
const (
	fileMode fs.FileMode = 0o644
	dirMode  fs.FileMode = 0o755
)

// tempSuffix marks Local's own temp files, which List never shows.
const tempSuffix = ".blob-tmp"

// Local keeps objects as files under Root: the key k is the file Root/k.
type Local struct {
	Root string
}

var _ Store = (*Local)(nil)

// NewLocal returns a Local store rooted at root.
func NewLocal(root string) *Local { return &Local{Root: root} }

// Name implements Store.
func (*Local) Name() string { return "local" }

// path returns the file of a valid key.
func (l *Local) path(key string) string { return filepath.Join(l.Root, filepath.FromSlash(key)) }

// Put implements Store: it writes a temp file beside the object and renames it into place.
func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, _ string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dest := l.path(key)
	if err := os.MkdirAll(filepath.Dir(dest), dirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*"+tempSuffix)
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	src := r
	if size >= 0 {
		src = io.LimitReader(r, size)
	}
	n, err := io.Copy(f, src)
	if err != nil {
		return fail(err)
	}
	if size >= 0 && n != size {
		return fail(fmt.Errorf("blob: %s: got %d bytes, want %d: %w", key, n, size, io.ErrUnexpectedEOF))
	}
	if err := f.Chmod(fileMode); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// PutFile implements Store. When path is the key's own file (the same file, through any link) there is nothing to
// do: the workspace is the store.
func (l *Local) PutFile(ctx context.Context, key, path, contentType string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	src, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !src.Mode().IsRegular() {
		return fmt.Errorf("blob: %s is not a file", path)
	}
	if dst, err := os.Stat(l.path(key)); err == nil && os.SameFile(src, dst) {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // the caller's own file, stored as given
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return l.Put(ctx, key, f, src.Size(), contentType)
}

// Get implements Store.
func (l *Local) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("blob: negative offset %d", offset)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(l.path(key)) //nolint:gosec // checkKey keeps the key inside Root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = ErrNotFound
		}
		return nil, err
	}
	if offset >= st.Size() || length == 0 {
		_ = f.Close()
		return emptyReader(), nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	if length < 0 {
		return f, nil
	}
	return limited{io.LimitReader(f, length), f}, nil
}

// limited reads a part of a file and closes the file.
type limited struct {
	io.Reader
	io.Closer
}

// Stat implements Store.
func (l *Local) Stat(ctx context.Context, key string) (Info, error) {
	if err := checkKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	st, err := os.Stat(l.path(key)) //nolint:gosec // checkKey keeps the key inside Root
	if errors.Is(err, fs.ErrNotExist) || err == nil && !st.Mode().IsRegular() {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return Info{Key: key, Size: st.Size(), Modified: st.ModTime()}, nil
}

// Delete implements Store.
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p := l.path(key)
	if st, err := os.Lstat(p); err == nil && st.IsDir() {
		return nil // a folder isn't an object
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List implements Store. It walks the folder the prefix names (or the one its last partial segment is in), and
// returns the regular files whose keys start with prefix.
func (l *Local) List(ctx context.Context, prefix string) ([]Info, error) {
	if !validPrefix(prefix) {
		return nil, badKey(prefix)
	}
	dir := ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		dir = prefix[:i]
	}
	start := l.Root
	if dir != "" {
		start = l.path(dir)
	}
	out := []Info{}
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // the prefix's folder isn't there: nothing under it
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.HasSuffix(d.Name(), tempSuffix) {
			return nil
		}
		rel, err := filepath.Rel(l.Root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !ValidKey(key) || !strings.HasPrefix(key, prefix) {
			return nil
		}
		st, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed while we walked
		}
		if err != nil {
			return err
		}
		out = append(out, Info{Key: key, Size: st.Size(), Modified: st.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}
