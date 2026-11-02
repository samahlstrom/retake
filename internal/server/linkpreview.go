package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/samahlstrom/retake/internal/linkcard"
)

// linkPreview is GET /api/link-preview?url=<address> (spec/api.md, "Link preview"): what a public web page says
// about itself, for a script's link cards (spec/scripts.md, "Embeds"). It needs watch, like every read.
func (s *Server) linkPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rd := s.opts.Links
	if rd == nil {
		rd = linkcard.Default()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*linkcard.Timeout)
	defer cancel()
	card, err := rd.Card(ctx, r.URL.Query().Get("url"))
	switch {
	case errors.Is(err, linkcard.ErrRefused):
		s.fail(w, r, http.StatusBadRequest, linkcard.ErrRefused.Error())
	case err != nil:
		s.fail(w, r, http.StatusBadGateway, linkcard.ErrUnreadable.Error())
	default:
		s.json(w, r, http.StatusOK, card)
	}
}
