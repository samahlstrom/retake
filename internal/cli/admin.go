package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/team"
)

// retake user ... and retake project ...: the admin commands of spec/server.md. They work on the server's stores
// directly (the same settings as retake serve), so run them where the server runs: docker compose exec retake ...

// adminCmd is what every admin command shares: its flags, its parsed settings and its stores.
type adminCmd struct {
	name   string
	env    Env
	sf     serverFlags
	f      flags
	asJSON bool
	s      serverSettings
	st     *hub.Stores
}

func newAdmin(name string, env Env) *adminCmd {
	a := &adminCmd{name: name, env: env}
	a.f = flags{bools: map[string]*bool{"--json": &a.asJSON}, values: map[string]*string{},
		multi: map[string]*[]string{}}
	a.sf.add(&a.f)
	return a
}

// parse parses args and opens the stores. want is the number of positionals. It returns an exit code and false
// when the command can't go on (help was shown, or the arguments or stores were bad).
func (a *adminCmd) parse(ctx context.Context, args []string, want int, what string) ([]string, int, bool) {
	pos, help, err := a.f.parse(args)
	if help {
		_, _ = io.WriteString(a.env.Stdout, usage)
		return nil, ExitOK, false
	}
	if err == nil && len(pos) != want {
		err = errors.New(what)
	}
	if err == nil {
		a.s, err = resolveServer(a.env, loadConfig(a.env), a.sf)
	}
	if err != nil {
		return nil, usageError(a.env, a.name, err), false
	}
	if a.st, err = openStores(ctx, a.s, false); err != nil {
		_, _ = fmt.Fprintf(a.env.Stderr, "retake %s: %v\n", a.name, err)
		return nil, ExitError, false
	}
	return pos, ExitOK, true
}

func (a *adminCmd) close() {
	if a.st != nil {
		_ = a.st.Close()
	}
}

func (a *adminCmd) failed(err error) int {
	if a.asJSON {
		_ = printJSON(a.env.Stdout, map[string]string{"error": err.Error()})
	} else {
		_, _ = fmt.Fprintf(a.env.Stderr, "retake %s: %v\n", a.name, err)
	}
	return ExitError
}

func (a *adminCmd) out(v any, text string) int {
	if a.asJSON {
		_ = printJSON(a.env.Stdout, v)
	} else {
		_, _ = io.WriteString(a.env.Stdout, text)
	}
	return ExitOK
}

func userCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		return usageError(env, "user", errors.New("use user add, link, list, remove, disable, enable, signout or sessions"))
	}
	switch args[0] {
	case "add", "link":
		return userLinkCmd(ctx, args[0], args[1:], env)
	case "list":
		a := newAdmin("user list", env)
		defer a.close()
		if _, code, ok := a.parse(ctx, args[1:], 0, "user list takes no arguments"); !ok {
			return code
		}
		us, err := a.st.Accounts.Users()
		if err != nil {
			return a.failed(err)
		}
		var b strings.Builder
		for _, u := range us {
			flags := ""
			if u.Admin {
				flags += " admin"
			}
			if u.Disabled != "" {
				flags += " disabled"
			}
			fmt.Fprintf(&b, "%-32s %s%s\n", u.Email, u.Name, flags)
		}
		if len(us) == 0 {
			b.WriteString("no users yet\n")
		}
		return a.out(map[string]any{"users": us}, b.String())
	case "remove", "disable", "enable", "signout", "sessions":
		return userOpCmd(ctx, args[0], args[1:], env)
	}
	return usageError(env, "user", fmt.Errorf("unknown user command %q", args[0]))
}

// userLinkCmd is retake user add (a new user and their first link) and retake user link (a new link).
func userLinkCmd(ctx context.Context, op string, args []string, env Env) int {
	a := newAdmin("user "+op, env)
	defer a.close()
	var name, expires string
	var admin bool
	a.f.values["--expires"] = &expires
	if op == "add" {
		a.f.values["--name"] = &name
		a.f.bools["--admin"] = &admin
	}
	pos, code, ok := a.parse(ctx, args, 1, "give one email address")
	if !ok {
		return code
	}
	ttl := time.Duration(0)
	if expires != "" {
		var err error
		if ttl, err = hub.TokenTTL(expires, team.Viewer); err != nil || ttl == 0 {
			return usageError(env, a.name, errors.New("--expires: a duration like 24h or 7d, at most 7d"))
		}
	}
	var u accounts.User
	var err error
	if op == "add" {
		u, err = a.st.Accounts.CreateUser(pos[0], name, admin)
		if errors.Is(err, accounts.ErrExists) {
			return a.failed(fmt.Errorf("%s already has an account: make a new link with retake user link", pos[0]))
		}
	} else {
		u, err = a.st.Accounts.User(pos[0])
	}
	if err != nil {
		return a.failed(err)
	}
	code2, exp, err := a.st.Accounts.CreateLink(u.ID, ttl)
	if err != nil {
		return a.failed(err)
	}
	link := hub.LinkURL(linkBase(a.s, a.st), code2)
	res := map[string]any{"user": u, "expires": exp.UTC().Format(time.RFC3339)}
	mail, err := a.s.mailer()
	if err != nil {
		return a.failed(err)
	}
	if mail != nil {
		if err := hub.MailLink(ctx, mail, u, link, exp); err != nil {
			return a.failed(fmt.Errorf("mailing the link: %w", err))
		}
		res["mailed"] = true
		return a.out(res, fmt.Sprintf("mailed a sign-in link to %s (it works once, until %s)\n", u.Email,
			exp.Local().Format("2 Jan 15:04")))
	}
	res["link"] = link
	verb := "added"
	if op == "link" {
		verb = "a new link for"
	}
	who := u.Email
	if u.Admin {
		who += " (admin)"
	}
	return a.out(res, fmt.Sprintf("%s %s. Their sign-in link, which works once, until %s:\n  %s\n", verb, who,
		exp.Local().Format("2 Jan 15:04"), link))
}

func userOpCmd(ctx context.Context, op string, args []string, env Env) int {
	a := newAdmin("user "+op, env)
	defer a.close()
	pos, code, ok := a.parse(ctx, args, 1, "give one email address")
	if !ok {
		return code
	}
	u, err := a.st.Accounts.User(pos[0])
	if err != nil {
		return a.failed(err)
	}
	switch op {
	case "remove":
		if err := a.st.Accounts.RemoveUser(u.Email); err != nil {
			return a.failed(err)
		}
		return a.out(map[string]any{"ok": true}, "removed "+u.Email+"\n")
	case "disable", "enable":
		if _, err := a.st.Accounts.SetDisabled(u.Email, op == "disable"); err != nil {
			return a.failed(err)
		}
		return a.out(map[string]any{"ok": true}, op+"d "+u.Email+"\n")
	case "signout":
		n, err := a.st.Accounts.EndSessions(u.ID)
		if err != nil {
			return a.failed(err)
		}
		return a.out(map[string]any{"ended": n}, fmt.Sprintf("ended %s of %s\n", plural(n, "session"), u.Email))
	default: // sessions
		ss, err := a.st.Accounts.Sessions(u.ID)
		if err != nil {
			return a.failed(err)
		}
		var b strings.Builder
		for _, x := range ss {
			fmt.Fprintf(&b, "#%d  since %s  last %s  until %s  %s %s\n", x.ID, x.Created, x.Seen, x.Expires, x.IP,
				x.Agent)
		}
		if len(ss) == 0 {
			b.WriteString("no sessions\n")
		}
		return a.out(map[string]any{"sessions": ss}, b.String())
	}
}

func projectCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		return usageError(env, "project", errors.New("use project add, list, member or token"))
	}
	switch args[0] {
	case "add":
		return projectAddCmd(ctx, args[1:], env)
	case "list":
		a := newAdmin("project list", env)
		defer a.close()
		if _, code, ok := a.parse(ctx, args[1:], 0, "project list takes no arguments"); !ok {
			return code
		}
		ps, err := a.st.Accounts.Projects()
		if err != nil {
			return a.failed(err)
		}
		var b strings.Builder
		for _, p := range ps {
			fmt.Fprintf(&b, "%-24s %s\n", p.Name, p.Title)
		}
		if len(ps) == 0 {
			b.WriteString("no projects yet\n")
		}
		return a.out(map[string]any{"projects": ps}, b.String())
	case "member":
		return memberCmd(ctx, args[1:], env)
	case "token":
		return projectTokenCmd(ctx, args[1:], env)
	}
	return usageError(env, "project", fmt.Errorf("unknown project command %q", args[0]))
}

func projectAddCmd(ctx context.Context, args []string, env Env) int {
	a := newAdmin("project add", env)
	defer a.close()
	var title, video, from string
	a.f.values["--title"] = &title
	a.f.values["--video"] = &video
	a.f.values["--from"] = &from
	pos, code, ok := a.parse(ctx, args, 1, "give the project's name")
	if !ok {
		return code
	}
	if video != "" && from != "" {
		return usageError(env, a.name, errors.New("give --video or --from, not both"))
	}
	if video != "" {
		if video = absPath(video, env.Getenv); !isFile(video) {
			return usageError(env, a.name, fmt.Errorf("no such file: %s", video))
		}
	}
	if from != "" {
		if from = absPath(from, env.Getenv); !isDir(from) {
			return usageError(env, a.name, fmt.Errorf("no such folder: %s", from))
		}
	}
	root := a.st.Workspace(pos[0])
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 && from != "" {
		return a.failed(fmt.Errorf("%s isn't empty", root))
	}
	if from != "" {
		if !accounts.ValidName(pos[0]) {
			return a.failed(fmt.Errorf("project name %q: use lower-case letters, digits and -", pos[0]))
		}
		if _, err := a.st.Accounts.Project(pos[0]); err == nil {
			return a.failed(fmt.Errorf("project %s: %w", pos[0], accounts.ErrExists))
		}
		if err := hub.CopyTree(from, root, a.s.Database == ""); err != nil {
			return a.failed(fmt.Errorf("copying %s: %w", from, err))
		}
	}
	p, err := a.st.CreateProject(ctx, pos[0], title)
	if err != nil {
		return a.failed(err)
	}
	res := map[string]any{"project": p, "workspace": root, "url": linkBase(a.s, a.st) + "/p/" + p.Name + "/"}
	text := fmt.Sprintf("added project %s: %s\n", p.Name, res["url"])
	if video != "" {
		st, err := a.st.ProjectStore(ctx, p)
		if err != nil {
			return a.failed(err)
		}
		defer func() { _ = st.Close() }()
		row, err := hub.FirstVersion(st, root, p.Name, video, "", "admin", "", false)
		if err != nil {
			return a.failed(fmt.Errorf("adding %s: %w", video, err))
		}
		res["version"] = row
		text += fmt.Sprintf("  %s is %s\n", row.Label, row.Path)
	}
	return a.out(res, text)
}

// projectRow finds a project by name for an admin command.
func (a *adminCmd) projectRow(name string) (accounts.Project, error) {
	p, err := a.st.Accounts.Project(name)
	if errors.Is(err, accounts.ErrNotFound) {
		return p, fmt.Errorf("no project %s (retake project list)", name)
	}
	return p, err
}

func memberCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] != "add" && args[0] != "remove" && args[0] != "list" {
		return usageError(env, "project member", errors.New("use project member add, remove or list"))
	}
	op := args[0]
	a := newAdmin("project member "+op, env)
	defer a.close()
	var role string
	want, what := 2, "give the project and the email address"
	if op == "add" {
		a.f.values["--role"] = &role
	}
	if op == "list" {
		want, what = 1, "give the project"
	}
	pos, code, ok := a.parse(ctx, args[1:], want, what)
	if !ok {
		return code
	}
	p, err := a.projectRow(pos[0])
	if err != nil {
		return a.failed(err)
	}
	if op == "list" {
		ms, err := a.st.Accounts.Members(p.ID)
		if err != nil {
			return a.failed(err)
		}
		var b strings.Builder
		for _, m := range ms {
			fmt.Fprintf(&b, "%-32s %s\n", m.User.Email, m.Role)
		}
		if len(ms) == 0 {
			b.WriteString("no members yet (admins own every project)\n")
		}
		return a.out(map[string]any{"members": ms}, b.String())
	}
	u, err := a.st.Accounts.User(pos[1])
	if errors.Is(err, accounts.ErrNotFound) {
		return a.failed(fmt.Errorf("no user %s: add them first with retake user add", pos[1]))
	}
	if err != nil {
		return a.failed(err)
	}
	if op == "remove" {
		if err := a.st.Accounts.RemoveMember(p.ID, u.ID); err != nil {
			return a.failed(err)
		}
		return a.out(map[string]any{"ok": true}, fmt.Sprintf("removed %s from %s\n", u.Email, p.Name))
	}
	if role == "" {
		return usageError(env, a.name, errors.New("--role is required: viewer, reviewer, approver or owner"))
	}
	r, err := accounts.ParseRole(role)
	if err != nil {
		return usageError(env, a.name, err)
	}
	if err := a.st.Accounts.SetMember(p.ID, u.ID, r); err != nil {
		return a.failed(err)
	}
	return a.out(map[string]any{"ok": true, "role": r}, fmt.Sprintf("%s is %s %s of %s\n", u.Email, article(string(r)),
		r, p.Name))
}

func projectTokenCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] != "create" && args[0] != "list" && args[0] != "revoke" {
		return usageError(env, "project token", errors.New("use project token create, list or revoke"))
	}
	op := args[0]
	a := newAdmin("project token "+op, env)
	defer a.close()
	var role, label, expires string
	want, what := 1, "give the project"
	switch op {
	case "create":
		a.f.values["--role"], a.f.values["--label"], a.f.values["--expires"] = &role, &label, &expires
	case "revoke":
		want, what = 2, "give the project and the token's id"
	}
	pos, code, ok := a.parse(ctx, args[1:], want, what)
	if !ok {
		return code
	}
	row, err := a.projectRow(pos[0])
	if err != nil {
		return a.failed(err)
	}
	st, err := a.st.ProjectStore(ctx, row)
	if err != nil {
		return a.failed(err)
	}
	defer func() { _ = st.Close() }()
	switch op {
	case "list":
		ts, err := st.Tokens()
		if err != nil {
			return a.failed(err)
		}
		var b strings.Builder
		now := time.Now()
		for _, t := range ts {
			fmt.Fprintf(&b, "#%d  %-8s %-8s %s\n", t.ID, t.Role, t.State(now), t.Label)
		}
		if len(ts) == 0 {
			b.WriteString("no tokens\n")
		}
		return a.out(map[string]any{"tokens": ts}, b.String())
	case "revoke":
		id, err := strconv.ParseInt(pos[1], 10, 64)
		if err != nil {
			return usageError(env, a.name, fmt.Errorf("%q is not a token id", pos[1]))
		}
		t, err := st.RevokeToken(id)
		if err != nil {
			return a.failed(err)
		}
		return a.out(t, fmt.Sprintf("revoked token #%d (%s)\n", t.ID, t.Role))
	}
	if role == "" {
		return usageError(env, a.name, errors.New("--role is required: viewer, reviewer, approver or agent"))
	}
	r, err := team.ParseRole(role)
	if err != nil {
		return usageError(env, a.name, err)
	}
	ttl, err := hub.TokenTTL(expires, r)
	if err != nil {
		return usageError(env, a.name, err)
	}
	base := linkBase(a.s, a.st)
	res, err := hub.MakeToken(st, base, row.Name, r, label, ttl)
	if err != nil {
		return a.failed(err)
	}
	var b strings.Builder
	if r == team.Agent {
		fmt.Fprintf(&b, "agent token #%d for %s (shown once):\n  %s\n\n", res.ID, row.Name, res.Value)
		fmt.Fprintf(&b, "push from the editor's machine:\n  retake remote add %s %s --token %s\n", row.Name, res.URL,
			res.Value)
		fmt.Fprintf(&b, "  retake push --remote %s clip_v2.mp4 --change 1=\"what changed\"\n\n", row.Name)
		fmt.Fprintf(&b, "MCP (Claude Code, Codex):\n  claude mcp add --transport http retake %s/mcp --header "+
			"\"Authorization: Bearer %s\"\n", res.URL, res.Value)
		fmt.Fprintf(&b, "  export RETAKE_TOKEN=%s\n  codex mcp add retake --url %s/mcp --bearer-token-env-var "+
			"RETAKE_TOKEN\n", res.Value, res.URL)
	} else {
		fmt.Fprintf(&b, "%s link #%d for %s (shown once):\n  %s\n", r, res.ID, row.Name, res.URL)
	}
	if res.Expires != "" {
		fmt.Fprintf(&b, "it expires %s; retake project token revoke %s %d ends it sooner\n", res.Expires, row.Name, res.ID)
	}
	return a.out(res, b.String())
}
