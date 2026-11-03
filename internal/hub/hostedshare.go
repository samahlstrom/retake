package hub

import (
	"context"
	"net/http"
	"strings"

	"github.com/samahlstrom/retake/internal/server"
)

// hostedSharing gives the share links made on the team server its own public address (spec/team.md, "How a link
// reaches people"): https://<host>/p/<name>/s/<token>.
type hostedSharing struct {
	h    *Hub
	base string // the project's path, /p/<name>
}

func (s hostedSharing) Reach(r *http.Request) server.Reach {
	ri := s.h.inspect(r)
	_, host, _ := strings.Cut(ri.origin, "://")
	return server.Reach{Kind: "hosted", Host: host, Base: ri.origin + s.base + "/"}
}

func (s hostedSharing) Ready(_ context.Context, r *http.Request) (server.Reach, error) {
	return s.Reach(r), nil
}
