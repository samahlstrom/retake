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
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/tus"
	"github.com/samahlstrom/retake/internal/userdir"
)

// Remotes and retake push --remote (spec/server.md, "CLI: remotes").

// Remote is a project on a team server, and the agent token that pushes to it.
type Remote struct {
	URL   string `toml:"url" json:"url"`
	Token string `toml:"token" json:"-"`
}

type remotesFile struct {
	Remote map[string]Remote `toml:"remote"`
}

func remotesPath(getenv func(string) string) (string, error) {
	p, err := config.Path(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "remotes.toml"), nil
}

func loadRemotes(getenv func(string) string) (map[string]Remote, string, error) {
	p, err := remotesPath(getenv)
	if err != nil {
		return nil, "", err
	}
	var f remotesFile
	if _, err := toml.DecodeFile(p, &f); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, p, fmt.Errorf("%s: %w", p, err)
	}
	if f.Remote == nil {
		f.Remote = map[string]Remote{}
	}
	return f.Remote, p, nil
}

// saveRemotes writes the remotes file, readable only by its owner (it holds tokens).
func saveRemotes(p string, rs map[string]Remote) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# retake remotes: team server projects and the agent tokens that push to them. Keep it private.\n")
	if err := toml.NewEncoder(&b).Encode(remotesFile{Remote: rs}); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

var remoteNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// projectURL checks a remote's URL: http(s), a host, and a /p/<name> path.
func projectURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%q isn't a project URL like https://review.example.com/p/demo", raw)
	}
	p := strings.TrimSuffix(u.Path, "/")
	if m, _ := path.Match("/p/*", p); !m {
		return "", fmt.Errorf("%q isn't a project URL: its path must be /p/<project>", raw)
	}
	return u.Scheme + "://" + u.Host + p, nil
}

func remoteCmd(_ context.Context, args []string, env Env) int {
	if len(args) == 0 {
		return usageError(env, "remote", errors.New("use remote add, list or remove"))
	}
	var asJSON bool
	var token string
	f := flags{bools: map[string]*bool{"--json": &asJSON}, values: map[string]*string{"--token": &token}}
	pos, help, err := f.parse(args[1:])
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err != nil {
		return usageError(env, "remote", err)
	}
	rs, p, err := loadRemotes(env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake remote: %v\n", err)
		return ExitError
	}
	switch args[0] {
	case "add":
		if len(pos) != 2 {
			return usageError(env, "remote add", errors.New("give a name and the project's URL"))
		}
		if !remoteNameRE.MatchString(pos[0]) {
			return usageError(env, "remote add", fmt.Errorf("%q: name a remote with letters, digits, . _ and -", pos[0]))
		}
		u, err := projectURL(pos[1])
		if err != nil {
			return usageError(env, "remote add", err)
		}
		if token == "" {
			token = env.Getenv("RETAKE_TOKEN")
		}
		if token == "" {
			return usageError(env, "remote add", errors.New("--token is required: an agent token of the project "+
				"(retake project token create <project> --role agent, on the server)"))
		}
		rs[pos[0]] = Remote{URL: u, Token: strings.TrimSpace(token)}
		if err := saveRemotes(p, rs); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake remote add: %v\n", err)
			return ExitError
		}
		if asJSON {
			_ = printJSON(env.Stdout, map[string]string{"name": pos[0], "url": u})
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "remote %s is %s\n", pos[0], u)
		}
		return ExitOK
	case "list":
		names := make([]string, 0, len(rs))
		for n := range rs {
			names = append(names, n)
		}
		sort.Strings(names)
		if asJSON {
			out := map[string]Remote{}
			for _, n := range names {
				out[n] = rs[n]
			}
			_ = printJSON(env.Stdout, map[string]any{"remotes": out})
			return ExitOK
		}
		for _, n := range names {
			_, _ = fmt.Fprintf(env.Stdout, "%-16s %s\n", n, rs[n].URL)
		}
		if len(names) == 0 {
			_, _ = fmt.Fprintln(env.Stdout, "no remotes (retake remote add <name> <project URL> --token <agent token>)")
		}
		return ExitOK
	case "remove":
		if len(pos) != 1 {
			return usageError(env, "remote remove", errors.New("give the remote's name"))
		}
		if _, ok := rs[pos[0]]; !ok {
			_, _ = fmt.Fprintf(env.Stderr, "retake remote remove: no remote %s\n", pos[0])
			return ExitError
		}
		delete(rs, pos[0])
		if err := saveRemotes(p, rs); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake remote remove: %v\n", err)
			return ExitError
		}
		_, _ = fmt.Fprintf(env.Stdout, "removed remote %s\n", pos[0])
		return ExitOK
	}
	return usageError(env, "remote", fmt.Errorf("unknown remote command %q", args[0]))
}

// pushesRemote reports whether a push's arguments name a remote.
func pushesRemote(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--remote" || strings.HasPrefix(a, "--remote=") {
			return true
		}
	}
	return false
}

// pendingUpload is an upload a push started, so a second push of the same file resumes it.
type pendingUpload struct {
	Remote string `json:"remote"`
	URL    string `json:"url"` // the remote's project URL
	File   string `json:"file"`
	Size   int64  `json:"size"`
	MTime  string `json:"mtime"`
	SHA256 string `json:"sha256"`
	Upload string `json:"upload"` // the upload's URL
}

func statePath(getenv func(string) string) string {
	dir := getenv("XDG_STATE_HOME")
	if dir == "" || !filepath.IsAbs(dir) {
		dir = userdir.Local(getenv) // Windows: %LOCALAPPDATA%
	}
	if dir == "" {
		home := userdir.Home(getenv)
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "retake", "uploads.json")
}

func loadPending(p string) []pendingUpload {
	var out []pendingUpload
	if b, err := os.ReadFile(p); err == nil { //nolint:gosec // our own state file
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func savePending(p string, list []pendingUpload) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (u pendingUpload) same(o pendingUpload) bool {
	return u.Remote == o.Remote && u.URL == o.URL && u.File == o.File && u.Size == o.Size && u.MTime == o.MTime &&
		u.SHA256 == o.SHA256
}

// remotePushCmd is retake push --remote <name> <file> ...: upload the file (tus, resumable), then push it.
func remotePushCmd(ctx context.Context, args []string, env Env) int {
	var asJSON bool
	var remote, parent, label string
	var changes, asks []string
	f := flags{bools: map[string]*bool{"--json": &asJSON},
		values: map[string]*string{"--remote": &remote, "--parent": &parent, "--label": &label},
		multi:  map[string]*[]string{"--change": &changes, "--ask": &asks}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give the new version's file (the remote names the project)")
	}
	var ch, ak []project.ChangeEntry
	if err == nil {
		ch, err = parseEntries("--change", changes)
	}
	if err == nil {
		ak, err = parseEntries("--ask", asks)
	}
	if err != nil {
		return usageError(env, "push", err)
	}
	rs, _, err := loadRemotes(env.Getenv)
	if err != nil {
		return opFailed(env, "push", err, asJSON)
	}
	rem, ok := rs[remote]
	if !ok {
		return opFailed(env, "push", fmt.Errorf("no remote %s (retake remote add %s <project URL> --token ...)", remote,
			remote), asJSON)
	}
	file := absPath(pos[0], env.Getenv)
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return opFailed(env, "push", &agent.Error{Code: agent.CodeNotFound, Message: "no such file: " + file}, asJSON)
	}
	progress := env.Stderr
	if asJSON {
		progress = io.Discard
	}
	_, _ = fmt.Fprintf(progress, "retake: hashing %s\n", filepath.Base(file))
	sum, err := tus.FileSHA256(file)
	if err != nil {
		return opFailed(env, "push", err, asJSON)
	}
	key := pendingUpload{Remote: remote, URL: rem.URL, File: file, Size: st.Size(),
		MTime: st.ModTime().UTC().Format(time.RFC3339Nano), SHA256: sum}
	sp := statePath(env.Getenv)
	pending := loadPending(sp)
	resume := ""
	for _, u := range pending {
		if u.same(key) {
			resume = u.Upload
		}
	}
	remember := func(upURL string) {
		list := loadPending(sp)
		out := list[:0]
		for _, u := range list {
			if !u.same(key) {
				out = append(out, u)
			}
		}
		if upURL != "" {
			k := key
			k.Upload = upURL
			out = append(out, k)
		}
		if err := savePending(sp, out); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: remembering the upload: %v\n", err)
		}
	}
	if resume != "" {
		_, _ = fmt.Fprintf(progress, "retake: resuming the upload of %s\n", filepath.Base(file))
	} else {
		_, _ = fmt.Fprintf(progress, "retake: uploading %s (%s) to %s\n", filepath.Base(file), sizeText(st.Size()),
			rem.URL)
	}
	client := &tus.Client{Token: rem.Token, Created: remember}
	upURL, err := client.Upload(ctx, rem.URL+"/api/uploads", file,
		map[string]string{"filename": filepath.Base(file), "sha256": sum}, resume)
	if err != nil {
		return opFailed(env, "push", fmt.Errorf("uploading: %w", err), asJSON)
	}
	if upURL != resume {
		remember(upURL)
	}
	body := map[string]any{"upload": path.Base(upURL), "parent": parent, "label": label}
	cl := []map[string]any{}
	for _, c := range ch {
		cl = append(cl, map[string]any{"note": c.Note, "summary": c.Summary})
	}
	qs := []map[string]any{}
	for _, q := range ak {
		qs = append(qs, map[string]any{"note": q.Note, "question": q.Summary})
	}
	body["changes"], body["asks"] = cl, qs
	var res agent.PushResult
	if err := postJSON(ctx, rem, "/api/push", body, &res); err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Code != agent.CodeFailed {
			ae.Message += " (the upload is kept: fix the push and run it again; the file isn't sent twice)"
		}
		return opFailed(env, "push", err, asJSON)
	}
	remember("")
	if asJSON {
		_ = printJSON(env.Stdout, res)
		return ExitOK
	}
	from := ""
	if res.Version.Parent != "" {
		from = " from " + res.Version.Parent
	}
	_, _ = fmt.Fprintf(env.Stdout, "pushed %s%s: %s/\n", res.Version.Label, from, rem.URL)
	if len(res.Answered) > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "  answered %s\n", ids(res.Answered))
	}
	if len(res.Questions) > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "  asked about %s\n", ids(entryNotes(ak)))
	}
	return ExitOK
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// postJSON posts a JSON body to the remote's project with its token, and decodes the answer into out. A refusal
// comes back as an *agent.Error.
func postJSON(ctx context.Context, rem Remote, route string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rem.URL+route, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rem.Token)
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		var ae agent.Error
		if json.Unmarshal(raw, &ae) == nil && ae.Message != "" {
			if ae.Code == "" {
				ae.Code = agent.CodeFailed
			}
			return &ae
		}
		return fmt.Errorf("the server answered %s", res.Status)
	}
	return json.Unmarshal(raw, out)
}
