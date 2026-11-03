package hub

import (
	"net/http"
	"testing"

	"github.com/samahlstrom/retake/internal/team"
)

func TestFeedbackAccess(t *testing.T) {
	t.Setenv("RETAKE_FEEDBACK_TOKEN", "")
	const body = `{"type":"bug","title":"Broken control","description":"The button does not respond."}`
	t.Run("folder home and review retain share permissions and origin checks", func(t *testing.T) {
		x := newFT(t, nil)
		for _, path := range []string{"/api/feedback", "/p/alpha/api/feedback"} {
			if r := x.get(path); r.code != http.StatusOK {
				t.Errorf("owner GET %s: %d %s", path, r.code, r.body)
			}
			if r := x.do("POST", path, body, shared(), origin("https://share.test")); r.code != http.StatusUnauthorized {
				t.Errorf("anonymous POST %s: %d", path, r.code)
			}
			for _, role := range []team.Role{team.Viewer, team.Reviewer, team.Approver} {
				tok := homeToken(t, x.dir, role, "feedback test")
				auth := cookie(ShareCookie, tok)
				if r := x.do("POST", path, body, shared(), origin("https://share.test"), auth); r.code != http.StatusServiceUnavailable {
					t.Errorf("%s POST %s: %d %s; want handler's 503", role, path, r.code, r.body)
				}
				if r := x.do("POST", path, body, shared(), origin("https://other.test"), auth); r.code != http.StatusForbidden {
					t.Errorf("cross-origin %s POST %s: %d", role, path, r.code)
				}
			}
		}
	})
	t.Run("hosted home requires a session but does not require admin", func(t *testing.T) {
		x := newTH(t, Config{}, nil)
		if r := x.post("/api/feedback", body); r.code != http.StatusUnauthorized {
			t.Errorf("anonymous: %d", r.code)
		}
		u := x.user("feedback@example.test", false)
		auth := cookie(SessionCookie, x.session(u))
		if r := x.do("GET", "/api/feedback", "", auth); r.code != http.StatusOK {
			t.Errorf("member GET: %d %s", r.code, r.body)
		}
		if r := x.post("/api/feedback", body, auth); r.code != http.StatusServiceUnavailable {
			t.Errorf("member POST: %d %s; want handler's 503", r.code, r.body)
		}
		if r := x.post("/api/feedback", body, auth, origin("https://other.test")); r.code != http.StatusForbidden {
			t.Errorf("cross-origin: %d", r.code)
		}
	})
}
