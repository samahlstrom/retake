package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/samahlstrom/retake/internal/thumb"
)

// poster is GET /api/poster?v=<label>: the version's first frame as a JPEG, for the page's <video poster> and the home
// screen's cards (spec/home.md, "Thumbnails and the poster"). The streaming side's poster beside the file wins; else
// a frame made once with ffmpeg and cached under the project root's .retake/thumbs/.
func (s *Server) poster(w http.ResponseWriter, r *http.Request) {
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	maker := s.opts.Thumbs
	if maker == nil {
		maker = thumb.Default
	}
	p, err := maker.For(r.Context(), s.root, v.Path)
	if err != nil {
		if !errors.Is(err, thumb.ErrNone) && r.Context().Err() == nil {
			_, _ = fmt.Fprintf(s.opts.Log, "retake: poster of %s: %v\n", v.Label, err)
		}
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	f, err := os.Open(p) //nolint:gosec // the version's poster or our own cached frame
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", st.ModTime().Truncate(time.Second), f)
}
