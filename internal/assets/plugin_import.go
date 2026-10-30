package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// assetRecord is an index's asset line.
type assetRecord struct {
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Path    string         `json:"path"`
	Import  string         `json:"import"`
	Title   string         `json:"title"`
	Tags    []string       `json:"tags"`
	Caption string         `json:"caption"`
	License LicenseArgs    `json:"license"`
	Media   *Media         `json:"media"`
	Meta    map[string]any `json:"meta"`
}

// spanRecord is an index's span line. Its type is span_type (type is the line's).
type spanRecord struct {
	Key           string         `json:"key"`
	Asset         string         `json:"asset"`
	T0            *float64       `json:"t0"`
	T1            *float64       `json:"t1"`
	SpanType      string         `json:"span_type"`
	Label         string         `json:"label"`
	Data          map[string]any `json:"data"`
	SourceOfLabel string         `json:"source_of_label"`
	Confidence    string         `json:"confidence"`
}

// clockRecord is an index's clock line.
type clockRecord struct {
	Asset      string  `json:"asset"`
	Domain     string  `json:"domain"`
	Epochs     []Epoch `json:"epochs"`
	Confidence string  `json:"confidence"`
	Method     string  `json:"method"`
}

// maxRecordErrors caps how many bad records an import result lists one by one.
const maxRecordErrors = 50

// importVia runs a plug-in's indexer and applies its records to the scope's catalog.
func (c *Catalog) importVia(ctx context.Context, scope string, a ImportArgs) (ImportResult, error) {
	name, indexer, _ := strings.Cut(a.Via, "/")
	p, err := c.plugin(name)
	if err != nil {
		return ImportResult{}, err
	}
	var ix *PluginIndexer
	for i := range p.Indexers {
		if indexer == "" || p.Indexers[i].Name == indexer {
			ix = &p.Indexers[i]
			break
		}
	}
	if ix == nil {
		return ImportResult{}, errorf(CodeBadRequest, "plug-in %s has no indexer %q", p.Name, indexer)
	}
	if len(a.Paths) > 1 {
		return ImportResult{}, errorf(CodeBadRequest, "an indexer reads one path at a time")
	}
	path := ""
	if len(a.Paths) == 1 {
		if path, err = filepath.Abs(a.Paths[0]); err != nil {
			return ImportResult{}, wrap(err)
		}
		if _, err := os.Stat(path); err != nil {
			return ImportResult{}, errorf(CodeNotFound, "no such file or folder: %s", path)
		}
		if len(ix.Accepts) > 0 && !slices.Contains(ix.Accepts, strings.ToLower(filepath.Ext(path))) {
			return ImportResult{}, errorf(CodeBadRequest, "%s/%s reads %s files, not %s", p.Name, ix.Name,
				strings.Join(ix.Accepts, ", "), filepath.Base(path))
		}
	} else if len(ix.Accepts) > 0 {
		return ImportResult{}, errorf(CodeBadRequest, "%s/%s reads a %s file: give its path", p.Name, ix.Name,
			strings.Join(ix.Accepts, " or "))
	}
	d, err := c.catalogFor(scope, true)
	if err != nil {
		return ImportResult{}, wrap(err)
	}
	if err := d.ensurePlugin(ctx, p); err != nil {
		return ImportResult{}, wrap(err)
	}
	var assets []assetRecord
	var spans []spanRecord
	var clocks []clockRecord
	res := ImportResult{Plugin: p.Name}
	var failure error
	req := pluginRequest{Method: "index", Indexer: ix.Name, Args: a.Args, Path: path, Context: c.pluginCtx(scope)}
	err = c.run(ctx, p, req, IndexTimeout, []string{"done", "error"}, func(l pluginLine) error {
		var err error
		switch l.Type {
		case "asset":
			var r assetRecord
			if err = json.Unmarshal(l.Raw, &r); err == nil {
				assets = append(assets, r)
			}
		case "span":
			var r spanRecord
			if err = json.Unmarshal(l.Raw, &r); err == nil {
				spans = append(spans, r)
			}
		case "clock":
			var r clockRecord
			if err = json.Unmarshal(l.Raw, &r); err == nil {
				clocks = append(clocks, r)
			}
		case "log":
			var m struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(l.Raw, &m) == nil && m.Message != "" && len(res.Log) < 100 {
				res.Log = append(res.Log, m.Message)
			}
		case "done":
			var m struct {
				Stats map[string]any `json:"stats"`
			}
			_ = json.Unmarshal(l.Raw, &m)
			res.Stats = m.Stats
		case "error":
			failure = lineError(p.Name, l.Raw)
		}
		if err != nil {
			return errorf(CodePlugin, "plug-in %s: bad %s line: %v", p.Name, l.Type, err)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if failure != nil {
		return res, failure
	}
	base := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		base = filepath.Dir(path)
	}
	if base == "" {
		base = p.Dir
	}
	ids := map[string]string{} // record key -> asset id in d
	for _, r := range assets {
		it := c.applyAsset(ctx, d, p, r, base, a)
		if it.ID != "" && it.Status != StatusDuplicate {
			ids[r.Key] = it.ID
		} else if it.Status == StatusDuplicate && strings.HasPrefix(it.ID, MakeID(d.scope, "")) {
			ids[r.Key] = it.ID
		}
		res.add(it)
	}
	errs := 0
	note := func(key, reason string) {
		errs++
		if errs <= maxRecordErrors {
			res.Items = append(res.Items, ImportItem{Key: key, Status: StatusSkipped, Reason: reason})
		}
		res.Skipped++
	}
	nSpans, err := c.applySpans(ctx, d, p, spans, ids, note)
	if err != nil {
		return res, wrap(err)
	}
	res.Spans = nSpans
	nClocks, err := applyClocks(ctx, d, p, clocks, ids, note)
	if err != nil {
		return res, wrap(err)
	}
	res.Clocks = nClocks
	if errs > maxRecordErrors {
		res.Log = append(res.Log, fmt.Sprintf("%d more records were skipped", errs-maxRecordErrors))
	}
	return res, nil
}

// assetFor maps a record's asset reference (a key from this run, an indexed key, or a catalog id) to an id in d.
func (d *db) assetFor(ctx context.Context, plugin, ref string, ids map[string]string) (string, error) {
	if id, ok := ids[ref]; ok {
		return id, nil
	}
	var id string
	err := d.rw.QueryRowContext(ctx, `SELECT id FROM assets WHERE plugin = ? AND plugin_key = ?`, plugin, ref).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, _, ok := ParseID(ref); ok {
		if err := d.rw.QueryRowContext(ctx, `SELECT id FROM assets WHERE id = ?`, ref).Scan(&id); err == nil {
			return id, nil
		}
	}
	return "", errorf(CodeNotFound, "no asset %q from this plug-in or in this catalog", ref)
}

// applyAsset creates or updates the asset of one record.
func (c *Catalog) applyAsset(ctx context.Context, d *db, p *Plugin, r assetRecord, base string, a ImportArgs) ImportItem {
	it := ImportItem{Key: r.Key}
	if r.Key == "" {
		it.Status, it.Reason = StatusSkipped, "an asset record needs a key"
		return it
	}
	if err := r.License.check(); err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	path := r.Path
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	it.Path = path
	name := r.Name
	if name == "" {
		name = p.Name + "/" + slug(r.Key)
	}
	if !validName(name) {
		it.Status, it.Reason = StatusSkipped, fmt.Sprintf("%q can't be an asset id", name)
		return it
	}
	if r.Import == "full" {
		if path == "" {
			it.Status, it.Reason = StatusSkipped, "a full import needs a path"
			return it
		}
		if existing, err := d.assetFor(ctx, p.Name, r.Key, nil); err == nil {
			return c.updateRecord(ctx, d, p, existing, r, path, it, false)
		}
		fa := ImportArgs{License: r.License, Tags: r.Tags, Title: r.Title, Caption: r.Caption, InPlace: a.InPlace,
			Proxy: a.Proxy, Transcribe: a.Transcribe, Importer: "plugin:" + p.Name}
		fi := c.importFile(ctx, d, path, name, fa)
		fi.Key = r.Key
		if fi.Status != StatusImported {
			return fi
		}
		upd := c.updateRecord(ctx, d, p, fi.ID, r, path, it, true)
		upd.Status, upd.Kind, upd.Hash, upd.Near, upd.Warnings = StatusImported, fi.Kind, fi.Hash, fi.Near,
			append(fi.Warnings, upd.Warnings...)
		return upd
	}
	if existing, err := d.assetFor(ctx, p.Name, r.Key, nil); err == nil {
		return c.updateRecord(ctx, d, p, existing, r, path, it, false)
	}
	kind := r.Kind
	if kind == "" {
		kind = kindOf(path)
	}
	if kind == "" {
		kind = KindOther
	}
	if !slices.Contains([]string{KindVideo, KindAudio, KindImage, KindFont, KindOther}, kind) {
		it.Status, it.Reason = StatusSkipped, fmt.Sprintf("unknown kind %q", kind)
		return it
	}
	lic := r.License.orDefault()
	media := Media{}
	if r.Media != nil {
		media = *r.Media
	}
	var size any
	if path != "" {
		if st, err := os.Stat(path); err == nil {
			size = st.Size()
		} else {
			it.Warnings = append(it.Warnings, "file not found: "+path)
		}
	}
	prov := Provenance{Importer: "plugin:" + p.Name, Source: path, Seen: d.stamp()}
	err := d.write(ctx, func(tx *sql.Tx) error {
		n, err := d.uniqueName(ctx, tx, d.scope, name)
		if err != nil {
			return err
		}
		it.ID = MakeID(d.scope, n)
		now := d.stamp()
		locator := ""
		if path != "" {
			locator = fileLocator(path)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO assets (id, scope, kind, size, locator, in_store, media, title, tags,
			caption, license, credit, license_source, license_note, content_id_risk, provenance, plugin, plugin_key, meta,
			created, updated) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.ID, d.scope, kind, size, locator, mustJSON(media), r.Title, mustJSON(normTags(r.Tags)), r.Caption,
			lic.Status, lic.Credit, lic.Source, lic.Note, lic.ContentIDRisk, mustJSON(prov), p.Name, r.Key,
			mustJSON(orEmpty(r.Meta)), now, now)
		if err != nil {
			return err
		}
		return indexAsset(ctx, tx, it.ID)
	})
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.Status, it.Kind = StatusImported, kind
	return it
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// updateRecord applies a record's metadata to an existing asset. A metadata-only asset whose path changed loses
// its hash (it is hashed again on resolve).
func (c *Catalog) updateRecord(ctx context.Context, d *db, p *Plugin, id string, r assetRecord, path string,
	it ImportItem, fresh bool) ImportItem {
	it.ID = id
	err := d.write(ctx, func(tx *sql.Tx) error {
		var locator string
		var inStore bool
		if err := tx.QueryRowContext(ctx, `SELECT locator, in_store FROM assets WHERE id = ?`, id).Scan(&locator,
			&inStore); err != nil {
			return err
		}
		sets := []string{"plugin = ?", "plugin_key = ?", "meta = ?", "updated = ?"}
		args := []any{p.Name, r.Key, mustJSON(orEmpty(r.Meta)), d.stamp()}
		if r.Title != "" {
			sets, args = append(sets, "title = ?"), append(args, r.Title)
		}
		if r.Tags != nil {
			sets, args = append(sets, "tags = ?"), append(args, mustJSON(normTags(r.Tags)))
		}
		if r.Caption != "" {
			sets, args = append(sets, "caption = ?"), append(args, r.Caption)
		}
		if r.License.Status != "" {
			sets = append(sets, "license = ?", "credit = ?", "license_source = ?", "license_note = ?", "content_id_risk = ?")
			args = append(args, r.License.Status, r.License.Credit, r.License.Source, r.License.Note, r.License.ContentIDRisk)
		}
		if r.Media != nil && !inStore {
			sets, args = append(sets, "media = ?"), append(args, mustJSON(*r.Media))
		}
		if path != "" && !inStore && fileLocator(path) != locator {
			sets, args = append(sets, "locator = ?", "hash = NULL", "mtime = NULL"), append(args, fileLocator(path))
		}
		args = append(args, id)
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil { //nolint:gosec // fixed column names
			return err
		}
		return indexAsset(ctx, tx, id)
	})
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.Status = StatusUpdated
	if fresh {
		it.Status = StatusImported
	}
	return it
}

// applySpans upserts spans by (plugin, key) in one transaction.
func (c *Catalog) applySpans(ctx context.Context, d *db, p *Plugin, recs []spanRecord, ids map[string]string,
	note func(key, reason string)) (int, error) {
	if len(recs) == 0 {
		return 0, nil
	}
	declared := map[string]*jsonschema.Resolved{}
	allowed := map[string]bool{SpanTag: true, SpanShot: true, SpanSpeech: true}
	for _, t := range p.SpanTypes {
		allowed[t.Type] = true
		if len(t.DataSchema) == 0 {
			continue
		}
		raw, _ := json.Marshal(t.DataSchema)
		var s jsonschema.Schema
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if rs, err := s.Resolve(nil); err == nil {
			declared[t.Type] = rs
		}
	}
	// Resolve asset references before the write transaction (assetFor reads on another connection).
	assetOf := map[string]string{}
	durations := map[string]float64{}
	for _, r := range recs {
		if _, ok := assetOf[r.Asset]; ok || r.Asset == "" {
			continue
		}
		id, err := d.assetFor(ctx, p.Name, r.Asset, ids)
		if err != nil {
			assetOf[r.Asset] = ""
			continue
		}
		assetOf[r.Asset] = id
		var media string
		if d.rw.QueryRowContext(ctx, `SELECT media FROM assets WHERE id = ?`, id).Scan(&media) == nil {
			durations[id] = unmarshalOr(media, Media{}).duration()
		}
	}
	n := 0
	err := d.write(ctx, func(tx *sql.Tx) error {
		for _, r := range recs {
			reason := ""
			id := assetOf[r.Asset]
			switch {
			case r.Key == "":
				reason = "a span record needs a key"
			case id == "":
				reason = fmt.Sprintf("span %s: no asset %q", r.Key, r.Asset)
			case r.T0 == nil || r.T1 == nil || *r.T0 < 0 || *r.T1 < *r.T0 || math.IsNaN(*r.T0) || math.IsNaN(*r.T1):
				reason = fmt.Sprintf("span %s: needs 0 <= t0 <= t1", r.Key)
			case !allowed[r.SpanType]:
				reason = fmt.Sprintf("span %s: plug-in %s doesn't declare span type %q", r.Key, p.Name, r.SpanType)
			case durations[id] > 0 && *r.T1 > durations[id]+0.5:
				reason = fmt.Sprintf("span %s: t1 %.3f is past the end of %s (%.3f s)", r.Key, *r.T1, id, durations[id])
			}
			if reason == "" {
				if rs := declared[r.SpanType]; rs != nil {
					var inst any = map[string]any{}
					if r.Data != nil {
						b, _ := json.Marshal(r.Data)
						_ = json.Unmarshal(b, &inst)
					}
					if err := rs.Validate(inst); err != nil {
						reason = fmt.Sprintf("span %s: data doesn't fit %s: %v", r.Key, r.SpanType, err)
					}
				}
			}
			if reason != "" {
				note(r.Key, reason)
				continue
			}
			src := r.SourceOfLabel
			if src == "" {
				src = p.Name
			}
			now := d.stamp()
			var sid int64
			err := tx.QueryRowContext(ctx, `INSERT INTO spans (asset, t0, t1, type, label, data, source_of_label,
				confidence, plugin, key, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (plugin, key) WHERE key IS NOT NULL DO UPDATE SET asset = excluded.asset, t0 = excluded.t0,
				t1 = excluded.t1, type = excluded.type, label = excluded.label, data = excluded.data,
				source_of_label = excluded.source_of_label, confidence = excluded.confidence, updated = excluded.updated
				RETURNING id`, id, round3(*r.T0), round3(*r.T1), r.SpanType, r.Label, mustJSON(orEmpty(r.Data)), src,
				r.Confidence, p.Name, r.Key, now, now).Scan(&sid)
			if err != nil {
				return err
			}
			if err := indexSpan(ctx, tx, sid); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// applyClocks replaces assets' clocks by domain.
func applyClocks(ctx context.Context, d *db, p *Plugin, recs []clockRecord, ids map[string]string,
	note func(key, reason string)) (int, error) {
	if len(recs) == 0 {
		return 0, nil
	}
	assetOf := map[string]string{}
	for _, r := range recs {
		if _, ok := assetOf[r.Asset]; !ok {
			id, err := d.assetFor(ctx, p.Name, r.Asset, ids)
			if err != nil {
				id = ""
			}
			assetOf[r.Asset] = id
		}
	}
	n := 0
	err := d.write(ctx, func(tx *sql.Tx) error {
		for _, r := range recs {
			id := assetOf[r.Asset]
			reason := ""
			switch {
			case id == "":
				reason = fmt.Sprintf("clock %s: no asset %q", r.Domain, r.Asset)
			case r.Domain == "" || !strings.Contains(r.Domain, ":"):
				reason = fmt.Sprintf("clock on %s: a domain is <plugin>:<thing>", r.Asset)
			case len(r.Epochs) == 0:
				reason = fmt.Sprintf("clock %s: no epochs", r.Domain)
			}
			for _, e := range r.Epochs {
				if reason == "" && e.AssetT[1] < e.AssetT[0] {
					reason = fmt.Sprintf("clock %s: an epoch ends before it starts", r.Domain)
				}
			}
			if reason != "" {
				note(r.Domain, reason)
				continue
			}
			for i := range r.Epochs {
				if r.Epochs[i].Rate == 0 {
					r.Epochs[i].Rate = 1
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO clocks (asset, domain, plugin, epochs, confidence, method)
				VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (asset, domain) DO UPDATE SET plugin = excluded.plugin,
				epochs = excluded.epochs, confidence = excluded.confidence, method = excluded.method`,
				id, r.Domain, p.Name, mustJSON(r.Epochs), r.Confidence, r.Method); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
