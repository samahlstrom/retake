package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/desktop"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/setup"
	"github.com/samahlstrom/retake/internal/userdir"
	"github.com/samahlstrom/retake/internal/worker"
)

// retake start: your videos folder, or the setup page the first time (spec/setup.md). The Mac app and the Linux
// app-menu entry run it.

type startArgs struct {
	port     *int
	noOpen   bool
	idleExit *float64
	app      bool
	uiDir    string
	verbose  bool
	help     bool
}

func parseStart(args []string) (startArgs, error) {
	var a startArgs
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "-h", "--help":
			a.help = true
		case "--port":
			v, err := value()
			if err != nil {
				return a, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 65535 {
				return a, fmt.Errorf("--port %q is not a port number", v)
			}
			a.port = &n
		case "--idle-exit":
			v, err := value()
			if err != nil {
				return a, err
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return a, fmt.Errorf("--idle-exit %q is not a number of minutes", v)
			}
			a.idleExit = &f
		case "--ui-dir":
			v, err := value()
			if err != nil {
				return a, err
			}
			a.uiDir = v
		case "--no-open", "--app", "--verbose":
			if hasVal {
				return a, fmt.Errorf("%s takes no value", name)
			}
			switch name {
			case "--no-open":
				a.noOpen = true
			case "--app":
				a.app = true
			default:
				a.verbose = true
			}
		default:
			if !isFlag(args[i]) {
				return a, fmt.Errorf("unexpected argument %q (retake start opens the folder from your settings; "+
					"use retake open <folder> for another one)", args[i])
			}
			return a, fmt.Errorf("unknown option %s", name)
		}
	}
	return a, nil
}

// startState is $XDG_STATE_HOME/retake/start.json (%LOCALAPPDATA%\retake\start.json on Windows): the running
// retake start.
type startState struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

// startStatePath is where retake start records itself.
func startStatePath(env Env) string {
	dir := env.Getenv("XDG_STATE_HOME")
	if dir == "" || !filepath.IsAbs(dir) {
		dir = userdir.Local(env.Getenv)
	}
	if dir == "" {
		h := userdir.Home(env.Getenv)
		if h == "" {
			return ""
		}
		dir = filepath.Join(h, ".local", "state")
	}
	return filepath.Join(dir, "retake", "start.json")
}

// startedAt is the URL of a retake start that's running and answering, or "".
func startedAt(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path) //nolint:gosec // our own state file
	if err != nil {
		return ""
	}
	var s startState
	if json.Unmarshal(b, &s) != nil || !strings.HasPrefix(s.URL, "http://127.0.0.1:") {
		return ""
	}
	c := http.Client{Timeout: 2 * time.Second}
	res, err := c.Get(s.URL + "api/ping")
	if err != nil {
		return ""
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	return s.URL
}

func startCmd(ctx context.Context, args []string, env Env) int {
	a, err := parseStart(args)
	if err != nil {
		return usageError(env, "start", err)
	}
	if a.help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if a.app && env.RefreshPath != nil {
		env.RefreshPath(ctx) // started from Finder or a menu: the person's own PATH, for their agents
	}
	cfg := loadConfig(env)
	st := config.Resolve(cfg, config.Flags{Port: a.port, IdleExit: a.idleExit, NoOpen: a.noOpen})
	if a.app && a.idleExit == nil && env.goos() == "darwin" {
		st.IdleExit = 0 // the menu bar has Quit
	}
	state := startStatePath(env)
	if u := startedAt(state); u != "" {
		_, _ = fmt.Fprintf(env.Stdout, "retake: already running at %s\n", u)
		if st.OpenBrowser {
			if err := env.OpenURL(u); err != nil {
				_, _ = fmt.Fprintf(env.Stderr, "retake: couldn't open a browser (%v): open %s\n", err, u)
			}
		}
		return ExitOK
	}
	page, ok := pageFiles(env, a.uiDir)
	if !ok {
		return ExitUsage
	}
	tries := env.PortTries
	if tries <= 0 {
		tries = config.PortTries
	}
	ln, msg := listen(st.Port, st.PortStrict, tries)
	if ln == nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: %s\n", msg)
		return ExitUsage
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if state != "" {
		if err := writeState(state, startState{PID: os.Getpid(), URL: url}); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		}
		defer removeState(state, os.Getpid())
	}
	ho := newHandoff(ln)
	defer ho.close()
	_, _ = fmt.Fprintf(env.Stdout, "retake: %s\n", url)
	if st.OpenBrowser {
		if err := env.OpenURL(url); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: couldn't open a browser (%v): open %s\n", err, url)
		}
	}
	if a.app {
		if stop := menuBar(env, url); stop != nil {
			defer stop()
		}
	}
	if env.Ready != nil {
		env.Ready(url)
	}
	for {
		cfg = reloadConfig(env, loadConfig(env))
		folder := config.FolderPath(cfg, userdir.Home(env.Getenv))
		if folder == "" || !isDir(folder) {
			if code, again := serveSetup(ctx, env, cfg, st, a, page, port, ho.phase()); !again {
				return code
			}
			continue
		}
		code, again := serveStartFolder(ctx, env, cfg, st, a, page, absPath(folder, env.Getenv), port, ho.phase())
		if !again {
			return code
		}
	}
}

// serveSetup serves the setup page on ln until it's done (again: serve the folder now), retake stops, or nothing has
// asked for it for the idle time.
func serveSetup(ctx context.Context, env Env, cfg config.Config, st config.Settings, a startArgs, page fs.FS,
	port int, ln net.Listener) (code int, again bool) {
	cfgPath, _ := config.Path(env.Getenv)
	done := make(chan string, 1)
	h := setup.New(setup.Options{UI: page, Port: port, ConfigPath: cfgPath, Home: userdir.Home(env.Getenv),
		Agents: func() []project.AgentProfile { return agentList(env, cfg) },
		Try:    tryAgent(env, cfg),
		Check: func(ctx context.Context) {
			if env.RefreshPath != nil {
				env.RefreshPath(ctx)
			}
		},
		Find: env.Find,
		Done: func(folder string) { done <- folder },
	})
	var mu sync.Mutex
	last := time.Now()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = time.Now()
		mu.Unlock()
		if a.verbose {
			_, _ = fmt.Fprintf(env.Stderr, "retake: \"%s %s\"\n", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
	errLog := io.Discard
	if a.verbose {
		errLog = env.Stderr
	}
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(errLog, "retake: ", 0)}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
	}
	_, _ = fmt.Fprintln(env.Stdout, "  setup  the first time: choose your videos folder and your agent in the browser")
	every := env.CheckEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	idle := idleDuration(st.IdleExit)
	for {
		select {
		case <-ctx.Done():
			stop()
			_, _ = fmt.Fprintln(env.Stdout, "\nretake: stopped")
			return ExitOK, false
		case folder := <-done:
			stop()
			_, _ = fmt.Fprintf(env.Stdout, "retake: set up; your videos are in %s\n", folder)
			return ExitOK, true
		case err := <-errc:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
				return ExitError, false
			}
			return ExitOK, false
		case <-tick.C:
			mu.Lock()
			gone := idle > 0 && time.Since(last) > idle
			mu.Unlock()
			if gone {
				stop()
				_, _ = fmt.Fprintln(env.Stdout, "retake: page gone, exiting")
				return ExitOK, false
			}
		}
	}
}

// serveStartFolder serves the videos folder on ln, as retake open <folder> does, until retake stops, the pages are
// gone for the idle time, or Settings picks another folder (again).
func serveStartFolder(ctx context.Context, env Env, cfg config.Config, st config.Settings, a startArgs, page fs.FS,
	dir string, port int, ln net.Listener) (code int, again bool) {
	tdir, named := localThemeDir(env, "")
	exts := localExts(env, cfg, nil, dir)
	page = withTheme(env, ext.Page(page), tdir, named)
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var switched sync.Once
	moved := false
	ls := localSettings(env, cfg, tdir, nil, dir)
	ls.SetFolder = func(string) error { // config.toml already says the new folder: serve it
		switched.Do(func() {
			moved = true
			cancel()
		})
		return nil
	}
	f := newFolder(env, cfg, st, hub.FolderOptions{Dir: dir, Port: port, UI: page, IdleExit: idleDuration(st.IdleExit),
		Verbose: a.verbose, Exts: exts, Settings: ls})
	printFolderStartup(env.Stdout, f, fmt.Sprintf("http://127.0.0.1:%d/", port), st.IdleExit)
	idle, err := f.Serve(fctx, ln)
	if cerr := f.Close(); cerr != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: closing the project stores: %v\n", cerr)
	}
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		return ExitError, false
	case moved && ctx.Err() == nil:
		return ExitOK, true
	case idle:
		_, _ = fmt.Fprintln(env.Stdout, "retake: pages gone, exiting")
	default:
		_, _ = fmt.Fprintln(env.Stdout, "\nretake: stopped")
	}
	return ExitOK, false
}

// agentList is the agents this computer offers now, the config read again.
func agentList(env Env, cfg config.Config) []project.AgentProfile {
	var out []project.AgentProfile
	for _, p := range agentProfiles(env, reloadConfig(env, cfg)) {
		out = append(out, p.AgentProfile)
	}
	return out
}

// tryAgent runs an agent's test by its id (spec/setup.md, "The agent test").
func tryAgent(env Env, cfg config.Config) func(context.Context, string) worker.TryResult {
	return func(ctx context.Context, id string) worker.TryResult {
		for _, p := range agentProfiles(env, reloadConfig(env, cfg)) {
			if p.ID == id {
				return worker.TryAgent(ctx, p)
			}
		}
		return worker.TryResult{Error: "There's no agent " + id + " here any more."}
	}
}

// menuBar starts the Mac app's menu-bar item (Contents/Resources/menubar.js) and returns what stops it; nil when
// retake isn't a Mac app or it couldn't start. Retake works the same without it.
func menuBar(env Env, url string) func() {
	if env.goos() != "darwin" {
		return nil
	}
	exe := desktop.Exe()
	app := desktop.AppBundle(exe)
	if app == "" {
		return nil
	}
	argv := menuCommand(app, os.Getpid(), url, exe)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // osascript with our own script
	cmd.Stdout, cmd.Stderr = env.Stderr, env.Stderr
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: no menu-bar item (%v); quit with Ctrl+C or the Activity Monitor\n", err)
		return nil
	}
	go func() { _ = cmd.Wait() }()
	return func() { _ = cmd.Process.Kill() }
}

// menuCommand is the menu-bar item's command line. It's a macOS command, so its paths are /-separated wherever it's
// built (path, not filepath).
func menuCommand(app string, pid int, url, exe string) []string {
	return []string{"/usr/bin/osascript", "-l", "JavaScript", path.Join(app, "Contents", "Resources", "menubar.js"),
		strconv.Itoa(pid), url, exe}
}

// handoff hands one listener's connections to one server after another (the setup page, then the folder's home),
// so the address, and the browser's permissions for it, never change.
type handoff struct {
	ln    net.Listener
	conns chan net.Conn
	done  chan struct{} // closed when the listener stops accepting
	stop  chan struct{}
	once  sync.Once
}

func newHandoff(ln net.Listener) *handoff {
	h := &handoff{ln: ln, conns: make(chan net.Conn), done: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer close(h.done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case h.conns <- c:
			case <-h.stop:
				_ = c.Close()
				return
			}
		}
	}()
	return h
}

// phase is a listener for one server: closing it ends that server, not the handoff.
func (h *handoff) phase() net.Listener { return &phaseListener{h: h, closed: make(chan struct{})} }

func (h *handoff) close() {
	h.once.Do(func() {
		close(h.stop)
		_ = h.ln.Close()
	})
	<-h.done
}

type phaseListener struct {
	h      *handoff
	closed chan struct{}
	once   sync.Once
}

func (p *phaseListener) Accept() (net.Conn, error) {
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-p.h.conns:
		return c, nil
	case <-p.closed:
		return nil, net.ErrClosed
	case <-p.h.done:
		return nil, net.ErrClosed
	}
}

func (p *phaseListener) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (p *phaseListener) Addr() net.Addr { return p.h.ln.Addr() }

// autostartCmd is retake autostart [on|off]: start Retake at login (spec/setup.md).
func autostartCmd(args []string, env Env) int {
	want := strings.Join(args, " ")
	if want != "" && want != "on" && want != "off" {
		return usageError(env, "autostart", errors.New("say on or off (or nothing, to see which)"))
	}
	as := desktop.Autostart{GOOS: env.goos(), Home: userdir.Home(env.Getenv), ConfigHome: env.Getenv("XDG_CONFIG_HOME"),
		Exe: desktop.StableExe()}
	if want != "" {
		if err := as.Set(want == "on"); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake autostart: %v\n", err)
			return ExitError
		}
	}
	if as.On() {
		_, _ = fmt.Fprintln(env.Stdout, "on")
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "off")
	}
	return ExitOK
}

func writeState(path string, s startState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeState removes the state file when it's still this process's.
func removeState(path string, pid int) {
	b, err := os.ReadFile(path) //nolint:gosec // our own state file
	if err != nil {
		return
	}
	var s startState
	if json.Unmarshal(b, &s) == nil && s.PID == pid {
		_ = os.Remove(path)
	}
}
