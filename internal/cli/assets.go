package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
	"github.com/samahlstrom/retake/internal/mcp"
	"github.com/samahlstrom/retake/internal/transcribe"
)

const assetsUsage = `retake assets: the asset catalog (spec/assets.md). Every command takes --project DIR and --json.

  retake assets import <path|dir>... [--scope proj|lib] [--license S] [--credit TEXT] [--license-source URL]
        [--content-id-risk R] [--tags a,b] [--title T] [--name N] [--caption C] [--in-place] [--proxy|--no-proxy]
        [--transcribe] [--track N] [--every S]
  retake assets import [<path>] --via PLUGIN[/INDEXER] [--arg k=v]...
  retake assets search "<query>" [--kind K]... [--scope S]... [--tags a,b] [--license S]... [--type T]...
        [--where SQL] [--limit N]
  retake assets get <id>
  retake assets preview <id> [--kind K] [--at T]... [--span sp:N]
  retake assets span find [--type T]... [--where SQL] [--near ID@T|DOMAIN@T] [--window S] [--asset ID]
        [--plugin P] [--limit N]
  retake assets span add <id> <t0> <t1> [--type T] [--label L] [--data JSON] [--confidence C]
  retake assets set <id> [--title T] [--tags a,b] [--add-tag T]... [--rm-tag T]... [--caption C]
        [--license S [--credit C] [--license-source U] [--content-id-risk R] [--license-note N]]
  retake assets resolve <id>...          one JSON object per line: {id, path, hash, kind}
  retake assets rehash <id>...
  retake assets license-report <timeline.json|manifest.json> [--resolved F]
  retake assets sql "<SELECT ...>" [--scope proj|lib] [--limit N]
  retake assets plugins
  retake assets plugin install <dir>
  retake assets plugin <name> <tool> [--args JSON]
  retake assets kit install <dir>
  retake assets mcp                      serve the asset tools alone over MCP on stdio

The project is --project DIR, else $RETAKE_PROJECT, else the nearest folder with a .retake folder, else the
current one. The library is $RETAKE_LIBRARY, else $XDG_DATA_HOME/retake/library. Plug-ins are found in
<project>/.retake/plugins, $RETAKE_PLUGIN_PATH and <library>/plugins. Licenses: owned, cleared, attribution,
review-only (the default), blocked. Times: seconds, m:ss or h:mm:ss.
`

// assetsEnv is what every assets subcommand shares.
type assetsEnv struct {
	env     Env
	project string
	asJSON  bool
}

func (a assetsEnv) open(transcribeOK bool) (*assets.Catalog, error) {
	root := a.project
	if root == "" {
		root = a.env.Getenv("RETAKE_PROJECT")
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = assets.FindRoot(wd)
	} else {
		root = absPath(root, a.env.Getenv)
	}
	o := assets.Options{Root: root, Getenv: a.env.Getenv, Log: a.env.Stderr}
	if transcribeOK {
		env := a.env
		o.Transcriber = func() (assets.Transcriber, error) { return transcribe.FromConfig(loadConfig(env)) }
	}
	return assets.Open(o)
}

// failed reports an asset operation's error: {"code", "error"} on stdout with --json, else on stderr.
func (a assetsEnv) failed(cmd string, err error) int {
	var ae *assets.Error
	if !errors.As(err, &ae) {
		ae = &assets.Error{Code: assets.CodeFailed, Message: err.Error()}
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, ae)
	} else {
		_, _ = fmt.Fprintf(a.env.Stderr, "retake assets %s: %s\n", cmd, ae.Message)
	}
	return ExitError
}

func (a assetsEnv) usageErr(cmd string, err error) int {
	_, _ = fmt.Fprintf(a.env.Stderr, "retake assets %s: %v\n\n%s", cmd, err, assetsUsage)
	return ExitUsage
}

func (a assetsEnv) out(format string, args ...any) {
	_, _ = fmt.Fprintf(a.env.Stdout, format, args...)
}

// assetsCmd runs retake assets <sub>.
func assetsCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = io.WriteString(env.Stdout, assetsUsage)
		return ExitOK
	}
	sub, rest := args[0], args[1:]
	if sub == "span" || sub == "kit" || (sub == "plugin" && len(rest) > 0 && rest[0] == "install") {
		if len(rest) == 0 {
			return (&assetsEnv{env: env}).usageErr(sub, errors.New("give a subcommand"))
		}
		sub, rest = sub+" "+rest[0], rest[1:]
	}
	a := &assetsEnv{env: env}
	f := flags{bools: map[string]*bool{"--json": &a.asJSON}, values: map[string]*string{"--project": &a.project},
		multi: map[string]*[]string{}}
	switch sub {
	case "import":
		return assetsImport(ctx, a, f, rest)
	case "search":
		return assetsSearch(ctx, a, f, rest)
	case "get":
		return assetsGet(ctx, a, f, rest)
	case "preview":
		return assetsPreview(ctx, a, f, rest)
	case "span find":
		return assetsSpanFind(ctx, a, f, rest)
	case "span add":
		return assetsSpanAdd(ctx, a, f, rest)
	case "set":
		return assetsSet(ctx, a, f, rest)
	case "resolve", "rehash":
		return assetsResolve(ctx, a, f, rest, sub)
	case "license-report":
		return assetsLicenseReport(ctx, a, f, rest)
	case "sql":
		return assetsSQL(ctx, a, f, rest)
	case "plugins":
		return assetsPlugins(a, f, rest)
	case "plugin install":
		return assetsPluginInstall(a, f, rest)
	case "plugin":
		return assetsPluginQuery(ctx, a, f, rest)
	case "kit install":
		return assetsKitInstall(ctx, a, f, rest)
	case "mcp":
		return assetsMCP(ctx, a, f, rest)
	}
	return a.usageErr(sub, fmt.Errorf("unknown command %q", sub))
}

// parse parses a subcommand's flags; it returns a non-negative exit code when the command is done (help, error).
func (a *assetsEnv) parse(cmd string, f flags, args []string, minPos, maxPos int) ([]string, int) {
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(a.env.Stdout, assetsUsage)
		return nil, ExitOK
	}
	if err == nil && (len(pos) < minPos || (maxPos >= 0 && len(pos) > maxPos)) {
		switch {
		case minPos == maxPos && minPos == 1:
			err = errors.New("give one asset id")
		case maxPos == 0:
			err = errors.New("takes no arguments")
		default:
			err = fmt.Errorf("give %d to %d arguments", minPos, maxPos)
		}
	}
	if err != nil {
		return nil, a.usageErr(cmd, err)
	}
	return pos, -1
}

func assetsImport(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var ia assets.ImportArgs
	var tags, track, every, via string
	var argList []string
	var proxy, noProxy bool
	f.values["--scope"] = &ia.Scope
	f.values["--license"] = &ia.License.Status
	f.values["--credit"] = &ia.License.Credit
	f.values["--license-source"] = &ia.License.Source
	f.values["--content-id-risk"] = &ia.License.ContentIDRisk
	f.values["--tags"] = &tags
	f.values["--title"] = &ia.Title
	f.values["--name"] = &ia.Name
	f.values["--caption"] = &ia.Caption
	f.values["--track"] = &track
	f.values["--every"] = &every
	f.values["--via"] = &via
	f.multi["--arg"] = &argList
	f.bools["--in-place"] = &ia.InPlace
	f.bools["--proxy"] = &proxy
	f.bools["--no-proxy"] = &noProxy
	f.bools["--transcribe"] = &ia.Transcribe
	minPos := 1
	if strings.Contains(strings.Join(args, " "), "--via") {
		minPos = 0
	}
	pos, code := a.parse("import", f, args, minPos, -1)
	if code >= 0 {
		return code
	}
	ia.Paths, ia.Tags, ia.Via = pos, assets.SplitTags(tags), via
	var err error
	switch {
	case proxy && noProxy:
		err = errors.New("--proxy or --no-proxy, not both")
	case proxy:
		ia.Proxy = assets.ProxyAlways
	case noProxy:
		ia.Proxy = assets.ProxyNever
	}
	if err == nil && track != "" {
		if ia.Track, err = strconv.Atoi(track); err != nil || ia.Track < 0 {
			err = fmt.Errorf("--track %q is not an audio track number (0 = the first)", track)
		}
	}
	if err == nil && every != "" {
		if ia.EveryS, err = assets.ParseTime(every); err != nil || ia.EveryS <= 0 {
			err = fmt.Errorf("--every %q is not a number of seconds", every)
		}
	}
	if err == nil && len(argList) > 0 {
		ia.Args, err = parseArgs(argList)
	}
	if err != nil {
		return a.usageErr("import", err)
	}
	c, err := a.open(ia.Transcribe)
	if err != nil {
		return a.failed("import", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.Import(ctx, ia)
	if err != nil {
		return a.failed("import", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, it := range res.Items {
		where := firstOf(it.Path, it.Key)
		switch it.Status {
		case assets.StatusImported, assets.StatusUpdated:
			a.out("%-9s %s (%s)  %s\n", it.Status, it.ID, firstOf(it.Kind, "?"), where)
		case assets.StatusDuplicate:
			a.out("duplicate of %s  %s\n", it.DuplicateOf, where)
		default:
			a.out("skipped   %s: %s\n", where, it.Reason)
		}
		for _, n := range it.Near {
			a.out("          looks like %s (distance %d)\n", n.ID, n.Distance)
		}
		for _, w := range it.Warnings {
			a.out("          warning: %s\n", w)
		}
	}
	for _, l := range res.Log {
		a.out("[%s] %s\n", res.Plugin, l)
	}
	line := fmt.Sprintf("%d imported, %d updated, %d duplicates, %d skipped", res.Imported, res.Updated, res.Duplicates,
		res.Skipped)
	if res.Plugin != "" {
		line += fmt.Sprintf("; %d spans, %d clocks from %s", res.Spans, res.Clocks, res.Plugin)
	}
	a.out("%s\n", line)
	return ExitOK
}

// parseArgs reads --arg k=v pairs; a value that is valid JSON (a number, true, a list) is taken as JSON.
func parseArgs(list []string) (map[string]any, error) {
	out := map[string]any{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--arg %q: write it as key=value", kv)
		}
		var j any
		if err := json.Unmarshal([]byte(v), &j); err == nil {
			out[k] = j
		} else {
			out[k] = v
		}
	}
	return out, nil
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func fmtSpan(t0, t1 float64) string {
	return fmt.Sprintf("%s-%s", assetClock(t0), assetClock(t1))
}

// assetClock formats seconds as m:ss.s, or h:mm:ss.s from an hour.
func assetClock(t float64) string {
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%04.1f", int64(t/3600), int64(modf(t, 3600)/60), modf(t, 60))
	}
	return fmt.Sprintf("%d:%04.1f", int64(t/60), modf(t, 60))
}

func assetsSearch(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var sa assets.SearchArgs
	var tags, limit string
	f.multi["--kind"] = &sa.Kind
	f.multi["--scope"] = &sa.Scope
	f.multi["--license"] = &sa.License
	f.multi["--type"] = &sa.Type
	f.values["--tags"] = &tags
	f.values["--where"] = &sa.Where
	f.values["--limit"] = &limit
	pos, code := a.parse("search", f, args, 0, 1)
	if code >= 0 {
		return code
	}
	if len(pos) == 1 {
		sa.Query = pos[0]
	}
	sa.Tags = assets.SplitTags(tags)
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n <= 0 {
			return a.usageErr("search", fmt.Errorf("--limit %q is not a count", limit))
		}
		sa.Limit = n
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("search", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.Search(ctx, sa)
	if err != nil {
		return a.failed("search", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, h := range res.Results {
		dur := ""
		if h.Duration != nil {
			dur = " " + assetClock(*h.Duration)
		}
		a.out("%s  %s%s  %s  %q", h.Asset, h.Kind, dur, h.License, h.Title)
		if len(h.Tags) > 0 {
			a.out("  [%s]", strings.Join(h.Tags, ", "))
		}
		if len(h.Matched) > 0 {
			a.out("  (%s)", strings.Join(h.Matched, ", "))
		}
		a.out("\n")
		if h.Preview != "" {
			a.out("    preview %s\n", h.Preview)
		}
		for _, s := range h.Spans {
			a.out("    %s %s %s %q%s\n", s.Span, fmtSpan(s.T0, s.T1), s.Type, s.Label, clocksText(s.Clocks))
		}
		for _, tx := range h.Transcript {
			a.out("    said at %s: %q\n", fmtSpan(tx.T0, tx.T1), tx.Text)
		}
	}
	if len(res.Results) == 0 {
		a.out("no assets found\n")
	}
	return ExitOK
}

func clocksText(cs []assets.SpanClock) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s %s", c.Domain, fmtSpan(max(c.T0, 0), max(c.T1, 0))))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  [" + strings.Join(parts, "; ") + "]"
}

func assetsGet(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	pos, code := a.parse("get", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("get", err)
	}
	defer func() { _ = c.Close() }()
	e, err := c.Get(ctx, pos[0])
	if err != nil {
		return a.failed("get", err)
	}
	// The entry is JSON either way: it is the catalog's record.
	_ = printJSON(a.env.Stdout, e)
	return ExitOK
}

func assetsPreview(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var pa assets.PreviewArgs
	var at []string
	f.values["--kind"] = &pa.Kind
	f.values["--span"] = &pa.Span
	f.multi["--at"] = &at
	pos, code := a.parse("preview", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	pa.ID = pos[0]
	for _, s := range at {
		for _, x := range strings.Split(s, ",") {
			t, err := assets.ParseTime(x)
			if err != nil {
				return a.usageErr("preview", fmt.Errorf("--at %q: %w", x, err))
			}
			pa.At = append(pa.At, t)
		}
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("preview", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.Preview(ctx, pa)
	if err != nil {
		return a.failed("preview", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, p := range res.Files {
		a.out("%s\n", assets.DescribePreview(p))
	}
	return ExitOK
}

func printSpan(a *assetsEnv, s assets.Span) {
	a.out("%-8s %s %s  %s %q%s", s.Span, s.Asset, fmtSpan(s.T0, s.T1), s.Type, s.Label, clocksText(s.Clocks))
	if s.SourceOfLabel != "" {
		a.out("  (%s", s.SourceOfLabel)
		if s.Confidence != "" {
			a.out(", %s", s.Confidence)
		}
		a.out(")")
	}
	a.out("\n")
	if s.Strip != "" {
		a.out("         strip %s\n", s.Strip)
	}
}

func assetsSpanFind(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var sa assets.SpanFindArgs
	var window, limit string
	f.multi["--type"] = &sa.Type
	f.values["--where"] = &sa.Where
	f.values["--near"] = &sa.Near
	f.values["--window"] = &window
	f.values["--asset"] = &sa.Asset
	f.values["--plugin"] = &sa.Plugin
	f.values["--limit"] = &limit
	if _, code := a.parse("span find", f, args, 0, 0); code >= 0 {
		return code
	}
	var err error
	if window != "" {
		if sa.Window, err = strconv.ParseFloat(window, 64); err != nil || sa.Window <= 0 {
			return a.usageErr("span find", fmt.Errorf("--window %q is not a number of seconds", window))
		}
	}
	if limit != "" {
		if sa.Limit, err = strconv.Atoi(limit); err != nil || sa.Limit <= 0 {
			return a.usageErr("span find", fmt.Errorf("--limit %q is not a count", limit))
		}
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("span find", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.SpanFind(ctx, sa)
	if err != nil {
		return a.failed("span find", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, s := range res.Spans {
		printSpan(a, s)
	}
	switch {
	case len(res.Spans) == 0:
		a.out("no spans found\n")
	case res.More:
		a.out("(more: raise --limit)\n")
	}
	return ExitOK
}

func assetsSpanAdd(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var sa assets.SpanAddArgs
	var data string
	f.values["--type"] = &sa.Type
	f.values["--label"] = &sa.Label
	f.values["--data"] = &data
	f.values["--confidence"] = &sa.Confidence
	pos, code := a.parse("span add", f, args, 3, 3)
	if code >= 0 {
		return code
	}
	sa.Asset = pos[0]
	var err error
	if sa.T0, err = assets.ParseTime(pos[1]); err == nil {
		sa.T1, err = assets.ParseTime(pos[2])
	}
	if err == nil && data != "" {
		if err = json.Unmarshal([]byte(data), &sa.Data); err != nil {
			err = fmt.Errorf("--data is a JSON object: %w", err)
		}
	}
	if err != nil {
		return a.usageErr("span add", err)
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("span add", err)
	}
	defer func() { _ = c.Close() }()
	s, err := c.SpanAdd(ctx, sa)
	if err != nil {
		return a.failed("span add", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, s)
		return ExitOK
	}
	printSpan(a, s)
	return ExitOK
}

func assetsSet(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var title, tags, caption string
	var addTags, rmTags []string
	var lic assets.LicenseArgs
	f.values["--title"] = &title
	f.values["--tags"] = &tags
	f.values["--caption"] = &caption
	f.multi["--add-tag"] = &addTags
	f.multi["--rm-tag"] = &rmTags
	f.values["--license"] = &lic.Status
	f.values["--credit"] = &lic.Credit
	f.values["--license-source"] = &lic.Source
	f.values["--content-id-risk"] = &lic.ContentIDRisk
	f.values["--license-note"] = &lic.Note
	pos, code := a.parse("set", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	sa := assets.SetArgs{ID: pos[0], AddTags: addTags, RmTags: rmTags}
	given := map[string]bool{}
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		given[name] = true
	}
	if given["--title"] {
		sa.Title = &title
	}
	if given["--caption"] {
		sa.Caption = &caption
	}
	if given["--tags"] {
		sa.Tags = assets.SplitTags(tags)
	}
	if lic != (assets.LicenseArgs{}) {
		if lic.Status == "" {
			return a.usageErr("set", errors.New("--credit, --license-source and --content-id-risk go with --license"))
		}
		sa.License = &lic
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("set", err)
	}
	defer func() { _ = c.Close() }()
	e, err := c.Set(ctx, sa)
	if err != nil {
		return a.failed("set", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, e)
		return ExitOK
	}
	a.out("%s  %q  [%s]  license %s", e.ID, e.Title, strings.Join(e.Tags, ", "), e.License.Status)
	if e.License.Credit != "" {
		a.out(" (%s)", e.License.Credit)
	}
	a.out("\n")
	return ExitOK
}

// assetsResolve prints one JSON object per id: the resolver seam the editor calls.
func assetsResolve(ctx context.Context, a *assetsEnv, f flags, args []string, cmd string) int {
	pos, code := a.parse(cmd, f, args, 1, -1)
	if code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed(cmd, err)
	}
	defer func() { _ = c.Close() }()
	w := bufio.NewWriter(a.env.Stdout)
	defer func() { _ = w.Flush() }()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	exit := ExitOK
	for _, id := range pos {
		var r assets.Resolved
		if cmd == "rehash" {
			r, err = c.Rehash(ctx, id)
		} else {
			r, err = c.Resolve(ctx, id)
		}
		if err != nil {
			var ae *assets.Error
			if !errors.As(err, &ae) {
				ae = &assets.Error{Code: assets.CodeFailed, Message: err.Error()}
			}
			_ = enc.Encode(struct {
				ID string `json:"id"`
				*assets.Error
			}{id, ae})
			exit = ExitError
			continue
		}
		_ = enc.Encode(r)
	}
	return exit
}

func assetsLicenseReport(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var resolved string
	f.values["--resolved"] = &resolved
	pos, code := a.parse("license-report", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("license-report", err)
	}
	defer func() { _ = c.Close() }()
	if resolved != "" {
		resolved = absPath(resolved, a.env.Getenv)
	}
	rep, err := c.LicenseReport(ctx, assets.LicenseReportArgs{Path: absPath(pos[0], a.env.Getenv), Resolved: resolved})
	if err != nil {
		return a.failed("license-report", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, rep)
		return ExitOK
	}
	for _, u := range rep.Assets {
		status := "NOT CATALOGED"
		if u.License != nil {
			status = u.License.Status
		}
		name := firstOf(u.Asset, u.TimelineID)
		if u.TimelineID != "" && u.Asset != "" {
			name += " (as " + u.TimelineID + ")"
		}
		a.out("%-14s %s  %s\n", status, name, rangesText(u.Ranges, len(u.Uses)))
	}
	for _, w := range rep.Warnings {
		a.out("warning: %s\n", w)
	}
	if rep.Credits != "" {
		a.out("\ncredits:\n%s\n", rep.Credits)
	}
	if rep.Publishable {
		a.out("\npublishable: no blockers\n")
		return ExitOK
	}
	a.out("\nNOT publishable:\n")
	for _, b := range rep.Blockers {
		a.out("  %s: %s  %s\n", b.Asset, b.Reason, rangesText(b.Ranges, 0))
	}
	return ExitOK
}

func rangesText(rs [][2]float64, uses int) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmtSpan(r[0], r[1]))
	}
	if len(parts) == 0 {
		if uses > 0 {
			return fmt.Sprintf("%s (times unresolved)", plural(uses, "use"))
		}
		return ""
	}
	return strings.Join(parts, ", ")
}

func assetsSQL(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var scope, limit string
	f.values["--scope"] = &scope
	f.values["--limit"] = &limit
	pos, code := a.parse("sql", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	n := 0
	if limit != "" {
		var err error
		if n, err = strconv.Atoi(limit); err != nil || n <= 0 {
			return a.usageErr("sql", fmt.Errorf("--limit %q is not a count", limit))
		}
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("sql", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.SQL(ctx, scope, pos[0], n)
	if err != nil {
		return a.failed("sql", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	a.out("%s\n", strings.Join(res.Columns, "\t"))
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			if v != nil {
				cells[i] = fmt.Sprint(v)
			}
		}
		a.out("%s\n", strings.Join(cells, "\t"))
	}
	a.out("(%s)\n", plural(len(res.Rows), "row"))
	return ExitOK
}

func assetsPlugins(a *assetsEnv, f flags, args []string) int {
	if _, code := a.parse("plugins", f, args, 0, 0); code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("plugins", err)
	}
	defer func() { _ = c.Close() }()
	res := c.Plugins()
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, p := range res.Plugins {
		a.out("%s %s  %s\n    %s\n", p.Name, p.Version, p.Description, p.Dir)
		var ix, tools, views []string
		for _, i := range p.Indexers {
			ix = append(ix, i.Name)
		}
		for _, t := range p.Tools {
			tools = append(tools, t.Name)
		}
		for _, v := range p.Views {
			views = append(views, v.Name)
		}
		a.out("    %s, indexers [%s], tools [%s], views [%s]\n", plural(len(p.SpanTypes), "span type"),
			strings.Join(ix, ", "), strings.Join(tools, ", "), strings.Join(views, ", "))
	}
	if len(res.Plugins) == 0 {
		a.out("no plug-ins (searched %s)\n", strings.Join(res.Searched, ", "))
	}
	for _, p := range res.Problems {
		a.out("problem: %s\n", p)
	}
	return ExitOK
}

func assetsPluginInstall(a *assetsEnv, f flags, args []string) int {
	pos, code := a.parse("plugin install", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("plugin install", err)
	}
	defer func() { _ = c.Close() }()
	p, err := c.InstallPlugin(absPath(pos[0], a.env.Getenv))
	if err != nil {
		return a.failed("plugin install", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, p)
		return ExitOK
	}
	a.out("installed %s %s from %s\n", p.Name, p.Version, p.Dir)
	return ExitOK
}

func assetsPluginQuery(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	var raw string
	f.values["--args"] = &raw
	pos, code := a.parse("plugin", f, args, 2, 2)
	if code >= 0 {
		return code
	}
	qa := assets.PluginQueryArgs{Plugin: pos[0], Tool: pos[1]}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &qa.Args); err != nil {
			return a.usageErr("plugin", fmt.Errorf("--args is a JSON object: %w", err))
		}
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("plugin", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.PluginQuery(ctx, qa)
	if err != nil {
		return a.failed("plugin", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
	} else {
		_ = printJSON(a.env.Stdout, res.Result)
	}
	return ExitOK
}

func assetsKitInstall(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	pos, code := a.parse("kit install", f, args, 1, 1)
	if code >= 0 {
		return code
	}
	c, err := a.open(false)
	if err != nil {
		return a.failed("kit install", err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.InstallKit(ctx, absPath(pos[0], a.env.Getenv))
	if err != nil {
		return a.failed("kit install", err)
	}
	if a.asJSON {
		_ = printJSON(a.env.Stdout, res)
		return ExitOK
	}
	for _, it := range res.Items {
		a.out("%-9s %s  %s %s\n", it.Status, firstOf(it.ID, it.Path), it.Reason, strings.Join(it.Warnings, "; "))
	}
	a.out("kit %s: %d installed, %d skipped\n", res.Kit, res.Imported, res.Skipped)
	return ExitOK
}

func assetsMCP(ctx context.Context, a *assetsEnv, f flags, args []string) int {
	if _, code := a.parse("mcp", f, args, 0, 0); code >= 0 {
		return code
	}
	open := func() (*assets.Catalog, error) { return a.open(true) }
	if err := mcp.ServeAssets(ctx, open, Version(), a.env.Stdin, a.env.Stdout); err != nil && ctx.Err() == nil {
		_, _ = fmt.Fprintf(a.env.Stderr, "retake assets mcp: %v\n", err)
		return ExitError
	}
	return ExitOK
}
