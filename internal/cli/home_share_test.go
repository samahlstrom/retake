//go:build unix

package cli

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// retake share <folder>: one link for the whole home (spec/home.md, "Sharing the home").
func TestShareAFolder(t *testing.T) {
	w, dir := newFolderWorld(t)
	bin, _ := fakeCloudflared(t, "folder-share.trycloudflare.com", "")
	env := w.environment(nil)
	env.SharePing = 20 * time.Millisecond
	link, cancel, done := runShare(t, env, dir, "--port", "0", "--tunnel-bin", bin, "--label", "ana", "--no-qr")
	u, err := url.Parse(link)
	if err != nil || u.Host != "folder-share.trycloudflare.com" || !strings.HasPrefix(u.Path, "/s/rt_") {
		t.Fatalf("link %q", link)
	}
	m := serverLine.FindStringSubmatch(w.stdout.String())
	if m == nil || m[2] != "started for this share" {
		t.Fatalf("stdout: %s", w.stdout)
	}
	port, _ := strconv.Atoi(m[1])

	// Through the tunnel: nothing without the link; the link's cookie opens the home and every project in it.
	if res := through(t, port, u.Host, "/api/home", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", res.StatusCode)
	}
	res := through(t, port, u.Host, u.Path, nil)
	cookies := res.Cookies()
	if res.StatusCode != http.StatusOK || len(cookies) != 1 || cookies[0].Path != "/" {
		t.Fatalf("link: %d %v", res.StatusCode, cookies)
	}
	hdr := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value}
	for _, p := range []string{"/api/home", "/p/clip/api/info", "/p/other-cut/api/info"} {
		if res := through(t, port, u.Host, p, hdr); res.StatusCode != http.StatusOK {
			t.Errorf("%s with the link: %d", p, res.StatusCode)
		}
	}
	// The token is the folder's; the project stores don't know it.
	st, err := project.OpenHomeExisting(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if ts, _ := st.Tokens(); len(ts) != 1 || ts[0].Label != "ana" {
		t.Errorf("tokens %+v", ts)
	}
	if hosts, _ := st.Hosts(); len(hosts) != 1 || hosts[0].Host != u.Host {
		t.Errorf("hosts %+v", hosts)
	}
	cancel()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
	if hosts, _ := st.Hosts(); len(hosts) != 0 {
		t.Errorf("hosts after the share: %+v", hosts)
	}
}

// A share of a folder attaches to the retake open of that folder that's already running.
func TestShareAFolderAttaches(t *testing.T) {
	w, dir := newFolderWorld(t)
	openURL, stopOpen, openDone := w.serve(t, w.environment(nil), "open", dir, "--port", "0", "--no-open",
		"--idle-exit", "0")
	defer stopOpen()
	port, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(openURL, "http://127.0.0.1:"), "/"))
	if !servesFolder(port, dir) || servesFolder(port, w.root) || serves(port, w.video) {
		t.Fatal("servesFolder/serves misread the folder's server")
	}
	bin, _ := fakeCloudflared(t, "attached-folder.trycloudflare.com", "")
	out := &buffer{}
	env := w.environment(nil)
	env.Stdout, env.SharePing = out, 20*time.Millisecond
	link, cancel, done := runShare(t, env, dir, "--port", strconv.Itoa(port), "--tunnel-bin", bin, "--json")
	var line shareJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &line); err != nil || !line.Attached ||
		line.Port != port {
		t.Fatalf("--json: %v %+v", err, line)
	}
	u, _ := url.Parse(link)
	if res := through(t, port, u.Host, u.Path, nil); res.StatusCode != http.StatusOK {
		t.Errorf("link through the attached server: %d", res.StatusCode)
	}
	cancel()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("share exit %d", code)
	}
	if res := through(t, port, u.Host, "/api/ping", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("the tunnel host is still answered after the share: %d", res.StatusCode)
	}
	stopOpen()
	if code := wait(t, openDone); code != ExitOK {
		t.Errorf("open exit %d", code)
	}
}
