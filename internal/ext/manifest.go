package ext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/samahlstrom/retake/internal/theme"
)

// maxManifest is the largest extension.toml read.
const maxManifest = 1 << 20

// Load reads the extension in fsys (its folder: extension.toml at the top). dir is its folder on disk ("" for a
// bundled one) and fallbackID its folder's name, the id used when the manifest has none. It never fails: what's wrong
// is in Problems.
func Load(fsys fs.FS, dir, where, fallbackID string) *Ext {
	e := &Ext{Dir: dir, FS: fsys, Where: where, Hash: hashFS(fsys)}
	e.ID = fallbackID
	b, err := readFile(fsys, ManifestFile, maxManifest)
	if err != nil {
		msg := fmt.Sprintf("%s: can't read it: %v", ManifestFile, err)
		if errors.Is(err, fs.ErrNotExist) {
			msg = "no " + ManifestFile + " in its folder"
		}
		e.problem("manifest", ManifestFile, msg)
		return e
	}
	var m Manifest
	md, err := toml.Decode(string(b), &m)
	if err != nil {
		e.problem("manifest", ManifestFile, fmt.Sprintf("%s doesn't parse: %v", ManifestFile, err))
		return e
	}
	e.Manifest = m
	if e.ID == "" {
		e.ID = fallbackID
	}
	for _, k := range md.Undecoded() {
		if len(k) > 3 && k[0] == "agent" && k[1] == "tools" && k[2] == "input_schema" {
			continue // a JSON Schema: any keys
		}
		e.problem("key", k.String(), fmt.Sprintf("%s: unknown key %q, ignored", ManifestFile, k.String()))
	}
	e.validate(m.ID)
	return e
}

func (e *Ext) problem(kind, name, msg string) {
	e.Problems = append(e.Problems, Problem{Kind: kind, Name: name, Message: msg})
}

func (e *Ext) bad(key, format string, a ...any) {
	e.problem("value", key, ManifestFile+": "+fmt.Sprintf(format, a...))
}

// validate checks the manifest's values and the files it names, and fills in the defaults (files' roles, the
// server's runs_on and needs).
func (e *Ext) validate(id string) {
	m := &e.Manifest
	switch {
	case m.API == 0:
		e.bad("api", "api is missing (this retake has extension API %d)", API)
	case m.API != API:
		e.problem("api", "api", fmt.Sprintf("%s %s needs extension API %d; this retake has %d", e.ID, m.Version, m.API, API))
	}
	if !ValidID(id) {
		e.bad("id", "id %q must be a lower-case letter, then up to 31 lower-case letters, digits or -", id)
	}
	if strings.TrimSpace(m.Name) == "" {
		e.bad("name", "name is missing")
	}
	if !semverRE.MatchString(m.Version) {
		e.bad("version", "version %q isn't a semver version (1.0.0)", m.Version)
	}
	if m.MinRetake != "" && !semverRE.MatchString(m.MinRetake) {
		e.bad("min_retake", "min_retake %q isn't a semver version (0.9.0)", m.MinRetake)
	}
	e.validateUI()
	kindFolders := e.validateKinds()
	e.validateSlots()
	e.validateFiles(kindFolders)
	e.validateAgent()
	e.validateServer()
}

var (
	moduleTypes = []string{".js", ".mjs"}
	iconTypes   = []string{".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico"}
)

func (e *Ext) validateUI() {
	m := &e.Manifest
	if m.UI.Module != "" {
		e.uiFile("ui.module", m.UI.Module, moduleTypes)
	} else if len(m.Slots) > 0 {
		e.bad("ui.module", "[ui] module is missing, and its slots need one")
	}
	if m.UI.Style != "" {
		e.uiFile("ui.style", m.UI.Style, []string{".css"})
	}
	if m.Nav == nil {
		return
	}
	if m.Nav.Icon != "" {
		e.uiFile("nav.icon", m.Nav.Icon, iconTypes)
	}
	switch m.Nav.List {
	case "":
	case "kind-only":
		if len(m.Kinds) == 0 {
			e.bad("nav.list", "nav list = \"kind-only\" needs a [[kinds]]")
		}
	default:
		e.bad("nav.list", "nav list %q isn't \"kind-only\"", m.Nav.List)
	}
	if a := m.Nav.Action; a != nil {
		if strings.TrimSpace(a.Title) == "" {
			e.bad("nav.action", "nav action needs a title")
		}
		if !routeRE.MatchString(a.Route) {
			e.bad("nav.action", "nav action route %q may have letters, digits and . _ ~ / - only", a.Route)
		}
	}
}

// uiFile checks a file [ui] or [nav] names: under ui/, of an allowed type, and there.
func (e *Ext) uiFile(key, name string, types []string) {
	if !ValidPath(name) || !strings.HasPrefix(name, "ui/") || !slices.Contains(types, strings.ToLower(path.Ext(name))) {
		e.bad(key, "%s %q must be a file under ui/ of type %s", key, name, strings.Join(types, " "))
		return
	}
	e.mustHave(key, name)
}

// mustHave reports a file the manifest names that isn't a regular file in its folder.
func (e *Ext) mustHave(key, name string) {
	st, err := fs.Stat(e.FS, name)
	if err != nil || !st.Mode().IsRegular() {
		e.problem("file", name, fmt.Sprintf("%s: %s names %s, which isn't there", ManifestFile, key, name))
	}
}

// validateKinds checks [[kinds]] and returns the version folders they claim.
func (e *Ext) validateKinds() []string {
	var folders []string
	ids := map[string]bool{}
	for i, k := range e.Kinds {
		key := fmt.Sprintf("kinds[%d]", i)
		if !ValidID(k.ID) {
			e.bad(key+".id", "%s: id %q must be a lower-case letter, then lower-case letters, digits or -", key, k.ID)
		} else if ids[k.ID] {
			e.bad(key+".id", "%s: the kind %q is listed twice", key, k.ID)
		}
		ids[k.ID] = true
		if k.Folder != "v0" {
			e.bad(key+".folder", "%s: folder %q: extension API %d allows only v0", key, k.Folder, API)
		} else if slices.Contains(folders, k.Folder) {
			e.bad(key+".folder", "%s: another of its kinds already claims %s", key, k.Folder)
		} else {
			folders = append(folders, k.Folder)
		}
		if !ValidPath(k.Marker) || strings.Contains(k.Marker, "/") || isFamilyVideo(k.Marker) {
			e.bad(key+".marker", "%s: marker %q must be a file name (no folder, not hidden)", key, k.Marker)
		}
	}
	return folders
}

func (e *Ext) validateSlots() {
	seen := map[string]bool{}
	for i, s := range e.Slots {
		key := fmt.Sprintf("slots[%d]", i)
		if _, ok := Slots[s.Slot]; !ok {
			e.problem("slot", s.Slot, fmt.Sprintf("%s: %s: unknown slot %q (page, version, sidebar-tab, review-panel), ignored",
				ManifestFile, key, s.Slot))
			continue
		}
		if seen[s.Slot] {
			e.bad(key, "%s: the slot %s is listed twice", key, s.Slot)
		}
		seen[s.Slot] = true
		if s.Slot == "version" && !slices.ContainsFunc(e.Kinds, func(k Kind) bool { return k.ID == s.Kind }) {
			e.bad(key+".kind", "%s: the version slot needs kind, one of its [[kinds]] ids (got %q)", key, s.Kind)
		}
		switch s.When {
		case "", "always":
		case "has-kind":
			if len(e.Kinds) == 0 {
				e.bad(key+".when", "%s: when = \"has-kind\" needs a [[kinds]]", key)
			}
		default:
			e.bad(key+".when", "%s: when %q isn't always or has-kind", key, s.When)
		}
	}
}

func (e *Ext) validateFiles(kindFolders []string) {
	f := e.Files
	if f == nil {
		return
	}
	for _, g := range f.Owns {
		if why := globProblem(g, kindFolders); why != "" {
			e.problem("owns", g, fmt.Sprintf("%s: owns %q %s; an extension may own neither .retake/, another version's "+
				"folder nor a family video file", ManifestFile, g, why))
		}
	}
	for _, r := range []struct {
		key string
		v   *string
		def string
	}{{"files.read", &f.Read, "watch"}, {"files.write", &f.Write, "record"}, {"files.create", &f.Create, "owner"}} {
		e.role(r.key, r.v, r.def)
	}
}

// role checks a role value and sets the default when it's empty.
func (e *Ext) role(key string, v *string, def string) {
	if *v == "" {
		*v = def
		return
	}
	if !slices.Contains(Roles, *v) {
		e.bad(key, "%s %q isn't watch, record, approve or owner", key, *v)
	}
}

func (e *Ext) validateAgent() {
	a := e.Agent
	if a == nil {
		return
	}
	for _, md := range []struct {
		key, name string
		limit     int
	}{{"agent.instructions", a.Instructions, MaxInstructions}, {"agent.context", a.Context, MaxContext}} {
		if md.name == "" {
			continue
		}
		if !ValidPath(md.name) {
			e.bad(md.key, "%s %q must be a file in its folder", md.key, md.name)
			continue
		}
		st, err := fs.Stat(e.FS, md.name)
		if err != nil || !st.Mode().IsRegular() {
			e.problem("file", md.name, fmt.Sprintf("%s: %s names %s, which isn't there", ManifestFile, md.key, md.name))
			continue
		}
		if st.Size() > int64(md.limit) {
			e.problem("size", md.name, fmt.Sprintf("%s is %s, over its %s cap: the prompt gets the first %s",
				md.name, kib(st.Size()), kib(int64(md.limit)), kib(int64(md.limit))))
		}
	}
	names := map[string]bool{}
	for i, t := range a.Tools {
		key := fmt.Sprintf("agent.tools[%d]", i)
		if !toolRE.MatchString(t.Name) {
			e.bad(key+".name", "%s: name %q must be a lower-case letter, then lower-case letters, digits, _ or -", key, t.Name)
		} else if names[t.Name] {
			e.bad(key+".name", "%s: the tool %q is listed twice", key, t.Name)
		}
		names[t.Name] = true
		e.exec(key+".exec", t.Exec)
	}
}

func (e *Ext) validateServer() {
	s := e.Server
	if s == nil {
		return
	}
	e.exec("server.command", s.Command)
	switch s.RunsOn {
	case "":
		s.RunsOn = "machine"
	case "machine", "host":
	default:
		e.bad("server.runs_on", "server runs_on %q isn't machine or host", s.RunsOn)
	}
	e.role("server.needs", &s.Needs, "watch")
}

// exec checks a tool's or the server's command: not empty, and a ./ program is in its folder.
func (e *Ext) exec(key string, argv []string) {
	if len(argv) == 0 || argv[0] == "" {
		e.bad(key, "%s is empty", key)
		return
	}
	if rel, ok := strings.CutPrefix(argv[0], "./"); ok {
		if !ValidPath(rel) {
			e.bad(key, "%s %q must be a file in its folder", key, argv[0])
			return
		}
		e.mustHave(key, rel)
	}
}

// checkRetake adds the problem of a min_retake newer than retake, this binary's version. A dev build ("dev",
// "dev-<rev>") or an unparsable version skips it.
func (e *Ext) checkRetake(retake string) {
	if e.MinRetake == "" || !semverRE.MatchString(e.MinRetake) {
		return
	}
	have, ok := semver(strings.TrimPrefix(retake, "v"))
	if !ok {
		return
	}
	want, _ := semver(e.MinRetake)
	if slices.Compare(have, want) < 0 {
		e.problem("retake", "min_retake", fmt.Sprintf("%s %s needs retake %s or newer; this retake is %s", e.ID,
			e.Version, e.MinRetake, retake))
	}
}

// semver is a version's major, minor and patch.
func semver(v string) ([]int, bool) {
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return nil, false
	}
	out := make([]int, 3)
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func kib(n int64) string {
	if n%1024 == 0 {
		return fmt.Sprintf("%d KiB", n/1024)
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

// readFile reads a file of fsys, at most limit bytes (more is an error).
func readFile(fsys fs.FS, name string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s isn't a file", name)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is over %s", name, kib(limit))
	}
	return b, nil
}

// contentType is the Content-Type a ui/ file is served with, or "" for one that never is (spec/extensions.md,
// "Loading and mounting"): scripts, styles, JSON, wasm, and the fonts and images of the theme's table. Never HTML.
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".wasm":
		return "application/wasm"
	case ".svg":
		return "image/svg+xml"
	}
	return theme.ContentType(name)
}
