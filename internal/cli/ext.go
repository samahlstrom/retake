package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samahlstrom/retake/extensions"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
)

// retake ext list, enable, disable and check (spec/extensions.md, "Commands").

// localSources are where retake open, share and connect find extensions: --ext's folders and RETAKE_EXT_PATH's,
// the opened folder's .retake/extensions/, the user's ~/.config/retake/extensions/ and the bundled ones.
func localSources(env Env, flagDirs []string, folder string) ext.Sources {
	src := ext.Sources{Bundled: extensions.FS, Retake: Version(), Project: folder}
	for _, d := range ext.FlagDirs(flagDirs, env.Getenv) {
		src.Flags = append(src.Flags, absPath(d, env.Getenv))
	}
	if u, err := ext.UserDir(env.Getenv); err == nil {
		src.User = u
	}
	return src
}

func extCmd(args []string, env Env) int {
	if len(args) == 0 {
		return usageError(env, "ext", errors.New("give list, enable, disable or check"))
	}
	switch args[0] {
	case "list":
		return extListCmd(args[1:], env)
	case "enable":
		return extEnableCmd(args[1:], env, true)
	case "disable":
		return extEnableCmd(args[1:], env, false)
	case "check":
		return extCheckCmd(args[1:], env)
	case "help", "-h", "--help":
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	return usageError(env, "ext", fmt.Errorf("unknown command %q: give list, enable, disable or check", args[0]))
}

// extWorld is what the ext commands look at: the extensions found from the folder, and the user's config.
type extWorld struct {
	found   []*ext.Ext
	cfg     config.Config
	cfgPath string
}

func loadExtWorld(env Env, folder string) (extWorld, error) {
	var w extWorld
	if folder == "" {
		wd, err := os.Getwd()
		if err != nil {
			return w, err
		}
		folder = wd
	}
	p, err := config.Path(env.Getenv)
	if err != nil {
		return w, err
	}
	c, _, err := config.Load(p)
	if err != nil {
		return w, fmt.Errorf("config %s: %w", p, err)
	}
	w.cfg, w.cfgPath = c, p
	w.found = ext.Discover(localSources(env, nil, absPath(folder, env.Getenv)))
	return w, nil
}

func (w extWorld) get(id string) *ext.Ext {
	id = config.ExtensionID(id) // a renamed extension's old id is its new one
	i := slices.IndexFunc(w.found, func(e *ext.Ext) bool { return e.ID == id })
	if i < 0 {
		return nil
	}
	return w.found[i]
}

// extItem is one extension in `retake ext list --json`.
type extItem struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Version  string        `json:"version"`
	API      int           `json:"api"`
	Where    string        `json:"where"`
	Dir      string        `json:"dir"`
	Enabled  bool          `json:"enabled"`
	On       bool          `json:"on"`
	Hash     string        `json:"hash"`
	Shadowed []string      `json:"shadowed"`
	Problems []ext.Problem `json:"problems"`
}

func extListCmd(args []string, env Env) int {
	var asJSON bool
	var folder string
	f := flags{bools: map[string]*bool{"--json": &asJSON}, values: map[string]*string{"--folder": &folder}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) > 0 {
		err = errors.New("retake ext list takes no arguments")
	}
	if err != nil {
		return usageError(env, "ext list", err)
	}
	w, err := loadExtWorld(env, folder)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake ext list: %v\n", err)
		return ExitError
	}
	set, warnings := ext.Resolve(w.found, w.cfg.Extensions.Enabled, w.cfg.Extensions.Pins)
	items := make([]extItem, 0, len(w.found))
	for _, e := range w.found {
		items = append(items, extItem{ID: e.ID, Name: e.Name, Version: e.Version, API: e.API, Where: e.Where,
			Dir: e.Dir, Enabled: e.Where == ext.WhereFlag || slices.Contains(w.cfg.Extensions.Enabled, e.ID),
			On: set.Get(e.ID) != nil, Hash: e.Hash, Shadowed: nonNil(e.Shadowed), Problems: nonNil(e.Problems)})
	}
	if asJSON {
		out := struct {
			Extensions []extItem `json:"extensions"`
			Warnings   []string  `json:"warnings"`
		}{items, nonNil(warnings)}
		if err := printJSON(env.Stdout, out); err != nil {
			return ExitError
		}
		return ExitOK
	}
	out := env.Stdout
	if len(items) == 0 {
		_, _ = fmt.Fprintln(out, "no extensions found")
	}
	idW, verW := 2, 7
	for _, it := range items {
		idW, verW = max(idW, len(it.ID)), max(verW, len(it.Version))
	}
	for i, it := range items {
		state := "off"
		switch {
		case it.On:
			state = "on"
		case it.Enabled:
			state = "error"
		}
		where := w.found[i].Display()
		if it.Dir != "" {
			where = it.Where + "  " + it.Dir
		}
		_, _ = fmt.Fprintf(out, "%-*s  %-*s  %-5s  %s\n", idW, it.ID, verW, it.Version, state, where)
		for _, s := range it.Shadowed {
			_, _ = fmt.Fprintf(out, "    shadows %s\n", s)
		}
		for _, p := range it.Problems {
			_, _ = fmt.Fprintf(out, "    %s\n", p.Message)
		}
	}
	for _, wn := range warnings {
		_, _ = fmt.Fprintf(out, "! %s\n", wn)
	}
	return ExitOK
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func extEnableCmd(args []string, env Env, on bool) int {
	cmd := "ext enable"
	if !on {
		cmd = "ext disable"
	}
	var folder string
	f := flags{values: map[string]*string{"--folder": &folder}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = fmt.Errorf("retake %s takes one extension id", cmd)
	}
	if err == nil && !ext.ValidID(pos[0]) {
		err = fmt.Errorf("%q isn't an extension id", pos[0])
	}
	if err != nil {
		return usageError(env, cmd, err)
	}
	id := config.ExtensionID(pos[0]) // retake ext enable ignition: scripts (spec/extensions.md, "Renamed ids")
	w, err := loadExtWorld(env, folder)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
		return ExitError
	}
	if !on {
		if err := config.SetExtension(w.cfgPath, id, false, ""); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake %s: %v\n", cmd, err)
			return ExitError
		}
		was := "is off"
		if !slices.Contains(w.cfg.Extensions.Enabled, id) {
			was = "was already off"
		}
		_, _ = fmt.Fprintf(env.Stdout, "retake ext disable: %s %s (%s). Restart retake to unload it.\n", id, was, w.cfgPath)
		return ExitOK
	}
	e := w.get(id)
	if e == nil {
		user, _ := ext.UserDir(env.Getenv)
		_, _ = fmt.Fprintf(env.Stderr, "retake ext enable: no extension %q found. retake ext list shows the ones found; "+
			"put its folder at %s, or try it for one run with retake open --ext DIR\n", id,
			filepath.Join(user, id))
		return ExitError
	}
	pin := ""
	if e.Where == ext.WhereProject {
		pin = e.Hash
	}
	if err := config.SetExtension(w.cfgPath, id, true, pin); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake ext enable: %v\n", err)
		return ExitError
	}
	out := env.Stdout
	_, _ = fmt.Fprintf(out, "retake ext enable: %s %s is on (%s, %s). Restart retake to load it.\n", e.ID, e.Version,
		e.Display(), w.cfgPath)
	if pin != "" {
		_, _ = fmt.Fprintf(out, "  It's this folder's own code, pinned to its files as they are now: after they change, "+
			"run retake ext enable %s again.\n", id)
	}
	if !e.OK() || e.API != ext.API {
		_, _ = fmt.Fprintf(out, "  It won't load until these are fixed:\n")
		for _, p := range e.Problems {
			if p.Fatal() {
				_, _ = fmt.Fprintf(out, "    %s\n", p.Message)
			}
		}
	}
	return ExitOK
}

func extCheckCmd(args []string, env Env) int {
	var asJSON bool
	var folder string
	f := flags{bools: map[string]*bool{"--json": &asJSON}, values: map[string]*string{"--folder": &folder}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err != nil {
		return usageError(env, "ext check", err)
	}
	w, err := loadExtWorld(env, folder)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake ext check: %v\n", err)
		return ExitError
	}
	var reports []ext.Report
	switch {
	case len(pos) == 0:
		for _, id := range w.cfg.Extensions.Enabled {
			if e := w.get(id); e != nil {
				reports = append(reports, ext.Check(e))
			} else {
				reports = append(reports, ext.Report{ID: id, API: ext.API, Problems: []ext.Problem{{Kind: "manifest",
					Name: id, Message: fmt.Sprintf("%s is enabled but wasn't found", id)}}})
			}
		}
	default:
		for _, a := range pos {
			switch {
			case isDir(a) || strings.ContainsAny(a, `/\`) || a == "." || a == "..":
				reports = append(reports, ext.CheckDir(absPath(a, env.Getenv), Version()))
			case w.get(a) != nil:
				reports = append(reports, ext.Check(w.get(a)))
			default:
				return usageError(env, "ext check", fmt.Errorf("%q is neither a folder nor an extension found here "+
					"(retake ext list)", a))
			}
		}
	}
	code := ExitOK
	for _, r := range reports {
		if len(r.Problems) > 0 {
			code = ExitError
		}
	}
	if asJSON {
		for i := range reports {
			reports[i].Problems = nonNil(reports[i].Problems)
		}
		if err := printJSON(env.Stdout, nonNil(reports)); err != nil {
			return ExitError
		}
		return code
	}
	out := env.Stdout
	if len(reports) == 0 {
		_, _ = fmt.Fprintln(out, "no extensions are enabled: give a folder or an id (retake ext list shows them)")
	}
	for _, r := range reports {
		for _, p := range r.Problems {
			_, _ = fmt.Fprintf(out, "  %s\n", p.Message)
		}
		if len(r.Problems) == 0 {
			_, _ = fmt.Fprintf(out, "ext ok: %s %s (api %d)\n", r.ID, r.Version, r.API)
		} else {
			_, _ = fmt.Fprintf(out, "ext %s %s: %s\n", r.ID, r.Version, plural(len(r.Problems), "problem"))
		}
	}
	return code
}
