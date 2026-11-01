package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// Relayed media (spec/automation.md, "The media relay"): a version that plays from a connected computer, a worker's
// render or one of its version files. The server always asks the worker for whole aligned chunks, cuts the browser's
// range out of them, and keeps the ones that won't change in its cache (Options.MediaCache).

// mediaTarget is one relayed file: who has it, what to ask for, and how its chunks are cached.
type mediaTarget struct {
	worker string
	req    relay.Request
	ctype  string
	cache  string // Cache-Control for the browser
	key    string // the cache key of its chunks, without the chunk's number; "" = never cached
	// A version file's size and mtime (Unix ms), as the worker reported them: a chunk is cached only when the
	// worker's answer has them too, so a file changed meanwhile isn't cached under the old one's key.
	size, modified int64
}

var (
	errOffline  = errors.New("editing machine is offline")
	errNoAnswer = errors.New("editing machine did not answer the media request")
	errMissing  = errors.New("no such media file")
)

// errRange is a range that starts past the end of a file of size bytes.
type errRange struct{ size int64 }

func (e errRange) Error() string { return "range not satisfiable" }

// remoteMedia serves a relayed version's file: /video/<label> (name "") or /video/<label>/<name>. It reports false
// when the version isn't relayed (it is on this server's disk).
func (s *Server) remoteMedia(w http.ResponseWriter, r *http.Request, v family.Version, name string) bool {
	if !v.Relay {
		return false
	}
	st, err := s.store(false)
	if err != nil {
		s.storeFailed(w, r, err)
		return true
	}
	a, err := st.Automation()
	if err != nil {
		s.storeFailed(w, r, err)
		return true
	}
	t, ok := relayTarget(&a, v.Label, name)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such media file")
		return true
	}
	s.relayMedia(w, r, t)
	return true
}

// relayTarget is the file a relayed version's URL names, if that may be read: a render's flat media file, or a
// version file itself, or a media file beside its playlist.
func relayTarget(a *project.AutomationState, label, name string) (mediaTarget, bool) {
	if v := a.Render(label); v != nil {
		if name == "" {
			name = "index.m3u8"
		}
		if !relay.ValidFile(name) {
			return mediaTarget{}, false
		}
		t := mediaTarget{worker: v.Worker, req: relay.Request{Render: v.ID, File: name}, ctype: mediaType(name),
			cache: "private, max-age=31536000, immutable", key: "render/" + v.ID + "/" + name}
		if family.IsPlaylist(name) {
			t.cache = "no-store"
			if !v.Complete {
				t.key = "" // it is still growing
			}
		}
		return t, true
	}
	fv := a.FileVersion(label)
	if fv == nil {
		return mediaTarget{}, false
	}
	identity := fmt.Sprintf("file/%s/%s/%d/%d/", fv.Worker, fv.Label, fv.Size, fv.Modified)
	t := mediaTarget{worker: fv.Worker, req: relay.Request{Version: fv.Label}, cache: "private, no-cache"}
	switch {
	case name == "" || name == fv.Name:
		t.req.File, t.ctype, t.size, t.modified = fv.Name, mediaType(fv.Name), fv.Size, fv.Modified
		t.key = identity + fv.Name
	case family.IsPlaylist(fv.Name) && relay.ValidFile(name):
		t.req.File, t.ctype = name, mediaType(name)
		t.key = identity + "beside/" + name
	default:
		return mediaTarget{}, false
	}
	if family.IsPlaylist(t.req.File) {
		t.key = "" // a playlist is small, and may be growing
	}
	return t, true
}

// relayMedia answers the browser from aligned chunks: a range gets at most the rest of the chunk it starts in (206),
// no range gets the whole file (200), chunk after chunk.
func (s *Server) relayMedia(w http.ResponseWriter, r *http.Request, t mediaTarget) {
	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", t.cache)
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		reply, err := s.ask(r.Context(), t, r.Header.Get("Range"), true)
		if err != nil {
			s.relayFailed(w, r, err)
			return
		}
		defer reply.Close()
		if reply.Status != http.StatusOK && reply.Status != http.StatusPartialContent {
			s.relayFailed(w, r, errMissing)
			return
		}
		h.Set("Content-Type", t.ctype)
		if reply.Range != "" {
			h.Set("Content-Range", reply.Range)
		}
		h.Set("Content-Length", strconv.FormatInt(reply.Length, 10))
		w.WriteHeader(reply.Status)
		return
	}
	ctx := r.Context()
	want, ranged := relay.ParseRange(r.Header.Get("Range"))
	k := int64(0)
	if ranged && !want.Suffix {
		k = want.Start / relay.Chunk
	}
	c, err := s.openChunk(ctx, t, k)
	if err != nil {
		s.relayFailed(w, r, err)
		return
	}
	total, start, end, status := c.Total, int64(0), c.Total-1, http.StatusOK
	if ranged {
		var ok bool
		if start, end, ok = want.Resolve(total); !ok {
			c.close()
			s.relayFailed(w, r, errRange{total})
			return
		}
		if start/relay.Chunk != k { // a suffix range: now that the size is known
			c.close()
			if c, err = s.openChunk(ctx, t, start/relay.Chunk); err != nil {
				s.relayFailed(w, r, err)
				return
			}
		}
		end = min(end, c.Start+c.Length-1)
		status = http.StatusPartialContent
		h.Set("Content-Range", relay.ContentRange(start, end, total))
	}
	h.Set("Content-Type", t.ctype)
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.WriteHeader(status)
	for pos := start; pos <= end; {
		if c == nil {
			if c, err = s.openChunk(ctx, t, pos/relay.Chunk); err != nil {
				return // the worker went away mid-file: the browser sees it cut short
			}
			if c.Total != total {
				c.close()
				return // the file changed meanwhile
			}
		}
		last := min(end, c.Start+c.Length-1)
		err = c.copyTo(w, pos, last)
		c.close()
		c = nil
		if err != nil {
			return // the browser left (it does all the time while seeking)
		}
		pos = last + 1
	}
	_ = http.NewResponseController(w).Flush()
}

// relayFailed answers a relayed request that couldn't be served.
func (s *Server) relayFailed(w http.ResponseWriter, r *http.Request, err error) {
	var rng errRange
	switch {
	case errors.Is(err, errOffline):
		w.Header().Set("Retry-After", "3")
		w.Header().Set("Cache-Control", "no-store")
		s.fail(w, r, http.StatusServiceUnavailable, "editing machine is offline")
	case errors.As(err, &rng):
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", rng.size))
		w.Header().Set("Cache-Control", "no-store")
		s.fail(w, r, http.StatusRequestedRangeNotSatisfiable, "range not satisfiable")
	case errors.Is(err, errMissing):
		w.Header().Set("Cache-Control", "no-store")
		s.fail(w, r, http.StatusNotFound, "no such media file")
	default:
		w.Header().Set("Cache-Control", "no-store")
		s.fail(w, r, http.StatusServiceUnavailable, errNoAnswer.Error())
	}
}

// ask sends one request to the target's worker, when it is online.
func (s *Server) ask(ctx context.Context, t mediaTarget, rng string, head bool) (relay.Response, error) {
	st, err := s.store(false)
	if err != nil {
		return relay.Response{}, err
	}
	a, err := st.Automation()
	if err != nil {
		return relay.Response{}, err
	}
	if m := a.Worker(t.worker); m == nil || m.Seen+10 < st.Now().Unix() {
		return relay.Response{}, errOffline
	}
	req := t.req
	req.Range, req.Head = rng, head
	reply, err := s.mediaRelay.Fetch(ctx, t.worker, req)
	if err != nil {
		return relay.Response{}, fmt.Errorf("%w: %v", errNoAnswer, err)
	}
	return reply, nil
}

// chunk is one aligned chunk of a relayed file, being read: from the cache, or from the worker.
type chunk struct {
	relay.Meta
	body  io.Reader // from the chunk's first byte
	close func()
}

// copyTo writes bytes from..to (of the file) to w.
func (c *chunk) copyTo(w io.Writer, from, to int64) error {
	if skip := from - c.Start; skip > 0 {
		if sk, ok := c.body.(io.Seeker); ok {
			if _, err := sk.Seek(skip, io.SeekStart); err != nil {
				return err
			}
		} else if _, err := io.CopyN(io.Discard, c.body, skip); err != nil {
			return err
		}
	}
	_, err := io.CopyN(w, c.body, to-from+1)
	return err
}

// openChunk opens chunk k of the target: from the cache, else from its worker. Of concurrent misses of one chunk,
// one asks the worker and the others wait for it to be cached.
func (s *Server) openChunk(ctx context.Context, t mediaTarget, k int64) (*chunk, error) {
	cache, scope, key := s.opts.MediaCache, s.opts.CacheScope, t.key+"#"+strconv.FormatInt(k, 10)
	if t.key == "" {
		cache = nil
	}
	for range 3 {
		if hit, ok := cache.Get(scope, key); ok {
			return &chunk{Meta: hit.Meta, body: hit.Body(), close: hit.Close}, nil
		}
		fill, wait := cache.Begin(scope, key)
		if wait == nil {
			return s.fetchChunk(ctx, t, k, fill)
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.fetchChunk(ctx, t, k, nil) // the fetches it waited for kept failing: ask for itself
}

// fetchChunk asks the worker for chunk k. With fill, the chunk's bytes also go to the cache as they're read, and
// closing it reads the rest into the cache (in the background) before letting the worker's request end.
func (s *Server) fetchChunk(ctx context.Context, t mediaTarget, k int64, fill *relay.Fill) (*chunk, error) {
	abort := func() {
		if fill != nil {
			fill.Abort()
		}
	}
	reply, err := s.ask(ctx, t, relay.ChunkRange(k), false)
	if err != nil {
		abort()
		return nil, err
	}
	m, err := chunkMeta(reply, k)
	if err != nil {
		reply.Close()
		abort()
		return nil, err
	}
	m.Type = t.ctype
	if fill != nil && t.size > 0 && (m.Total != t.size || reply.Modified != t.modified) {
		fill.Abort() // not the file the key names: serve it, keep nothing
		fill = nil
	}
	body := io.LimitReader(reply.Body, m.Length)
	if fill == nil {
		return &chunk{Meta: m, body: body, close: reply.Close}, nil
	}
	fill.Open(m)
	tee := io.TeeReader(body, fill) // a Fill's writes never fail
	return &chunk{Meta: m, body: tee, close: func() {
		go func() {
			_, _ = io.Copy(io.Discard, tee) // the browser's part is sent; the rest of the chunk goes to the cache
			fill.Commit()
			reply.Close()
		}()
	}}, nil
}

// chunkMeta checks that a worker's answer is chunk k of its file, and describes it.
func chunkMeta(reply relay.Response, k int64) (relay.Meta, error) {
	switch reply.Status {
	case http.StatusPartialContent:
		a, b, size, ok := relay.ParseContentRange(reply.Range)
		if !ok || a != k*relay.Chunk || b != min((k+1)*relay.Chunk, size)-1 || reply.Length != b-a+1 {
			return relay.Meta{}, errNoAnswer
		}
		return relay.Meta{Start: a, Length: b - a + 1, Total: size}, nil
	case http.StatusOK:
		if k != 0 || reply.Length > relay.Chunk {
			return relay.Meta{}, errNoAnswer
		}
		return relay.Meta{Length: reply.Length, Total: reply.Length}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		var size int64
		if _, err := fmt.Sscanf(strings.TrimSpace(reply.Range), "bytes */%d", &size); err != nil {
			return relay.Meta{}, errNoAnswer
		}
		return relay.Meta{}, errRange{size}
	case http.StatusNotFound:
		return relay.Meta{}, errMissing
	}
	return relay.Meta{}, errNoAnswer
}
