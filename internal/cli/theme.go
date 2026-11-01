package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/samahlstrom/retake/internal/theme"
)

// retake theme init and retake theme check (spec/theme.md, "Commands"), and the theme folder the servers wrap the
// page with: --theme, $RETAKE_THEME, then ~/.config/retake/theme (retake open, retake share) or <data>/theme (serve).

// themeDir resolves the theme folder: flag, else $RETAKE_THEME, else def. It returns "" when theming is off, and
// whether the folder was named (a named folder that isn't there is worth a warning; the default isn't).
func themeDir(env Env, flag, def string) (dir string, named bool) {
	named = flag != "" || strings.TrimSpace(env.Getenv(theme.EnvVar)) != ""
	d := theme.Dir(flag, env.Getenv, def)
	if d == "" {
		return "", named
	}
	return absPath(d, env.Getenv), named
}

// localThemeDir is the theme folder of retake open and retake share.
func localThemeDir(env Env, flag string) (string, bool) {
	def, err := theme.LocalDir(env.Getenv)
	if err != nil {
		def = ""
	}
	return themeDir(env, flag, def)
}

// withTheme wraps the page with the theme folder dir, after saying where the theme is and what's wrong with it. A
// missing or broken theme never stops the server: the page shows the built-in look for whatever doesn't apply.
func withTheme(env Env, page fs.FS, dir string, named bool) fs.FS {
	if page == nil || dir == "" {
		return page
	}
	line, problems := theme.Warnings(dir)
	switch {
	case line == "" && named:
		_, _ = fmt.Fprintf(env.Stderr, "retake: theme: no folder at %s; using the built-in look\n", dir)
	case line != "":
		_, _ = fmt.Fprintf(env.Stdout, "retake: theme %s\n", line)
	}
	for _, p := range problems {
		_, _ = fmt.Fprintf(env.Stderr, "retake: theme: %s\n", p.Message)
	}
	return theme.Page(page, dir)
}

func themeCmd(args []string, env Env) int {
	if len(args) == 0 {
		return usageError(env, "theme", errors.New("give init or check"))
	}
	switch args[0] {
	case "init":
		return themeInitCmd(args[1:], env)
	case "check":
		return themeCheckCmd(args[1:], env)
	case "help", "-h", "--help":
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	return usageError(env, "theme", fmt.Errorf("unknown command %q: give init or check", args[0]))
}

// themeArg is the folder a theme command works on: its argument, else the folder retake open would use.
func themeArg(env Env, cmd string, pos []string) (string, error) {
	switch len(pos) {
	case 0:
		dir, _ := localThemeDir(env, "")
		if dir == "" {
			return "", fmt.Errorf("theming is off (%s=%s): give the theme folder", theme.EnvVar, theme.Off)
		}
		return dir, nil
	case 1:
		return absPath(pos[0], env.Getenv), nil
	}
	return "", fmt.Errorf("retake theme %s takes one folder", cmd)
}

func themeInitCmd(args []string, env Env) int {
	var force bool
	f := flags{bools: map[string]*bool{"--force": &force}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	var dir string
	if err == nil {
		dir, err = themeArg(env, "init", pos)
	}
	if err != nil {
		return usageError(env, "theme init", err)
	}
	wrote, err := theme.Init(dir, force)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake theme init: %v\n", err)
		return ExitError
	}
	w := env.Stdout
	_, _ = fmt.Fprintf(w, "retake theme init: wrote %s\n", strings.Join(wrote, " and "))
	_, _ = fmt.Fprintln(w, "  Every token and part is in theme.css, commented out: uncomment a line to change it, then reload.")
	if local, _ := localThemeDir(env, ""); local == dir {
		_, _ = fmt.Fprintln(w, "  retake open and retake share use this folder.")
	} else {
		_, _ = fmt.Fprintf(w, "  Use it with --theme %s, or %s=%s; on a server, put it in <data dir>/theme.\n", dir,
			theme.EnvVar, dir)
	}
	_, _ = fmt.Fprintln(w, "  After an update, retake theme check says if anything in it isn't used any more.")
	return ExitOK
}

func themeCheckCmd(args []string, env Env) int {
	var asJSON bool
	f := flags{bools: map[string]*bool{"--json": &asJSON}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	var dir string
	if err == nil {
		dir, err = themeArg(env, "check", pos)
	}
	if err != nil {
		return usageError(env, "theme check", err)
	}
	r := theme.Check(dir)
	code := ExitOK
	if len(r.Problems) > 0 {
		code = ExitError
	}
	if asJSON {
		if err := printJSON(env.Stdout, r); err != nil {
			return ExitError
		}
		return code
	}
	w := env.Stdout
	for _, p := range r.Problems {
		_, _ = fmt.Fprintf(w, "  %s\n", p.Message)
	}
	if len(r.Problems) > 0 {
		_, _ = fmt.Fprintf(w, "theme %s: %s (Theme API v%d)\n", dir, plural(len(r.Problems), "problem"), r.API)
		return code
	}
	_, _ = fmt.Fprintf(w, "theme ok: %s, %s (%s, Theme API v%d)\n", plural(len(r.Tokens), "token"),
		plural(len(r.Parts), "part"), dir, r.API)
	return code
}
