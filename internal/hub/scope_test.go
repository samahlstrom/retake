package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// scopedToken makes a token with a scope in the folder's home store.
func scopedToken(t *testing.T, dir, scope string) string {
	t.Helper()
	st, err := project.OpenHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	token, hash, err := team.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateToken(project.NewToken{Hash: hash, Role: "reviewer", Scope: scope}); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestScopeAllows(t *testing.T) {
	for _, c := range []struct {
		scope, video string
		want         bool
	}{
		{"", "alpha", true},
		{"video:alpha", "alpha", true},
		{"video:alpha", "bravo", false},
		{"video:", "alpha", false},
		{"project:default", "alpha", false},
		{"group:default/review", "alpha", false},
		{"something", "alpha", false},
	} {
		if got := scopeAllows(c.scope, c.video); got != c.want {
			t.Errorf("scopeAllows(%q, %q) = %v", c.scope, c.video, got)
		}
	}
	for _, s := range []string{"project:default", "group:default/review", "project:", "group:"} {
		if !retiredScope(s) {
			t.Errorf("%q is a retired scope", s)
		}
	}
	for _, s := range []string{"", "video:alpha", "projects", "groupie:x"} {
		if retiredScope(s) {
			t.Errorf("%q isn't a retired scope", s)
		}
	}
}

// A video's own share link, opened on the folder's address, opens a home of that video only, and nothing of another.
func TestLegacyVideoShareOpensOnlyItsVideo(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.AllowHosts = nil })
	token := projectToken(t, filepath.Join(x.dir, "alpha"), team.Approver)
	st, err := project.Open(filepath.Join(x.dir, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err = st.AddHost("old-review.example", "none", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if r := x.get("/api/home", host("old-review.example")); r.code != 401 {
		t.Fatal(r)
	}
	r := x.get("/s/"+token, host("old-review.example"))
	if r.code != 200 || setCookie(t, r, ShareCookie) == nil {
		t.Fatal(r)
	}
	auth := cookie(ShareCookie, token)
	h := x.home(t, host("old-review.example"), auth)
	if len(h.Projects) != 1 || h.Projects[0].Name != "alpha" || h.Role != "approver" || h.Folder != "" {
		t.Fatal(h)
	}
	for _, route := range []string{"/p/bravo/", "/p/bravo/api/info", "/p/bravo/video", "/p/bravo/api/events", "/p/bravo/api/notes?v=v1"} {
		if r = x.get(route, host("old-review.example"), auth); r.code != 403 {
			t.Fatalf("%s: %v", route, r)
		}
	}
	if r = x.do("POST", "/p/alpha/api/versions/v2/unwatch", "", host("old-review.example"), auth, origin("https://old-review.example")); r.code != 204 {
		t.Fatal(r)
	}
	if r = x.do("POST", "/p/bravo/api/notes/1/delete", "", host("old-review.example"), auth, origin("https://old-review.example")); r.code != 403 {
		t.Fatal(r)
	}
	row, err := st.TokenByHash(team.Hash(token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.RevokeToken(row.ID); err != nil {
		t.Fatal(err)
	}
	if r = x.get("/api/home", host("old-review.example"), auth); r.code != 401 {
		t.Fatal("revoked link still works", r)
	}
}

// A folder token scoped to one video, stored in the home store, works the same way.
func TestVideoScopedFolderToken(t *testing.T) {
	x := newFT(t, nil)
	token := scopedToken(t, x.dir, "video:bravo")
	h := x.home(t, shared(), bearerTok(token))
	if len(h.Projects) != 1 || h.Projects[0].Name != "bravo" {
		t.Fatal(h)
	}
	if r := x.get("/p/bravo/api/info", shared(), bearerTok(token)); r.code != 200 {
		t.Fatal(r)
	}
	if r := x.get("/p/alpha/api/info", shared(), bearerTok(token)); r.code != 403 {
		t.Fatal(r)
	}
}

// The project organization is gone (spec/home.md, "Share scopes"): its API answers 404, and a link made for one of
// its projects or groups is refused everywhere with 410 and a page that says why, never a crash or an empty home.
func TestRetiredProjectAndGroupLinks(t *testing.T) {
	x := newFT(t, nil)
	if r := x.do("POST", "/api/workspace", `{"revision":0,"projects":[],"placements":[]}`); r.code != 404 {
		t.Fatalf("organization API still there: %v", r)
	}
	if strings.Contains(x.get("/api/home").body, `"workspace"`) {
		t.Fatal("/api/home still has the organization")
	}
	for _, scope := range []string{"project:default", "group:default/review"} {
		token := scopedToken(t, x.dir, scope)
		// The link itself: a page, and no cookie.
		r := x.get("/s/"+token, shared())
		if r.code != 410 || !strings.Contains(r.body, "no longer has") || setCookie(t, r, ShareCookie) != nil {
			t.Fatalf("%s: /s/: %v", scope, r)
		}
		// A cookie set before the change: the home page says the same.
		if r = x.get("/", shared(), cookie(ShareCookie, token)); r.code != 410 || !strings.Contains(r.body, "no longer has") {
			t.Fatalf("%s: home page: %v", scope, r)
		}
		if r = x.get("/p/alpha/", shared(), cookie(ShareCookie, token)); r.code != 410 ||
			!strings.Contains(r.body, "no longer has") {
			t.Fatalf("%s: review page: %v", scope, r)
		}
		// The API: JSON.
		for _, route := range []string{"/api/home", "/p/alpha/api/info", "/p/alpha/api/notes?v=v2"} {
			r = x.get(route, shared(), bearerTok(token))
			if r.code != 410 || r.json(t)["error"] != "retired link" {
				t.Fatalf("%s: %s: %v", scope, route, r)
			}
		}
	}
	// The owner's own access is untouched.
	if h := x.home(t); len(h.Projects) != 3 {
		t.Fatal(h)
	}
}
