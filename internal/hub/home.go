package hub

import (
	"net/http"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
)

// apiHome is GET /api/home on the team server: the home screen's cards for the signed-in person's projects
// (spec/home.md), so the review page's "All videos" and "Next video" work here too, and the computers that edit them.
func (h *Hub) apiHome(w http.ResponseWriter, r *http.Request, s *session) {
	ps, err := h.st.Accounts.ProjectsOf(s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	cards := make([]home.Card, 0, len(ps))
	var workers [][]project.EditingWorker
	for _, p := range ps {
		pr, err := h.project(p.Name)
		if err != nil {
			h.logf("project %s: %v", p.Name, err)
			continue
		}
		src := home.Source{Name: p.Name, Title: p.Title, Path: p.Path, Project: p.ProjectPath, Store: pr.store}
		if srv := pr.server(); srv != nil {
			src.Info = srv.InfoJSON()
		}
		c := home.Make(src)
		if c.Thumb == "" && pr.hasPoster() {
			c.Thumb = "p/" + p.Name + "/api/poster" // the poster its computer sent, until it has a version
		}
		cards = append(cards, c)
		if a, err := pr.store.Automation(); err == nil {
			workers = append(workers, a.Workers)
		}
	}
	home.Sort(cards)
	role := "member"
	if s.user.Admin {
		role = "owner"
	}
	const title = "Your projects"
	h.json(w, r, http.StatusOK, home.Response{Title: title, Role: role, User: &home.User{Email: s.user.Email,
		Name: s.user.Name}, Projects: cards, Tree: home.BuildTree(title, cards, nil),
		Machines: home.Machines(h.opts.Now(), workers...)})
}
