package cli

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/testmedia"
)

const fakeTimeline = `{"schema":"timeline/0.1","id":"t","rev":1,"format":{"size":[64,36],"fps":12,"sample_rate":48000},` +
	`"assets":{},"spine":[],"scenes":[]}`

var jpegBytes = string([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0})

// The whole hosted setup from the command line: retake serve prints the setup link, the owner is made in the
// browser (here: the form's POST), and retake connect, with the line the page shows, puts the folder's timeline
// projects on the server and runs a worker for each (with a fake agent and renderer). Footage stays here.
func TestConnectRegistersProjectsAndRunsWorkers(t *testing.T) {
	testmedia.NeedShell(t)
	w := newWorld(t)
	data := filepath.Join(w.root, "data")
	base, session, line, done := hosted(t, w, data)

	// A folder: a timeline project with a version (and its poster), a new timeline project, and a plain video.
	folder := filepath.Join(w.root, "Projects")
	writeFile(t, filepath.Join(folder, "Intro Cut", "edit", "timeline.json"), fakeTimeline)
	writeFile(t, filepath.Join(folder, "Intro Cut", "v1", "intro_v1.mp4"), "footage that must stay here")
	writeFile(t, filepath.Join(folder, "Intro Cut", "v1", "intro_v1.poster.jpg"), jpegBytes)
	writeFile(t, filepath.Join(folder, "fresh", "edit", "timeline.json"), fakeTimeline)
	writeFile(t, filepath.Join(folder, "plain", "plain_v1.mp4"), "a finished video")
	editor := filepath.Join(w.root, "fake-retake-edit")
	writeFile(t, editor, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(editor, 0o700); err != nil {
		t.Fatal(err)
	}
	w.env["RETAKE_EDIT"] = editor
	profiles := filepath.Join(w.root, "profiles.json")
	writeFile(t, profiles, `[{"id":"fake","name":"Fake agent","command":["sh","-c","exit 0"]}]`)

	// A wrong token stops it at once.
	bad := "rk_" + strings.Repeat("A", 43)
	if code := w.run("connect", base, "--token", bad, "--folder", folder); code != ExitError ||
		!strings.Contains(w.stderr.String(), "bad token") {
		t.Fatalf("bad token: %d %s", code, w.stderr)
	}
	if _, err := os.Stat(filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "connect.toml")); !os.IsNotExist(err) {
		t.Fatal("a refused connection was saved")
	}

	env := w.environment(nil)
	env.ConnectEvery = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan int, 1)
	go func() {
		exited <- Main(ctx, []string{"connect", line[1], "--token", line[2], "--folder", folder, "--name", "test-pc",
			"--profiles", profiles}, env)
	}()
	stop := func() {
		cancel()
		select {
		case code := <-exited:
			if code != ExitOK {
				t.Errorf("connect exited %d\n%s%s", code, w.stdout, w.stderr)
			}
		case <-time.After(15 * time.Second):
			t.Error("connect didn't stop")
		}
	}
	defer func() {
		if ctx.Err() == nil {
			stop()
		}
	}()

	homeOf := func() home.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+"/api/home", nil)
		req.AddCookie(&http.Cookie{Name: "retake_session", Value: session})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var h home.Response
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	names := func(h home.Response) string {
		var out []string
		for _, c := range h.Projects {
			out = append(out, c.Name)
		}
		return strings.Join(out, " ")
	}
	waitFor := func(what string, ok func(home.Response) bool) home.Response {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			h := homeOf()
			if ok(h) {
				return h
			}
			if time.Now().After(deadline) {
				b, _ := json.Marshal(h)
				t.Fatalf("waiting for %s: %s\nstdout:\n%s\nstderr:\n%s", what, b, w.stdout, w.stderr)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	h := waitFor("the projects and the machine", func(h home.Response) bool {
		return len(h.Projects) == 3 && len(h.Machines) == 1 && h.Machines[0].Online
	})
	if got := strings.Fields(names(h)); !slices.Contains(got, "fresh") || !slices.Contains(got, "intro-cut") ||
		!slices.Contains(got, "plain") {
		t.Fatalf("projects: %s", names(h)) // the plain folder too: retake open shows it
	}
	for _, c := range h.Projects {
		if c.Name == "intro-cut" && (c.Title != "Intro Cut" || c.Thumb != "p/intro-cut/api/poster" ||
			c.Path != "Intro Cut" || c.Project != "") {
			t.Errorf("intro-cut: %+v", c)
		}
	}
	// The server's tree is the folder's, as retake open shows it.
	// Each folder holds one video, so they are the videos of the connected folder's own project.
	if h.Tree == nil || len(h.Tree.Children) != 0 || !h.Tree.Project || len(h.Tree.Videos) != 3 {
		b, _ := json.Marshal(h.Tree)
		t.Errorf("tree: %s", b)
	}
	// The poster is the version's first frame, sent from here.
	req, _ := http.NewRequest(http.MethodGet, base+"/p/intro-cut/api/poster", nil)
	req.AddCookie(&http.Cookie{Name: "retake_session", Value: session})
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(b) != jpegBytes {
			t.Fatalf("poster: %d %q", resp.StatusCode, b)
		}
	}
	if mc := h.Machines[0]; mc.Name != "test-pc" || len(mc.Agents) != 1 || mc.Agents[0].Name != "Fake agent" {
		t.Fatalf("machine: %+v", mc)
	}
	if !strings.Contains(w.stdout.String(), "plain: on "+base+"/p/plain/") {
		t.Errorf("the plain project wasn't connected:\n%s", w.stdout)
	}

	// The connection is saved, privately.
	cp := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "connect.toml")
	st, err := os.Stat(cp)
	saved, _ := os.ReadFile(cp)
	if err != nil || st.Mode().Perm() != 0o600 || !strings.Contains(string(saved), line[2]) ||
		!strings.Contains(string(saved), folder) {
		t.Fatalf("connect.toml: %v %v\n%s", st, err, saved)
	}

	// A project made later is picked up.
	writeFile(t, filepath.Join(folder, "later", "edit", "timeline.json"), fakeTimeline)
	waitFor("the new project", func(h home.Response) bool { return len(h.Projects) == 4 })
	stop()

	// Nothing but the poster reached the server: no footage, source or renders.
	err = filepath.WalkDir(filepath.Join(data, "projects"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if family.IsVideo(d.Name()) || strings.HasSuffix(d.Name(), "timeline.json") || strings.Contains(p, "footage") {
			t.Errorf("on the server: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A bare retake connect, from anywhere, reconnects the same folder to the same server.
	w.stdout.Reset()
	ctx2, cancel2 := context.WithCancel(context.Background())
	again := make(chan int, 1)
	go func() { again <- Main(ctx2, []string{"connect", "--profiles", profiles}, env) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(w.stdout.String(), "later: on "+base+"/p/later/") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel2()
	if code := <-again; code != ExitOK || !strings.Contains(w.stdout.String(), "Connected "+folder+" to "+base) {
		t.Fatalf("bare connect: %d\n%s%s", code, w.stdout, w.stderr)
	}

	// Without a saved connection or an address there's nothing to do.
	_ = os.Remove(cp)
	if code := w.run("connect"); code != ExitUsage || !strings.Contains(w.stderr.String(), "nothing to reconnect") {
		t.Fatalf("nothing saved: %d %s", code, w.stderr)
	}
	if code := w.run("connect", base+"/p/x", "--token", line[2]); code != ExitUsage {
		t.Fatalf("a project URL: %d", code)
	}
	_ = done
}

// hosted starts retake serve with its data in data, makes the owner as the browser's setup page does, and returns
// the server's address, the owner's session and the Connect your computer line (its address and token).
func hosted(t *testing.T, w *world, data string, args ...string) (base, session string, line []string, done <-chan int) {
	t.Helper()
	base, _, done = w.serve(t, w.environment(nil), append([]string{"serve", "--data", data, "--listen", "127.0.0.1:0"},
		args...)...)
	base = strings.Replace(strings.TrimSuffix(base, "/"), "localhost", "127.0.0.1", 1)
	// The setup link is printed once, with its one-time code.
	m := regexp.MustCompile(`/setup\?code=(su_[A-Za-z0-9_-]{43})`).FindStringSubmatch(w.stdout.String())
	if m == nil {
		t.Fatalf("no setup link:\n%s", w.stdout)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/setup", strings.NewReader(url.Values{"code": {m[1]},
		"name": {"Owner"}, "email": {"owner@example.com"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "retake_session" {
			session = c.Value
		}
	}
	line = regexp.MustCompile(`retake connect (\S+) --token (rk_[A-Za-z0-9_-]{43})`).FindStringSubmatch(string(page))
	if resp.StatusCode != http.StatusOK || session == "" || line == nil || line[1] != base {
		t.Fatalf("setup: %d %q\n%s", resp.StatusCode, session, page)
	}
	return base, session, line, done
}

func TestConnectProjects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", "a_v1.mp4"), "x")
	writeFile(t, filepath.Join(dir, "a", "edit", "timeline.json"), fakeTimeline)
	writeFile(t, filepath.Join(dir, "b", "b_v2.mp4"), "x")
	writeFile(t, filepath.Join(dir, "A", "edit", "timeline.json"), fakeTimeline) // no version yet; "a" is taken
	writeFile(t, filepath.Join(dir, ".hidden", "edit", "timeline.json"), fakeTimeline)
	writeFile(t, filepath.Join(dir, "notes", "readme.txt"), "x")
	// Deeper: a dated folder's project, a project's short, and a timeline project in a folder, as retake open finds.
	writeFile(t, filepath.Join(dir, "2026", "jump", "v2", "jump_v2.mp4"), "x")
	writeFile(t, filepath.Join(dir, "b", "shorts", "one_v1.mp4"), "x")
	writeFile(t, filepath.Join(dir, "drafts", "next", "edit", "timeline.json"), fakeTimeline)
	var got []string
	for _, p := range connectProjects(dir) {
		s := p.Name + "=" + filepath.Base(p.Root) + "@" + p.Path + ":" + p.Project
		if p.timeline {
			s += "+timeline"
		}
		got = append(got, s)
	}
	want := "a=a@a:+timeline b=b@b:b 2026-jump=jump@2026/jump:2026 b-shorts=shorts@b/shorts:b " +
		"a-2=A@A:+timeline drafts-next=next@drafts/next:drafts+timeline"
	// On a case-insensitive disk (Windows, macOS by default) "A" is the folder "a": there's no second project.
	if a, err := os.Stat(filepath.Join(dir, "a")); err == nil {
		if upper, err := os.Stat(filepath.Join(dir, "A")); err == nil && os.SameFile(a, upper) {
			want = strings.Replace(want, " a-2=A@A:+timeline", "", 1)
		}
	}
	if strings.Join(got, " ") != want {
		t.Fatalf("%v", got)
	}
}
