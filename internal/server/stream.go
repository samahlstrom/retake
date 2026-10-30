package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/stream"
)

// Streamed versions render on demand (spec/bridge.md, "Streaming"): a page watches the one it shows, and the
// server renders it while one does.

// streamsFor returns the stream manager: Options.Streams, else one of its own made now.
func (s *Server) streamsFor() *stream.Manager {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams == nil {
		if s.opts.Streams != nil {
			s.streams = s.opts.Streams
		} else {
			s.streams, s.ownStreams = stream.New(stream.Options{Editor: s.opts.Editor, Log: s.opts.Log}), true
		}
	}
	return s.streams
}

type watchResponse struct {
	Label  string             `json:"label"`
	Stream family.StreamState `json:"stream"`
}

// watch is POST /api/versions/<label>/watch?viewer=<id> (on) and .../unwatch (off).
func (s *Server) watch(w http.ResponseWriter, r *http.Request, label string, on bool) {
	v, ok := s.version(label)
	if !ok {
		s.Rescan() // a version pushed since the last scan
		if v, ok = s.version(label); !ok {
			s.fail(w, r, http.StatusNotFound, "no such version")
			return
		}
	}
	viewer := strings.TrimSpace(query(r, "viewer"))
	if viewer == "" || len(viewer) > 100 {
		s.fail(w, r, http.StatusBadRequest, "bad viewer")
		return
	}
	if v.Stream == nil {
		s.fail(w, r, http.StatusBadRequest, "not a streamed version")
		return
	}
	m := s.streamsFor()
	var st family.StreamState
	var err error
	if on {
		st, err = m.Watch(stream.Target{Label: v.Label, Playlist: v.Path, Root: s.root, Emit: s.emitVersion}, viewer)
	} else {
		m.Unwatch(v.Path, viewer)
		st, err = m.State(v.Path)
	}
	switch {
	case errors.Is(err, stream.ErrNotStreamed):
		s.fail(w, r, http.StatusBadRequest, "not a streamed version")
	case err != nil:
		s.fail(w, r, http.StatusInternalServerError, err.Error())
	default:
		s.json(w, r, http.StatusOK, watchResponse{Label: v.Label, Stream: st})
	}
}

// emitVersion sends a streamed version's event to the pages, through the project store's event log.
func (s *Server) emitVersion(ev project.VersionEvent) {
	st, err := s.store(true)
	if err == nil {
		err = st.EmitVersion(ev)
	}
	if err != nil {
		_, _ = fmt.Fprintf(s.opts.Log, "retake: the %s event of %s: %v\n", ev.Why, ev.Label, err)
	}
}
