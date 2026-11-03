package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/trash"
)

// made is POST api/shares's answer.
type made struct {
	URL   string `json:"url"`
	Reach struct {
		Kind, Host, Note string
	} `json:"reach"`
}

func (x *ft) makeLink(t *testing.T, video, body string) made {
	t.Helper()
	r := x.do(http.MethodPost, "/p/"+video+"/api/shares", body, origin(x.url))
	if r.code != http.StatusCreated {
		t.Fatalf("making a link: %d %s", r.code, r.body)
	}
	var m made
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func tokenOf(t *testing.T, url string) string {
	t.Helper()
	i := strings.LastIndex(url, "/s/")
	if i < 0 || !team.WellFormed(url[i+3:]) {
		t.Fatalf("not a share link: %s", url)
	}
	return url[i+3:]
}

func TestFolderVideoLinkOpensThatVideoOnly(t *testing.T) {
	x := newFT(t, nil) // share.test is an --allow-host name: a tunnel that runs for this server
	m := x.makeLink(t, "alpha", `{"label":"Alex"}`)
	if m.Reach.Kind != "tunnel" || !strings.HasPrefix(m.URL, "https://share.test/s/") {
		t.Fatalf("link %+v", m)
	}
	tok := tokenOf(t, m.URL)
	// The token is the video's own: in alpha's store.
	st, err := project.OpenExisting(filepath.Join(x.dir, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Tokens()
	_ = st.Close()
	if len(rows) != 1 || rows[0].Label != "Alex" || rows[0].Role != "reviewer" {
		t.Fatalf("alpha's tokens: %+v", rows)
	}

	r := x.get("/s/"+tok, shared())
	if r.code != 200 || !strings.Contains(r.body, `location.replace("/p/alpha/")`) {
		t.Fatalf("the link bounces to the video: %d %s", r.code, r.body)
	}
	c := r.hdr.Get("Set-Cookie")
	if !strings.Contains(c, "retake_share="+tok) || !strings.Contains(c, "Secure") || !strings.Contains(c, "Path=/;") {
		t.Fatalf("cookie: %s", c)
	}
	h := x.home(t, shared(), cookie(ShareCookie, tok))
	if len(h.Projects) != 1 || h.Projects[0].Name != "alpha" {
		t.Fatalf("the home through the link: %v", summary(h))
	}
	if r := x.get("/p/alpha/api/info", shared(), cookie(ShareCookie, tok)); r.code != 200 ||
		!strings.Contains(r.body, `"role":"reviewer"`) {
		t.Fatalf("alpha: %d %s", r.code, r.body)
	}
	for _, p := range []string{"/p/bravo/api/info", "/p/bravo/video", "/p/charlie/api/notes"} {
		if r := x.get(p, shared(), cookie(ShareCookie, tok)); r.code != http.StatusForbidden {
			t.Fatalf("%s through alpha's link: %d", p, r.code)
		}
	}
	// A reviewer's link can't make links; a folder's approver link can.
	if r := x.do(http.MethodPost, "/p/alpha/api/shares", `{}`, shared(), cookie(ShareCookie, tok),
		origin("https://share.test")); r.code != http.StatusForbidden {
		t.Fatalf("a reviewer made a link: %d", r.code)
	}
	ap := homeToken(t, x.dir, team.Approver, "Sam phone")
	if r := x.do(http.MethodPost, "/p/bravo/api/shares", `{"role":"viewer"}`, shared(), cookie(ShareCookie, ap),
		origin("https://share.test")); r.code != http.StatusCreated {
		t.Fatalf("an approver's link: %d %s", r.code, r.body)
	}

	// A folder's link still opens the whole folder, and bounces to the home.
	ft := homeToken(t, x.dir, team.Viewer, "")
	if r := x.get("/s/"+ft, shared()); r.code != 200 || !strings.Contains(r.body, `location.replace("/")`) {
		t.Fatalf("folder link: %d %s", r.code, r.body)
	}
	if h := x.home(t, shared(), cookie(ShareCookie, ft)); len(h.Projects) != 3 {
		t.Fatalf("folder link sees %v", summary(h))
	}

	// Revoked in the page: it stops at once.
	if r := x.do(http.MethodPost, "/p/alpha/api/shares/1/revoke", ``, origin(x.url)); r.code != 200 {
		t.Fatalf("revoke: %d %s", r.code, r.body)
	}
	if r := x.get("/p/alpha/api/info", shared(), cookie(ShareCookie, tok)); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", r.code)
	}
}

// fakeCloudflared stands in for cloudflared: it prints a quick tunnel's address and waits to be stopped. It's a
// shell script (a test that runs it calls testmedia.NeedShell); on Windows it's named .exe so that it's found.
func fakeCloudflared(t *testing.T, host string) (bin, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "pid")
	script := "#!/bin/sh\necho $$ > \"" + pidFile + "\"\necho \"INF |  https://" + host + "  |\" >&2\nexec sleep 30\n"
	bin = filepath.Join(dir, "cloudflared")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, pidFile
}

func TestFolderStartsATunnelForALink(t *testing.T) {
	testmedia.NeedShell(t)
	bin, _ := fakeCloudflared(t, "quick-words.trycloudflare.com")
	x := newFT(t, func(o *FolderOptions) {
		o.AllowHosts = nil
		o.Share = FolderShare{Cloudflared: bin}
	})
	r := x.get("/p/bravo/api/shares")
	if r.code != 200 || !strings.Contains(r.body, `"kind":"tunnel","host":""`) {
		t.Fatalf("before: %d %s", r.code, r.body)
	}
	m := x.makeLink(t, "bravo", `{"role":"viewer","expires":"never"}`)
	if m.URL != "https://quick-words.trycloudflare.com/s/"+tokenOf(t, m.URL) {
		t.Fatalf("link %s", m.URL)
	}
	// The tunnel's host is the folder's now: the link works through it.
	tok := tokenOf(t, m.URL)
	if r := x.get("/s/"+tok, host("quick-words.trycloudflare.com")); r.code != 200 ||
		!strings.Contains(r.body, "/p/bravo/") {
		t.Fatalf("through the tunnel: %d %s", r.code, r.body)
	}
	st, err := project.OpenHomeExisting(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	hosts, _ := st.Hosts()
	_ = st.Close()
	if len(hosts) != 1 || hosts[0].Host != "quick-words.trycloudflare.com" || hosts[0].PID != os.Getpid() {
		t.Fatalf("hosts: %+v", hosts)
	}
	// Closing the folder stops it, and its host goes.
	if err := x.f.Close(); err != nil {
		t.Fatal(err)
	}
	st, _ = project.OpenHomeExisting(x.dir)
	hosts, _ = st.Hosts()
	_ = st.Close()
	if len(hosts) != 0 {
		t.Fatalf("the tunnel's host stayed: %+v", hosts)
	}
}

func TestFolderLinkOnTheLocalNetwork(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) {
		o.AllowHosts = nil
		o.Share = FolderShare{Off: true, LANAddr: func() string { return "127.0.0.2" }} // loopback stands in for Wi-Fi
	})
	lan := "127.0.0.2:" + itoa(x.port)
	m := x.makeLink(t, "alpha", `{}`)
	if m.Reach.Kind != "lan" || m.URL != "http://"+lan+"/s/"+tokenOf(t, m.URL) || !strings.Contains(m.Reach.Note, "Settings") {
		t.Fatalf("link %+v", m)
	}
	tok := tokenOf(t, m.URL)
	get := func(path, hostHeader string, hdr map[string]string) (int, http.Header, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+lan+path, nil)
		req.Host = hostHeader
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		c := &http.Client{Timeout: 5 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}
	code, hdr, body := get("/s/"+tok, lan, nil)
	if code != 200 || !strings.Contains(body, "/p/alpha/") {
		t.Fatalf("the link on the network: %d %s", code, body)
	}
	if c := hdr.Get("Set-Cookie"); !strings.Contains(c, "retake_share=") || strings.Contains(c, "Secure") {
		t.Fatalf("cookie on plain HTTP: %s", c)
	}
	// Nothing on the network listener is the owner's, whatever its Host says.
	for _, h := range []string{lan, "127.0.0.1:" + itoa(x.port), "localhost:" + itoa(x.port)} {
		if code, _, _ := get("/api/home", h, nil); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Fatalf("Host %s on the network listener without a token: %d", h, code)
		}
	}
	if code, _, body := get("/p/alpha/api/info", lan, map[string]string{"Cookie": ShareCookie + "=" + tok}); code != 200 ||
		!strings.Contains(body, `"role":"reviewer"`) {
		t.Fatalf("with the cookie: %d %s", code, body)
	}
	// The owner's own address is unchanged.
	if r := x.get("/api/home"); r.code != 200 {
		t.Fatal(r.code)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// scratchTrash is a trash under a scratch XDG_DATA_HOME: tests never touch the real one.
func scratchTrash(t *testing.T) trash.Options {
	t.Helper()
	root := t.TempDir()
	return trash.Options{GOOS: "linux", Home: root, DataHome: filepath.Join(root, "data"), UID: os.Getuid(),
		SameDisk: func(string, string) bool { return true }}
}

func TestFolderMoveToTrashAndUndo(t *testing.T) {
	to := scratchTrash(t)
	x := newFT(t, func(o *FolderOptions) { o.Trash = to })
	r := x.get("/api/videos/alpha/trash")
	var v trashView
	if r.code != 200 || json.Unmarshal([]byte(r.body), &v) != nil {
		t.Fatalf("preview: %d %s", r.code, r.body)
	}
	if !v.OK || v.Versions != 2 || v.Files != 2 || v.Size == 0 || !strings.HasSuffix(filepath.ToSlash(v.Path), "Shows/alpha") {
		t.Fatalf("preview %+v", v)
	}
	// Somebody else can't.
	tok := homeToken(t, x.dir, team.Approver, "")
	if r := x.get("/api/videos/alpha/trash", shared(), cookie(ShareCookie, tok)); r.code != http.StatusForbidden {
		t.Fatalf("an approver's preview: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/api/videos/alpha/trash", "", shared(), cookie(ShareCookie, tok),
		origin("https://share.test")); r.code != http.StatusForbidden {
		t.Fatalf("an approver's move: %d", r.code)
	}

	// The page says what this computer calls its trash.
	if r := x.get("/api/home"); !strings.Contains(r.body, `"trash":"Trash"`) {
		t.Fatalf("home: %s", r.body)
	}
	// Open it first, so it has a server and a store to close.
	if r := x.get("/p/alpha/api/info"); r.code != 200 {
		t.Fatal(r.code)
	}
	r = x.do(http.MethodPost, "/api/videos/alpha/trash", "", origin(x.url))
	var moved struct{ ID, Path string }
	if r.code != 200 || json.Unmarshal([]byte(r.body), &moved) != nil || moved.ID == "" {
		t.Fatalf("move: %d %s", r.code, r.body)
	}
	if _, err := os.Stat(filepath.Join(x.dir, "alpha")); !os.IsNotExist(err) {
		t.Fatal("alpha is still there")
	}
	inTrash := filepath.Join(to.DataHome, "Trash", "files", "alpha", "v2", "alpha_v2.mp4")
	if _, err := os.Stat(inTrash); err != nil {
		t.Fatal("not in the trash", err)
	}
	info, err := os.ReadFile(filepath.Join(to.DataHome, "Trash", "info", "alpha.trashinfo"))
	if err != nil || !strings.Contains(string(info), "Path="+filepath.ToSlash(filepath.Join(x.dir, "alpha"))) {
		t.Fatalf("info file: %s %v", info, err)
	}
	if got := summary(x.home(t)); len(got) != 2 {
		t.Fatalf("the home still lists it: %v", got)
	}
	if r := x.get("/p/alpha/api/info"); r.code != http.StatusNotFound {
		t.Fatalf("its page: %d", r.code)
	}

	// Undo.
	if r := x.do(http.MethodPost, "/api/trash/"+moved.ID+"/restore", "", origin(x.url)); r.code != 200 ||
		!strings.Contains(r.body, `"name":"alpha"`) {
		t.Fatalf("undo: %d %s", r.code, r.body)
	}
	if got := summary(x.home(t)); len(got) != 3 {
		t.Fatalf("not back: %v", got)
	}
	if r := x.get("/p/alpha/api/info"); r.code != 200 {
		t.Fatalf("its page after Undo: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/api/trash/"+moved.ID+"/restore", "", origin(x.url)); r.code != http.StatusNotFound {
		t.Fatalf("undo twice: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/api/trash/nope/restore", "", origin(x.url)); r.code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/api/videos/alpha/trash", "", origin("https://evil.test")); r.code != http.StatusForbidden {
		t.Fatalf("another origin: %d", r.code)
	}
}

func TestFolderTrashRefuses(t *testing.T) {
	to := scratchTrash(t)
	x := newFT(t, func(o *FolderOptions) { o.Trash = to })
	// A project's own video: its folder holds another video.
	file(t, filepath.Join(x.dir, "bravo", "shorts", "v1", "shorts_v1.mp4"), time.Now())
	x.f.rescan(true)
	r := x.get("/api/videos/bravo/trash")
	if !strings.Contains(r.body, `"ok":false`) || !strings.Contains(r.body, whyShared) {
		t.Fatalf("shared folder: %s", r.body)
	}
	if r := x.do(http.MethodPost, "/api/videos/bravo/trash", "", origin(x.url)); r.code != http.StatusConflict {
		t.Fatalf("shared folder moved: %d", r.code)
	}
	// An agent at work.
	st, err := project.Open(filepath.Join(x.dir, "charlie"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		a.Tasks = append(a.Tasks, project.EditingTask{ID: "t", State: "running", Events: []project.TaskEvent{}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if r := x.do(http.MethodPost, "/api/videos/charlie/trash", "", origin(x.url)); r.code != http.StatusConflict ||
		!strings.Contains(r.body, "agent is working") {
		t.Fatalf("an agent at work: %d %s", r.code, r.body)
	}
	if _, err := os.Stat(filepath.Join(x.dir, "charlie")); err != nil {
		t.Fatal("charlie moved")
	}
	if r := x.get("/api/videos/nope/trash"); r.code != http.StatusNotFound {
		t.Fatalf("unknown video: %d", r.code)
	}
}

func TestFolderRevealUsesRetakeReveal(t *testing.T) {
	testmedia.NeedShell(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "reveal")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \""+out+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RETAKE_REVEAL", bin)
	x := newFT(t, nil)
	if r := x.do(http.MethodPost, "/api/videos/charlie/reveal", "", origin(x.url)); r.code != 200 {
		t.Fatalf("reveal: %d %s", r.code, r.body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(out)
		if err == nil && string(b) == filepath.Join(x.dir, "charlie") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the opener got %q (%v)", b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	tok := homeToken(t, x.dir, team.Approver, "")
	if r := x.do(http.MethodPost, "/api/videos/charlie/reveal", "", shared(), cookie(ShareCookie, tok),
		origin("https://share.test")); r.code != http.StatusForbidden {
		t.Fatalf("through a link: %d", r.code)
	}
}
