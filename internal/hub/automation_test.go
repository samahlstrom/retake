package hub

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

func TestHostedWorkerBootstrapsProjectWithoutMediaUpload(t *testing.T) {
	x := newTH(t, Config{}, nil)
	x.project("stream", true)
	agent := x.token("stream", team.Agent, "worker")
	viewer := x.token("stream", team.Viewer, "watch")
	if got := x.post("/p/stream/api/worker/poll", `{}`, cookie(ShareCookie, agent)); got.code != http.StatusUnauthorized {
		t.Fatal("worker accepted a cookie", got.code)
	}
	headers := []reqOpt{bearerTok(agent)}
	poll := `{"worker":{"id":"machine","session":"session","agents":[{"id":"codex","name":"Codex"}]}}`
	if got := x.do(http.MethodPost, "/p/stream/api/worker/poll", poll, headers...); got.code != 200 {
		t.Fatal(got.code, got.body)
	}
	headers = append(headers, header("X-Retake-Worker", "machine"), header("X-Retake-Session", "session"))
	render := project.LiveRender{ID: "revision", Worker: "machine", Source: strings.Repeat("a", 64), Width: 320, Height: 180, FPS: 24, Duration: 6, Done: 3, Total: 3, Seconds: 6, Complete: true}
	b, _ := json.Marshal(render)
	if got := x.do(http.MethodPost, "/p/stream/api/worker/render", string(b), headers...); got.code != 200 {
		t.Fatal(got.code, got.body)
	}
	if got := x.do(http.MethodGet, "/p/stream/api/info", "", cookie(ShareCookie, viewer)); got.code != 200 || !strings.Contains(got.body, `"index.m3u8"`) {
		t.Fatal("host did not expose live first version", got.code, got.body)
	}
	// Reconstruct the mounted server from persisted metadata, with no video file on the host.
	p, e := x.h.project("stream")
	if e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	if e = p.srv.Close(); e != nil {
		t.Fatal(e)
	}
	p.srv = nil
	p.mu.Unlock()
	if got := x.do(http.MethodGet, "/p/stream/api/info", "", cookie(ShareCookie, viewer)); got.code != 200 || !strings.Contains(got.body, `"v1"`) {
		t.Fatal("restart lost remote family", got.code, got.body)
	}
}
