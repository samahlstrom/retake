package server

import (
	"bytes"
	"net/http"

	"github.com/samahlstrom/retake/internal/team"
)

// What the home screen needs of a project's Server (spec/home.md): its /api/info for the card, and the unwatch call
// that tells the streaming side a viewer left.

// unwatch is POST /api/versions/<label>/unwatch?viewer=<id>: the page left that version ("Close means close"). It
// answers 204, and tells the streaming side through Options.Unwatch when there is one.
func (s *Server) unwatch(w http.ResponseWriter, r *http.Request, label string) {
	v, ok := s.version(label)
	if !ok || label == "" {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	if s.opts.Unwatch != nil {
		s.opts.Unwatch(v.Label, query(r, "viewer"))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// isUnwatch reports whether a path is /api/versions/<label>/unwatch.
func isUnwatch(parts []string) bool {
	return len(parts) == 4 && parts[0] == "api" && parts[1] == "versions" && parts[3] == "unwatch"
}

// InfoJSON returns what GET /api/info answers the owner: the current version, the family and its takes. The home
// screen reads it for the project's card, so whatever the streaming side adds to /api/info reaches the card too.
func (s *Server) InfoJSON() []byte {
	r, err := http.NewRequest(http.MethodGet, "/api/info", nil)
	if err != nil {
		return nil
	}
	rec := &recorder{header: http.Header{}}
	s.info(rec, withAccess(r, access{role: team.Owner}))
	if rec.code != 0 && rec.code != http.StatusOK {
		return nil
	}
	return rec.body.Bytes()
}

// Forwarded reports whether a request carries a proxy header, so it can't pass as local (spec/team.md, "Hosts").
func Forwarded(r *http.Request) bool { return forwarded(r) }

// ClientKey is who a request's failed token attempts are counted against (spec/team.md, "Rate limiting").
func ClientKey(r *http.Request) string { return clientKey(r) }

// MediaType is the content type a page file or playlist neighbor is served with.
func MediaType(name string) string { return mediaType(name) }

// recorder is an in-memory http.ResponseWriter.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(b)
}
func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}
