// Package relay carries media requests over worker-initiated HTTP connections (spec/automation.md, "The media
// relay"). Media is retained on the worker; the host holds only in-flight responses, and, with a Cache, the chunks it
// already relayed.
package relay

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

const (
	// Chunk is the most one relayed response carries, and the unit the host asks for and caches: chunk k is bytes
	// k*Chunk to (k+1)*Chunk-1 of a file.
	Chunk = 8 << 20
	// Wait is how long the host waits for a worker to start answering a request.
	Wait = 30 * time.Second
	// Pickup is how long a request may wait for a worker to take it: a worker that polls takes it at once, unless all
	// of its media loops are busy, so one that doesn't has gone away.
	Pickup = 10 * time.Second
	// Hold is how long a worker's answer may take to stream through to the browser.
	Hold = 5 * time.Minute
)

// ValidFile accepts only flat media names, never source, manifests or arbitrary files.
func ValidFile(name string) bool {
	if len(name) == 0 || len(name) > 180 || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	for _, c := range name {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
		if !valid {
			return false
		}
	}
	switch filepath.Ext(name) {
	case ".m3u8", ".m4s", ".mp4", ".ts":
		return true
	}
	return false
}

// Request asks a worker for part of one file: a render's media file (Render), or a version file of its project
// (Version, with File "" or its own name, or a file beside its playlist).
type Request struct {
	ID      string `json:"id"`
	Render  string `json:"render,omitempty"`
	Version string `json:"version,omitempty"`
	File    string `json:"file"`
	Range   string `json:"range,omitempty"`
	Head    bool   `json:"head,omitempty"`
}

// Response is a worker's answer. Body streams the worker's upload as it arrives: whoever got it from Fetch reads it
// and then calls Close, which lets the worker's request end.
type Response struct {
	Status      int
	Type, Range string
	Length      int64 // the Content-Length the browser is told
	Modified    int64 // a version file's mtime (Unix ms); 0 for a render's media
	Body        io.Reader

	done chan struct{}
	once *sync.Once
}

// Close says the body has been read (or never will be). It is safe to call more than once.
func (r Response) Close() {
	if r.once != nil {
		r.once.Do(func() { close(r.done) })
	}
}

type pending struct {
	worker    string
	request   Request
	delivered bool
	reply     chan Response
}

// Hub matches reviewer requests with outbound worker polls.
type Hub struct {
	// Pickup overrides the package's Pickup (tests); 0 = Pickup.
	Pickup time.Duration

	mu      sync.Mutex
	pending map[string]*pending
	bell    chan struct{}
}

func (h *Hub) init() {
	if h.pending == nil {
		h.pending = map[string]*pending{}
		h.bell = make(chan struct{})
	}
}

// Fetch's errors: too many requests wait for workers, or no worker took this one (within Pickup).
var (
	ErrBusy     = errors.New("media relay busy")
	ErrNoWorker = errors.New("no worker took the media request")
)

// Fetch waits for a worker's answer, releasing its queue slot on cancellation, when no worker takes it within
// Pickup, or after Wait. The caller must Close the response.
func (h *Hub) Fetch(ctx context.Context, worker string, r Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, Wait)
	defer cancel()
	h.mu.Lock()
	h.init()
	if len(h.pending) >= 64 {
		h.mu.Unlock()
		return Response{}, ErrBusy
	}
	r.ID = project.NewWorkID()
	p := &pending{worker: worker, request: r, reply: make(chan Response, 1)}
	h.pending[r.ID] = p
	close(h.bell)
	h.bell = make(chan struct{})
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.pending, r.ID)
		h.mu.Unlock()
		// An answer that came in as this gave up: nobody reads it, so let the worker's request end now.
		select {
		case v := <-p.reply:
			v.Close()
		default:
		}
	}()
	wait := h.Pickup
	if wait <= 0 {
		wait = Pickup
	}
	pickup := time.NewTimer(wait)
	defer pickup.Stop()
	for {
		select {
		case v := <-p.reply:
			return v, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-pickup.C:
			h.mu.Lock()
			taken := p.delivered
			h.mu.Unlock()
			if !taken {
				return Response{}, ErrNoWorker
			}
		}
	}
}

// Next waits for one media read belonging to this worker. Empty responses are keepalives.
func (h *Hub) Next(ctx context.Context, worker string) (Request, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		h.mu.Lock()
		h.init()
		for _, p := range h.pending {
			if p.worker == worker && !p.delivered {
				p.delivered = true
				r := p.request
				h.mu.Unlock()
				return r, nil
			}
		}
		bell := h.bell
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return Request{}, ctx.Err()
		case <-bell:
		}
	}
}

// Reply hands a worker's answer to the request waiting for it. It never accepts an answer from another worker, or
// after the browser stopped waiting. done closes once the reader is finished with r.Body: the worker's request
// must stay open (and r.Body readable) until then.
func (h *Hub) Reply(worker, id string, r Response) (done <-chan struct{}, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pending[id]
	if p == nil || p.worker != worker || !p.delivered {
		return nil, false
	}
	if r.Body == nil {
		r.Body = strings.NewReader("")
	}
	r.done, r.once = make(chan struct{}), &sync.Once{}
	select {
	case p.reply <- r:
		return r.done, true
	default:
		return nil, false
	}
}
