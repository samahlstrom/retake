package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// LicenseArgs set an entry's license.
type LicenseArgs struct {
	Status        string `json:"status,omitempty"`
	Credit        string `json:"credit,omitempty"`
	Source        string `json:"source,omitempty"`
	Note          string `json:"note,omitempty"`
	ContentIDRisk string `json:"content_id_risk,omitempty"`
}

// check validates the status and risk; an empty status stays empty.
func (l LicenseArgs) check() error {
	if l.Status != "" && !slices.Contains(LicenseStatuses, l.Status) {
		return errorf(CodeBadRequest, "unknown license status %q: use one of %s", l.Status, strings.Join(LicenseStatuses, ", "))
	}
	switch l.ContentIDRisk {
	case "", "low", "medium", "high":
	default:
		return errorf(CodeBadRequest, "content_id_risk is low, medium or high, not %q", l.ContentIDRisk)
	}
	return nil
}

// orDefault fills an import's license: review-only with a note when no status was given.
func (l LicenseArgs) orDefault() LicenseArgs {
	if l.Status == "" {
		l.Status = LicenseReviewOnly
		if l.Note == "" {
			l.Note = "no license given at import"
		}
	}
	return l
}

// Proxy policies for ImportArgs.Proxy.
const (
	ProxyAuto   = ""
	ProxyAlways = "always"
	ProxyNever  = "never"
)

// ImportArgs are retake assets import's options (spec/assets.md, "Import").
type ImportArgs struct {
	Paths      []string
	Scope      string // proj (default) or lib
	License    LicenseArgs
	Tags       []string
	Title      string // single file only
	Caption    string
	Name       string // the id name, single file only
	InPlace    bool   // reference the files where they are, whatever their size
	Proxy      string // ProxyAuto, ProxyAlways or ProxyNever
	Transcribe bool
	Track      int     // which audio stream to transcribe: 0 = the first
	EveryS     float64 // contact sheet interval; 0 = automatic
	Via        string  // plugin[/indexer]
	Args       map[string]any
	Importer   string // provenance: cli, mcp

	kit bool // a brand kit's file: always stored, never reported as a duplicate
}

// Import statuses.
const (
	StatusImported  = "imported"
	StatusDuplicate = "duplicate"
	StatusUpdated   = "updated"
	StatusSkipped   = "skipped"
)

// ImportItem is what happened to one file or plug-in record.
type ImportItem struct {
	Path        string   `json:"path,omitempty"`
	Key         string   `json:"key,omitempty"` // a plug-in record's key
	ID          string   `json:"id,omitempty"`
	Status      string   `json:"status"`
	Kind        string   `json:"kind,omitempty"`
	Hash        string   `json:"hash,omitempty"`
	DuplicateOf string   `json:"duplicate_of,omitempty"`
	Near        []Near   `json:"near,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// ImportResult is an import's outcome.
type ImportResult struct {
	Items      []ImportItem   `json:"items"`
	Imported   int            `json:"imported"`
	Duplicates int            `json:"duplicates"`
	Updated    int            `json:"updated"`
	Skipped    int            `json:"skipped"`
	Plugin     string         `json:"plugin,omitempty"`
	Spans      int            `json:"spans,omitempty"`
	Clocks     int            `json:"clocks,omitempty"`
	Stats      map[string]any `json:"stats,omitempty"`
	Log        []string       `json:"log,omitempty"`
}

func (r *ImportResult) add(it ImportItem) {
	r.Items = append(r.Items, it)
	switch it.Status {
	case StatusImported:
		r.Imported++
	case StatusDuplicate:
		r.Duplicates++
	case StatusUpdated:
		r.Updated++
	default:
		r.Skipped++
	}
}

// Import brings files into the catalog: hash, dedup, copy or reference in place, probe, previews, near-duplicates,
// transcript, search index. With Via it runs a plug-in's indexer instead.
func (c *Catalog) Import(ctx context.Context, a ImportArgs) (ImportResult, error) {
	if a.Importer == "" {
		a.Importer = "cli"
	}
	if err := a.License.check(); err != nil {
		return ImportResult{}, err
	}
	scope, err := ParseScope(a.Scope)
	if err != nil {
		return ImportResult{}, errorf(CodeBadRequest, "%v", err)
	}
	if scope == ScopeBrand {
		return ImportResult{}, errorf(CodeBadRequest, "brand assets come from kits: retake assets kit install <dir>")
	}
	switch a.Proxy {
	case ProxyAuto, ProxyAlways, ProxyNever:
	default:
		return ImportResult{}, errorf(CodeBadRequest, "proxy is always or never, not %q", a.Proxy)
	}
	if a.Via != "" {
		return c.importVia(ctx, scope, a)
	}
	if len(a.Paths) == 0 {
		return ImportResult{}, errorf(CodeBadRequest, "give one or more files or folders to import")
	}
	type job struct{ path, name string }
	var jobs []job
	var components []string
	var res ImportResult
	for _, p := range a.Paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return res, wrap(err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return res, errorf(CodeNotFound, "no such file or folder: %s", abs)
		}
		if st.IsDir() && isComponentDir(abs) { // a component package is one entry (spec/components.md)
			components = append(components, abs)
			continue
		}
		if !st.IsDir() {
			name := a.Name
			if name == "" {
				name = slug(strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
			}
			jobs = append(jobs, job{abs, name})
			continue
		}
		if a.Name != "" || a.Title != "" {
			return res, errorf(CodeBadRequest, "--name and --title are for a single file, not a folder")
		}
		base := filepath.Base(abs)
		err = filepath.WalkDir(abs, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path != abs && strings.HasPrefix(de.Name(), ".") {
				if de.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if de.IsDir() || !de.Type().IsRegular() {
				return nil
			}
			if kindOf(path) == "" {
				res.add(ImportItem{Path: path, Status: StatusSkipped, Reason: "not a media, image or font file"})
				return nil
			}
			rel, _ := filepath.Rel(abs, path)
			jobs = append(jobs, job{path, slug(base + "/" + strings.TrimSuffix(rel, filepath.Ext(rel)))})
			return nil
		})
		if err != nil {
			return res, wrap(err)
		}
	}
	if len(a.Paths) == 1 && len(jobs) == 0 && len(res.Items) == 0 && len(components) == 0 {
		return res, errorf(CodeNotFound, "nothing to import in %s", a.Paths[0])
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].path < jobs[j].path })
	if len(jobs) == 0 && len(components) == 0 {
		return res, nil
	}
	d, err := c.catalogFor(scope, true)
	if err != nil {
		return res, wrap(err)
	}
	for _, dir := range components {
		res.add(c.importComponent(ctx, d, dir, a))
	}
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.add(c.importFile(ctx, d, j.path, j.name, a))
	}
	return res, nil
}

// importFile imports one file into d. Failures are the item's, not the whole import's.
func (c *Catalog) importFile(ctx context.Context, d *db, path, name string, a ImportArgs) ImportItem {
	it := ImportItem{Path: path}
	if !validName(name) {
		it.Status, it.Reason = StatusSkipped, fmt.Sprintf("%q can't be an asset id", name)
		return it
	}
	kind := kindOf(path)
	if kind == "" {
		kind = KindOther
	}
	it.Kind = kind
	hash, size, mtime, err := d.fileHash(ctx, path)
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.Hash = hash
	if !a.kit { // a kit owns its copy
		dup, err := c.byHash(ctx, hash)
		if err != nil {
			it.Status, it.Reason = StatusSkipped, err.Error()
			return it
		}
		if dup != "" {
			it.Status, it.DuplicateOf, it.ID = StatusDuplicate, dup, dup
			return it
		}
	}
	inPlace := !a.kit && (a.InPlace || size >= c.o.InPlaceOver)
	locator, file := fileLocator(path), path
	ext := strings.ToLower(filepath.Ext(path))
	if !inPlace {
		r, h, _, err := d.blobs().putFile(path, ext)
		if err != nil {
			it.Status, it.Reason = StatusSkipped, "copying into the store: "+err.Error()
			return it
		}
		if h != hash { // the file changed between hashing and copying
			hash, it.Hash = h, h
		}
		locator = "blob:" + r
		file, _ = d.blobs().path(r)
	}

	var media Media
	var img image.Image
	switch kind {
	case KindVideo, KindAudio:
		if m, err := c.tools.probe(ctx, file); err != nil {
			it.Warnings = append(it.Warnings, err.Error())
		} else {
			media = m
			kind = refineKind(kind, m)
			it.Kind = kind
		}
	case KindImage:
		img, media, err = c.loadImage(ctx, file)
		if err != nil {
			it.Warnings = append(it.Warnings, err.Error())
		}
	case KindFont:
		if m, err := fontMedia(file); err != nil {
			it.Warnings = append(it.Warnings, err.Error())
		} else {
			media = m
		}
	}

	lic := a.License.orDefault()
	title := a.Title
	if title == "" {
		title = titleFromFile(path)
	}
	prov := Provenance{Importer: a.Importer, Source: path, Seen: d.stamp()}
	var id string
	err = d.write(ctx, func(tx *sql.Tx) error {
		n, err := d.uniqueName(ctx, tx, d.scope, name)
		if err != nil {
			return err
		}
		id = MakeID(d.scope, n)
		now := d.stamp()
		var mt any
		if inPlace {
			mt = mtime
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO assets (id, scope, kind, hash, size, mtime, locator, in_store, media,
			title, tags, caption, license, credit, license_source, license_note, content_id_risk, provenance, created,
			updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, d.scope, kind, hash, size, mt, locator, !inPlace, mustJSON(media), title, mustJSON(normTags(a.Tags)),
			a.Caption, lic.Status, lic.Credit, lic.Source, lic.Note, lic.ContentIDRisk, mustJSON(prov), now, now)
		if err != nil {
			return err
		}
		return indexAsset(ctx, tx, id)
	})
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.ID, it.Status = id, StatusImported

	// Everything after the row exists is best effort: warnings, not failures.
	src := sourceIdentity(hash, size, mtime)
	pv, warns := c.makePreviews(ctx, d, id, file, kind, media, img, src, a)
	it.Warnings = append(it.Warnings, warns...)
	if err := d.setJSON(ctx, id, "previews", pv); err != nil {
		it.Warnings = append(it.Warnings, err.Error())
	}
	if kind == KindVideo && wantProxy(a.Proxy, media) {
		if err := c.makeProxy(ctx, d, id, file, src); err != nil {
			it.Warnings = append(it.Warnings, "proxy: "+err.Error())
		}
	}
	if img != nil {
		near, err := c.nearImages(ctx, d, id, img)
		if err != nil {
			it.Warnings = append(it.Warnings, "near-duplicate check: "+err.Error())
		}
		it.Near = near
	}
	if a.Transcribe && (kind == KindAudio || kind == KindVideo) && len(media.Audio) > 0 {
		if err := c.transcribe(ctx, d, id, file, a.Track, media); err != nil {
			it.Warnings = append(it.Warnings, "transcript: "+err.Error())
		}
	}
	return it
}

// sourceIdentity names a file's content for derived files: its hash, else its size and mtime.
func sourceIdentity(hash string, size, mtime int64) string {
	if hash != "" {
		return hash
	}
	return fmt.Sprintf("stat:%d:%d", size, mtime)
}

func wantProxy(policy string, m Media) bool {
	switch policy {
	case ProxyAlways:
		return m.hasVideo()
	case ProxyNever:
		return false
	}
	return m.hasVideo() && m.duration() > 0 && m.duration() <= 600 && m.Size != nil &&
		(m.Size[0] > 960 || m.Size[1] > 540)
}

// titleFromFile makes a title from a file name: the part before a "__" field separator, with - and _ as spaces.
func titleFromFile(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if head, _, ok := strings.Cut(base, "__"); ok && head != "" {
		base = head
	}
	return strings.Join(strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' }), " ")
}

// byHash returns the id of an asset with this hash in any visible catalog, project first.
func (c *Catalog) byHash(ctx context.Context, hash string) (string, error) {
	cats, err := c.visible()
	if err != nil {
		return "", err
	}
	for _, d := range cats {
		var id string
		err := d.rw.QueryRowContext(ctx, `SELECT id FROM assets WHERE hash = ? ORDER BY created LIMIT 1`, hash).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}

// jsonCols are the JSON columns setJSON may write.
var jsonCols = map[string]bool{"previews": true, "proxy": true, "near": true, "media": true, "meta": true,
	"tags": true, "provenance": true}

// setJSON writes one JSON column of an asset.
func (d *db) setJSON(ctx context.Context, id, col string, v any) error {
	if !jsonCols[col] {
		return fmt.Errorf("setJSON: column %s", col)
	}
	return d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE assets SET `+col+` = ?, updated = ? WHERE id = ?`, mustJSON(v), d.stamp(), id) //nolint:gosec // col is from the allowlist above
		if err != nil {
			return err
		}
		if col == "tags" {
			return indexAsset(ctx, tx, id)
		}
		return nil
	})
}

// putDerived records a derived blob of an asset.
func (d *db) putDerived(ctx context.Context, asset, recipe, source, blob string, meta any) error {
	return d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO derived (asset, recipe, source, blob, meta, created)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (asset, recipe) DO UPDATE SET source = excluded.source,
			blob = excluded.blob, meta = excluded.meta, created = excluded.created`,
			asset, recipe, source, blob, mustJSON(meta), d.stamp())
		return err
	})
}

// derivedBlob returns a derived blob's ref if one exists for this source and its file is still there.
func (d *db) derivedBlob(ctx context.Context, asset, recipe, source string) string {
	var blob string
	if err := d.rw.QueryRowContext(ctx, `SELECT blob FROM derived WHERE asset = ? AND recipe = ? AND source = ?`,
		asset, recipe, source).Scan(&blob); err != nil {
		return ""
	}
	p, err := d.blobs().path(blob)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return blob
}
