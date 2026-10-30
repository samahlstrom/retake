package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Blob says where an asset's bytes are.
type Blob struct {
	Hash    *string `json:"hash"` // b3:<hex>; null until hashed
	Size    *int64  `json:"size,omitempty"`
	Locator string  `json:"locator"`
	InStore bool    `json:"in_store"`
	Path    string  `json:"path"`   // where to read it now
	Exists  bool    `json:"exists"` // whether that file is there
}

// Proxy is a smaller copy of a video for fast frame grabs.
type Proxy struct {
	Hash string `json:"hash"` // a blob ref
	Size [2]int `json:"size"`
	GOP  int    `json:"gop"`
}

// Loudness is an audio asset's EBU R128 measurement.
type Loudness struct {
	Integrated float64 `json:"integrated_lufs"`
	Range      float64 `json:"range_lu"`
	Peak       float64 `json:"peak_db"`
}

// Previews are an asset's preview blobs by kind. Paths holds their files.
type Previews struct {
	ContactSheet string            `json:"contact_sheet,omitempty"`
	EveryS       float64           `json:"every_s,omitempty"`
	Waveform     string            `json:"waveform,omitempty"`
	Loudness     *Loudness         `json:"loudness,omitempty"`
	Thumbnail    string            `json:"thumbnail,omitempty"`
	Specimen     string            `json:"specimen,omitempty"`
	Paths        map[string]string `json:"paths,omitempty"`
}

// TranscriptInfo says an asset was transcribed.
type TranscriptInfo struct {
	Model string `json:"model"`
	Track int    `json:"track"`
	Words int    `json:"words"`
}

// Epoch is one piece of a clock: domain_t = DomainT0 + (asset_t - AssetT[0]) * Rate within AssetT.
type Epoch struct {
	AssetT   [2]float64 `json:"asset_t"`
	DomainT0 float64    `json:"domain_t0"`
	Rate     float64    `json:"rate"`
}

// Clock maps an asset's time to a domain's.
type Clock struct {
	Domain     string  `json:"domain"`
	Plugin     string  `json:"plugin,omitempty"`
	Epochs     []Epoch `json:"epochs"`
	Confidence string  `json:"confidence,omitempty"`
	Method     string  `json:"method,omitempty"`
}

// toDomain maps an asset time to the domain, if an epoch covers it.
func (c Clock) toDomain(t float64) (float64, bool) {
	for _, e := range c.Epochs {
		if t >= e.AssetT[0] && t <= e.AssetT[1] {
			r := e.Rate
			if r == 0 {
				r = 1
			}
			return e.DomainT0 + (t-e.AssetT[0])*r, true
		}
	}
	return 0, false
}

// toAsset maps a domain time to the asset, if an epoch covers it.
func (c Clock) toAsset(dt float64) (float64, bool) {
	for _, e := range c.Epochs {
		r := e.Rate
		if r == 0 {
			r = 1
		}
		t := e.AssetT[0] + (dt-e.DomainT0)/r
		if t >= e.AssetT[0]-1e-9 && t <= e.AssetT[1]+1e-9 {
			return t, true
		}
	}
	return 0, false
}

// License is an entry's license record (spec/assets.md, "Licenses").
type License struct {
	Status        string `json:"status"`
	Publish       bool   `json:"publish"`
	Credit        string `json:"credit,omitempty"`
	Source        string `json:"source,omitempty"`
	Note          string `json:"note,omitempty"`
	ContentIDRisk string `json:"content_id_risk,omitempty"`
}

// Provenance says where an entry came from.
type Provenance struct {
	Importer string `json:"importer"`
	Source   string `json:"source,omitempty"`
	Seen     string `json:"seen,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Recipe   string `json:"recipe,omitempty"`
}

// Near is a near-duplicate found at import.
type Near struct {
	ID       string `json:"id"`
	Distance int    `json:"distance"`
}

// Entry is a catalog entry (spec/assets.md, "A catalog entry").
type Entry struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Scope      string          `json:"scope"`
	Blob       Blob            `json:"blob"`
	Media      Media           `json:"media"`
	Proxy      *Proxy          `json:"proxy"`
	Title      string          `json:"title"`
	Tags       []string        `json:"tags"`
	Caption    string          `json:"caption"`
	Transcript *TranscriptInfo `json:"transcript"`
	Previews   Previews        `json:"previews"`
	Spans      int             `json:"spans"`
	Clocks     []Clock         `json:"clocks"`
	License    License         `json:"license"`
	Provenance Provenance      `json:"provenance"`
	Near       []Near          `json:"near"`
	Meta       map[string]any  `json:"meta"`
	Plugin     string          `json:"plugin,omitempty"`
}

// row is an assets row as stored.
type row struct {
	id, scope, kind                         string
	hash                                    sql.NullString
	size, mtime                             sql.NullInt64
	locator                                 string
	inStore                                 bool
	media                                   string
	proxy                                   sql.NullString
	title, tags, caption, previews          string
	license, credit, licenseSource, licNote string
	risk, phash, near, provenance           string
	plugin                                  string
	pluginKey                               sql.NullString
	meta, created, updated                  string
}

const rowCols = `id, scope, kind, hash, size, mtime, locator, in_store, media, proxy, title, tags, caption, previews,
	license, credit, license_source, license_note, content_id_risk, phash, near, provenance, plugin, plugin_key, meta,
	created, updated`

type scanner interface{ Scan(dest ...any) error }

func scanRow(s scanner) (row, error) {
	var r row
	err := s.Scan(&r.id, &r.scope, &r.kind, &r.hash, &r.size, &r.mtime, &r.locator, &r.inStore, &r.media, &r.proxy,
		&r.title, &r.tags, &r.caption, &r.previews, &r.license, &r.credit, &r.licenseSource, &r.licNote, &r.risk,
		&r.phash, &r.near, &r.provenance, &r.plugin, &r.pluginKey, &r.meta, &r.created, &r.updated)
	return r, err
}

// filePath returns the file a row's locator points at.
func (d *db) filePath(locator string) string {
	if rest, ok := strings.CutPrefix(locator, "blob:"); ok {
		p, err := d.blobs().path(rest)
		if err != nil {
			return ""
		}
		return p
	}
	if u, err := url.Parse(locator); err == nil && u.Scheme == "file" {
		return filepath.FromSlash(u.Path)
	}
	return locator
}

// fileLocator makes a file:// locator for an absolute path.
func fileLocator(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func unmarshalOr[T any](s string, def T) T {
	if s == "" {
		return def
	}
	v := def
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return def
	}
	return v
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// publishable reports whether a license status may be published.
func publishable(status string) bool {
	return status == LicenseOwned || status == LicenseCleared || status == LicenseAttribution
}

// entry builds the full entry of a row: paths, spans count, clocks, transcript.
func (d *db) entry(ctx context.Context, r row) (Entry, error) {
	e := Entry{ID: r.id, Kind: r.kind, Scope: r.scope, Title: r.title, Caption: r.caption, Plugin: r.plugin}
	e.Blob = Blob{Locator: r.locator, InStore: r.inStore}
	if r.hash.Valid {
		h := r.hash.String
		e.Blob.Hash = &h
	}
	if r.size.Valid {
		s := r.size.Int64
		e.Blob.Size = &s
	}
	e.Blob.Path = d.filePath(r.locator)
	if st, err := os.Stat(e.Blob.Path); err == nil && st.Mode().IsRegular() {
		e.Blob.Exists = true
	}
	e.Media = unmarshalOr(r.media, Media{})
	if r.proxy.Valid {
		p := unmarshalOr(r.proxy.String, Proxy{})
		e.Proxy = &p
	}
	e.Tags = unmarshalOr(r.tags, []string{})
	e.Previews = unmarshalOr(r.previews, Previews{})
	e.Previews.Paths = d.previewPaths(e.Previews)
	e.License = License{Status: r.license, Publish: publishable(r.license), Credit: r.credit, Source: r.licenseSource,
		Note: r.licNote, ContentIDRisk: r.risk}
	e.Provenance = unmarshalOr(r.provenance, Provenance{})
	e.Near = unmarshalOr(r.near, []Near{})
	e.Meta = unmarshalOr(r.meta, map[string]any{})
	if err := d.rw.QueryRowContext(ctx, `SELECT COUNT(*) FROM spans WHERE asset = ?`, r.id).Scan(&e.Spans); err != nil {
		return e, err
	}
	clocks, err := d.clocks(ctx, r.id)
	if err != nil {
		return e, err
	}
	e.Clocks = clocks
	var model string
	var track int
	var words string
	err = d.rw.QueryRowContext(ctx, `SELECT model, track, words FROM transcripts WHERE asset = ?`, r.id).Scan(&model,
		&track, &words)
	switch {
	case err == nil:
		var ws [][3]any
		_ = json.Unmarshal([]byte(words), &ws)
		e.Transcript = &TranscriptInfo{Model: model, Track: track, Words: len(ws)}
	case !errors.Is(err, sql.ErrNoRows):
		return e, err
	}
	return e, nil
}

func (d *db) previewPaths(p Previews) map[string]string {
	out := map[string]string{}
	for k, r := range map[string]string{"contact_sheet": p.ContactSheet, "waveform": p.Waveform,
		"thumbnail": p.Thumbnail, "specimen": p.Specimen} {
		if r == "" {
			continue
		}
		if path, err := d.blobs().path(r); err == nil {
			out[k] = path
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (d *db) clocks(ctx context.Context, asset string) ([]Clock, error) {
	rows, err := d.rw.QueryContext(ctx, `SELECT domain, plugin, epochs, confidence, method FROM clocks WHERE asset = ?
		ORDER BY domain`, asset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Clock{}
	for rows.Next() {
		var c Clock
		var epochs string
		if err := rows.Scan(&c.Domain, &c.Plugin, &epochs, &c.Confidence, &c.Method); err != nil {
			return nil, err
		}
		c.Epochs = unmarshalOr(epochs, []Epoch{})
		out = append(out, c)
	}
	return out, rows.Err()
}

// getRow loads one row by its exact id: sql.ErrNoRows when it isn't there.
func (d *db) getRow(ctx context.Context, id string) (row, error) {
	return scanRow(d.rw.QueryRowContext(ctx, `SELECT `+rowCols+` FROM assets WHERE id = ?`, id))
}
