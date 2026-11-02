// Package ext is the server's half of spec/extensions.md: an extension's manifest, where extensions are found, which
// are on, serving their ui/ files and styles, the files they own in a video's folder with a history of the agent's
// edits, and `retake ext check`. An extension is a folder outside the install (or bundled, extensions/); nothing here
// writes to it.
package ext

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// API is the extension API's version (spec/extensions.md, "Versioning").
const API = 1

// Limits of spec/extensions.md.
const (
	MaxInstructions = 16 << 10 // [agent] instructions, for a task that targets the extension
	MaxContext      = 4 << 10  // [agent] context, for every task of a video that has its files
	MaxWrite        = 25 << 20 // one owned file written through the files API
	MaxCreate       = 1 << 20  // all the files of a new video folder (POST /api/ext/<id>/videos)
	MaxUIFile       = 10 << 20 // one file under ui/
)

// ManifestFile is the manifest's name in an extension's folder.
const ManifestFile = "extension.toml"

// Where an extension was found (spec/extensions.md, "Where extensions are found").
const (
	WhereFlag    = "flag"    // --ext DIR or RETAKE_EXT_PATH
	WhereProject = "project" // <opened folder>/.retake/extensions/<id>/
	WhereUser    = "user"    // $XDG_CONFIG_HOME/retake/extensions/<id>/
	WhereData    = "data"    // <data dir>/extensions/<id>/ (retake serve)
	WhereBundled = "bundled" // extensions/<id>/ in this repo, built into the binary
)

// Roles are the files and server roles, least first (spec/team.md's).
var Roles = []string{"watch", "record", "approve", "owner"}

// Slots are API 1's slot names and the module export each one calls.
var Slots = map[string]string{
	"page":         "page",
	"version":      "version",
	"sidebar-tab":  "sidebarTab",
	"review-panel": "reviewPanel",
}

// Manifest is extension.toml.
type Manifest struct {
	API         int     `toml:"api" json:"api"`
	ID          string  `toml:"id" json:"id"`
	Name        string  `toml:"name" json:"name"`
	Version     string  `toml:"version" json:"version"`
	Description string  `toml:"description" json:"description,omitempty"`
	MinRetake   string  `toml:"min_retake" json:"min_retake,omitempty"`
	UI          UI      `toml:"ui" json:"ui"`
	Nav         *Nav    `toml:"nav" json:"nav,omitempty"`
	Kinds       []Kind  `toml:"kinds" json:"kinds,omitempty"`
	Slots       []Slot  `toml:"slots" json:"slots,omitempty"`
	Files       *Files  `toml:"files" json:"files,omitempty"`
	Agent       *Agent  `toml:"agent" json:"agent,omitempty"`
	Server      *Server `toml:"server" json:"server,omitempty"`
}

// UI is [ui]: the ES module the page imports and its optional stylesheet, both under ui/.
type UI struct {
	Module string `toml:"module" json:"module,omitempty"`
	Style  string `toml:"style" json:"style,omitempty"`
}

// Nav is [nav]: a section in the home's left column.
type Nav struct {
	Title  string  `toml:"title" json:"title"`
	Icon   string  `toml:"icon" json:"icon,omitempty"`
	List   string  `toml:"list" json:"list,omitempty"` // "" or "kind-only"
	Action *Action `toml:"action" json:"action,omitempty"`
}

// Action is [nav] action: a + button on the section's row that opens #ext/<id>/<route>.
type Action struct {
	Title string `toml:"title" json:"title"`
	Route string `toml:"route" json:"route"`
}

// Kind is one [[kinds]]: a kind of version that isn't a video.
type Kind struct {
	ID     string  `toml:"id" json:"id"`
	Folder string  `toml:"folder" json:"folder"` // API 1: v0
	Marker Markers `toml:"marker" json:"marker"`
	Label  string  `toml:"label" json:"label"`
	Status string  `toml:"status" json:"status"`
	Record bool    `toml:"record" json:"record"`
}

// Markers is a kind's marker: a file name or a glob (*_v0.md), or a list of them; the kind's folder is the kind when
// any one matches a regular file in it.
type Markers []string

// UnmarshalTOML reads one string or a list of strings.
func (m *Markers) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case string:
		*m = Markers{x}
	case []any:
		out := make(Markers, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("a marker is a file name or a glob, not %v", item)
			}
			out = append(out, s)
		}
		*m = out
	default:
		return fmt.Errorf("a marker is a file name, a glob or a list of them, not %v", v)
	}
	return nil
}

// Matches reports whether the file name matches one of the markers.
func (m Markers) Matches(name string) bool {
	for _, g := range m {
		if ok, err := path.Match(g, name); err == nil && ok {
			return true
		}
	}
	return false
}

// Slot is one [[slots]]: where its UI mounts.
type Slot struct {
	Slot  string `toml:"slot" json:"slot"`
	Kind  string `toml:"kind" json:"kind,omitempty"`   // the version slot's kind (its short id)
	Title string `toml:"title" json:"title,omitempty"` // sidebar-tab and review-panel
	When  string `toml:"when" json:"when,omitempty"`   // "" (always), "always" or "has-kind"
}

// Files is [files]: the files it owns in a video's folder and the least role for each use (Roles).
type Files struct {
	Owns   []string `toml:"owns" json:"owns"`
	Read   string   `toml:"read" json:"read"`
	Write  string   `toml:"write" json:"write"`
	Create string   `toml:"create" json:"create"`
}

// Agent is [agent]: Markdown for the agent's prompt, and its tools.
type Agent struct {
	Instructions string `toml:"instructions" json:"instructions,omitempty"`
	Context      string `toml:"context" json:"context,omitempty"`
	Tools        []Tool `toml:"tools" json:"tools,omitempty"`
}

// Tool is one [[agent.tools]]: a program in the asset plug-in tool protocol.
type Tool struct {
	Name        string         `toml:"name" json:"name"`
	Description string         `toml:"description" json:"description,omitempty"`
	Exec        []string       `toml:"exec" json:"exec"`
	InputSchema map[string]any `toml:"input_schema" json:"input_schema,omitempty"`
}

// Server is [server]: its own server process.
type Server struct {
	Command []string `toml:"command" json:"command"`
	RunsOn  string   `toml:"runs_on" json:"runs_on"` // machine (default) or host
	Needs   string   `toml:"needs" json:"needs"`     // the least role for any request to it (default watch)
}

// Problem is one thing wrong with an extension: a load problem, or what `retake ext check` found.
type Problem struct {
	// Kind is manifest, value, file, api, retake or owns (fatal: the extension isn't loaded), or key, slot, size,
	// export, css, token or deprecated-token (warnings).
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"` // the key, file, slot, selector or token it's about
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Message }

// Fatal reports whether the problem keeps the extension from loading.
func (p Problem) Fatal() bool {
	switch p.Kind {
	case "manifest", "value", "file", "api", "retake", "owns":
		return true
	}
	return false
}

// Ext is an extension as found in one folder.
type Ext struct {
	Manifest
	Dir      string    // its folder on disk, absolute; "" for a bundled one
	FS       fs.FS     // its files (os.Root on disk, so nothing leaves the folder)
	Where    string    // flag, project, user, data or bundled
	Problems []Problem // what's wrong with it; any Fatal one keeps it off
	Shadowed []string  // the folders of the same id found after it, which it hides
	Hash     string    // "b3:<hex>": blake3 of its files, what a project extension's pin holds
}

// OK reports whether it can load: no fatal problem.
func (e *Ext) OK() bool {
	return e != nil && !slices.ContainsFunc(e.Problems, Problem.Fatal)
}

// FullKind is a kind's full id, "<ext id>.<kind id>" (scripts.doc).
func (e *Ext) FullKind(k Kind) string { return e.ID + "." + k.ID }

// Owns reports whether path (slash-separated, relative to a video's folder) is one of its files: a valid path that
// matches files.owns and isn't .retake/, another version's folder or a family video file. It doesn't look at the
// disk; Folder does that too.
func (e *Ext) Owns(path string) bool {
	if e == nil || e.Files == nil || !allowed(path) {
		return false
	}
	return slices.ContainsFunc(e.Files.Owns, func(g string) bool { return MatchGlob(g, path) })
}

// Where's display name, for list output.
func (e *Ext) whereName() string {
	if e.Where == WhereBundled {
		return "built in"
	}
	return e.Where
}

// Display is where it is, for list and warning lines: its folder, or "built in".
func (e *Ext) Display() string {
	if e.Dir == "" {
		return e.whereName()
	}
	return e.Dir
}

// AgentText is its [agent] instructions or context ("instructions" or "context") as it goes into a prompt, cut to
// MaxInstructions or MaxContext bytes at a line (truncated says so). No file: "".
func (e *Ext) AgentText(which string) (text string, truncated bool) {
	if e == nil || e.Agent == nil || e.FS == nil {
		return "", false
	}
	name, limit := e.Agent.Instructions, MaxInstructions
	if which == "context" {
		name, limit = e.Agent.Context, MaxContext
	} else if which != "instructions" {
		return "", false
	}
	if name == "" || !ValidPath(name) {
		return "", false
	}
	f, err := e.FS.Open(name)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false
	}
	if len(b) <= limit {
		return string(b), false
	}
	return cut(b, limit), true
}

// cut shortens b to at most n bytes, at the last line break when there is one in the second half, and never in the
// middle of a UTF-8 character.
func cut(b []byte, n int) string {
	b = b[:n]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	if i := strings.LastIndexByte(string(b), '\n'); i >= n/2 {
		b = b[:i+1]
	}
	return string(b)
}

var (
	idRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	toolRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	semverRE  = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	routeRE   = regexp.MustCompile(`^[A-Za-z0-9._~/-]{0,200}$`)
	versionRE = regexp.MustCompile(`(?i)^v\d{1,4}$`) // a version folder: v7, V12 (project.IsVersionDir)
)

// ValidID reports whether id is an extension id: ^[a-z][a-z0-9-]{0,31}$.
func ValidID(id string) bool { return idRE.MatchString(id) }
