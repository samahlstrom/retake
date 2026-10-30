package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/project"
)

// Media in object storage (spec/server.md, "Media"): every file of a project's workspace except .retake/ is also
// stored at projects/<name>/<path>. The workspace is a cache the server writes first; the publisher uploads what
// changed, and downloads what the workspace lacks when the project opens.

// stamp is what the publisher knows of a file it uploaded: its size and modification time.
type stamp struct {
	size  int64
	mtime time.Time
}

type publisher struct {
	blobs  blob.Store
	prefix string // projects/<name>/
	root   string
	log    io.Writer

	mu    sync.Mutex // one sync at a time; guards known
	known map[string]stamp
	kicks chan struct{}
}

func newPublisher(b blob.Store, name, root string, log io.Writer) *publisher {
	return &publisher{blobs: b, prefix: "projects/" + name + "/", root: root, log: log, known: map[string]stamp{},
		kicks: make(chan struct{}, 1)}
}

// Key returns the object key of a workspace file (its slash path inside the workspace).
func (pb *publisher) key(rel string) string { return pb.prefix + rel }

// skip reports whether a workspace path stays out of object storage: the store and locks (.retake/), and every
// other hidden file or folder (temporary files of atomic writes).
func skip(rel string) bool {
	for part := range strings.SplitSeq(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// hydrate downloads the project's objects that the workspace lacks (or has at another size), and records what the
// bucket holds.
func (pb *publisher) hydrate(ctx context.Context) error {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	objs, err := pb.blobs.List(ctx, pb.prefix)
	if err != nil {
		return err
	}
	for _, o := range objs {
		rel := strings.TrimPrefix(o.Key, pb.prefix)
		if rel == "" || skip(rel) || !filepath.IsLocal(filepath.FromSlash(rel)) {
			continue
		}
		path := filepath.Join(pb.root, filepath.FromSlash(rel))
		st, err := os.Stat(path)
		if err != nil || st.Size() != o.Size {
			if err := pb.download(ctx, o.Key, path); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if st, err = os.Stat(path); err != nil {
				return err
			}
		}
		pb.known[rel] = stamp{st.Size(), st.ModTime()}
	}
	return nil
}

func (pb *publisher) download(ctx context.Context, key, path string) error {
	rc, err := pb.blobs.Get(ctx, key, 0, -1)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".retake-tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640) //nolint:gosec // inside the workspace
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// sync uploads every workspace file whose size or modification time changed since it was last uploaded.
func (pb *publisher) sync(ctx context.Context) error {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	var errs []error
	err := filepath.WalkDir(pb.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // a file that went away mid-walk
			}
			return err
		}
		rel, err := filepath.Rel(pb.root, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // gone since the listing: the next sync sees what's there
		}
		now := stamp{st.Size(), st.ModTime()}
		if k, ok := pb.known[rel]; ok && k.size == now.size && k.mtime.Equal(now.mtime) {
			return nil
		}
		if err := pb.blobs.PutFile(ctx, pb.key(rel), path, mediaType(rel)); err != nil {
			errs = append(errs, fmt.Errorf("uploading %s: %w", rel, err))
			return nil
		}
		pb.known[rel] = now
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// kick asks for a sync soon.
func (pb *publisher) kick() {
	select {
	case pb.kicks <- struct{}{}:
	default:
	}
}

// run syncs after kicks, after the store's events (pushes, finished jobs), and every `every`, until ctx is done.
func (pb *publisher) run(ctx context.Context, st *project.Store, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pb.kicks:
		case <-st.Changed():
		case <-tick.C:
		}
		// Let a burst of writes settle (a take's /data and Done come close together).
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
		if err := pb.sync(ctx); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(pb.log, "retake: object storage: %v\n", err)
		}
	}
}

// serve is the mounted Server's Media hook: it proxies a version file or take image from object storage, with
// ranges. A file that isn't in the bucket yet is left to the Server, which serves it from the workspace.
func (pb *publisher) serve(w http.ResponseWriter, r *http.Request, path, contentType string) bool {
	rel, err := filepath.Rel(pb.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	rel = filepath.ToSlash(rel)
	if skip(rel) {
		return false
	}
	err = blob.Serve(w, r, pb.blobs, pb.key(rel), contentType)
	if errors.Is(err, blob.ErrNotFound) {
		return false
	}
	if err != nil {
		_, _ = fmt.Fprintf(pb.log, "retake: serving %s from object storage: %v\n", rel, err)
		return false
	}
	return true
}

// mediaType is the content type an object is stored with.
func mediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v", ".m4s":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ogg":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown; charset=utf-8"
	}
	return "application/octet-stream"
}
