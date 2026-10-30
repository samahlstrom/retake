package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// SpanClock is a span's times on one of its asset's clocks.
type SpanClock struct {
	Domain string  `json:"domain"`
	T0     float64 `json:"t0"`
	T1     float64 `json:"t1"`
}

// Span is a labeled moment inside an asset (spec/assets.md, "Spans").
type Span struct {
	Span          string         `json:"span"`
	Asset         string         `json:"asset"`
	T0            float64        `json:"t0"`
	T1            float64        `json:"t1"`
	Type          string         `json:"type"`
	Label         string         `json:"label"`
	Data          map[string]any `json:"data"`
	SourceOfLabel string         `json:"source_of_label"`
	Confidence    string         `json:"confidence,omitempty"`
	Plugin        string         `json:"plugin,omitempty"`
	Key           string         `json:"key,omitempty"`
	Clocks        []SpanClock    `json:"clocks,omitempty"`
	Strip         string         `json:"strip,omitempty"` // the strip preview's path, once made
}

// Core span types.
const (
	SpanTag    = "tag"
	SpanShot   = "shot"
	SpanSpeech = "speech"
)

const spanCols = `id, asset, t0, t1, type, label, data, source_of_label, confidence, plugin, key`

func (d *db) scanSpan(s scanner) (Span, error) {
	var sp Span
	var id int64
	var data string
	var key sql.NullString
	if err := s.Scan(&id, &sp.Asset, &sp.T0, &sp.T1, &sp.Type, &sp.Label, &data, &sp.SourceOfLabel, &sp.Confidence,
		&sp.Plugin, &key); err != nil {
		return sp, err
	}
	sp.Span = SpanID(d.scope, id)
	sp.Data = unmarshalOr(data, map[string]any{})
	sp.Key = key.String
	return sp, nil
}

// decorate adds clock times and the strip path to spans.
func (d *db) decorate(ctx context.Context, spans []Span) error {
	clocks := map[string][]Clock{}
	for i := range spans {
		sp := &spans[i]
		cs, ok := clocks[sp.Asset]
		if !ok {
			var err error
			if cs, err = d.clocks(ctx, sp.Asset); err != nil {
				return err
			}
			clocks[sp.Asset] = cs
		}
		for _, c := range cs {
			a, okA := c.toDomain(sp.T0)
			b, okB := c.toDomain(sp.T1)
			if okA && okB {
				sp.Clocks = append(sp.Clocks, SpanClock{Domain: c.Domain, T0: round3(a), T1: round3(b)})
			}
		}
		_, n, _ := ParseSpanID(sp.Span)
		var blob string
		err := d.rw.QueryRowContext(ctx, `SELECT blob FROM derived WHERE asset = ? AND recipe LIKE ?`, sp.Asset,
			fmt.Sprintf("strip span=%d %%", n)).Scan(&blob)
		if err == nil {
			if p, err := d.blobs().path(blob); err == nil {
				sp.Strip = p
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// spanType is a known span type.
type spanType struct {
	Type        string         `json:"type"`
	Plugin      string         `json:"plugin,omitempty"`
	Description string         `json:"description,omitempty"`
	DataSchema  map[string]any `json:"data_schema,omitempty"`
}

// spanTypes returns the types a catalog knows plus those of the discovered plug-ins.
func (c *Catalog) spanTypes(ctx context.Context, d *db) (map[string]spanType, error) {
	out := map[string]spanType{}
	if d != nil {
		rows, err := d.rw.QueryContext(ctx, `SELECT type, plugin, description, data_schema FROM span_types`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var t spanType
			var schema string
			if err := rows.Scan(&t.Type, &t.Plugin, &t.Description, &schema); err != nil {
				_ = rows.Close()
				return nil, err
			}
			t.DataSchema = unmarshalOr(schema, map[string]any{})
			out[t.Type] = t
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	for _, p := range c.pluginList() {
		for _, t := range p.SpanTypes {
			if _, ok := out[t.Type]; !ok {
				out[t.Type] = spanType{Type: t.Type, Plugin: p.Name, Description: t.Description, DataSchema: t.DataSchema}
			}
		}
	}
	for _, t := range []spanType{{Type: SpanTag}, {Type: SpanShot}, {Type: SpanSpeech}} {
		if _, ok := out[t.Type]; !ok {
			out[t.Type] = t
		}
	}
	return out, nil
}

// checkData validates span data against a type's schema.
func checkData(t spanType, data map[string]any) error {
	if len(t.DataSchema) == 0 {
		return nil
	}
	raw, err := json.Marshal(t.DataSchema)
	if err != nil {
		return err
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("span type %s has a bad data_schema: %w", t.Type, err)
	}
	rs, err := s.Resolve(nil)
	if err != nil {
		return fmt.Errorf("span type %s has a bad data_schema: %w", t.Type, err)
	}
	var inst any = map[string]any{}
	if data != nil {
		// Validate the JSON form, so numbers are float64 as the schema library expects.
		b, _ := json.Marshal(data)
		_ = json.Unmarshal(b, &inst)
	}
	if err := rs.Validate(inst); err != nil {
		return fmt.Errorf("data doesn't fit span type %s: %w", t.Type, err)
	}
	return nil
}

// SpanAddArgs add a span.
type SpanAddArgs struct {
	Asset         string
	T0, T1        float64
	Type          string // default tag
	Label         string
	Data          map[string]any
	Confidence    string
	SourceOfLabel string // manual (CLI) or agent (MCP)
}

// SpanAdd adds a span to an asset and makes its strip preview when the asset is a reachable video.
func (c *Catalog) SpanAdd(ctx context.Context, a SpanAddArgs) (Span, error) {
	d, r, err := c.find(ctx, a.Asset)
	if err != nil {
		return Span{}, wrap(err)
	}
	if a.Type == "" {
		a.Type = SpanTag
	}
	if a.SourceOfLabel == "" {
		a.SourceOfLabel = "manual"
	}
	if math.IsNaN(a.T0) || math.IsNaN(a.T1) || a.T0 < 0 || a.T1 < a.T0 {
		return Span{}, errorf(CodeBadRequest, "a span needs 0 <= t0 <= t1 (got %g, %g)", a.T0, a.T1)
	}
	m := unmarshalOr(r.media, Media{})
	if dur := m.duration(); dur > 0 && a.T1 > dur+0.05 {
		return Span{}, errorf(CodeBadRequest, "t1 %.3f is past the end of %s (%.3f s)", a.T1, r.id, dur)
	}
	types, err := c.spanTypes(ctx, d)
	if err != nil {
		return Span{}, wrap(err)
	}
	t, ok := types[a.Type]
	if !ok {
		return Span{}, errorf(CodeBadRequest, "unknown span type %q: use tag with a label, or a type a plug-in declares "+
			"(retake assets plugins)", a.Type)
	}
	if err := checkData(t, a.Data); err != nil {
		return Span{}, errorf(CodeBadRequest, "%v", err)
	}
	if a.Data == nil {
		a.Data = map[string]any{}
	}
	var id int64
	err = d.write(ctx, func(tx *sql.Tx) error {
		now := d.stamp()
		res, err := tx.ExecContext(ctx, `INSERT INTO spans (asset, t0, t1, type, label, data, source_of_label,
			confidence, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.id, round3(a.T0), round3(a.T1), a.Type,
			a.Label, mustJSON(a.Data), a.SourceOfLabel, a.Confidence, now, now)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return indexSpan(ctx, tx, id)
	})
	if err != nil {
		return Span{}, wrap(err)
	}
	if r.kind == KindVideo {
		e, err := d.entry(ctx, r)
		if err == nil && e.Blob.Exists {
			_, _ = c.strip(ctx, d, r.id, c.frameSource(d, e), e.Media, id, round3(a.T0), round3(a.T1), entrySource(e))
		}
	}
	sp, err := d.span(ctx, id)
	return sp, wrap(err)
}

// span loads one span by row id, decorated.
func (d *db) span(ctx context.Context, id int64) (Span, error) {
	sp, err := d.scanSpan(d.rw.QueryRowContext(ctx, `SELECT `+spanCols+` FROM spans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sp, errorf(CodeNotFound, "no span %s", SpanID(d.scope, id))
	}
	if err != nil {
		return sp, err
	}
	spans := []Span{sp}
	err = d.decorate(ctx, spans)
	return spans[0], err
}

// GetSpan returns one span by id.
func (c *Catalog) GetSpan(ctx context.Context, id string) (Span, error) {
	scope, n, err := ParseSpanID(id)
	if err != nil {
		return Span{}, errorf(CodeBadRequest, "%v", err)
	}
	d, err := c.catalogFor(scope, false)
	if err != nil {
		return Span{}, wrap(err)
	}
	if d == nil {
		return Span{}, errorf(CodeNotFound, "no span %s", id)
	}
	sp, err := d.span(ctx, n)
	return sp, wrap(err)
}

// SpanFindArgs filter spans.
type SpanFindArgs struct {
	Type   []string // exact types, or LIKE patterns with %
	Where  string   // a SQL condition on spans
	Near   string   // <asset id>@<t> or <clock domain>@<t>
	Window float64  // seconds around Near; default 15
	Asset  string
	Plugin string
	Limit  int // default 50, at most 1000
}

// SpanFindResult lists spans.
type SpanFindResult struct {
	Spans []Span `json:"spans"`
	More  bool   `json:"more"` // more spans matched than the limit
}

// SpanFind lists the spans matching structured filters, by asset and time.
func (c *Catalog) SpanFind(ctx context.Context, a SpanFindArgs) (SpanFindResult, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 1000)
	if a.Window <= 0 {
		a.Window = 15
	}
	cats, err := c.visible()
	if err != nil {
		return SpanFindResult{}, wrap(err)
	}
	type nearAt struct {
		asset string
		t     float64
	}
	var nears []nearAt
	if a.Near != "" {
		target, ts, ok := strings.Cut(a.Near, "@")
		t, err := ParseTime(ts)
		if !ok || err != nil {
			return SpanFindResult{}, errorf(CodeBadRequest, "near is <asset id>@<time> or <clock domain>@<time>, e.g. "+
				"proj:rec/x@1:02:03 or deadlock:match/123@9:30")
		}
		if _, _, isID := ParseID(target); isID {
			_, r, err := c.find(ctx, target)
			if err != nil {
				return SpanFindResult{}, wrap(err)
			}
			nears = append(nears, nearAt{r.id, t})
		} else {
			for _, d := range cats {
				rows, err := d.rw.QueryContext(ctx, `SELECT asset, epochs FROM clocks WHERE domain = ?`, target)
				if err != nil {
					return SpanFindResult{}, wrap(err)
				}
				for rows.Next() {
					var asset, epochs string
					if err := rows.Scan(&asset, &epochs); err != nil {
						_ = rows.Close()
						return SpanFindResult{}, wrap(err)
					}
					if at, ok := (Clock{Epochs: unmarshalOr(epochs, []Epoch{})}).toAsset(t); ok {
						nears = append(nears, nearAt{asset, at})
					}
				}
				if err := rows.Close(); err != nil {
					return SpanFindResult{}, wrap(err)
				}
			}
			if len(nears) == 0 {
				return SpanFindResult{Spans: []Span{}}, errorf(CodeNotFound, "no asset has clock %s at %s", target, ts)
			}
		}
	}
	if err := checkWhere(a.Where); err != nil {
		return SpanFindResult{}, err
	}
	assetID := ""
	if a.Asset != "" {
		_, r, err := c.find(ctx, a.Asset)
		if err != nil {
			return SpanFindResult{}, wrap(err)
		}
		assetID = r.id
	}
	out := SpanFindResult{Spans: []Span{}}
	for _, d := range cats {
		var conds []string
		var args []any
		if tc, targs := typeCond("type", a.Type); tc != "" {
			conds, args = append(conds, tc), append(args, targs...)
		}
		if assetID != "" {
			conds, args = append(conds, "asset = ?"), append(args, assetID)
		}
		if a.Plugin != "" {
			conds, args = append(conds, "plugin = ?"), append(args, a.Plugin)
		}
		if len(nears) > 0 {
			var ors []string
			for _, n := range nears {
				ors = append(ors, "(asset = ? AND t1 >= ? AND t0 <= ?)")
				args = append(args, n.asset, n.t-a.Window, n.t+a.Window)
			}
			conds = append(conds, "("+strings.Join(ors, " OR ")+")")
		}
		if a.Where != "" {
			conds = append(conds, "("+a.Where+")")
		}
		q := `SELECT ` + spanCols + ` FROM spans`
		if len(conds) > 0 {
			q += " WHERE " + strings.Join(conds, " AND ")
		}
		q += " ORDER BY asset, t0, id LIMIT " + strconv.Itoa(limit-len(out.Spans)+1) //nolint:gosec // a number
		rows, err := d.ro.QueryContext(ctx, q, args...)
		if err != nil {
			return out, errorf(CodeBadRequest, "span query failed: %v", err)
		}
		var got []Span
		for rows.Next() {
			sp, err := d.scanSpan(rows)
			if err != nil {
				_ = rows.Close()
				return out, wrap(err)
			}
			got = append(got, sp)
		}
		if err := rows.Close(); err != nil {
			return out, errorf(CodeBadRequest, "span query failed: %v", err)
		}
		if len(out.Spans)+len(got) > limit {
			got = got[:limit-len(out.Spans)]
			out.More = true
		}
		if err := d.decorate(ctx, got); err != nil {
			return out, wrap(err)
		}
		out.Spans = append(out.Spans, got...)
		if out.More {
			break
		}
	}
	return out, nil
}

// typeCond builds "col IN (…)" or LIKE conditions for a type list.
func typeCond(col string, types []string) (string, []any) {
	var ors []string
	var args []any
	for _, t := range types {
		for _, x := range strings.Split(t, ",") {
			x = strings.TrimSpace(x)
			if x == "" {
				continue
			}
			if strings.Contains(x, "%") {
				ors, args = append(ors, col+" LIKE ?"), append(args, x)
			} else {
				ors, args = append(ors, col+" = ?"), append(args, x)
			}
		}
	}
	if len(ors) == 0 {
		return "", nil
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// checkWhere refuses SQL that is more than one condition. The query also runs on a query_only connection.
func checkWhere(where string) error {
	if strings.Contains(where, ";") {
		return errorf(CodeBadRequest, "where is one SQL condition on spans, without ';'")
	}
	return nil
}

// ParseTime reads a time as people write it: seconds (75.5), m:ss(.s) or h:mm:ss(.s).
func ParseTime(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("no time")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("%q is not a time", s)
	}
	total := 0.0
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("%q is not a time", s)
		}
		if i > 0 && v >= 60 {
			return 0, fmt.Errorf("%q is not a time", s)
		}
		total = total*60 + v
	}
	return total, nil
}
