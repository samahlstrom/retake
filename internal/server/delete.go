package server

import (
	"errors"
	"net/http"

	"github.com/samahlstrom/retake/internal/project"
)

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request, text string, restore bool) {
	id, ok := noteID(text)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such note")
		return
	}
	st, err := s.store(false)
	if errors.Is(err, project.ErrNoProject) {
		s.fail(w, r, http.StatusNotFound, "no such note")
		return
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	if restore {
		err = st.RestoreNote(id)
	} else {
		err = st.DeleteNote(id)
	}
	if err != nil {
		s.transitionFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
}
