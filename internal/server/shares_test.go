package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// fakeSharing gives links a fixed address, or fails as a server with no way out would.
type fakeSharing struct {
	reach Reach
	err   error
	ready int
}

func (f *fakeSharing) Reach(*http.Request) Reach { return f.reach }

func (f *fakeSharing) Ready(context.Context, *http.Request) (Reach, error) {
	f.ready++
	return f.reach, f.err
}

type sharesView struct {
	Links []ShareLink `json:"links"`
	Reach Reach       `json:"reach"`
}

type madeShare struct {
	URL   string    `json:"url"`
	Link  ShareLink `json:"link"`
	Reach Reach     `json:"reach"`
}

func TestSharesMakeListRevoke(t *testing.T) {
	sh := &fakeSharing{reach: Reach{Kind: "tunnel", Host: "words.trycloudflare.com", Base: "https://words.trycloudflare.com/"}}
	h := startTeam(t, Options{Sharing: sh})

	got := h.get("/api/shares", nil)
	var v sharesView
	if got.code != 200 || json.Unmarshal(got.body, &v) != nil || len(v.Links) != 0 || v.Reach.Kind != "tunnel" ||
		v.Reach.Host != "words.trycloudflare.com" {
		t.Fatalf("empty list: %d %s", got.code, got.body)
	}
	if sh.ready != 0 {
		t.Fatal("listing started something")
	}

	got = h.post("/api/shares", `{"label":"  Alex  ","role":"reviewer","expires":"30d"}`, h.origin())
	var m madeShare
	if got.code != http.StatusCreated || json.Unmarshal(got.body, &m) != nil {
		t.Fatalf("make: %d %s", got.code, got.body)
	}
	tok, ok := strings.CutPrefix(m.URL, "https://words.trycloudflare.com/s/")
	if !ok || !team.WellFormed(tok) || m.Link.Label != "Alex" || m.Link.Role != "reviewer" || m.Link.Expires == "" {
		t.Fatalf("made: %+v", m)
	}
	// The token is the video's: it opens the page through the share host, with its name as the author.
	page := h.req(http.MethodGet, "/api/info", "share.test", map[string]string{"Authorization": "Bearer " + tok})
	if page.code != 200 || !strings.Contains(string(page.body), `"role":"reviewer"`) {
		t.Fatalf("the link's token: %d %s", page.code, page.body)
	}

	// Defaults: a reviewer for 7 days; and a viewer that never expires.
	if got = h.post("/api/shares", `{}`, h.origin()); got.code != http.StatusCreated {
		t.Fatalf("defaults: %d %s", got.code, got.body)
	}
	if got = h.post("/api/shares", `{"role":"viewer","expires":"never"}`, h.origin()); got.code != http.StatusCreated {
		t.Fatalf("viewer: %d %s", got.code, got.body)
	}
	// An agent's token isn't a link.
	makeToken(t, h, "agent", "ci", 0)

	got = h.get("/api/shares", nil)
	if json.Unmarshal(got.body, &v) != nil || len(v.Links) != 3 || v.Links[0].Role != "viewer" || v.Links[0].Expires != "" ||
		v.Links[2].Label != "Alex" {
		t.Fatalf("list: %s", got.body)
	}

	if got = h.post("/api/shares/"+itoa(m.Link.ID)+"/revoke", ``, h.origin()); got.code != 200 {
		t.Fatalf("revoke: %d %s", got.code, got.body)
	}
	page = h.req(http.MethodGet, "/api/info", "share.test", map[string]string{"Authorization": "Bearer " + tok})
	if page.code != http.StatusUnauthorized {
		t.Fatalf("a revoked link still works: %d", page.code)
	}
	got = h.get("/api/shares", nil)
	if json.Unmarshal(got.body, &v) != nil || len(v.Links) != 2 {
		t.Fatalf("revoked link still listed: %s", got.body)
	}
	for _, path := range []string{"/api/shares/999/revoke", "/api/shares/x/revoke"} {
		if got = h.post(path, ``, h.origin()); got.code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, got.code)
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestSharesRefuseBadInput(t *testing.T) {
	h := startTeam(t, Options{Sharing: &fakeSharing{reach: Reach{Kind: "hosted", Base: "https://x.test/p/a/"}}})
	for _, body := range []string{`{"role":"approver"}`, `{"role":"agent"}`, `{"expires":"24h"}`,
		`{"label":"` + strings.Repeat("a", 81) + `"}`, `{"label":"a\u0007b"}`, `not json`} {
		if got := h.post("/api/shares", body, h.origin()); got.code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, got.code, got.body)
		}
	}
	if got := h.post("/api/shares", `{}`, map[string]string{"Origin": "https://evil.test"}); got.code != http.StatusForbidden {
		t.Fatalf("another origin: %d", got.code)
	}
}

func TestSharesOwnerOnly(t *testing.T) {
	h := startTeam(t, Options{Sharing: &fakeSharing{reach: Reach{Kind: "tunnel", Base: "https://share.test/"}}})
	for _, role := range []string{"viewer", "reviewer", "approver", "agent"} {
		tok := makeToken(t, h, role, role, 0)
		hdr := map[string]string{"Authorization": "Bearer " + tok}
		if got := h.get("/api/shares", hdr); got.code != http.StatusForbidden {
			t.Fatalf("%s listed links: %d", role, got.code)
		}
		hdr["Origin"] = strings.TrimSuffix(h.url, "/")
		if got := h.post("/api/shares", `{}`, hdr); got.code != http.StatusForbidden {
			t.Fatalf("%s made a link: %d", role, got.code)
		}
	}
}

func TestSharesNoAddress(t *testing.T) {
	sh := &fakeSharing{err: errors.New("no address")}
	h := startTeam(t, Options{Sharing: sh})
	if got := h.post("/api/shares", `{}`, h.origin()); got.code != http.StatusServiceUnavailable {
		t.Fatalf("no address: %d %s", got.code, got.body)
	}
	st, err := h.srv.store(false)
	if err == nil {
		rows, _ := st.Tokens()
		if len(rows) != 0 {
			t.Fatal("a token was kept without a link", rows)
		}
	}
	// No Sharing at all: still lists, can't make.
	h2 := startTeam(t, Options{})
	if got := h2.get("/api/shares", nil); got.code != 200 {
		t.Fatal(got.code)
	}
	if got := h2.post("/api/shares", `{}`, h2.origin()); got.code != http.StatusServiceUnavailable {
		t.Fatal(got.code)
	}
}

func TestVideoAgentRoute(t *testing.T) {
	h := startTeam(t, Options{})
	agent := makeToken(t, h, "agent", "worker", 0)
	reviewer := makeToken(t, h, "reviewer", "alex", 0)
	poll := `{"worker":{"id":"machine","session":"s","name":"pc","agents":[{"id":"claude","name":"Claude Code"},{"id":"codex","name":"Codex"}]}}`
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
	if got := h.post("/api/worker/poll", poll, bearer(agent)); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	// Only the owner picks the video's agent.
	hdr := bearer(reviewer)
	hdr["Origin"] = strings.TrimSuffix(h.url, "/")
	if got := h.post("/api/automation/agent", `{"agent":"codex"}`, hdr); got.code != http.StatusForbidden {
		t.Fatalf("a reviewer picked the agent: %d", got.code)
	}
	if got := h.post("/api/automation/agent", `{"agent":"gemini"}`, h.origin()); got.code != http.StatusBadRequest {
		t.Fatalf("an agent no computer offers: %d", got.code)
	}
	if got := h.post("/api/automation/agent", `{"agent":"codex"}`, h.origin()); got.code != 200 {
		t.Fatalf("set: %d %s", got.code, got.body)
	}
	var a struct {
		Config     project.AutomationConfig `json:"config"`
		VideoAgent string                   `json:"video_agent"`
	}
	if got := h.get("/api/automation", nil); json.Unmarshal(got.body, &a) != nil || a.VideoAgent != "codex" ||
		a.Config.Agent != "claude" {
		t.Fatalf("GET: %s", got.body)
	}
	// A reviewer's Send without a pick goes to the video's agent; a held Send's pick wins.
	st, err := h.srv.store(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Sync(project.Run{Version: "v6", Comments: []project.Comment{{Key: "a", Text: "one"}}}); err != nil {
		t.Fatal(err)
	}
	if got := h.post("/api/automation/tasks", `{"notes":[1]}`, hdr); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	st2, _ := st.Automation()
	if len(st2.Tasks) != 1 || st2.Tasks[0].Agent != "codex" {
		t.Fatalf("send: %+v", st2.Tasks)
	}
	if got := h.post("/api/automation/agent", `{"agent":""}`, h.origin()); got.code != 200 {
		t.Fatal(got.code)
	}
	if got := h.get("/api/automation", nil); strings.Contains(string(got.body), `"video_agent"`) {
		t.Fatalf("cleared, still there: %s", got.body)
	}
}
