package server

import (
	"net/http"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// authorOf is the name of the person behind r: a signed-in member's or a guest's, a share link's label, else the
// server's own.
func (s *Server) authorOf(r *http.Request) string {
	a := accessOf(r)
	switch {
	case a.author != "":
		return a.author
	case a.token != nil && a.token.Label != "":
		return a.token.Label
	}
	return s.opts.Author
}

// attachAsks puts each task's open question under its comment (spec/notes.md, "A task's question"): a note in
// question whose newest question its task's agent asked (retake ask) gets it as ask, its time mapped onto v when both
// versions have render manifests, and that question's own item leaves the list.
func (s *Server) attachAsks(st *project.Store, v family.Version, items []project.Item, a project.AutomationState) []project.Item {
	asked := map[int64]bool{}
	for i := range items {
		it := &items[i]
		if it.Kind != "note" || it.Status != project.StatusQuestion || it.Work == nil || len(it.Work.Asked) == 0 {
			continue
		}
		j := a.Task(it.Work.ID)
		if j == nil {
			continue
		}
		qs, err := st.TaskQuestions(*j, s.questionURL)
		if err != nil {
			continue
		}
		var open *project.TaskQuestion
		for k := range qs {
			if qs[k].Note == it.ID && qs[k].Answer == nil {
				open = &qs[k] // oldest first: the newest unanswered one wins
			}
		}
		if open == nil {
			continue
		}
		ask := &project.ItemAsk{ID: open.ID, Task: j.ID, T: open.At, From: open.From, To: open.To, Version: open.Version,
			Text: open.Text, Visuals: open.Visuals}
		if open.Version != v.Label {
			if origin, ok := s.version(open.Version); ok {
				if from, to := s.manifestOf(origin), s.manifestOf(v); from != nil && to != nil {
					tm := bridge.BuildTimeMap(origin.Label, v.Label, from, to)
					ask.T = tm.Map(ask.T)
					if ask.From != nil && ask.To != nil {
						a, b := tm.Map(*ask.From), tm.Map(*ask.To)
						ask.From, ask.To = &a, &b
					}
				}
			}
		}
		it.Ask = ask
		it.Question = open.Text
		asked[open.ID] = true
	}
	return slices.DeleteFunc(items, func(it project.Item) bool { return it.Kind == "question" && asked[it.ID] })
}

// questionURL is where the page loads a file of question id's visuals.
func (s *Server) questionURL(id int64, file string) string {
	return s.opts.BasePath + "/api/questions/" + strconv.FormatInt(id, 10) + "/" + file
}

// questionFile is GET /api/questions/<id>/<file>: an image of a task question's visuals (spec/notes.md).
func (s *Server) questionFile(w http.ResponseWriter, r *http.Request, id, name string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || !project.QuestionFile(name) {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	st, err := s.store(false)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	s.serveFile(w, r, filepath.Join(st.QuestionDir(n), name), project.ImageTypes[filepath.Ext(name)])
}

// frameSize is a version's frame size, for a question's pixel coordinates.
func (s *Server) frameSize(label string) (w, h int, ok bool) {
	v, found := s.version(label)
	if !found || label == "" || v.Width == nil || v.Height == nil || *v.Width <= 0 || *v.Height <= 0 {
		return 0, 0, false
	}
	return *v.Width, *v.Height, true
}
