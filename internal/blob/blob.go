// Package blob is where `retake serve` keeps a project's media (spec/server.md, "Media"): the versions, take audio
// and images, the takes' JSON, transcripts and notes outputs. Two drivers implement Store: Local (files under a
// folder, the default: the workspace is the only copy) and S3 (any S3-compatible service, through minio-go). Serve
// sends a stored object to the page with the single-range rules of spec/api.md.
package blob

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned for a key that has no object.
var ErrNotFound = errors.New("blob: not found")

// Info describes one stored object.
type Info struct {
	Key      string
	Size     int64
	Modified time.Time
}

// Store keeps objects by key. A key is a slash path (ValidKey), such as projects/demo/media/demo_v1.mp4.
type Store interface {
	// Name is the driver's name: "local" or "s3".
	Name() string
	// Put stores size bytes from r at key, replacing what was there. It is all or nothing: a failed Put leaves the
	// old object (or none). A negative size reads r to its end.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// PutFile stores the file at path at key. Local does nothing when path is the key's own file.
	PutFile(ctx context.Context, key, path, contentType string) error
	// Get returns length bytes of the object from offset (length < 0: to the end). A missing key gives
	// ErrNotFound; an offset at or past the end gives an empty reader.
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	// Stat describes the object at key, or returns ErrNotFound.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes the object at key. A missing key is not an error.
	Delete(ctx context.Context, key string) error
	// List returns every object whose key starts with prefix, in every folder below it, sorted by key.
	List(ctx context.Context, prefix string) ([]Info, error)
}

// ValidKey reports whether key is a usable object key: a non-empty relative slash path whose segments are all
// non-empty and none is "." or "..", with no backslash and no NUL. Both drivers refuse other keys, so a key can't
// reach outside a Local root.
func ValidKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00") {
		return false
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// validPrefix reports whether prefix can start a List: "" (everything), or a key prefix, possibly ending in "/" or
// in the middle of a segment.
func validPrefix(prefix string) bool {
	return prefix == "" || ValidKey(strings.TrimSuffix(prefix, "/"))
}

// badKey is the error for a key ValidKey refuses (a NUL in it is shown escaped).
type badKey string

func (k badKey) Error() string { return "blob: bad key " + strconv.Quote(string(k)) }

func checkKey(key string) error {
	if !ValidKey(key) {
		return badKey(key)
	}
	return nil
}

// emptyReader is what Get returns for a range that starts at or past the end of the object.
func emptyReader() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
