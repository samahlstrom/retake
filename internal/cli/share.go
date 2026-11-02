package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/qrterm"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/tunnel"
	"github.com/samahlstrom/retake/internal/ui"
)

// retake share and retake token: team mode, slice 1 (spec/team.md).

// DefaultSharePing is how often a share pings the server it attached to, so its idle exit doesn't end the share.
const DefaultSharePing = 30 * time.Second

// tunnelGrace is how long a share waits, once its tunnel has exited, for the Ctrl+C that may have stopped both.
const tunnelGrace = time.Second

// parseExpiry reads --expires: a Go duration (90m, 24h), days (7d), or 0 / never for no expiry.
func parseExpiry(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "0" || s == "never" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n <= 0 || n > 3650 {
			return 0, fmt.Errorf("--expires %q is not a number of days", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--expires %q: give a duration like 90m, 24h or 7d, or never", s)
	}
	return d, nil
}

// bareHost checks a hostname given on the command line.
func bareHost(flag, v string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(v))
	if h == "" || strings.ContainsAny(h, "/:@ ") {
		return "", fmt.Errorf("%s %q: give a bare hostname like review.example.com", flag, v)
	}
	return h, nil
}

// openStore opens (creating it if needed) the project store of the video named on the command line, or the home
// store of a folder (spec/home.md, "Sharing the home").
func openStore(env Env, cmd, video string) (*project.Store, string, int) {
	path := absPath(video, env.Getenv)
	if isDir(path) {
		st, err := project.OpenHome(path)
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
			return nil, "", ExitError
		}
		return st, path, ExitOK
	}
	if !isFile(path) {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: no such file: %s\n", cmd, path)
		return nil, "", ExitUsage
	}
	st, err := project.Open(project.Root(path))
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
		return nil, "", ExitError
	}
	return st, path, ExitOK
}

// liveHosts returns the hosts of shares whose process still runs, newest first, and forgets the others.
func liveHosts(st *project.Store) []project.Host {
	hosts, err := st.Hosts()
	if err != nil {
		return nil
	}
	out := []project.Host{}
	for _, h := range hosts {
		if h.PID > 0 && !processAlive(h.PID) {
			_ = st.RemoveHost(h.Host, h.PID)
			continue
		}
		out = append(out, h)
	}
	return out
}

func fmtWhen(stamp string) string {
	t, err := project.ParseTime(stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("2006-01-02 15:04")
}

func untilText(t project.Token) string {
	if t.Expires == "" {
		return "no expiry"
	}
	return "until " + fmtWhen(t.Expires)
}

func labelText(t project.Token) string {
	if t.Label == "" {
		return ""
	}
	return fmt.Sprintf(" for %q", t.Label)
}

// newToken makes a token and stores its hash.
func newToken(st *project.Store, role team.Role, label string, ttl time.Duration) (string, project.Token, error) {
	tok, hash, err := team.NewToken()
	if err != nil {
		return "", project.Token{}, err
	}
	row, err := st.CreateToken(project.NewToken{Hash: hash, Role: string(role), Label: label, TTL: ttl})
	return tok, row, err
}

type shareArgs struct {
	role, expires, label, tunnel, host, bin, port, theme string
	funnel, noQR, asJSON, verbose                        bool
	exts                                                 []string // --ext: extension folders for this run
}

func shareCmd(ctx context.Context, args []string, env Env) int {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return tokenListCmd(args[1:], env, "share list")
		case "revoke":
			return tokenRevokeCmd(args[1:], env, "share revoke")
		}
	}
	a := shareArgs{role: string(team.Reviewer), expires: "24h", tunnel: tunnel.Cloudflare}
	f := flags{
		bools: map[string]*bool{"--funnel": &a.funnel, "--no-qr": &a.noQR, "--json": &a.asJSON, "--verbose": &a.verbose},
		values: map[string]*string{"--role": &a.role, "--expires": &a.expires, "--label": &a.label,
			"--tunnel": &a.tunnel, "--host": &a.host, "--tunnel-bin": &a.bin, "--port": &a.port, "--theme": &a.theme},
		multi: map[string]*[]string{"--ext": &a.exts},
	}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	var role team.Role
	if err == nil {
		role, err = team.ParseRole(a.role)
	}
	if err == nil && role == team.Agent {
		err = errors.New("an agent connects with a token, not a link: retake token create <video> --role agent")
	}
	var ttl time.Duration
	if err == nil {
		ttl, err = parseExpiry(a.expires)
	}
	if err == nil {
		err = a.checkTunnel()
	}
	var port *int
	if err == nil && a.port != "" {
		n, perr := strconv.Atoi(a.port)
		if perr != nil || n < 0 || n > 65535 {
			err = fmt.Errorf("--port %q is not a port number", a.port)
		}
		port = &n
	}
	if err != nil {
		return usageError(env, "share", err)
	}
	st, video, code := openStore(env, "share", pos[0])
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()
	liveHosts(st) // forget the hosts of shares that died
	s := &share{env: env, a: a, video: video, given: pos[0], st: st, role: role, ttl: ttl, folder: isDir(video)}
	return s.run(ctx, port)
}

func (a *shareArgs) checkTunnel() error {
	switch a.tunnel {
	case tunnel.Cloudflare, tunnel.Tailscale:
		if a.host != "" {
			return errors.New("--host is for --tunnel none; a tunnel brings its own hostname")
		}
	case tunnel.None:
		if a.host == "" {
			return errors.New("--tunnel none needs --host, the hostname your reverse proxy serves")
		}
		h, err := bareHost("--host", a.host)
		if err != nil {
			return err
		}
		a.host = h
	default:
		return fmt.Errorf("--tunnel %q: use cloudflare, tailscale or none", a.tunnel)
	}
	if a.funnel && a.tunnel != tunnel.Tailscale {
		return errors.New("--funnel is for --tunnel tailscale")
	}
	return nil
}

// share is one run of retake share.
type share struct {
	env    Env
	a      shareArgs
	video  string // absolute
	given  string // as typed, for the revoke hint
	folder bool   // video is a folder: share its home (spec/home.md)
	st     *project.Store
	role   team.Role
	ttl    time.Duration

	port     int
	attached bool
	srvDone  chan struct{} // closed when the server we started returns; nil when attached
	stopSrv  func()
}

// infoProbe is what retake share reads from a running server's /api/info to decide whether to attach to it.
type infoProbe struct {
	Role     *string `json:"role"`
	Path     string  `json:"path"`
	Versions []struct {
		Path string `json:"path"`
	} `json:"versions"`
}

// serves reports whether a retake server that knows tokens serves video's family on port.
func serves(port int, video string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/info", port))
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	var info infoProbe
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&info) != nil ||
		info.Role == nil {
		return false
	}
	if family.SamePath(info.Path, video) {
		return true
	}
	for _, v := range info.Versions {
		if family.SamePath(v.Path, video) {
			return true
		}
	}
	return false
}

func ping(port int) bool {
	c := http.Client{Timeout: 5 * time.Second}
	res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// server attaches to the running server of the family, or starts one.
func (s *share) server(cfg config.Config, port *int) error {
	zero := 0.0
	sets := config.Resolve(cfg, config.Flags{Port: port, IdleExit: &zero, NoOpen: true})
	tries := s.env.PortTries
	if tries <= 0 {
		tries = config.PortTries
	}
	n := tries
	if sets.PortStrict {
		n = 1
	}
	for p := sets.Port; p < sets.Port+n && p <= 65535; p++ {
		if p > 0 && (s.folder && servesFolder(p, s.video) || !s.folder && serves(p, s.video)) {
			s.port, s.attached = p, true
			return nil
		}
	}
	ln, msg := listen(sets.Port, sets.PortStrict, tries)
	if ln == nil {
		return errors.New(msg)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	var srv interface {
		Serve(ctx context.Context, ln net.Listener) (bool, error)
		Close() error
	}
	dir, named := localThemeDir(s.env, s.a.theme)
	page := withTheme(s.env, ext.Page(ui.Embedded()), dir, named)
	if s.folder {
		srv = newFolder(s.env, cfg, sets, hub.FolderOptions{Dir: s.video, Port: s.port, UI: page,
			Verbose: s.a.verbose, Exts: localExts(s.env, cfg, s.a.exts, s.video)})
	} else {
		srv = newServer(s.env, cfg, sets, server.Options{Video: s.video, Port: s.port, UI: page,
			Verbose: s.a.verbose, Exts: localExts(s.env, cfg, s.a.exts, "")})
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.srvDone = make(chan struct{})
	go func() {
		defer close(s.srvDone)
		if _, err := srv.Serve(ctx, ln); err != nil {
			_, _ = fmt.Fprintf(s.env.Stderr, "retake share: %v\n", err)
		}
		if err := srv.Close(); err != nil {
			_, _ = fmt.Fprintf(s.env.Stderr, "retake share: closing the project store: %v\n", err)
		}
	}()
	s.stopSrv = func() {
		cancel()
		<-s.srvDone
	}
	return nil
}

func (s *share) tunnelBin(cfg config.Config) string {
	if s.a.bin != "" {
		return absPath(s.a.bin, s.env.Getenv)
	}
	var p *string
	switch s.a.tunnel {
	case tunnel.Cloudflare:
		p = cfg.Share.Cloudflared
	case tunnel.Tailscale:
		p = cfg.Share.Tailscale
	}
	if p != nil && *p != "" {
		return absPath(*p, s.env.Getenv)
	}
	return ""
}

func (s *share) fail(err error) int {
	if s.stopSrv != nil {
		s.stopSrv()
	}
	_, _ = fmt.Fprintf(s.env.Stderr, "retake share: %v\n", err)
	return ExitError
}

func (s *share) run(ctx context.Context, port *int) int {
	env := s.env
	cfg := loadConfig(env)
	if err := s.server(cfg, port); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake share: %v\n", err)
		return ExitUsage
	}
	host := s.a.host
	var tun *tunnel.Tunnel
	if s.a.tunnel != tunnel.None {
		log := io.Discard
		if s.a.verbose {
			log = env.Stderr
		}
		t, err := tunnel.Start(ctx, tunnel.Options{Kind: s.a.tunnel, Bin: s.tunnelBin(cfg), Port: s.port,
			Funnel: s.a.funnel, Log: log})
		if errors.Is(err, context.Canceled) {
			return s.fail(errors.New("stopped before the tunnel was up"))
		}
		if err != nil {
			return s.fail(err)
		}
		tun, host = t, t.Host
		defer func() { _ = tun.Stop() }()
	}
	pid := os.Getpid()
	if err := s.st.AddHost(host, s.a.tunnel, pid); err != nil {
		return s.fail(err)
	}
	defer func() { _ = s.st.RemoveHost(host, pid) }()
	tok, row, err := newToken(s.st, s.role, s.a.label, s.ttl)
	if err != nil {
		return s.fail(err)
	}
	link := "https://" + host + "/s/" + tok
	s.print(link, host, row)
	if env.Ready != nil {
		env.Ready(link)
	}

	every := env.SharePing
	if every <= 0 {
		every = DefaultSharePing
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	var tunDone <-chan struct{}
	if tun != nil {
		tunDone = tun.Done()
	}
	misses := 0
	var reason error
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case <-tunDone:
			// Ctrl+C in a terminal interrupts the tunnel too (it is in our process group), and it may exit first:
			// that is a normal stop once our own interrupt arrives.
			select {
			case <-ctx.Done():
				break wait
			case <-time.After(tunnelGrace):
			}
			reason = tun.Err()
			if reason == nil {
				reason = errors.New("the tunnel stopped")
			}
			break wait
		case <-s.srvDone:
			reason = errors.New("the server stopped")
			s.stopSrv = nil
			break wait
		case <-tick.C:
			if !s.attached {
				continue
			}
			// Contact keeps the attached server from idling out while the share runs.
			if ping(s.port) {
				misses = 0
			} else if misses++; misses >= 3 {
				reason = fmt.Errorf("the server on port %d stopped", s.port)
				break wait
			}
		}
	}
	if tun != nil {
		_ = tun.Stop()
	}
	_ = s.st.RemoveHost(host, pid)
	if s.stopSrv != nil {
		s.stopSrv()
	}
	if reason != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake share: %v\n", reason)
		return ExitError
	}
	if !s.a.asJSON {
		_, _ = fmt.Fprintf(env.Stdout, "\nretake: stopped sharing. Token #%d is still valid (%s); share revoke ends it\n", row.ID,
			untilText(row))
	}
	return ExitOK
}

// shareJSON is retake share --json's line.
type shareJSON struct {
	ID       int64  `json:"id"`
	Role     string `json:"role"`
	Label    string `json:"label"`
	Expires  string `json:"expires"`
	Host     string `json:"host"`
	URL      string `json:"url"`
	Port     int    `json:"port"`
	Attached bool   `json:"attached"`
}

func (s *share) print(link, host string, row project.Token) {
	w := s.env.Stdout
	if s.a.asJSON {
		b, _ := json.Marshal(shareJSON{ID: row.ID, Role: row.Role, Label: row.Label, Expires: row.Expires, Host: host,
			URL: link, Port: s.port, Attached: s.attached})
		_, _ = fmt.Fprintln(w, string(b))
		return
	}
	_, _ = fmt.Fprintf(w, "retake: sharing %s with %s %s link%s, %s\n", s.given, article(row.Role), row.Role, labelText(row),
		untilText(row))
	_, _ = fmt.Fprintf(w, "  link   %s\n", link)
	if !s.a.noQR {
		_, _ = fmt.Fprintln(w)
		_ = qrterm.Write(w, link, "  ")
		_, _ = fmt.Fprintln(w)
	}
	how := "started for this share"
	if s.attached {
		how = "already running"
	}
	_, _ = fmt.Fprintf(w, "  server http://127.0.0.1:%d/ (%s)\n", s.port, how)
	_, _ = fmt.Fprintf(w, "  token  #%d; end it early with: retake share revoke %s %d\n", row.ID, s.given, row.ID)
	_, _ = fmt.Fprintln(w, "  Ctrl+C stops sharing")
}

func tokenCmd(_ context.Context, args []string, env Env) int {
	if len(args) > 0 {
		switch args[0] {
		case "create":
			return tokenCreateCmd(args[1:], env)
		case "list":
			return tokenListCmd(args[1:], env, "token list")
		case "revoke":
			return tokenRevokeCmd(args[1:], env, "token revoke")
		}
	}
	return usageError(env, "token", errors.New("use token create, token list or token revoke"))
}

// tokenJSON is retake token create --json's output.
type tokenJSON struct {
	project.Token
	Secret string `json:"token"`
	URL    string `json:"url"` // the share link, or the /mcp endpoint for an agent
}

func tokenCreateCmd(args []string, env Env) int {
	var asJSON bool
	var role, label, host string
	expires := "30d"
	f := flags{bools: map[string]*bool{"--json": &asJSON},
		values: map[string]*string{"--role": &role, "--label": &label, "--expires": &expires, "--host": &host}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	if err == nil && role == "" {
		err = errors.New("--role is required: viewer, reviewer, approver or agent")
	}
	var r team.Role
	if err == nil {
		r, err = team.ParseRole(role)
	}
	var ttl time.Duration
	if err == nil {
		ttl, err = parseExpiry(expires)
	}
	if err == nil && host != "" {
		host, err = bareHost("--host", host)
	}
	if err == nil && r == team.Agent && isDir(absPath(pos[0], env.Getenv)) {
		err = errors.New("an agent works on one project: retake token create <video> --role agent " +
			"(it then reaches /p/<name>/mcp of the folder's server too)")
	}
	if err != nil {
		return usageError(env, "token create", err)
	}
	st, _, code := openStore(env, "token create", pos[0])
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()
	base := ""
	switch {
	case host != "":
		base = "https://" + host
	default:
		if hosts := liveHosts(st); len(hosts) > 0 {
			base = "https://" + hosts[0].Host
		}
	}
	tok, row, err := newToken(st, r, label, ttl)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake token create: %v\n", err)
		return ExitError
	}
	url := ""
	switch {
	case r == team.Agent && base == "":
		sets := config.Resolve(loadConfig(env), config.Flags{})
		url = fmt.Sprintf("http://127.0.0.1:%d/mcp", sets.Port)
	case r == team.Agent:
		url = base + "/mcp"
	case base != "":
		url = base + "/s/" + tok
	}
	if asJSON {
		_ = printJSON(env.Stdout, tokenJSON{Token: row, Secret: tok, URL: url})
		return ExitOK
	}
	w := env.Stdout
	_, _ = fmt.Fprintf(w, "token #%d (%s%s, %s):\n  %s\n", row.ID, row.Role, labelText(row), untilText(row), tok)
	_, _ = fmt.Fprintf(w, "It is shown only this once. Revoke it with: retake token revoke %s %d\n", pos[0], row.ID)
	switch {
	case r == team.Agent:
		_, _ = fmt.Fprintf(w, "\nClaude Code:\n  claude mcp add --transport http retake %s --header \"Authorization: Bearer %s\"\n",
			url, tok)
		_, _ = fmt.Fprintf(w, "Codex:\n  export RETAKE_TOKEN=%s\n  codex mcp add retake --url %s --bearer-token-env-var RETAKE_TOKEN\n",
			tok, url)
		if base == "" {
			_, _ = fmt.Fprintln(w, "(That is the local server; for an agent elsewhere, share the review and use its host: --host H.)")
		}
	case url != "":
		_, _ = fmt.Fprintf(w, "link: %s\n", url)
	default:
		_, _ = fmt.Fprintln(w, "Open it as https://<your share's host>/s/<token>, or send it as Authorization: Bearer.")
	}
	return ExitOK
}

// tokenView is a token as share list shows it.
type tokenView struct {
	project.Token
	State string `json:"state"`
}

func tokenListCmd(args []string, env Env, cmd string) int {
	var asJSON bool
	pos, help, err := flags{bools: map[string]*bool{"--json": &asJSON}}.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	if err != nil {
		return usageError(env, cmd, err)
	}
	st, _, code := openStore(env, cmd, pos[0])
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()
	tokens, err := st.Tokens()
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
		return ExitError
	}
	hosts := liveHosts(st)
	now := time.Now()
	views := make([]tokenView, len(tokens))
	for i, t := range tokens {
		views[i] = tokenView{t, t.State(now)}
	}
	if asJSON {
		_ = printJSON(env.Stdout, struct {
			Tokens []tokenView    `json:"tokens"`
			Hosts  []project.Host `json:"hosts"`
		}{views, hosts})
		return ExitOK
	}
	if len(views) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "no tokens")
	}
	for _, v := range views {
		state := v.State
		if v.State == project.TokenActive {
			state = untilText(v.Token)
		}
		label := v.Label
		if label == "" {
			label = "-"
		}
		_, _ = fmt.Fprintf(env.Stdout, "#%-3d %-8s %-12s %-24s made %s\n", v.ID, v.Role, label, state, fmtWhen(v.Created))
	}
	for _, h := range hosts {
		_, _ = fmt.Fprintf(env.Stdout, "sharing at https://%s (%s, pid %d)\n", h.Host, h.Tunnel, h.PID)
	}
	return ExitOK
}

func tokenRevokeCmd(args []string, env Env, cmd string) int {
	var asJSON bool
	pos, help, err := flags{bools: map[string]*bool{"--json": &asJSON}}.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 2 {
		err = errors.New("give the video and the token's id (from share list)")
	}
	var id int64
	if err == nil {
		id, err = strconv.ParseInt(strings.TrimPrefix(pos[1], "#"), 10, 64)
		if err != nil || id <= 0 {
			err = fmt.Errorf("%q is not a token id", pos[1])
		}
	}
	if err != nil {
		return usageError(env, cmd, err)
	}
	st, _, code := openStore(env, cmd, pos[0])
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()
	t, err := st.RevokeToken(id)
	if errors.Is(err, project.ErrNotFound) {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: no token #%d\n", cmd, id)
		return ExitError
	}
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
		return ExitError
	}
	if asJSON {
		_ = printJSON(env.Stdout, tokenView{t, t.State(time.Now())})
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stdout, "revoked #%d (%s%s): it stops working now\n", t.ID, t.Role, labelText(t))
	return ExitOK
}

// article is "an" before a vowel sound (an approver), else "a" (a viewer, a reviewer).
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}
