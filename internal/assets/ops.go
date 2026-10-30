package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// Resolved is what the editor needs to read an asset (spec/assets.md, "Resolver").
type Resolved struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Hash          string `json:"hash"`
	Kind          string `json:"kind"`
	Stale         bool   `json:"stale,omitempty"`
	CatalogedHash string `json:"cataloged_hash,omitempty"`
}

// Resolve returns an asset's file, hash and kind. A metadata-only asset is hashed now (once); an in-place file that
// changed since it was cataloged is reported stale, with its current hash.
func (c *Catalog) Resolve(ctx context.Context, id string) (Resolved, error) {
	d, r, err := c.find(ctx, id)
	if err != nil {
		return Resolved{}, wrap(err)
	}
	path := d.filePath(r.locator)
	out := Resolved{ID: r.id, Path: path, Kind: r.kind}
	if path == "" {
		return out, errorf(CodeNotFound, "%s has no file", r.id)
	}
	st, err := os.Stat(path)
	if err != nil {
		return out, errorf(CodeNotFound, "%s: its file isn't there: %s", r.id, path)
	}
	if r.inStore && r.hash.Valid {
		out.Hash = r.hash.String
		return out, nil
	}
	if r.hash.Valid && r.mtime.Valid && r.size.Valid && r.size.Int64 == st.Size() && r.mtime.Int64 == st.ModTime().UnixNano() {
		out.Hash = r.hash.String
		return out, nil
	}
	h, size, mtime, err := d.fileHash(ctx, path)
	if err != nil {
		return out, wrap(err)
	}
	out.Hash = h
	if r.hash.Valid && r.hash.String != h {
		out.Stale, out.CatalogedHash = true, r.hash.String
		return out, nil
	}
	// First hash of a metadata-only asset (or a touched file with the same content): record it.
	err = d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE assets SET hash = ?, size = ?, mtime = ? WHERE id = ?`, h, size, mtime, r.id)
		return err
	})
	return out, wrap(err)
}

// SetArgs edit an entry: the only way to change a license.
type SetArgs struct {
	ID      string
	Title   *string
	Tags    []string // replaces the tags when not nil
	AddTags []string
	RmTags  []string
	Caption *string
	License *LicenseArgs // replaces the license when not nil
}

// Set edits an entry's title, tags, caption or license.
func (c *Catalog) Set(ctx context.Context, a SetArgs) (Entry, error) {
	d, r, err := c.find(ctx, a.ID)
	if err != nil {
		return Entry{}, wrap(err)
	}
	if a.License != nil {
		if err := a.License.check(); err != nil {
			return Entry{}, err
		}
		if a.License.Status == "" {
			return Entry{}, errorf(CodeBadRequest, "give the license status")
		}
		if a.License.Status == LicenseAttribution && a.License.Credit == "" && r.credit == "" {
			return Entry{}, errorf(CodeBadRequest, "an attribution license needs its credit line (--credit)")
		}
	}
	tags := unmarshalOr(r.tags, []string{})
	if a.Tags != nil {
		tags = normTags(a.Tags)
	}
	tags = normTags(append(tags, a.AddTags...))
	if len(a.RmTags) > 0 {
		rm := normTags(a.RmTags)
		var keep []string
		for _, t := range tags {
			if !containsStr(rm, t) {
				keep = append(keep, t)
			}
		}
		tags = normTags(keep)
	}
	err = d.write(ctx, func(tx *sql.Tx) error {
		sets := []string{"tags = ?", "updated = ?"}
		args := []any{mustJSON(tags), d.stamp()}
		if a.Title != nil {
			sets, args = append(sets, "title = ?"), append(args, *a.Title)
		}
		if a.Caption != nil {
			sets, args = append(sets, "caption = ?"), append(args, *a.Caption)
		}
		if l := a.License; l != nil {
			credit := l.Credit
			if credit == "" && l.Status == LicenseAttribution {
				credit = r.credit
			}
			sets = append(sets, "license = ?", "credit = ?", "license_source = ?", "license_note = ?", "content_id_risk = ?")
			args = append(args, l.Status, credit, firstNonEmpty(l.Source, r.licenseSource), l.Note, l.ContentIDRisk)
		}
		args = append(args, r.id)
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil { //nolint:gosec // fixed column names
			return err
		}
		return indexAsset(ctx, tx, r.id)
	})
	if err != nil {
		return Entry{}, wrap(err)
	}
	return c.Get(ctx, r.id)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// SQLResult is a read-only query's rows.
type SQLResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	More    bool     `json:"more"`
}

// SQL runs one read-only SELECT on a scope's catalog (project by default), plug-in views included.
func (c *Catalog) SQL(ctx context.Context, scope, query string, limit int) (SQLResult, error) {
	if err := checkSelect(query); err != nil {
		return SQLResult{}, err
	}
	sc, err := ParseScope(scope)
	if err != nil {
		return SQLResult{}, errorf(CodeBadRequest, "%v", err)
	}
	d, err := c.catalogFor(sc, false)
	if err != nil {
		return SQLResult{}, wrap(err)
	}
	if d == nil {
		return SQLResult{}, errorf(CodeNotFound, "the %s catalog is empty", scopeWord(sc))
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := d.ro.QueryContext(ctx, strings.TrimRight(strings.TrimSpace(query), ";"))
	if err != nil {
		return SQLResult{}, errorf(CodeBadRequest, "SQL error: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return SQLResult{}, wrap(err)
	}
	res := SQLResult{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if len(res.Rows) >= limit {
			res.More = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return res, wrap(err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return res, errorf(CodeBadRequest, "SQL error: %v", err)
	}
	return res, nil
}

// Rehash hashes an in-place asset's file now and records it.
func (c *Catalog) Rehash(ctx context.Context, id string) (Resolved, error) {
	d, r, err := c.find(ctx, id)
	if err != nil {
		return Resolved{}, wrap(err)
	}
	if r.inStore {
		return Resolved{ID: r.id, Path: d.filePath(r.locator), Hash: r.hash.String, Kind: r.kind}, nil
	}
	path := d.filePath(r.locator)
	h, size, mtime, err := d.fileHash(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Resolved{}, errorf(CodeNotFound, "%s: its file isn't there: %s", r.id, path)
		}
		return Resolved{}, wrap(err)
	}
	err = d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE assets SET hash = ?, size = ?, mtime = ?, updated = ? WHERE id = ?`, h, size,
			mtime, d.stamp(), r.id)
		return err
	})
	if err != nil {
		return Resolved{}, wrap(err)
	}
	return Resolved{ID: r.id, Path: path, Hash: h, Kind: r.kind}, nil
}

// String formats an Error for logs.
func (e *Error) String() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
