package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/workspace"
)

func TestWorkspaceOrganizationPersistsAndRejectsStaleWrites(t *testing.T) {
	x := newFT(t, nil)
	initial := x.home(t)
	if initial.Workspace == nil || initial.Workspace.Projects[0].Title != "Shows" || initial.Workspace.Revision != 0 {
		t.Fatal(initial.Workspace)
	}
	layout := *initial.Workspace
	layout.Projects = append(layout.Projects, workspace.Project{ID: "launch", Title: "Launch", Groups: []workspace.Group{{ID: "shorts", Title: "Shorts"}}})
	layout.Placements = []workspace.Placement{{Video: "alpha", Project: "launch", Group: "shorts"}}
	body, _ := json.Marshal(layout)
	r := x.do("POST", "/api/workspace", string(body))
	if r.code != 200 {
		t.Fatal(r)
	}
	saved := x.home(t).Workspace
	if saved.Revision != 1 || saved.Place("alpha").Group != "shorts" || saved.Place("bravo").Project != "default" {
		t.Fatal(saved)
	}
	// A second server reads the same stored layout, independent of the first process/browser.
	second := NewFolder(FolderOptions{Dir: x.dir})
	defer func() { _ = second.Close() }()
	loaded, err := second.workspace()
	if err != nil || loaded.Revision != 1 || loaded.Place("alpha").Project != "launch" {
		t.Fatal(loaded, err)
	}
	if r = x.do("POST", "/api/workspace", string(body)); r.code != 409 {
		t.Fatal(r)
	}
	layout.Revision = 1
	layout.Placements[0].Video = "unknown"
	body, _ = json.Marshal(layout)
	if r = x.do("POST", "/api/workspace", string(body)); r.code != 400 {
		t.Fatal(r)
	}
}

func TestWorkspaceOnlyOwnerCanOrganize(t *testing.T) {
	x := newFT(t, nil)
	body, _ := json.Marshal(workspace.Default("Shows"))
	for _, role := range []team.Role{team.Viewer, team.Reviewer, team.Approver, team.Agent} {
		token := homeToken(t, x.dir, role, "Guest")
		r := x.do("POST", "/api/workspace", string(body), bearerTok(token))
		if r.code != 403 {
			t.Fatalf("%s: %v", role, r)
		}
	}
	r := x.do("POST", "/api/workspace", string(body), func(r *http.Request) { r.Header.Set("Origin", "https://elsewhere.example") })
	if r.code != 403 {
		t.Fatal(r)
	}
}

func TestLegacyVideoShareOpensOnlyItsVideoInWorkspace(t *testing.T) {
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
	layout := workspace.Default("Shows")
	layout.Projects[0].Groups = []workspace.Group{{ID: "visible", Title: "Visible group"}, {ID: "secret", Title: "Private group"}}
	layout.Projects = append(layout.Projects, workspace.Project{ID: "secret", Title: "Private project", Groups: []workspace.Group{}})
	layout.Placements = []workspace.Placement{{Video: "alpha", Project: "default", Group: "visible"}, {Video: "bravo", Project: "secret"}}
	body, _ := json.Marshal(layout)
	if r := x.do("POST", "/api/workspace", string(body)); r.code != 200 {
		t.Fatal(r)
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
	if len(h.Workspace.Projects) != 1 || len(h.Workspace.Projects[0].Groups) != 1 || h.Workspace.Projects[0].Groups[0].ID != "visible" {
		t.Fatal(h.Workspace)
	}
	for _, route := range []string{"/p/bravo/", "/p/bravo/api/info", "/p/bravo/video", "/p/bravo/api/events", "/p/bravo/api/notes?v=v1"} {
		if r = x.get(route, host("old-review.example"), auth); r.code != 403 {
			t.Fatalf("%s: %v", route, r)
		}
	}
	if r = x.do("POST", "/api/workspace", string(body), host("old-review.example"), auth, origin("https://old-review.example")); r.code != 403 {
		t.Fatal(r)
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

func TestGroupShareLosesAccessWhenVideoMovesOut(t *testing.T) {
	x := newFT(t, nil)
	layout := workspace.Default("Shows")
	layout.Projects[0].Groups = []workspace.Group{{ID: "review", Title: "For review"}, {ID: "drafts", Title: "Private drafts"}}
	layout.Placements = []workspace.Placement{{Video: "alpha", Project: "default", Group: "review"}}
	body, _ := json.Marshal(layout)
	if r := x.do("POST", "/api/workspace", string(body)); r.code != 200 {
		t.Fatal(r)
	}
	st, err := project.OpenHome(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	token, hash, err := team.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateToken(project.NewToken{Hash: hash, Role: "reviewer", Scope: "group:default/review"}); err != nil {
		t.Fatal(err)
	}
	h := x.home(t, shared(), bearerTok(token))
	if len(h.Projects) != 1 || h.Projects[0].Name != "alpha" || len(h.Workspace.Projects[0].Groups) != 1 {
		t.Fatal(h)
	}
	layout.Revision = 1
	layout.Placements[0].Group = "drafts"
	body, _ = json.Marshal(layout)
	if r := x.do("POST", "/api/workspace", string(body)); r.code != 200 {
		t.Fatal(r)
	}
	h = x.home(t, shared(), bearerTok(token))
	if len(h.Projects) != 0 || len(h.Workspace.Projects[0].Groups) != 1 || h.Workspace.Projects[0].Groups[0].ID != "review" {
		t.Fatal(h)
	}
	if r := x.get("/p/alpha/api/info", shared(), bearerTok(token)); r.code != 403 {
		t.Fatal(r)
	}
}
