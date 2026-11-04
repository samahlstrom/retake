package hub

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// libraryView deliberately decodes the public boundary as generic JSON. The
// linked-folder contract is a route contract; it must not require its handler
// or storage types to exist before the route does.
func libraryView(t *testing.T, r res) map[string]any {
	t.Helper()
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/library: %d %s", r.code, r.body)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("GET /api/library JSON: %v: %s", err, r.body)
	}
	return v
}

func libraryFolders(t *testing.T, v map[string]any) []map[string]any {
	t.Helper()
	raw, ok := v["folders"].([]any)
	if !ok {
		t.Fatalf("library folders: %#v", v["folders"])
	}
	folders := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		folder, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("library folder: %#v", item)
		}
		folders = append(folders, folder)
	}
	return folders
}

func libraryString(t *testing.T, v map[string]any, key string) string {
	t.Helper()
	s, ok := v[key].(string)
	if !ok || s == "" {
		t.Fatalf("library %q: %#v", key, v[key])
	}
	return s
}

func libraryRevision(t *testing.T, v map[string]any) float64 {
	t.Helper()
	revision, ok := v["revision"].(float64)
	if !ok || revision < 0 {
		t.Fatalf("library revision: %#v", v["revision"])
	}
	return revision
}

func folderAt(t *testing.T, folders []map[string]any, path string) map[string]any {
	t.Helper()
	for _, folder := range folders {
		if folder["path"] == path {
			return folder
		}
	}
	t.Fatalf("linked folder %q not in %#v", path, folders)
	return nil
}

func replaceLibrary(t *testing.T, x *ft, revision float64, folders []map[string]any, opts ...reqOpt) res {
	t.Helper()
	body, err := json.Marshal(map[string]any{"revision": revision, "folders": folders})
	if err != nil {
		t.Fatal(err)
	}
	return x.do(http.MethodPost, "/api/library", string(body), opts...)
}

func sourceBytes(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(paths))
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = string(b)
	}
	return out
}

func requireUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("source changed %s: %v %q", path, err, got)
		}
	}
}

// restartLibraryFT restarts the local hub with the same options. A library is
// persistent state, so its selected and offline links must survive this seam.
func restartLibraryFT(t *testing.T, x *ft) {
	t.Helper()
	o := x.f.opts
	oldTS, oldFolder := x.ts, x.f
	oldTS.Close()
	if err := oldFolder.Close(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	o.Port = ts.Listener.Addr().(*net.TCPAddr).Port
	x.port = o.Port
	x.url = "http://" + ts.Listener.Addr().String()
	folder := NewFolder(o)
	ts.Config.Handler = folder
	ts.Start()
	x.f = folder
	x.ts = ts
	x.t.Cleanup(func() {
		ts.Close()
		_ = folder.Close()
	})
}

// TestLibraryLinkedFoldersHTTP is the owner-visible contract for linked
// folders. Request folders retain an id only when the caller is relocating an
// existing link; a new selection omits id and receives one in the response.
func TestLibraryLinkedFoldersHTTP(t *testing.T) {
	stateHome := t.TempDir()
	x := newFT(t, func(o *FolderOptions) {
		o.RescanEvery = time.Nanosecond
		o.Settings = &LocalSettings{Home: stateHome}
	})

	outside := t.TempDir()
	externalOne := filepath.Join(t.TempDir(), "Drive One")
	externalTwo := filepath.Join(t.TempDir(), "Drive Two")
	file(t, filepath.Join(externalOne, "cut", "v1", "cut_v1.mp4"), time.Now())
	file(t, filepath.Join(externalTwo, "cut", "v1", "cut_v1.mp4"), time.Now())
	escapeVideo := filepath.Join(outside, "escape", "v1", "escape_v1.mp4")
	file(t, escapeVideo, time.Now())
	if err := os.Symlink(filepath.Join(outside, "escape"), filepath.Join(externalOne, "escape")); err != nil {
		t.Fatal(err)
	}
	externalVideo := filepath.Join(externalOne, "cut", "v1", "cut_v1.mp4")
	alphaVideo := filepath.Join(x.dir, "alpha", "v2", "alpha_v2.mp4")
	before := sourceBytes(t, alphaVideo, externalVideo, escapeVideo)

	// An existing open edit is a hard stop: changing the library cannot quietly
	// detach work. This uses the same durable busy state as the folder trash flow.
	st, err := project.Open(filepath.Join(x.dir, "charlie"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		a.Tasks = append(a.Tasks, project.EditingTask{ID: "linked-folders", State: "running"})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var initialRevision float64
	var initialID, viewer, scoped string
	if !t.Run("owner initial link and capabilities", func(t *testing.T) {
		initial := libraryView(t, x.get("/api/library"))
		computer, ok := initial["computer"].(map[string]any)
		if !ok || libraryString(t, computer, "name") == "" || libraryString(t, computer, "platform") == "" {
			t.Fatalf("library computer: %#v", initial["computer"])
		}
		if _, ok := initial["native_picker"].(bool); !ok {
			t.Fatalf("library native_picker: %#v", initial["native_picker"])
		}
		initialRevision = libraryRevision(t, initial)
		initialFolders := libraryFolders(t, initial)
		if len(initialFolders) != 1 {
			t.Fatalf("initial links: %#v", initialFolders)
		}
		initialID = libraryString(t, folderAt(t, initialFolders, x.dir), "id")
		viewer = homeToken(t, x.dir, team.Viewer, "viewer")
		scoped = scopedToken(t, x.dir, "video:alpha")
	}) {
		return
	}

	selection := []map[string]any{
		{"id": initialID, "path": filepath.Join(x.dir, "alpha"), "subfolders": true},
		{"path": externalOne, "subfolders": true},
		{"path": externalTwo, "subfolders": true},
	}
	if !t.Run("busy edit refuses removal atomically", func(t *testing.T) {
		if r := replaceLibrary(t, x, initialRevision, selection); r.code != http.StatusConflict {
			t.Fatalf("busy replacement: %d %s", r.code, r.body)
		}
		if got := libraryView(t, x.get("/api/library")); libraryRevision(t, got) != initialRevision ||
			len(libraryFolders(t, got)) != 1 {
			t.Fatalf("busy replacement changed library: %#v", got)
		}
	}) {
		return
	}

	st, err = project.Open(filepath.Join(x.dir, "charlie"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		a.Tasks = nil
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var updatedRevision float64
	var links []map[string]any
	var reordered []map[string]any
	if !t.Run("multi-root selection discovery and scopes", func(t *testing.T) {
		r := replaceLibrary(t, x, initialRevision, selection)
		updated := libraryView(t, r)
		updatedRevision = libraryRevision(t, updated)
		if updatedRevision <= initialRevision {
			t.Fatalf("replacement did not advance revision: %#v", updated)
		}
		links = libraryFolders(t, updated)
		if len(links) != 3 || libraryString(t, folderAt(t, links, filepath.Join(x.dir, "alpha")), "id") != initialID {
			t.Fatalf("replacement links: %#v", links)
		}
		for _, link := range links {
			if _, ok := link["available"].(bool); !ok {
				t.Fatalf("link availability: %#v", link)
			}
		}

		// The old alpha URL is retained after narrowing. Extra roots with the same
		// leaf name receive distinct, stable URLs; neither unlinked siblings nor a
		// symlink that escapes a selected root is discovered.
		h := x.home(t)
		urls := map[string]bool{}
		alpha := ""
		for _, card := range h.Projects {
			if urls[card.URL] {
				t.Fatalf("duplicate project URL: %+v", h.Projects)
			}
			urls[card.URL] = true
			if card.Name == "alpha" {
				alpha = card.URL
			}
			if card.Name == "bravo" || card.Name == "charlie" || card.Name == "escape" {
				t.Fatalf("unlinked or symlink-escaped card: %+v", card)
			}
		}
		if alpha != "p/alpha/" || len(urls) != 3 {
			t.Fatalf("linked project URLs: alpha=%q projects=%+v", alpha, h.Projects)
		}
		for _, path := range []string{externalOne, externalTwo} {
			libraryString(t, folderAt(t, links, path), "id")
		}
		for label, credential := range map[string]reqOpt{"home token": bearerTok(viewer), "video token": bearerTok(scoped)} {
			sharedHome := x.home(t, shared(), credential)
			if len(sharedHome.Projects) != 1 || sharedHome.Projects[0].Name != "alpha" {
				t.Fatalf("%s exposed newly linked roots: %+v", label, sharedHome.Projects)
			}
			for _, card := range h.Projects {
				if card.Name == "alpha" {
					continue
				}
				if r := x.get("/p/"+card.Name+"/api/info", shared(), credential); r.code != http.StatusForbidden {
					t.Fatalf("%s accessed linked external project %q: %d %s", label, card.Name, r.code, r.body)
				}
			}
			r := x.get("/api/library", shared(), credential)
			if r.code != http.StatusForbidden || strings.Contains(r.body, externalOne) || strings.Contains(r.body, externalTwo) {
				t.Fatalf("%s library privacy: %d %s", label, r.code, r.body)
			}
		}
		reordered = []map[string]any{selection[2], selection[1], selection[0]}
		reordered[0]["id"] = libraryString(t, folderAt(t, links, externalTwo), "id")
		reordered[1]["id"] = libraryString(t, folderAt(t, links, externalOne), "id")
		reordered[2]["id"] = initialID
		if r := replaceLibrary(t, x, updatedRevision, reordered); r.code != http.StatusOK {
			t.Fatalf("reorder: %d %s", r.code, r.body)
		}
		again := x.home(t)
		if len(again.Projects) != 3 || again.Projects[0].URL == "" {
			t.Fatalf("reordered projects: %+v", again.Projects)
		}
		// Removing and re-adding an external duplicate does not change the public
		// set of project URLs. Relocating the retained original link above is the
		// corresponding Locate case.
		beforeURLs := map[string]bool{}
		for _, card := range again.Projects {
			beforeURLs[card.URL] = true
		}
		current := libraryView(t, x.get("/api/library"))
		withoutTwo := []map[string]any{}
		for _, link := range libraryFolders(t, current) {
			if link["path"] == externalTwo {
				continue
			}
			withoutTwo = append(withoutTwo, map[string]any{"id": libraryString(t, link, "id"),
				"path": libraryString(t, link, "path"), "subfolders": link["subfolders"]})
		}
		if r := replaceLibrary(t, x, libraryRevision(t, current), withoutTwo); r.code != http.StatusOK {
			t.Fatalf("remove external link: %d %s", r.code, r.body)
		}
		if remaining := x.home(t).Projects; len(remaining) != 2 {
			t.Fatalf("remove external link projects: %+v", remaining)
		}
		current = libraryView(t, x.get("/api/library"))
		readd := append(withoutTwo, map[string]any{"path": externalTwo, "subfolders": true})
		if r := replaceLibrary(t, x, libraryRevision(t, current), readd); r.code != http.StatusOK {
			t.Fatalf("re-add external link: %d %s", r.code, r.body)
		}
		afterURLs := map[string]bool{}
		for _, card := range x.home(t).Projects {
			afterURLs[card.URL] = true
		}
		if len(afterURLs) != len(beforeURLs) {
			t.Fatalf("re-add project URLs: before=%v after=%v", beforeURLs, afterURLs)
		}
		for url := range beforeURLs {
			if !afterURLs[url] {
				t.Fatalf("re-add lost stable project URL %q: %v", url, afterURLs)
			}
		}
		current = libraryView(t, x.get("/api/library"))
		reordered = reordered[:0]
		for _, link := range libraryFolders(t, current) {
			reordered = append(reordered, map[string]any{"id": libraryString(t, link, "id"),
				"path": libraryString(t, link, "path"), "subfolders": link["subfolders"]})
		}
		requireUnchanged(t, before)
	}) {
		return
	}

	if !t.Run("conflict and invalid path are atomic", func(t *testing.T) {
		// Conflict and bad paths leave the last valid library untouched.
		current := libraryView(t, x.get("/api/library"))
		currentRevision := libraryRevision(t, current)
		if r := replaceLibrary(t, x, currentRevision-1, reordered); r.code != http.StatusConflict {
			t.Fatalf("stale revision: %d %s", r.code, r.body)
		}
		badFile := filepath.Join(t.TempDir(), "not-a-folder")
		if err := os.WriteFile(badFile, []byte("no"), 0o600); err != nil {
			t.Fatal(err)
		}
		bad := append(append([]map[string]any{}, reordered...), map[string]any{"path": badFile, "subfolders": true})
		if r := replaceLibrary(t, x, currentRevision, bad); r.code != http.StatusBadRequest {
			t.Fatalf("invalid path: %d %s", r.code, r.body)
		}
		overlapping := append(append([]map[string]any{}, reordered...),
			map[string]any{"path": filepath.Join(x.dir, "alpha", "v1"), "subfolders": true})
		if r := replaceLibrary(t, x, currentRevision, overlapping); r.code != http.StatusBadRequest {
			t.Fatalf("overlapping path: %d %s", r.code, r.body)
		}
		if after := libraryView(t, x.get("/api/library")); libraryRevision(t, after) != currentRevision ||
			len(libraryFolders(t, after)) != len(reordered) {
			t.Fatalf("invalid path changed library: %#v", after)
		}
	}) {
		return
	}

	if !t.Run("offline link persists across restart", func(t *testing.T) {
		// A missing linked root stays visible and survives restart. It is not
		// automatically removed or followed through its old project URL.
		offline := externalOne + " offline"
		if err := os.Rename(externalOne, offline); err != nil {
			t.Fatal(err)
		}
		before[filepath.Join(offline, "cut", "v1", "cut_v1.mp4")] = before[externalVideo]
		delete(before, externalVideo)
		if link := folderAt(t, libraryFolders(t, libraryView(t, x.get("/api/library"))), externalOne); link["available"] != false {
			t.Fatalf("offline link: %#v", link)
		}
		if r := x.get("/p/alpha/api/info"); r.code != http.StatusOK {
			t.Fatalf("retained project: %d %s", r.code, r.body)
		}
		restartLibraryFT(t, x)
		if link := folderAt(t, libraryFolders(t, libraryView(t, x.get("/api/library"))), externalOne); link["available"] != false {
			t.Fatalf("offline link after restart: %#v", link)
		}
	}) {
		return
	}

	t.Run("empty library and stale routes stay private", func(t *testing.T) {
		// Empty is a valid persisted library. The old routes must refuse access,
		// and neither a whole-home token nor a video-scoped token sees library data.
		current := libraryView(t, x.get("/api/library"))
		if r := replaceLibrary(t, x, libraryRevision(t, current), nil); r.code != http.StatusOK {
			t.Fatalf("empty library: %d %s", r.code, r.body)
		}
		restartLibraryFT(t, x)
		if got := libraryFolders(t, libraryView(t, x.get("/api/library"))); len(got) != 0 {
			t.Fatalf("empty library after restart: %#v", got)
		}
		if r := x.get("/p/alpha/api/info"); r.code != http.StatusNotFound {
			t.Fatalf("stale project route: %d %s", r.code, r.body)
		}
		for _, credential := range []reqOpt{bearerTok(viewer), bearerTok(scoped)} {
			r := x.get("/api/library", shared(), credential)
			if r.code != http.StatusForbidden || strings.Contains(r.body, x.dir) || strings.Contains(r.body, externalOne) || strings.Contains(r.body, externalTwo) {
				t.Fatalf("shared library privacy: %d %s", r.code, r.body)
			}
			if r = replaceLibrary(t, x, 0, nil, shared(), credential); r.code != http.StatusForbidden {
				t.Fatalf("shared library mutation: %d %s", r.code, r.body)
			}
		}
		requireUnchanged(t, before)
	})
}
