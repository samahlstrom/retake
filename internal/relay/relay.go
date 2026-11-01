// Package relay carries bounded media requests over worker-initiated HTTP connections.
// Media is retained on the worker; the host holds only in-flight responses.
package relay

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// MaxBody bounds a single HLS segment, not the full video.
const MaxBody = 64 << 20

// ValidFile accepts only flat generated HLS media names, never source, manifests or arbitrary files.
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

// Request describes a read of one immutable render's flat media directory.
type Request struct {
	ID     string `json:"id"`
	Render string `json:"render"`
	File   string `json:"file"`
	Range  string `json:"range,omitempty"`
	Head   bool   `json:"head,omitempty"`
}

// Response contains a worker's media response. Headers are explicitly allowlisted by the caller.
type Response struct {
	Status      int
	Type, Range string
	Body        []byte
}

type pending struct {
	worker    string
	request   Request
	delivered bool
	reply     chan Response
}

// Hub matches reviewer requests with outbound worker polls.
type Hub struct {
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

// Fetch waits for a response, releasing its queue slot on cancellation or timeout.
func (h *Hub) Fetch(ctx context.Context, worker string, r Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	h.mu.Lock()
	h.init()
	if len(h.pending) >= 64 {
		h.mu.Unlock()
		return Response{}, errors.New("media relay busy")
	}
	r.ID = project.NewWorkID()
	p := &pending{worker: worker, request: r, reply: make(chan Response, 1)}
	h.pending[r.ID] = p
	close(h.bell)
	h.bell = make(chan struct{})
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.pending, r.ID); h.mu.Unlock() }()
	select {
	case v := <-p.reply:
		return v, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
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

// Reply never accepts responses from another worker, or after the browser stopped waiting.
func (h *Hub) Reply(worker, id string, r Response) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pending[id]
	if p == nil || p.worker != worker || !p.delivered {
		return false
	}
	select {
	case p.reply <- r:
		return true
	default:
		return false
	}
}
