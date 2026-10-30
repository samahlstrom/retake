// Package assets is the asset catalog of spec/assets.md: a project catalog (<root>/.retake/assets.db) and a
// user-wide library (lib: and brand: scopes), each SQLite with a content-addressed blob store, previews an agent can
// look at, licenses, spans and clocks, full-text search, and plug-ins that speak JSON over stdio. The CLI
// (retake assets …) and the MCP tools both call the operations here.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/transcribe"
)

// License statuses.
const (
	LicenseOwned       = "owned"
	LicenseCleared     = "cleared"
	LicenseAttribution = "attribution"
	LicenseReviewOnly  = "review-only"
	LicenseBlocked     = "blocked"
)

// LicenseStatuses lists the statuses in order, from most to least free.
var LicenseStatuses = []string{LicenseOwned, LicenseCleared, LicenseAttribution, LicenseReviewOnly, LicenseBlocked}

// Error codes.
const (
	CodeBadRequest = "bad_request" // a missing or malformed argument
	CodeNotFound   = "not_found"   // no such asset, span, plug-in or file
	CodeAmbiguous  = "ambiguous"   // an unprefixed id that two brand kits hold
	CodePlugin     = "plugin"      // a plug-in failed or answered wrongly
	CodeFailed     = "failed"      // anything else (I/O, the database, ffmpeg)
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
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return &Error{Code: CodeFailed, Message: err.Error()}
}

// Transcriber transcribes an audio file (internal/transcribe's interface).
type Transcriber = transcribe.Transcriber

// VectorIndex is the seam for embedding search (later): it returns asset ids, best first, for a query. Search fuses
// its list with the full-text lists by reciprocal rank.
type VectorIndex interface {
	Search(ctx context.Context, query string, limit int) ([]string, error)
}

// Options configure a Catalog. Zero fields get defaults.
type Options struct {
	Root    string              // the project root; "" = no project (library only)
	Library string              // the library folder; "" = $RETAKE_LIBRARY, else $XDG_DATA_HOME/retake/library
	Getenv  func(string) string // nil = os.Getenv
	Log     io.Writer           // warnings and plug-in stderr; nil = discarded
	Now     func() time.Time
	FFmpeg  string // "" = ffmpeg on PATH
	FFprobe string // "" = ffprobe on PATH
	// Transcriber returns the configured transcriber for --transcribe; nil = transcription isn't configured.
	Transcriber func() (Transcriber, error)
	// EditBin is retake-edit, for license reports on timelines; "" = $RETAKE_EDIT, else retake-edit on PATH.
	EditBin string
	// InPlaceOver is the file size from which imports are referenced in place; 0 = 512 MiB.
	InPlaceOver int64
	// Vectors is the embedding index; nil = none yet.
	Vectors VectorIndex
}

// DefaultInPlaceOver is the size from which imported files are referenced in place rather than copied.
const DefaultInPlaceOver = 512 << 20

// Catalog is the asset catalog of one project plus the user's library.
type Catalog struct {
	o     Options
	tools tools

	mu   sync.Mutex
	proj *db
	lib  *db

	pluginsOnce sync.Once
	plugins     *pluginSet
}

// Open returns the catalog. Databases are opened on first use; reads never create them.
func Open(o Options) (*Catalog, error) {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.FFmpeg == "" {
		o.FFmpeg = "ffmpeg"
	}
	if o.FFprobe == "" {
		o.FFprobe = "ffprobe"
	}
	if o.InPlaceOver == 0 {
		o.InPlaceOver = DefaultInPlaceOver
	}
	if o.Library == "" {
		o.Library = LibraryDir(o.Getenv)
	}
	if o.Root != "" {
		abs, err := filepath.Abs(o.Root)
		if err != nil {
			return nil, err
		}
		o.Root = abs
	}
	return &Catalog{o: o, tools: tools{ffmpeg: o.FFmpeg, ffprobe: o.FFprobe}}, nil
}

// LibraryDir returns the user library's folder: $RETAKE_LIBRARY, else $XDG_DATA_HOME/retake/library, else
// ~/.local/share/retake/library.
func LibraryDir(getenv func(string) string) string {
	if d := getenv("RETAKE_LIBRARY"); d != "" {
		return d
	}
	if d := getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "retake", "library")
	}
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".local", "share", "retake", "library")
}

// FindRoot returns the project root for a working folder: the nearest folder at or above dir with a .retake
// folder, else dir itself.
func FindRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; {
		if st, err := os.Stat(filepath.Join(d, ".retake")); err == nil && st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		d = parent
	}
}

// Root returns the project root ("" without a project).
func (c *Catalog) Root() string { return c.o.Root }

// Library returns the library folder.
func (c *Catalog) Library() string { return c.o.Library }

// Close closes the open databases.
func (c *Catalog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, d := range []*db{c.proj, c.lib} {
		if d != nil {
			errs = append(errs, d.close())
		}
	}
	c.proj, c.lib = nil, nil
	return errors.Join(errs...)
}

// catalogFor returns the database holding a scope: the project's for ScopeProject, the library for brand and lib.
// With create unset it returns nil, nil when that catalog doesn't exist yet.
func (c *Catalog) catalogFor(scope string, create bool) (*db, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if scope == ScopeProject {
		if c.o.Root == "" {
			if create {
				return nil, errorf(CodeBadRequest, "no project: pass --project DIR, or use the lib scope")
			}
			return nil, nil
		}
		if c.proj == nil {
			d, err := openDB(filepath.Join(c.o.Root, ".retake"), ScopeProject, create, c.o.Now)
			if err != nil || d == nil {
				return nil, err
			}
			c.proj = d
		}
		return c.proj, nil
	}
	if c.lib == nil {
		d, err := openDB(c.o.Library, ScopeLib, create, c.o.Now)
		if err != nil || d == nil {
			return nil, err
		}
		c.lib = d
	}
	return c.lib, nil
}

// visible returns the catalogs that exist, in lookup order: project, then library.
func (c *Catalog) visible() ([]*db, error) {
	var out []*db
	for _, scope := range []string{ScopeProject, ScopeLib} {
		d, err := c.catalogFor(scope, false)
		if err != nil {
			return nil, err
		}
		if d != nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// find returns the catalog and row of an asset id, following the lookup order for unprefixed ids.
func (c *Catalog) find(ctx context.Context, id string) (*db, row, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, row{}, errorf(CodeBadRequest, "give an asset id, e.g. proj:memes/thanos")
	}
	scope, name, ok := ParseID(id)
	if !ok {
		return c.findUnscoped(ctx, id)
	}
	d, err := c.catalogFor(scope, false)
	if err != nil {
		return nil, row{}, err
	}
	if d == nil {
		return nil, row{}, errorf(CodeNotFound, "no asset %s: the %s catalog is empty", id, scopeWord(scope))
	}
	if scope == ScopeBrand {
		r, err := d.findBrand(ctx, name)
		if err != nil {
			return nil, row{}, err
		}
		return d, r, nil
	}
	r, err := d.getRow(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, row{}, errorf(CodeNotFound, "no asset %s (search with asset_search, or list with retake assets search)", id)
	}
	return d, r, err
}

func scopeWord(scope string) string {
	if scope == ScopeProject {
		return "project"
	}
	return "library"
}

// findBrand finds brand:<kit>[@<version>]/<key>: the newest installed version when none is given.
func (d *db) findBrand(ctx context.Context, name string) (row, error) {
	kit, version, key, ok := brandParts(name)
	if !ok {
		return row{}, errorf(CodeBadRequest, "brand ids are brand:<kit>/<key> or brand:<kit>@<version>/<key>, not brand:%s", name)
	}
	if version != "" {
		r, err := d.getRow(ctx, MakeID(ScopeBrand, kit+"@"+version+"/"+key))
		if errors.Is(err, sql.ErrNoRows) {
			return row{}, errorf(CodeNotFound, "no asset brand:%s", name)
		}
		return r, err
	}
	rows, err := d.rw.QueryContext(ctx, `SELECT `+rowCols+` FROM assets WHERE scope = 'brand' AND id LIKE ? ESCAPE '\'`,
		likeEscape(prefixBrand+kit+"@")+"%/"+likeEscape(key))
	if err != nil {
		return row{}, err
	}
	defer func() { _ = rows.Close() }()
	var best row
	bestV := ""
	found := false
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return row{}, err
		}
		_, n, _ := ParseID(r.id)
		_, v, k, _ := brandParts(n)
		if k != key {
			continue
		}
		if !found || compareVersions(v, bestV) > 0 {
			best, bestV, found = r, v, true
		}
	}
	if err := rows.Err(); err != nil {
		return row{}, err
	}
	if !found {
		return row{}, errorf(CodeNotFound, "no asset brand:%s (installed kits: retake assets kit list)", name)
	}
	return best, nil
}

// findUnscoped looks an id up as proj:<id>, then in each installed brand kit, then lib:<id>.
func (c *Catalog) findUnscoped(ctx context.Context, name string) (*db, row, error) {
	if d, err := c.catalogFor(ScopeProject, false); err != nil {
		return nil, row{}, err
	} else if d != nil {
		r, err := d.getRow(ctx, MakeID(ScopeProject, name))
		if err == nil {
			return d, r, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, row{}, err
		}
	}
	lib, err := c.catalogFor(ScopeLib, false)
	if err != nil {
		return nil, row{}, err
	}
	if lib != nil {
		kits, err := lib.kitNames(ctx)
		if err != nil {
			return nil, row{}, err
		}
		var hits []row
		for _, k := range kits {
			r, err := lib.findBrand(ctx, k+"/"+name)
			if err == nil {
				hits = append(hits, r)
				continue
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeNotFound {
				return nil, row{}, err
			}
		}
		switch len(hits) {
		case 1:
			return lib, hits[0], nil
		case 0:
		default:
			ids := make([]string, len(hits))
			for i, h := range hits {
				ids[i] = h.id
			}
			return nil, row{}, errorf(CodeAmbiguous, "%s is in several brand kits: %s; use the full id", name,
				strings.Join(ids, ", "))
		}
		r, err := lib.getRow(ctx, MakeID(ScopeLib, name))
		if err == nil {
			return lib, r, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, row{}, err
		}
	}
	return nil, row{}, errorf(CodeNotFound, "no asset %s in the project, the brand kits or the library", name)
}

func (d *db) kitNames(ctx context.Context) ([]string, error) {
	rows, err := d.rw.QueryContext(ctx, `SELECT DISTINCT name FROM kits ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Get returns an asset's entry.
func (c *Catalog) Get(ctx context.Context, id string) (Entry, error) {
	d, r, err := c.find(ctx, id)
	if err != nil {
		return Entry{}, wrap(err)
	}
	e, err := d.entry(ctx, r)
	return e, wrap(err)
}

// uniqueName returns name, or name-2, name-3, … whichever id is free in d.
func (d *db) uniqueName(ctx context.Context, tx *sql.Tx, scope, name string) (string, error) {
	for i := 1; ; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s-%d", name, i)
		}
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM assets WHERE id = ?`, MakeID(scope, n)).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return n, nil
		}
		if err != nil {
			return "", err
		}
	}
}

// indexAsset rewrites an asset's row in the search index.
func indexAsset(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM assets_fts WHERE id = ?`, id); err != nil {
		return err
	}
	var title, tags, caption string
	var tx2 sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT a.title, a.tags, a.caption, t.text FROM assets a
		LEFT JOIN transcripts t ON t.asset = a.id WHERE a.id = ?`, id).Scan(&title, &tags, &caption, &tx2)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tagList := unmarshalOr(tags, []string{})
	// Tags are also searchable with their separators as spaces: "time-skip" finds "time skip".
	tagText := strings.Join(tagList, " ")
	_, err = tx.ExecContext(ctx, `INSERT INTO assets_fts (id, title, tags, caption, transcript) VALUES (?, ?, ?, ?, ?)`,
		id, title, tagText, caption, tx2.String)
	return err
}

// indexSpan rewrites a span's row in the search index.
func indexSpan(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM spans_fts WHERE span = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO spans_fts (span, label, type, data)
		SELECT id, label, replace(type, '_', ' '), data FROM spans WHERE id = ?`, id)
	return err
}

// normTags trims, lowercases and dedups tags, keeping their order.
func normTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// SplitTags splits a tag list as people write it: commas or semicolons.
func SplitTags(s string) []string {
	return normTags(strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }))
}
