package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/lib"
)

const libUsage = `retake lib: components, brand kits and edit recipes as shareable packages (spec/components.md).
Every command takes --project DIR and --json.

  retake lib search "<query>" [--registry R] [--type component|kit|recipe] [--timeline T]
        search a registry and the user library (<library>/components)
  retake lib add <name>[@version] [--registry R] [--timeline T] [--force]
        copy a package (and what it requires) into components/ next to the timeline and pin it in the lockfile
  retake lib list ["<query>"] [--timeline T]
        the components and kits the project can use, which the timeline uses, and whether they're locked
  retake lib describe <name>[@version] [--registry R] [--timeline T]
        props schema, defaults, examples, preview paths, changelog and a use to patch in
  retake lib pack <dir> [--index FILE]
        validate a package (license gate included) and print its registry entry, or write it into an index.json
  retake lib recipe export [<timeline>] -o DIR
        the timeline, its patch log, its lockfile and its packages, with placeholders for the media
  retake lib lint [<timeline>]
        retake-edit lint: the timeline against its kits' house rules (exit 1 on errors)

The registry is --registry (a path to an index.json, a folder holding one, or an http(s) URL), else
$RETAKE_REGISTRY. The timeline is --timeline, else the project's (retake.toml [edit] timeline, or
edit/timeline.json), else ./timeline.json. scripts/build-registry.sh builds library/index.json.
`

type libEnv struct {
	env      Env
	project  string
	registry string
	timeline string
	asJSON   bool
}

func (l *libEnv) root() string {
	root := l.project
	if root == "" {
		root = l.env.Getenv("RETAKE_PROJECT")
	}
	if root != "" {
		return absPath(root, l.env.Getenv)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return assets.FindRoot(wd)
}

// findTimeline is --timeline, else a positional, else the project's, else ./timeline.json.
func (l *libEnv) findTimeline(pos string) (string, error) {
	for _, t := range []string{l.timeline, pos} {
		if t != "" {
			p := absPath(t, l.env.Getenv)
			if !isFile(p) {
				return "", &lib.Error{Code: lib.CodeNotFound, Message: "no timeline at " + p}
			}
			return p, nil
		}
	}
	if p, err := bridge.FindTimeline(l.root()); err == nil {
		return p, nil
	}
	wd, _ := os.Getwd()
	if p := filepath.Join(wd, "timeline.json"); isFile(p) {
		return p, nil
	}
	return "", &lib.Error{Code: lib.CodeNotFound, Message: "no timeline: pass --timeline, or run in a timeline " +
		"project (retake.toml [edit] timeline, or edit/timeline.json)"}
}

func (l *libEnv) reg() string {
	if l.registry != "" {
		if strings.HasPrefix(l.registry, "http://") || strings.HasPrefix(l.registry, "https://") {
			return l.registry
		}
		return absPath(l.registry, l.env.Getenv)
	}
	return l.env.Getenv("RETAKE_REGISTRY")
}

func (l *libEnv) catalog() (*assets.Catalog, error) {
	return assets.Open(assets.Options{Root: l.root(), Getenv: l.env.Getenv, Log: l.env.Stderr})
}

func (l *libEnv) failed(cmd string, err error) int {
	var le *lib.Error
	if !errors.As(err, &le) {
		var ae *assets.Error
		if errors.As(err, &ae) {
			le = &lib.Error{Code: ae.Code, Message: ae.Message}
		} else {
			le = &lib.Error{Code: lib.CodeFailed, Message: err.Error()}
		}
	}
	if l.asJSON {
		_ = printJSON(l.env.Stdout, le)
	} else {
		_, _ = fmt.Fprintf(l.env.Stderr, "retake lib %s: %s\n", cmd, le.Message)
	}
	return ExitError
}

func (l *libEnv) usageErr(cmd string, err error) int {
	_, _ = fmt.Fprintf(l.env.Stderr, "retake lib %s: %v\n\n%s", cmd, err, libUsage)
	return ExitUsage
}

func (l *libEnv) out(format string, args ...any) { _, _ = fmt.Fprintf(l.env.Stdout, format, args...) }

// libCmd runs retake lib <sub>.
func libCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = io.WriteString(env.Stdout, libUsage)
		return ExitOK
	}
	l := &libEnv{env: env}
	sub, rest := args[0], args[1:]
	if sub == "recipe" {
		if len(rest) == 0 || rest[0] != "export" {
			return l.usageErr("recipe", errors.New("the subcommand is export"))
		}
		sub, rest = "recipe export", rest[1:]
	}
	var force bool
	var typ, index, outDir string
	f := flags{bools: map[string]*bool{"--json": &l.asJSON, "--force": &force},
		values: map[string]*string{"--project": &l.project, "--registry": &l.registry, "--timeline": &l.timeline,
			"--type": &typ, "--index": &index, "-o": &outDir, "--out": &outDir},
		multi: map[string]*[]string{}}
	pos, help, err := f.parse(rest)
	if help {
		_, _ = io.WriteString(env.Stdout, libUsage)
		return ExitOK
	}
	if err != nil {
		return l.usageErr(sub, err)
	}
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}
	if most := map[string]int{"search": 1, "add": 1, "list": 1, "describe": 1, "pack": 1, "recipe export": 1,
		"lint": 1}[sub]; most == 0 {
		return l.usageErr(sub, fmt.Errorf("unknown command %q", sub))
	} else if len(pos) > most {
		return l.usageErr(sub, fmt.Errorf("too many arguments: %s", strings.Join(pos, " ")))
	}
	switch sub {
	case "search":
		tl, _ := l.findTimeline("")
		res, err := lib.Search(ctx, lib.SearchArgs{Query: arg(0), Registry: l.reg(), Type: typ, Timeline: tl,
			Getenv: env.Getenv})
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		if len(res.Hits) == 0 {
			l.out("nothing found\n")
		}
		for _, h := range res.Hits {
			mark := ""
			if h.Installed {
				mark = "  (in the project)"
			}
			l.out("%-28s %-9s %-10s %s%s\n", h.Name+"@"+h.Version, h.Type, h.Source, h.Title, mark)
		}
		return ExitOK
	case "add":
		if arg(0) == "" {
			return l.usageErr(sub, errors.New("give a package: name or name@version"))
		}
		tl, err := l.findTimeline("")
		if err != nil {
			return l.failed(sub, err)
		}
		res, err := lib.Add(ctx, lib.AddArgs{Ref: arg(0), Registry: l.reg(), Timeline: tl, Force: force})
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		for _, p := range res.Packages {
			by := ""
			if p.Because != "" {
				by = " (for " + p.Because + ")"
			}
			l.out("%-9s %-28s %s%s\n", p.Status, p.Ref, p.Path, by)
		}
		l.out("pinned in %s\n%s\n", res.Lock, res.Use)
		return ExitOK
	case "list":
		tl, _ := l.findTimeline("")
		res, err := lib.List(lib.ListArgs{Timeline: tl, Query: arg(0), Getenv: env.Getenv})
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		for _, group := range [][]lib.Listed{res.Kits, res.Components} {
			for _, c := range group {
				state := ""
				if len(c.Used) > 0 {
					state = "used by " + strings.Join(c.Used, ", ")
					if !c.Locked {
						state += ", NOT LOCKED"
					}
				}
				l.out("%-30s %-9s %-34s %s\n", c.Ref, c.Type, c.Title, state)
			}
		}
		return ExitOK
	case "describe":
		if arg(0) == "" {
			return l.usageErr(sub, errors.New("give a package: name or name@version"))
		}
		tl, _ := l.findTimeline("")
		d, err := lib.Describe(ctx, lib.DescribeArgs{Ref: arg(0), Timeline: tl, Registry: l.reg(), Getenv: env.Getenv})
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, d)
			return ExitOK
		}
		l.out("%s  %s (%s, %s)\n%s\n", d.Ref, d.Title, d.Type, d.License, d.Description)
		if d.Previews["sheet"] != "" {
			l.out("sheet: %s\n", d.Previews["sheet"])
		}
		for _, k := range sortedProps(d.Props) {
			l.out("  %s\n", k)
		}
		if d.Use != nil {
			l.out("use:\n")
			_ = printJSON(env.Stdout, d.Use)
		}
		return ExitOK
	case "pack":
		if arg(0) == "" {
			return l.usageErr(sub, errors.New("give a package folder"))
		}
		c, err := l.catalog()
		if err != nil {
			return l.failed(sub, err)
		}
		defer func() { _ = c.Close() }()
		res, err := lib.Pack(ctx, lib.PackArgs{Dir: absPath(arg(0), env.Getenv), Index: index, Catalog: c})
		for _, w := range res.Warnings {
			_, _ = fmt.Fprintf(env.Stderr, "retake lib pack: %s\n", w)
		}
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON || res.Index == "" {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		l.out("%s %s → %s\n", res.Entry.Ref(), res.Entry.Hash, res.Index)
		return ExitOK
	case "recipe export":
		tl, err := l.findTimeline(arg(0))
		if err != nil {
			return l.failed(sub, err)
		}
		if outDir == "" {
			return l.usageErr(sub, errors.New("give the folder to export into: -o DIR"))
		}
		c, err := l.catalog()
		if err != nil {
			return l.failed(sub, err)
		}
		defer func() { _ = c.Close() }()
		res, err := lib.ExportRecipe(ctx, lib.RecipeArgs{Timeline: tl, Out: absPath(outDir, env.Getenv), Catalog: c,
			Getenv: env.Getenv})
		if err != nil {
			return l.failed(sub, err)
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, res)
			return ExitOK
		}
		l.out("%s: %d placeholders, %d packages, %d patches\n", res.Dir, len(res.Recipe.Placeholders),
			len(res.Recipe.Packages), res.Recipe.Patches)
		for _, w := range res.Recipe.Warnings {
			l.out("  warning: %s\n", w)
		}
		return ExitOK
	case "lint":
		tl, err := l.findTimeline(arg(0))
		if err != nil {
			return l.failed(sub, err)
		}
		res, err := lib.Lint(ctx, editorOf(env), tl)
		if err != nil {
			return l.failed(sub, err)
		}
		code := ExitOK
		if res.Errors > 0 {
			code = ExitError
		}
		if l.asJSON {
			_ = printJSON(env.Stdout, res)
			return code
		}
		l.out("%s: %d error(s), %d warning(s)\n", res.Timeline, res.Errors, res.Warnings)
		for _, v := range res.Violations {
			l.out("  %-7s %-24s %s\n", v.Severity, v.ID, v.Message)
		}
		return code
	}
	return l.usageErr(sub, fmt.Errorf("unknown command %q", sub))
}

// sortedProps lists a props schema's properties as "name: description".
func sortedProps(schema map[string]any) []string {
	ps, _ := schema["properties"].(map[string]any)
	var out []string
	for k, v := range ps {
		m, _ := v.(map[string]any)
		d, _ := m["description"].(string)
		out = append(out, k+": "+d)
	}
	sort.Strings(out)
	return out
}
