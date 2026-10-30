// Package lib is the shared library of spec/components.md, the Go side: components and brand kits as packages, their
// content hash, the lockfile, static registries (an index.json anyone can host), `retake lib add|search|pack|list|
// describe`, edit recipes, and lint through retake-edit. Nothing is published: a registry is a file or a URL the user
// gives. The editor (retake-edit) expands, locks, previews and lints; this package agrees with it on the formats.
package lib

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"lukechampine.com/blake3"

	"github.com/samahlstrom/retake/internal/assets"
)

// Formats.
const (
	ComponentSchema = "retake-component/0.1"
	KitSchema       = "retake-kit/0.1"
	LockSchema      = "retake-lock/0.1"
	RegistrySchema  = "retake-registry/0.1"
	RecipeSchema    = "retake-recipe/0.1"
)

// Package types.
const (
	TypeComponent = "component"
	TypeKit       = "kit"
	TypeRecipe    = "recipe"
)

// AllowedLicenses are the SPDX ids a package may carry (spec/components.md, "A component").
var AllowedLicenses = []string{"CC0-1.0", "MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "OFL-1.1",
	"CC-BY-4.0"}

// Error codes.
const (
	CodeBadRequest = "bad_request" // a missing or malformed argument
	CodeNotFound   = "not_found"   // no such package, version, timeline or registry
	CodeInvalid    = "invalid"     // a package that doesn't validate
	CodeLicense    = "license"     // the license gate refused
	CodeHash       = "hash"        // a file or package doesn't match its hash
	CodeConflict   = "conflict"    // the project's copy differs from the one being added
	CodeFailed     = "failed"      // anything else (I/O, the network, retake-edit)
)

// Error is an operation's failure, phrased for the agent.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"error"`
}

func (e *Error) Error() string { return e.Message }

func errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrap turns any error into an *Error (CodeFailed unless it already is one).
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var le *Error
	if errors.As(err, &le) {
		return le
	}
	var ae *assets.Error
	if errors.As(err, &ae) {
		return &Error{Code: ae.Code, Message: ae.Message}
	}
	return &Error{Code: CodeFailed, Message: err.Error()}
}

// Example is one prop set of a component.
type Example struct {
	Name        string         `json:"name"`
	Props       map[string]any `json:"props"`
	Duration    float64        `json:"duration,omitempty"`
	Description string         `json:"description,omitempty"`
}

// Component is the part of a component.json the Go side reads; the editor reads the rest.
type Component struct {
	Schema      string           `json:"schema,omitempty"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	License     string           `json:"license"`
	Authors     []string         `json:"authors,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Category    string           `json:"category,omitempty"`
	UseAs       []string         `json:"use_as"`
	Canvas      []int            `json:"canvas,omitempty"`
	Duration    float64          `json:"duration,omitempty"`
	Props       map[string]any   `json:"props"`
	Examples    []Example        `json:"examples"`
	Changelog   []map[string]any `json:"changelog,omitempty"`
}

// Ref is name@version.
func (c Component) Ref() string { return c.Name + "@" + c.Version }

// Kit is the part of a kit.json the Go side reads.
type Kit struct {
	Schema      string            `json:"schema,omitempty"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	License     json.RawMessage   `json:"license,omitempty"`
	Tokens      map[string]any    `json:"tokens,omitempty"`
	Components  []string          `json:"components,omitempty"`
	Rules       []map[string]any  `json:"rules,omitempty"`
	Assets      []assets.KitAsset `json:"assets,omitempty"`
}

// SPDX is the kit's license id: `license` as a string, or the catalog's license object's `spdx`.
func (k Kit) SPDX() string {
	var s string
	if json.Unmarshal(k.License, &s) == nil {
		return s
	}
	var o struct {
		SPDX string `json:"spdx"`
	}
	_ = json.Unmarshal(k.License, &o)
	return o.SPDX
}

var (
	nameRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	semverRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

// ParseRef splits name[@version]; version is "" when absent.
func ParseRef(s string) (name, version string, err error) {
	name, version, _ = strings.Cut(strings.TrimSpace(s), "@")
	if !nameRE.MatchString(name) {
		return "", "", errorf(CodeBadRequest, "%q isn't a package name (a-z, 0-9 and -)", name)
	}
	if version != "" && !semverRE.MatchString(version) {
		return "", "", errorf(CodeBadRequest, "%q isn't a semver version (1.0.0)", version)
	}
	return name, version, nil
}

// CompareVersions orders semver strings; pre-releases come before their release.
func CompareVersions(a, b string) int {
	split := func(v string) ([]int, string) {
		core, pre, _ := strings.Cut(v, "-")
		var out []int
		for _, p := range strings.Split(core, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out, pre
	}
	ca, pa := split(a)
	cb, pb := split(b)
	if c := slices.Compare(ca, cb); c != 0 {
		return c
	}
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	return strings.Compare(pa, pb)
}

// Package is a component or kit folder on disk.
type Package struct {
	Dir       string     `json:"path"`
	Type      string     `json:"type"`
	Component *Component `json:"-"`
	Kit       *Kit       `json:"-"`
}

// Name, Version and Ref of either type.
func (p Package) Name() string {
	if p.Kit != nil {
		return p.Kit.Name
	}
	return p.Component.Name
}

// Version is the package's version.
func (p Package) Version() string {
	if p.Kit != nil {
		return p.Kit.Version
	}
	return p.Component.Version
}

// Ref is name@version.
func (p Package) Ref() string { return p.Name() + "@" + p.Version() }

// License is the package's SPDX id.
func (p Package) License() string {
	if p.Kit != nil {
		return p.Kit.SPDX()
	}
	return p.Component.License
}

// ReadPackage reads a folder holding a component.json or a kit.json.
func ReadPackage(dir string) (Package, error) {
	if b, err := os.ReadFile(filepath.Join(dir, "component.json")); err == nil { //nolint:gosec // a package folder
		var c Component
		if err := json.Unmarshal(b, &c); err != nil {
			return Package{}, errorf(CodeInvalid, "%s/component.json: %v", dir, err)
		}
		if c.Name == "" || c.Version == "" {
			return Package{}, errorf(CodeInvalid, "%s/component.json has no name or version", dir)
		}
		if len(c.UseAs) == 0 {
			c.UseAs = []string{"overlay"}
		}
		return Package{Dir: dir, Type: TypeComponent, Component: &c}, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "kit.json")) //nolint:gosec // a package folder
	if err != nil {
		return Package{}, errorf(CodeNotFound, "%s holds no component.json or kit.json", dir)
	}
	var k Kit
	if err := json.Unmarshal(b, &k); err != nil {
		return Package{}, errorf(CodeInvalid, "%s/kit.json: %v", dir, err)
	}
	if k.Name == "" || k.Version == "" {
		return Package{}, errorf(CodeInvalid, "%s/kit.json has no name or version", dir)
	}
	return Package{Dir: dir, Type: TypeKit, Kit: &k}, nil
}

// PackageFiles lists a package's files: slash-separated paths relative to dir, sorted, without previews/, hidden
// files and folders, and links. They are what the package hash covers and what a registry entry lists.
func PackageFiles(dir string) ([]string, error) { return assets.PackageFiles(dir) }

// FileHash is b3:<hex> of a file.
func FileHash(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // a package file
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := blake3.New(32, nil)
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("b3:%x", h.Sum(nil)), n, nil
}

// PackageHash is the content hash of a package folder (spec/components.md, "The lockfile"), as the editor computes it.
func PackageHash(dir string) (string, error) { return assets.PackageHash(dir) }

// SearchRoots are the folders packages are found in, as the editor looks: components/ next to the timeline,
// $RETAKE_COMPONENT_PATH, then the user library's components/.
func SearchRoots(timelineDir string, getenv func(string) string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}
	var out []string
	if timelineDir != "" {
		out = append(out, filepath.Join(timelineDir, "components"))
	}
	for _, p := range filepath.SplitList(getenv("RETAKE_COMPONENT_PATH")) {
		if p != "" {
			out = append(out, p)
		}
	}
	return append(out, UserComponents(getenv))
}

// UserComponents is the user library's component folder: <library>/components.
func UserComponents(getenv func(string) string) string {
	return filepath.Join(assets.LibraryDir(getenv), "components")
}

// FindPackage finds name@version in the roots: <root>/<name>@<version>/, then <root>/<name>/ holding that version.
// An empty version finds the newest.
func FindPackage(roots []string, name, version string) (Package, error) {
	var best *Package
	for _, p := range Available(roots) {
		if p.Name() != name {
			continue
		}
		if version != "" {
			if p.Version() == version {
				return p, nil
			}
			continue
		}
		if best == nil || CompareVersions(p.Version(), best.Version()) > 0 {
			pp := p
			best = &pp
		}
	}
	if best != nil {
		return *best, nil
	}
	if version != "" {
		return Package{}, errorf(CodeNotFound, "%s@%s isn't in %s", name, version, strings.Join(roots, ", "))
	}
	return Package{}, errorf(CodeNotFound, "%s isn't in %s", name, strings.Join(roots, ", "))
}

// Available lists every package in the roots, in root order (a later copy of the same ref is skipped).
func Available(roots []string) []Package {
	var out []Package
	seen := map[string]bool{}
	for _, r := range roots {
		ents, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p, err := ReadPackage(filepath.Join(r, e.Name()))
			if err != nil || seen[p.Ref()] {
				continue
			}
			seen[p.Ref()] = true
			out = append(out, p)
		}
	}
	return out
}

// LockEntry pins one package.
type LockEntry struct {
	Type    string `json:"type"`
	Hash    string `json:"hash"`
	Path    string `json:"path,omitempty"`
	Source  string `json:"source,omitempty"`
	License string `json:"license,omitempty"`
}

// Lock is a timeline's lockfile, <stem>.lock.
type Lock struct {
	Schema   string               `json:"schema"`
	Packages map[string]LockEntry `json:"packages"`
}

// LockPath is <stem>.lock next to a timeline.
func LockPath(timeline string) string {
	return strings.TrimSuffix(timeline, filepath.Ext(timeline)) + ".lock"
}

// ReadLock reads a lockfile; a missing one is empty.
func ReadLock(path string) (Lock, error) {
	l := Lock{Schema: LockSchema, Packages: map[string]LockEntry{}}
	b, err := os.ReadFile(path) //nolint:gosec // the project's lockfile
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, errorf(CodeInvalid, "%s isn't a lockfile: %v", path, err)
	}
	if l.Packages == nil {
		l.Packages = map[string]LockEntry{}
	}
	return l, nil
}

// Write writes the lockfile atomically.
func (l Lock) Write(path string) error {
	if l.Schema == "" {
		l.Schema = LockSchema
	}
	return writeJSON(path, l)
}

// writeJSON writes v as indented JSON with a final newline, atomically.
func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil { //nolint:gosec // a project file, not a secret
		return err
	}
	return os.Rename(tmp, path)
}

// licenseAllowed reports whether an SPDX id passes the gate.
func licenseAllowed(spdx string) bool { return slices.Contains(AllowedLicenses, spdx) }

// CatalogStatus maps a package's SPDX license to a catalog license status (spec/assets.md, "Licenses").
func CatalogStatus(spdx string) (status, credit string) {
	switch spdx {
	case "CC-BY-4.0":
		return assets.LicenseAttribution, "CC BY 4.0"
	case "":
		return assets.LicenseReviewOnly, ""
	}
	if licenseAllowed(spdx) {
		return assets.LicenseCleared, ""
	}
	return assets.LicenseReviewOnly, ""
}
