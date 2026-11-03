package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// Editing a comment (spec/notes.md, "Editing a comment"): POST /api/notes/<id>/edit {"text"} changes a comment's
// words while it is still to do, for its author, approvers and the owner.

// mayEdit reports whether r may change the words of a comment by author: an approver or the owner, or anyone who
// records under that same name.
func (s *Server) mayEdit(r *http.Request, author string) bool {
	role := accessOf(r).role
	if role.Can(team.PermApprove) {
		return true
	}
	return role.Can(team.PermRecord) && author != "" && author == s.authorOf(r)
}

// editComment is POST /api/notes/<id>/edit.
func (s *Server) editComment(w http.ResponseWriter, r *http.Request, idText string, body []byte) {
	id, ok := noteID(idText)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such note")
		return
	}
	var b struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(body, &b); err != nil || b.Text == nil {
		s.fail(w, r, http.StatusBadRequest, `the body is {"text": "the new words"}`)
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
	n, err := st.Note(id)
	if err != nil {
		s.transitionFailed(w, r, err)
		return
	}
	if !s.mayEdit(r, n.Author) {
		s.fail(w, r, http.StatusForbidden, "not allowed")
		return
	}
	pictured := false
	if images, e := st.ImagesOf([]project.Note{n}, s.takeDirOf(s.Family())); e == nil {
		pictured = len(images[n.ID]) > 0
	}
	n, _, err = st.EditNote(id, project.NoteEdit{Text: *b.Text, By: s.authorOf(r), Pictured: pictured})
	switch {
	case errors.Is(err, project.ErrBadEdit):
		s.fail(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, project.ErrNotEditable):
		s.fail(w, r, http.StatusConflict, err.Error())
	case err != nil:
		s.transitionFailed(w, r, err)
	default:
		s.json(w, r, http.StatusOK, map[string]any{"ok": true, "text": n.Text})
	}
}

// markEditable sets each note item's Editable for r (spec/notes.md, "Editing a comment"): open, not the agent's yet,
// and r may edit its author's comments.
func (s *Server) markEditable(r *http.Request, items []project.Item, a project.AutomationState) {
	for i := range items {
		it := &items[i]
		if it.Kind != "note" || it.Status != project.StatusOpen || !s.mayEdit(r, it.By) {
			continue
		}
		it.Editable = project.EditBlock(project.Note{ID: it.ID, Status: it.Status}, false, a) == nil
	}
}
