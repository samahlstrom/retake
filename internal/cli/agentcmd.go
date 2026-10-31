package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/app"
	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/mcp"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
)

// flags describes a command's options: booleans, single values and repeatable values.
type flags struct {
	bools  map[string]*bool
	values map[string]*string
	multi  map[string]*[]string
}

// parse splits args into positionals and options, which may come in any order (--x v, --x=v; -- ends options).
func (f flags) parse(args []string) (pos []string, help bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(pos, args[i+1:]...), help, nil //nolint:gosec // i < len(args), so i+1 is a valid bound
		}
		if !isFlag(arg) {
			pos = append(pos, arg)
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
		if name == "-h" || name == "--help" {
			help = true
			continue
		}
		if b, ok := f.bools[name]; ok {
			if hasVal {
				return nil, false, fmt.Errorf("%s takes no value", name)
			}
			*b = true
			continue
		}
		_, single := f.values[name]
		_, many := f.multi[name]
		if !single && !many {
			return nil, false, fmt.Errorf("unknown option %s", name)
		}
		if !hasVal {
			rest := args[i+1:] //nolint:gosec // i < len(args), so i+1 is a valid bound
			if len(rest) == 0 {
				return nil, false, fmt.Errorf("%s needs a value", name)
			}
			val = rest[0]
			i++
		}
		if single {
			*f.values[name] = val
		} else {
			*f.multi[name] = append(*f.multi[name], val)
		}
	}
	return pos, help, nil
}

// usageError prints a command's usage problem and returns ExitUsage.
func usageError(env Env, cmd string, err error) int {
	_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n\n%s", cmd, err, usage)
	return ExitUsage
}

// printJSON writes v as indented JSON.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// opFailed reports an operation's error: as {"code", "error", "notes"} on stdout with --json, else on stderr.
func opFailed(env Env, cmd string, err error, asJSON bool) int {
	var ae *agent.Error
	if !errors.As(err, &ae) {
		ae = &agent.Error{Code: agent.CodeFailed, Message: err.Error()}
	}
	if asJSON {
		_ = printJSON(env.Stdout, ae)
	} else {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %s\n", cmd, ae.Message)
	}
	return ExitError
}

func (e Env) prober() family.Prober {
	if e.Prober != nil {
		return e.Prober
	}
	return probe.New(e.Stderr)
}

func (e Env) engines() jobs.Engines {
	if e.Engines != nil {
		return e.Engines(loadConfig(e))
	}
	return app.Engines(loadConfig(e))
}

// openProject opens the project of the video named on the command line.
func openProject(env Env, cmd, video string) (*agent.Project, int) {
	path := absPath(video, env.Getenv)
	if !isFile(path) {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: no such file: %s\n", cmd, path)
		return nil, ExitUsage
	}
	p, err := agent.Open(path, env.prober())
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
		return nil, ExitError
	}
	p.Editor = editorOf(env)
	return p, ExitOK
}

// editorOf says where retake-edit is: [edit] retake_edit, else $RETAKE_EDIT, $PATH, editor/target/release.
func editorOf(env Env) bridge.Editor {
	e := bridge.Editor{Getenv: env.Getenv}
	if c := loadConfig(env); c.Edit.RetakeEdit != nil {
		e.Configured = absPath(*c.Edit.RetakeEdit, env.Getenv)
	}
	return e
}

// videoClock formats video seconds as m:ss.s.
func videoClock(t float64) string {
	return fmt.Sprintf("%d:%04.1f", int64(t/60), modf(t, 60))
}

func noteLine(n agent.NoteView) string {
	line := fmt.Sprintf("#%-3d %-4s %s-%s  %-9s %s: %s", n.ID, n.Version, videoClock(n.T0), videoClock(n.T1), n.Status,
		n.Author, n.Text)
	switch n.Status {
	case project.StatusAnswered, project.StatusAccepted:
		line += fmt.Sprintf("  -> %s: %s", n.AnsweredIn, n.Change)
	case project.StatusQuestion:
		for i := len(n.Threads) - 1; i >= 0; i-- {
			if n.Threads[i].Kind == project.KindQuestion {
				line += "  ? " + n.Threads[i].Text
				break
			}
		}
	}
	var extra []string
	if len(n.Strokes) > 0 {
		extra = append(extra, plural(len(n.Strokes), "stroke"))
	}
	if len(n.Images) > 0 {
		extra = append(extra, plural(len(n.Images), "image"))
	}
	if len(n.Targets) > 0 {
		extra = append(extra, "on "+n.Targets[0].ID)
	}
	if len(extra) > 0 {
		line += "  [" + strings.Join(extra, ", ") + "]"
	}
	return line
}

func answerLine(th project.Thread) string {
	about := "the version"
	if th.Note != nil {
		about = fmt.Sprintf("#%d", *th.Note)
	}
	q := int64(0)
	if th.AnswerTo != nil {
		q = *th.AnswerTo
	}
	return fmt.Sprintf("answer to question %d (%s) from %s: %s", q, about, th.By, th.Text)
}

func notesAgentCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "wait" && args[0] != "sync") {
		return usageError(env, "notes", errors.New("use notes list, notes wait or notes sync"))
	}
	sub, args := args[0], args[1:]
	var asJSON bool
	var status []string
	var version, since, timeout string
	f := flags{bools: map[string]*bool{"--json": &asJSON}, values: map[string]*string{}, multi: map[string]*[]string{}}
	switch sub {
	case "list":
		f.multi["--status"] = &status
		f.values["--version"] = &version
	case "wait":
		f.values["--since"] = &since
		f.values["--timeout"] = &timeout
	}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	if err != nil {
		return usageError(env, "notes "+sub, err)
	}
	var wait agent.WaitArgs
	if sub == "wait" {
		if wait.Since, err = parseSince(since); err == nil {
			wait.Timeout, err = parseSeconds("--timeout", timeout)
		}
		if err != nil {
			return usageError(env, "notes wait", err)
		}
	}
	p, code := openProject(env, "notes "+sub, pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	switch sub {
	case "list":
		res, err := p.NotesList(ctx, agent.ListArgs{Status: status, Version: version})
		if err != nil {
			return opFailed(env, "notes list", err, asJSON)
		}
		if asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		for _, n := range res.Notes {
			_, _ = fmt.Fprintln(env.Stdout, noteLine(n))
		}
		if len(res.Notes) == 0 {
			_, _ = fmt.Fprintln(env.Stdout, "no notes")
		}
		_, _ = fmt.Fprintf(env.Stdout, "cursor %d\n", res.Cursor)
	case "wait":
		res, err := p.NotesWait(ctx, wait)
		if err != nil {
			if ctx.Err() != nil {
				return ExitError
			}
			return opFailed(env, "notes wait", err, asJSON)
		}
		if asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		for _, n := range res.Notes {
			_, _ = fmt.Fprintln(env.Stdout, noteLine(n))
		}
		for _, a := range res.Answers {
			_, _ = fmt.Fprintln(env.Stdout, answerLine(a))
		}
		if res.TimedOut {
			_, _ = fmt.Fprintf(env.Stdout, "nothing new (cursor %d)\n", res.Cursor)
		} else {
			_, _ = fmt.Fprintf(env.Stdout, "cursor %d\n", res.Cursor)
		}
	case "sync":
		return buildNotes(ctx, p, env, asJSON)
	}
	return ExitOK
}

func buildNotes(ctx context.Context, p *agent.Project, env Env, asJSON bool) int {
	fam := p.Family()
	var v family.Version
	for _, x := range fam {
		if x.Current {
			v = x
		}
	}
	res, err := jobs.Rebuild(ctx, p.Store, v, fam, env.engines(), env.Stderr)
	if err != nil {
		return opFailed(env, "notes sync", err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, struct {
			Version string `json:"version"`
			project.SyncResult
		}{v.Label, res})
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stdout, "notes for %s: %d new, %d changed, %d retracted, %d reinstated, %d answers\n",
		v.Label, len(res.Added), len(res.Updated), len(res.Retracted), len(res.Reinstated), len(res.Answers))
	return ExitOK
}

func parseSince(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--since %q is not a cursor", s)
	}
	return n, nil
}

func parseSeconds(name, s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsNaN(f) || f > 1e9 {
		return 0, fmt.Errorf("%s %q is not a number of seconds", name, s)
	}
	return time.Duration(f * float64(time.Second)), nil
}

func parseNoteID(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a note id", s)
	}
	return n, nil
}

func askCmd(ctx context.Context, args []string, env Env) int {
	var asJSON bool
	var options []string
	var version string
	f := flags{bools: map[string]*bool{"--json": &asJSON}, values: map[string]*string{"--version": &version},
		multi: map[string]*[]string{"--option": &options}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 3 {
		err = errors.New(`give the video, the note id and the question: retake ask clip_v7.mp4 3 "which track?"`)
	}
	var note int64
	if err == nil {
		note, err = parseNoteID(pos[1])
	}
	if err != nil {
		return usageError(env, "ask", err)
	}
	p, code := openProject(env, "ask", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	th, err := p.NoteAsk(ctx, agent.AskArgs{Note: note, Text: pos[2], Options: options, Version: version})
	if err != nil {
		return opFailed(env, "ask", err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, th)
		return ExitOK
	}
	about := "version " + th.Version
	if th.Note != nil {
		about = fmt.Sprintf("#%d", *th.Note)
	}
	_, _ = fmt.Fprintf(env.Stdout, "asked about %s (question %d): %s\n", about, th.ID, th.Text)
	return ExitOK
}

func noteCmd(ctx context.Context, args []string, env Env) int {
	var asJSON bool
	var status, in, change string
	f := flags{bools: map[string]*bool{"--json": &asJSON},
		values: map[string]*string{"--status": &status, "--in": &in, "--change": &change}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 2 {
		err = errors.New("give the video and the note id")
	}
	if err == nil && status == "" {
		err = errors.New("--status is required")
	}
	var id int64
	if err == nil {
		id, err = parseNoteID(pos[1])
	}
	if err != nil {
		return usageError(env, "note", err)
	}
	p, code := openProject(env, "note", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	n, err := p.NoteSet(ctx, agent.SetArgs{ID: id, Status: status, In: in, Change: change})
	if err != nil {
		return opFailed(env, "note", err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, n)
		return ExitOK
	}
	_, _ = fmt.Fprintln(env.Stdout, noteLine(n))
	return ExitOK
}

// parseEntries parses repeated ID="text" options.
func parseEntries(name string, vals []string) ([]project.ChangeEntry, error) {
	var out []project.ChangeEntry
	for _, v := range vals {
		id, text, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf(`%s %q: write it as ID="text", e.g. %s 3="title at 120%%"`, name, v, name)
		}
		n, err := parseNoteID(strings.TrimSpace(id))
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s %q: %q is not a note id", name, v, id)
		}
		out = append(out, project.ChangeEntry{Note: n, Summary: text})
	}
	return out, nil
}

func pushCmd(ctx context.Context, args []string, env Env) int {
	var asJSON, move, render, draft, stream, noStream, export bool
	var parent, label string
	var changes, asks []string
	f := flags{bools: map[string]*bool{"--json": &asJSON, "--move": &move, "--render": &render, "--draft": &draft,
		"--stream": &stream, "--no-stream": &noStream, "--export": &export},
		values: map[string]*string{"--parent": &parent, "--label": &label},
		multi:  map[string]*[]string{"--change": &changes, "--ask": &asks}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	switch {
	case err != nil:
	case render && len(pos) != 1:
		err = errors.New("with --render, give only the video (any version of the family): the render is the new file")
	case render && move:
		err = errors.New("--move is for a file you give; a render is always moved in")
	case !render && draft:
		err = errors.New("--draft goes with --render")
	case !render && (stream || noStream || export):
		err = errors.New("--stream, --no-stream and --export go with --render")
	case stream && noStream:
		err = errors.New("--stream or --no-stream, not both")
	case noStream && export:
		err = errors.New("--export is for a streamed render: without streaming the render is one MP4 already")
	case !render && len(pos) != 2:
		err = errors.New("give the video (any version of the family) and the new version's file, or --render")
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
	p, code := openProject(env, "push", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	a := agent.PushArgs{Parent: parent, Label: label, Changes: ch, Move: move, Render: render, Draft: draft,
		Stream: stream, NoStream: noStream, Export: export}
	a.Notify = func(s string) { _, _ = fmt.Fprintln(env.Stderr, s) } // stderr: --json stays one object on stdout
	if !render {
		a.File = absPath(pos[1], env.Getenv)
	}
	for _, x := range ak {
		a.Asks = append(a.Asks, project.PushAsk{Note: x.Note, Text: x.Summary})
	}
	res, err := p.VersionPush(ctx, a)
	if err != nil {
		return opFailed(env, "push", err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, res)
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stdout, "pushed %s from %s: %s\n", res.Version.Label, res.Version.Parent, res.Version.Path)
	if res.Note != "" {
		_, _ = fmt.Fprintf(env.Stdout, "  %s\n", res.Note)
	}
	if r := res.Render; r != nil {
		_, _ = fmt.Fprintf(env.Stdout, "  rendered %s rev %d: %d units, %d reused, %d rendered\n", r.Timeline, r.Rev,
			r.Units.Total, r.Units.Reused, r.Units.Rendered)
		for _, c := range res.Version.Changelog {
			_, _ = fmt.Fprintf(env.Stdout, "  #%d %s", c.Note, c.Summary)
			if len(c.Patches) > 0 {
				_, _ = fmt.Fprintf(env.Stdout, " (%s)", strings.Join(c.Patches, ", "))
			}
			_, _ = fmt.Fprintln(env.Stdout)
		}
		for _, u := range r.Unrequested {
			_, _ = fmt.Fprintf(env.Stdout, "  unrequested %s: %s\n", u.ID, u.Summary)
		}
		for _, s := range r.Skipped {
			_, _ = fmt.Fprintf(env.Stdout, "  skipped %s's answer %s: %s\n", s.Patch, s.Answer, s.Why)
		}
	}
	if len(res.Answered) > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "  answered %s\n", ids(res.Answered))
	}
	if len(res.Questions) > 0 {
		_, _ = fmt.Fprintf(env.Stdout, "  asked about %s\n", ids(entryNotes(ak)))
	}
	return ExitOK
}

func entryNotes(entries []project.ChangeEntry) []int64 {
	out := make([]int64, len(entries))
	for i, e := range entries {
		out[i] = e.Note
	}
	return out
}

func ids(list []int64) string {
	out := make([]string, len(list))
	for i, id := range list {
		out[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(out, " ")
}

func versionsCmd(ctx context.Context, args []string, env Env) int {
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
		return usageError(env, "versions", err)
	}
	p, code := openProject(env, "versions", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	res, err := p.VersionsList(ctx)
	if err != nil {
		return opFailed(env, "versions", err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, res)
		return ExitOK
	}
	for _, v := range res.Versions {
		dur := "?"
		if v.Duration != nil {
			dur = fmtDur(*v.Duration, true)
		}
		line := fmt.Sprintf("%-4s %s  %s  %d open", v.Label, v.Path, dur, v.Open)
		if v.Parent != "" {
			line += fmt.Sprintf("  (from %s by %s, %s)", v.Parent, v.By, plural(len(v.Changelog), "change"))
		}
		if !v.InFamily {
			line += "  [missing]"
		}
		_, _ = fmt.Fprintln(env.Stdout, line)
		for _, c := range v.Changelog {
			line := fmt.Sprintf("       #%d %s", c.Note, c.Summary)
			if len(c.Patches) > 0 {
				line += " (" + strings.Join(c.Patches, ", ") + ")"
			}
			_, _ = fmt.Fprintln(env.Stdout, line)
		}
		if r := v.Render; r != nil {
			for _, u := range r.Unrequested {
				_, _ = fmt.Fprintf(env.Stdout, "       unrequested %s: %s\n", u.ID, u.Summary)
			}
			_, _ = fmt.Fprintf(env.Stdout, "       rendered from %s rev %d: %d units, %d reused\n", r.Timeline, r.Rev,
				r.Units.Total, r.Units.Reused)
		}
	}
	return ExitOK
}

func mcpCmd(ctx context.Context, args []string, env Env) int {
	pos, help, err := flags{}.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	if err != nil {
		return usageError(env, "mcp", err)
	}
	p, code := openProject(env, "mcp", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	if err := mcp.Serve(ctx, p, Version(), env.Stdin, env.Stdout); err != nil && ctx.Err() == nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake mcp: %v\n", err)
		return ExitError
	}
	return ExitOK
}
