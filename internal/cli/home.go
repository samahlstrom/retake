package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/samahlstrom/retake/internal/app"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/probe"
)

// retake open <folder> and retake share <folder>: every project in a folder, with the home screen (spec/home.md).

// openFolder serves every project in dir, with the home screen at /.
func openFolder(ctx context.Context, a openArgs, env Env, cfg config.Config, st config.Settings, dir string) int {
	if len(a.versions) > 0 {
		return usageError(env, "open", errors.New("--versions takes a video, not a folder"))
	}
	page, ok := pageFiles(env, a.uiDir)
	if !ok {
		return ExitUsage
	}
	tdir, named := a.themeDir(env)
	page = withTheme(env, page, tdir, named)
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
	f := newFolder(env, cfg, st, hub.FolderOptions{Dir: dir, Port: port, UI: page, IdleExit: idleDuration(st.IdleExit),
		Verbose: a.verbose, AllowHosts: a.allow})
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	printFolderStartup(env.Stdout, f, url, st.IdleExit)
	if st.OpenBrowser {
		if err := env.OpenURL(url); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: couldn't open a browser (%v): open %s\n", err, url)
		}
	}
	if env.Ready != nil {
		env.Ready(url)
	}
	idle, err := f.Serve(ctx, ln)
	if cerr := f.Close(); cerr != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: closing the project stores: %v\n", cerr)
	}
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		return ExitError
	case idle:
		_, _ = fmt.Fprintln(env.Stdout, "retake: pages gone, exiting")
	default:
		_, _ = fmt.Fprintln(env.Stdout, "\nretake: stopped")
	}
	return ExitOK
}

// newFolder fills in what every project's server needs (the prober, the author, the engines, the test hooks and the
// log), as newServer does for one, and makes the folder's server.
func newFolder(env Env, cfg config.Config, st config.Settings, o hub.FolderOptions) *hub.Folder {
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
	o.CheckEvery, o.Log, o.Version = env.CheckEvery, env.Stderr, Version()
	o.LocalWorker = localWorker(env, cfg) // each project's editing worker runs in this process
	if env.Clock != nil {
		o.Now = env.Clock.Now
	}
	return hub.NewFolder(o)
}

// statusText is how the startup lines name a status.
var statusText = map[home.Status]string{home.ToReview: "to review", home.Reviewed: "reviewed",
	home.BeingEdited: "being edited"}

// printFolderStartup prints the folder, its projects with their newest version and status, and the page URL.
func printFolderStartup(w io.Writer, f *hub.Folder, url string, idleMinutes float64) {
	cards := f.Cards()
	todo := 0
	for _, c := range cards {
		if c.Status == home.ToReview {
			todo++
		}
	}
	_, _ = fmt.Fprintf(w, "retake: %s  (%s, %d to review)\n", f.Dir(), plural(len(cards), "video"), todo)
	width := 8
	for _, c := range cards {
		width = max(width, len(c.Name))
	}
	for _, c := range cards {
		v := c.Version
		if v == "" {
			v = "-"
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %-5s %s\n", width, c.Name, v, statusText[c.Status])
	}
	if len(cards) == 0 {
		_, _ = fmt.Fprintln(w, "  no videos yet: a video is a file named like clip_v1.mp4, in this folder or one of its folders")
	}
	_, _ = fmt.Fprintf(w, "  page   %s\n", url)
	idle := ""
	if idleDuration(idleMinutes) > 0 {
		idle = fmt.Sprintf(", auto-exits %s min after the pages are closed", strconv.FormatFloat(idleMinutes, 'g', 6, 64))
	}
	_, _ = fmt.Fprintf(w, "  Ctrl+C stops the server%s\n", idle)
}

// servesFolder reports whether a retake server on port serves the home of dir (its /api/home names the folder).
func servesFolder(port int, dir string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/home", port))
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	var h struct {
		Folder string `json:"folder"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&h) != nil {
		return false
	}
	return h.Folder != "" && family.SamePath(h.Folder, dir)
}
