package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/thumb"
	"github.com/samahlstrom/retake/internal/worker"
)

// retake connect (spec/automation.md, "Connect your computer"): put a folder's timeline projects on a hosted server
// and run one worker per project in this process, with the machine token from the browser's Connect your computer.

// DefaultConnectEvery is how often retake connect looks for new projects in its folder.
const DefaultConnectEvery = 10 * time.Second

// connection is what retake connect saves, so a bare `retake connect` reconnects.
type connection struct {
	URL    string `toml:"url"`
	Token  string `toml:"token"`
	Folder string `toml:"folder"`
}

func connectionPath(getenv func(string) string) (string, error) {
	p, err := config.Path(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "connect.toml"), nil
}

func loadConnection(getenv func(string) string) (connection, string, error) {
	p, err := connectionPath(getenv)
	if err != nil {
		return connection{}, "", err
	}
	var c connection
	if _, err := toml.DecodeFile(p, &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return connection{}, p, fmt.Errorf("%s: %w", p, err)
	}
	return c, p, nil
}

// saveConnection writes the connection, readable only by its owner (it holds the token).
func saveConnection(p string, c connection) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# retake connect: the server this computer's projects are connected to. Keep it private.\n")
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// serverURL checks a server's address: http(s), a host, and no path (a trailing / is fine).
func serverURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return "", fmt.Errorf("%q isn't a server address like https://review.example.com", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func connectCmd(ctx context.Context, args []string, env Env) int {
	var token, folder, name, profiles string
	f := flags{values: map[string]*string{"--token": &token, "--folder": &folder, "--name": &name,
		"--profiles": &profiles}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) > 1 {
		err = fmt.Errorf("unexpected argument %q", pos[1])
	}
	if err != nil {
		return usageError(env, "connect", err)
	}
	saved, path, err := loadConnection(env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake connect: %v\n", err)
		return ExitError
	}
	var c connection
	switch {
	case len(pos) == 1:
		if c.URL, err = serverURL(pos[0]); err != nil {
			return usageError(env, "connect", err)
		}
		c.Token = strings.TrimSpace(token)
		if c.Token == "" {
			c.Token = strings.TrimSpace(env.Getenv("RETAKE_TOKEN"))
		}
		if c.Token == "" && saved.URL == c.URL {
			c.Token = saved.Token
		}
		if c.Token == "" {
			return usageError(env, "connect", errors.New("--token is required: copy the whole line from Connect "+
				"your computer, on the server's home page"))
		}
		c.Folder = "."
	case token != "":
		return usageError(env, "connect", errors.New("give the server's address with --token"))
	case saved.URL == "" || saved.Token == "":
		return usageError(env, "connect", errors.New("nothing to reconnect: run the line from Connect your computer "+
			"(retake connect https://your-server --token rk_…) in your projects folder"))
	default:
		c = saved
	}
	if folder != "" {
		c.Folder = folder
	}
	c.Folder = absPath(c.Folder, env.Getenv)
	if !isDir(c.Folder) {
		return usageError(env, "connect", fmt.Errorf("no such folder: %s", c.Folder))
	}
	ps := agentProfiles(env, loadConfig(env))
	if profiles != "" {
		ps = nil // --profiles replaces the configured and found agents
		b, e := os.ReadFile(absPath(profiles, env.Getenv))
		if e == nil {
			e = json.Unmarshal(b, &ps)
		}
		if e != nil {
			return usageError(env, "connect", fmt.Errorf("--profiles: %w", e))
		}
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	cn := &connector{c: c, env: env, name: name, profiles: ps, editor: editorOf(env),
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // never carry the token to another address or a sign-in page
		}}}
	if err := cn.check(ctx); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake connect: %v\n", err)
		return ExitError
	}
	if err := saveConnection(path, c); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake connect: saving %s: %v\n", path, err)
	}
	cn.printf("Connected %s to %s as %q. Your footage stays on this computer.", c.Folder, c.URL, name)
	if len(ps) == 0 {
		cn.printf("No agent CLI found (Claude Code or Codex): install one so this computer can edit.")
	}
	cn.printf("Ctrl+C disconnects. Next time, plain `retake connect` reconnects.")
	cn.run(ctx)
	cn.printf("Disconnected.")
	return ExitOK
}

// connector runs retake connect: it registers the folder's projects and keeps one worker per project running.
type connector struct {
	c        connection
	env      Env
	name     string
	profiles []worker.Profile
	editor   bridge.Editor
	http     *http.Client

	logMu sync.Mutex
}

func (cn *connector) printf(format string, args ...any) {
	cn.logMu.Lock()
	defer cn.logMu.Unlock()
	_, _ = fmt.Fprintf(cn.env.Stdout, format+"\n", args...)
}

// do sends one request to a machine route with the token.
func (cn *connector) do(ctx context.Context, method, path, ctype string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, cn.c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cn.c.Token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := cn.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s answered %d: %s", cn.c.URL, resp.StatusCode, e.Error)
		}
		return fmt.Errorf("%s answered %d", cn.c.URL, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// check makes sure the server is there and takes the token.
func (cn *connector) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res struct {
		OK bool `json:"ok"`
	}
	err := cn.do(ctx, http.MethodGet, "/api/machine", "", nil, &res)
	if err != nil && strings.Contains(err.Error(), "answered 401") {
		return fmt.Errorf("%w (make a new line with Connect your computer)", err)
	}
	if err == nil && !res.OK {
		err = fmt.Errorf("%s isn't a Retake server", cn.c.URL)
	}
	return err
}

// connectProject is a project of the folder, and whether it can stream from this computer.
type connectProject struct {
	home.Found
	timeline bool
}

// connectProjects finds the folder's projects as retake open <folder> does, plus candidate folders with a native
// timeline and no version yet (spec/automation.md).
func connectProjects(dir string) []connectProject {
	found := home.Discover(dir)
	used := map[string]bool{}
	byRoot := map[string]bool{}
	var out []connectProject
	for _, f := range found {
		_, err := bridge.FindTimeline(f.Root)
		out = append(out, connectProject{Found: f, timeline: err == nil})
		used[f.Name], byRoot[f.Root] = true, true
	}
	candidates := []string{dir}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".review") {
				continue
			}
			if p := filepath.Join(dir, n); isDir(p) {
				candidates = append(candidates, p)
			}
		}
	}
	for _, c := range candidates {
		if byRoot[c] {
			continue
		}
		if _, err := bridge.FindTimeline(c); err != nil {
			continue
		}
		title := filepath.Base(c)
		name := home.Slug(title)
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d", home.Slug(title), i)
		}
		used[name] = true
		out = append(out, connectProject{Found: home.Found{Name: name, Title: title, Root: c}, timeline: true})
	}
	return out
}

// running is one project's worker.
type running struct {
	root   string
	cancel context.CancelFunc
	done   chan struct{}
	err    error // set before done closes
}

func (rw *running) exited() bool {
	select {
	case <-rw.done:
		return true
	default:
		return false
	}
}

// run registers the projects and runs their workers until ctx ends, looking at the folder again every so often.
func (cn *connector) run(ctx context.Context) {
	every := cn.env.ConnectEvery
	if every <= 0 {
		every = DefaultConnectEvery
	}
	workers := map[string]*running{}
	problem := map[string]string{} // the last problem said about a project, so it isn't said again every look
	connected := map[string]bool{}
	trouble := func(name, msg string) {
		if problem[name] != msg {
			problem[name] = msg
			cn.printf("%s: %s", name, msg)
		}
	}
	defer func() {
		for _, rw := range workers {
			rw.cancel()
		}
		for _, rw := range workers {
			<-rw.done
		}
	}()
	for {
		seen := map[string]bool{}
		for _, p := range connectProjects(cn.c.Folder) {
			seen[p.Name] = true
			if !p.timeline {
				trouble(p.Name, "skipped: it has no edit timeline, so its video can't stream from this computer")
				continue
			}
			rw := workers[p.Name]
			if rw != nil && rw.root != p.Root { // another folder has the name now
				rw.cancel()
				<-rw.done
				rw = nil
			}
			if rw != nil && rw.exited() {
				if rw.err != nil {
					trouble(p.Name, rw.err.Error()+" (trying again)")
				}
				rw = nil
			}
			if rw != nil {
				continue
			}
			delete(workers, p.Name)
			if err := cn.register(ctx, p); err != nil {
				if ctx.Err() != nil {
					return
				}
				trouble(p.Name, "couldn't add it to the server: "+err.Error()+" (trying again)")
				continue
			}
			if !connected[p.Name] {
				connected[p.Name] = true
				cn.printf("%s: on %s/p/%s/", p.Name, cn.c.URL, p.Name)
			}
			workers[p.Name] = cn.start(ctx, p)
		}
		for name, rw := range workers {
			if !seen[name] {
				rw.cancel()
				<-rw.done
				delete(workers, name)
				delete(connected, name)
				delete(problem, name)
				cn.printf("%s: disconnected, its folder is gone", name)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// register puts a project on the server: its name and title, and its newest version's first frame as the poster.
// Never footage or source.
func (cn *connector) register(ctx context.Context, p connectProject) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"name": p.Name, "title": p.Title})
	if err := cn.do(ctx, http.MethodPost, "/api/machine/projects", "application/json", body, nil); err != nil {
		return err
	}
	if p.Anchor == "" {
		return nil
	}
	poster, err := thumb.Default.For(ctx, p.Root, p.Anchor)
	if err != nil {
		return nil //nolint:nilerr // no poster is fine: the card shows none until the first render
	}
	b, err := os.ReadFile(poster) //nolint:gosec // the version's poster, or our own cached frame
	if err != nil || len(b) > 2<<20 {
		return nil //nolint:nilerr // as above
	}
	return cn.do(ctx, http.MethodPut, "/api/machine/projects/"+p.Name+"/poster", "image/jpeg", b, nil)
}

// start runs a project's worker, as retake worker does, with the machine token as its agent token.
func (cn *connector) start(ctx context.Context, p connectProject) *running {
	wctx, cancel := context.WithCancel(ctx)
	rw := &running{root: p.Root, cancel: cancel, done: make(chan struct{})}
	log := &prefixWriter{mu: &cn.logMu, w: cn.env.Stdout, prefix: p.Name + ": "}
	go func() {
		defer close(rw.done)
		rw.err = worker.Run(wctx, worker.Options{URL: cn.c.URL + "/p/" + p.Name, Token: cn.c.Token, Root: p.Root,
			Name: cn.name, Profiles: slices.Clone(cn.profiles), Editor: cn.editor, Log: log})
	}()
	return rw
}

// prefixWriter puts a prefix before every line a worker logs, so several workers can share one terminal.
type prefixWriter struct {
	mu     *sync.Mutex
	w      io.Writer
	prefix string
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out bytes.Buffer
	for line := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
		out.WriteString(p.prefix + line + "\n")
	}
	if _, err := p.w.Write(out.Bytes()); err != nil {
		return 0, err
	}
	return len(b), nil
}
