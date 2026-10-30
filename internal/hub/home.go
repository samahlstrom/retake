package hub

import (
	"net/http"

	"github.com/samahlstrom/retake/internal/home"
)

// apiHome is GET /api/home on the team server: the home screen's cards for the signed-in person's projects
// (spec/home.md), so the review page's "All videos" and "Next video" work here too.
func (h *Hub) apiHome(w http.ResponseWriter, r *http.Request, s *session) {
	ps, err := h.st.Accounts.ProjectsOf(s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	cards := make([]home.Card, 0, len(ps))
	for _, p := range ps {
		pr, err := h.project(p.Name)
		if err != nil {
			h.logf("project %s: %v", p.Name, err)
			continue
		}
		src := home.Source{Name: p.Name, Title: p.Title, Store: pr.store}
		if srv := pr.server(); srv != nil {
			src.Info = srv.InfoJSON()
		}
		cards = append(cards, home.Make(src))
	}
	home.Sort(cards)
	h.json(w, r, http.StatusOK, home.Response{Title: "Your projects", User: &home.User{Email: s.user.Email,
		Name: s.user.Name}, Projects: cards})
}
