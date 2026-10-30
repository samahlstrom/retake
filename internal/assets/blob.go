package assets

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lukechampine.com/blake3"
)

// HashPrefix starts every content hash: BLAKE3, as the editor writes it.
const HashPrefix = "b3:"

// blobStore is a content-addressed folder: blobs/b3/<first 2 hex>/<hex><.ext>.
type blobStore struct{ dir string }

// refHex splits a blob ref, b3:<hex><.ext>, into its hex digest and extension.
func refHex(ref string) (hexs, ext string, err error) {
	rest, ok := strings.CutPrefix(ref, HashPrefix)
	if !ok {
		return "", "", fmt.Errorf("%q is not a blob ref", ref)
	}
	hexs, ext = rest, ""
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		hexs, ext = rest[:i], rest[i:]
	}
	if len(hexs) != 64 {
		return "", "", fmt.Errorf("%q is not a blob ref", ref)
	}
	if _, err := hex.DecodeString(hexs); err != nil {
		return "", "", fmt.Errorf("%q is not a blob ref", ref)
	}
	return hexs, ext, nil
}

// path returns the file of a blob ref.
func (b blobStore) path(ref string) (string, error) {
	hexs, ext, err := refHex(ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(b.dir, "b3", hexs[:2], hexs+ext), nil
}

// ref makes a blob ref from a hash (b3:<hex>) and an extension (".jpg" or "").
func ref(hash, ext string) string { return hash + strings.ToLower(ext) }

// hashReader hashes r by streaming it.
func hashReader(r io.Reader) (string, int64, error) {
	h := blake3.New(32, nil)
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return HashPrefix + hex.EncodeToString(h.Sum(nil)), n, nil
}

// HashFile returns the BLAKE3 of a file, read as a stream, and its size.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // a file the user pointed us at, opened read-only
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	return hashReader(f)
}

// HashBytes returns the BLAKE3 of b.
func HashBytes(b []byte) string {
	s := blake3.Sum256(b)
	return HashPrefix + hex.EncodeToString(s[:])
}

// putFile copies src into the store, hashing it on the way (one read), and returns its ref and size. A blob that
// is already there is kept.
func (b blobStore) putFile(src, ext string) (string, string, int64, error) {
	in, err := os.Open(src) //nolint:gosec // a file the user pointed us at, opened read-only
	if err != nil {
		return "", "", 0, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Join(b.dir, "b3"), 0o755); err != nil { //nolint:gosec // a user folder
		return "", "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Join(b.dir, "b3"), ".incoming-*")
	if err != nil {
		return "", "", 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := blake3.New(32, nil)
	n, err := io.Copy(io.MultiWriter(tmp, h), in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", 0, err
	}
	hash := HashPrefix + hex.EncodeToString(h.Sum(nil))
	r := ref(hash, ext)
	if err := b.place(tmp.Name(), r); err != nil {
		return "", "", 0, err
	}
	return r, hash, n, nil
}

// putBytes stores data and returns its ref.
func (b blobStore) putBytes(data []byte, ext string) (string, error) {
	r := ref(HashBytes(data), ext)
	dst, err := b.path(r)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return r, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a user folder
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".incoming-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return r, os.Rename(tmp.Name(), dst)
}

// putTemp moves a finished temporary file into the store and returns its ref.
func (b blobStore) putTemp(path, ext string) (string, error) {
	hash, _, err := HashFile(path)
	if err != nil {
		return "", err
	}
	r := ref(hash, ext)
	if err := b.place(path, r); err != nil {
		return "", err
	}
	return r, nil
}

// place renames a file into the store as r, unless the blob already exists.
func (b blobStore) place(path, r string) error {
	dst, err := b.path(r)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a user folder
		return err
	}
	if err := os.Rename(path, dst); err != nil {
		// Across file systems: copy instead.
		return copyFile(path, dst)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // our own temporary file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp) //nolint:gosec // inside the blob store
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// fileHash returns a file's hash, from the catalog's hash cache when the file's size and modification time are the
// ones it was hashed at; otherwise it hashes the file (streaming) and caches the result.
func (d *db) fileHash(ctx context.Context, path string) (hash string, size, mtime int64, err error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, 0, fmt.Errorf("%s is not a regular file", path)
	}
	size, mtime = st.Size(), st.ModTime().UnixNano()
	var h string
	err = d.rw.QueryRowContext(ctx, `SELECT hash FROM hash_cache WHERE path = ? AND size = ? AND mtime = ?`,
		path, size, mtime).Scan(&h)
	if err == nil {
		return h, size, mtime, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, 0, err
	}
	h, _, err = HashFile(path)
	if err != nil {
		return "", 0, 0, err
	}
	err = d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO hash_cache (path, size, mtime, hash) VALUES (?, ?, ?, ?)
			ON CONFLICT (path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, hash = excluded.hash`,
			path, size, mtime, h)
		return err
	})
	return h, size, mtime, err
}
