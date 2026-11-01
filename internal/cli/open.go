package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/app"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/store"
	"github.com/samahlstrom/retake/internal/ui"
)

type openArgs struct {
	video    string
	versions []string
	port     *int
	noOpen   bool
	idleExit *float64
	uiDir    string
	allow    []string
	verbose  bool
	help     bool
}

// isFlag reports whether arg looks like an option (and not a negative number), as argparse decides.
func isFlag(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	_, err := strconv.ParseFloat(arg, 64)
	return err != nil
}

func parseOpen(args []string) (openArgs, error) {
	var a openArgs
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !isFlag(arg) {
			pos = append(pos, arg)
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
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
		case "--versions":
			if hasVal {
				a.versions = append(a.versions, val)
			}
			for i+1 < len(args) && !isFlag(args[i+1]) && args[i+1] != "--" {
				i++
				a.versions = append(a.versions, args[i])
			}
			if len(a.versions) == 0 {
				return a, errors.New("--versions needs at least one file")
			}
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
		case "--allow-host":
			v, err := value()
			if err != nil {
				return a, err
			}
			h := strings.ToLower(strings.TrimSpace(v))
			if h == "" || strings.ContainsAny(h, "/:@ ") {
				return a, fmt.Errorf("--allow-host %q: give a bare hostname like abc.trycloudflare.com", v)
			}
			a.allow = append(a.allow, h)
		case "--no-open", "--verbose":
			if hasVal {
				return a, fmt.Errorf("%s takes no value", name)
			}
			if name == "--no-open" {
				a.noOpen = true
			} else {
				a.verbose = true
			}
		default:
			return a, fmt.Errorf("unknown option %s", name)
		}
	}
	if a.help {
		return a, nil
	}
	switch len(pos) {
	case 0:
		return a, errors.New("missing the video file (or a folder of videos)")
	case 1:
		a.video = pos[0]
	default:
		return a, fmt.Errorf("unexpected argument %q (one video or folder; use --versions for more)", pos[1])
	}
	return a, nil
}

// idleDuration turns minutes into the watchdog's limit; zero, negative or absurd values mean never.
func idleDuration(minutes float64) time.Duration {
	if math.IsNaN(minutes) || minutes <= 0 || minutes > 1e8 {
		return 0
	}
	return time.Duration(minutes * float64(time.Minute))
}

// listen binds 127.0.0.1: exactly port when strict, else port or one of the next tries-1.
func listen(port int, strict bool, tries int) (net.Listener, string) {
	if strict {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return nil, fmt.Sprintf("port %d is busy", port)
		}
		return ln, ""
	}
	for p := port; p < port+tries && p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			return ln, ""
		}
	}
	return nil, "no free port"
}

// osUser returns the login name of the user running retake, for session.json "author".
func osUser(getenv func(string) string) string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 { // DOMAIN\user on Windows
			name = name[i+1:]
		}
		return name
	}
	for _, k := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func loadConfig(env Env) config.Config {
	path, err := config.Path(env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		return config.Config{}
	}
	c, warnings, err := config.Load(path)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(env.Stderr, "retake: config %s: %s\n", path, w)
	}
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: config %s: %v (ignored)\n", path, err)
		return config.Config{}
	}
	return c
}

func openCmd(ctx context.Context, args []string, env Env) int {
	a, err := parseOpen(args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake open: %v\n\n%s", err, usage)
		return ExitUsage
	}
	if a.help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	cfg := loadConfig(env)
	st := config.Resolve(cfg, config.Flags{Port: a.port, IdleExit: a.idleExit, NoOpen: a.noOpen})

	video := absPath(a.video, env.Getenv)
	if isDir(video) {
		return openFolder(ctx, a, env, cfg, st, video) // every project in it, with the home screen (spec/home.md)
	}
	if !isFile(video) {
		_, _ = fmt.Fprintf(env.Stderr, "retake: no such file: %s\n", video)
		return ExitUsage
	}
	var explicit []string
	for _, x := range a.versions {
		p := absPath(x, env.Getenv)
		if !isFile(p) {
			_, _ = fmt.Fprintf(env.Stderr, "retake: no such file: %s\n", p)
			return ExitUsage
		}
		explicit = append(explicit, p)
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
	srv := newServer(env, cfg, st, server.Options{Video: video, Explicit: explicit, Port: port, UI: page,
		IdleExit: idleDuration(st.IdleExit), Verbose: a.verbose, AllowHosts: a.allow})

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	printStartup(env.Stdout, srv, video, url, st.IdleExit)
	if st.OpenBrowser {
		if err := env.OpenURL(url); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: couldn't open a browser (%v): open %s\n", err, url)
		}
	}
	if env.Ready != nil {
		env.Ready(url)
	}
	idle, err := srv.Serve(ctx, ln)
	if cerr := srv.Close(); cerr != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: closing the project store: %v\n", cerr)
	}
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		return ExitError
	case idle:
		_, _ = fmt.Fprintln(env.Stdout, "retake: page gone, exiting")
	default:
		_, _ = fmt.Fprintln(env.Stdout, "\nretake: stopped")
	}
	return ExitOK
}

// newServer fills in what open and share serve alike (the prober, the author, the engines, the test hooks and the
// log) and makes the server.
func newServer(env Env, cfg config.Config, st config.Settings, o server.Options) *server.Server {
	o.Prober = env.Prober
	if o.Prober == nil {
		if !probe.Available() {
			_, _ = fmt.Fprintln(env.Stderr, "retake: ffprobe not found on PATH: install ffmpeg so versions can be detected")
		}
		o.Prober = probe.New(env.Stderr)
	}
	o.Author = st.Name
	if o.Author == "" {
		o.Author = osUser(env.Getenv)
	}
	engines := app.Engines(cfg)
	if env.Engines != nil {
		engines = env.Engines(cfg)
	}
	o.Jobs = &engines
	o.CheckEvery, o.Clock, o.Log, o.Version = env.CheckEvery, env.Clock, env.Stderr, Version()
	o.Editor = editorOf(env)
	o.LocalWorker = localWorker(env, cfg) // Send works with nothing else to run
	return server.New(o)
}

// pageFiles returns the page to serve: --ui-dir's folder, else the build in the binary. ok is false (after saying
// why) when the folder doesn't exist.
func pageFiles(env Env, uiDir string) (page fs.FS, ok bool) {
	if uiDir == "" {
		return ui.Embedded(), true
	}
	dir := absPath(uiDir, env.Getenv)
	page, err := ui.Files(dir)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: --ui-dir: no such folder: %s\n", dir)
		return nil, false
	}
	if _, err := fs.Stat(page, "index.html"); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: --ui-dir %s has no index.html; / will say the page isn't built\n", dir)
	}
	return page, true
}

// printStartup prints the legacy player's startup lines: the video, the family, the page URL, where takes go.
func printStartup(w io.Writer, srv *server.Server, video, url string, idleMinutes float64) {
	cur := srv.Current()
	size := func(p *int) string {
		if p == nil {
			return "?"
		}
		return strconv.Itoa(*p)
	}
	dur, ok := 0.0, cur.Duration != nil
	if ok {
		dur = float64(*cur.Duration)
	}
	_, _ = fmt.Fprintf(w, "retake: %s  (%sx%s, %s fps, %s)\n", filepath.Base(video), size(cur.Width), size(cur.Height),
		jsonx.FormatFloat(float64(cur.FPS)), fmtDur(dur, ok))
	labels := make([]string, 0, len(srv.Family()))
	for _, v := range srv.Family() {
		if v.Current {
			labels = append(labels, "["+v.Label+"]")
		} else {
			labels = append(labels, v.Label)
		}
	}
	sep := string(filepath.Separator)
	_, _ = fmt.Fprintf(w, "  versions %s\n", strings.Join(labels, " "))
	_, _ = fmt.Fprintf(w, "  page   %s\n", url)
	_, _ = fmt.Fprintf(w, "  saves  %s%s<take>%s   (other versions save next to their own file)\n",
		store.ReviewDir(video), sep, sep)
	idle := ""
	if idleDuration(idleMinutes) > 0 {
		idle = fmt.Sprintf(", auto-exits %s min after the page is closed", strconv.FormatFloat(idleMinutes, 'g', 6, 64))
	}
	_, _ = fmt.Fprintf(w, "  Ctrl+C stops the server%s\n", idle)
}

var _ family.Prober = (*probe.Prober)(nil)
