package assets

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SearchArgs are asset_search's filters and query (spec/assets.md, "Search").
type SearchArgs struct {
	Query   string
	Kind    []string
	Scope   []string // proj, brand, lib
	Tags    []string // all must be present
	License []string
	Type    []string // span types
	Where   string   // a SQL condition on spans
	Limit   int      // default 20, at most 200
}

// SpanHit is a span in a search result.
type SpanHit struct {
	Span   string      `json:"span"`
	T0     float64     `json:"t0"`
	T1     float64     `json:"t1"`
	Type   string      `json:"type"`
	Label  string      `json:"label"`
	Clocks []SpanClock `json:"clocks,omitempty"`
	Strip  string      `json:"strip,omitempty"`
}

// Hit is one asset in a search result.
type Hit struct {
	Asset      string          `json:"asset"`
	Kind       string          `json:"kind"`
	Scope      string          `json:"scope"`
	Title      string          `json:"title"`
	Tags       []string        `json:"tags"`
	License    string          `json:"license"`
	Duration   *float64        `json:"duration,omitempty"`
	Preview    string          `json:"preview,omitempty"` // the path of its main preview
	Score      float64         `json:"score"`
	Matched    []string        `json:"matched"` // title, tags, caption, transcript, span
	Spans      []SpanHit       `json:"spans,omitempty"`
	Transcript []TranscriptHit `json:"transcript,omitempty"`
}

// SearchResult is a search's hits, best first.
type SearchResult struct {
	Query   string `json:"query"`
	Results []Hit  `json:"results"`
}

// queryTerms splits a query into lowercase words.
func queryTerms(q string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ftsQuery makes an FTS5 query from words: every word, as a prefix.
func ftsQuery(terms []string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = `"` + t + `"*`
	}
	return strings.Join(parts, " ")
}

// assetFilter builds the structured conditions on assets (alias a).
func assetFilter(a SearchArgs) (string, []any, error) {
	var conds []string
	var args []any
	in := func(col string, vals []string) {
		var qs []string
		for _, v := range vals {
			for _, x := range strings.Split(v, ",") {
				if x = strings.TrimSpace(x); x != "" {
					qs, args = append(qs, "?"), append(args, x)
				}
			}
		}
		if len(qs) > 0 {
			conds = append(conds, col+" IN ("+strings.Join(qs, ", ")+")")
		}
	}
	in("a.kind", a.Kind)
	var scopes []string
	for _, s := range a.Scope {
		for _, x := range strings.Split(s, ",") {
			if strings.TrimSpace(x) == "" {
				continue
			}
			sc, err := ParseScope(x)
			if err != nil {
				return "", nil, errorf(CodeBadRequest, "%v", err)
			}
			scopes = append(scopes, sc)
		}
	}
	in("a.scope", scopes)
	in("a.license", a.License)
	for _, t := range normTags(a.Tags) {
		conds = append(conds, "EXISTS (SELECT 1 FROM json_each(a.tags) WHERE json_each.value = ?)")
		args = append(args, t)
	}
	return strings.Join(conds, " AND "), args, nil
}

func and(conds ...string) string {
	var out []string
	for _, c := range conds {
		if c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "1"
	}
	return strings.Join(out, " AND ")
}

// rrfK is reciprocal-rank fusion's constant.
const rrfK = 60.0

// List weights in the fusion.
const (
	weightAssets  = 1.0
	weightSpans   = 0.9
	weightVectors = 1.0
)

type candidate struct {
	d       *db
	id      string
	score   float64
	order   int // first-seen order, for ties
	spans   []SpanHit
	matched map[string]bool
}

// Search runs structured filters, then full-text search over assets and spans, fused by reciprocal rank.
func (c *Catalog) Search(ctx context.Context, a SearchArgs) (SearchResult, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	if err := checkWhere(a.Where); err != nil {
		return SearchResult{}, err
	}
	filter, fargs, err := assetFilter(a)
	if err != nil {
		return SearchResult{}, err
	}
	cats, err := c.visible()
	if err != nil {
		return SearchResult{}, wrap(err)
	}
	terms := queryTerms(a.Query)
	fts := ftsQuery(terms)
	spanMode := a.Where != "" || len(a.Type) > 0
	cands := map[string]*candidate{}
	next := 0
	get := func(d *db, id string) *candidate {
		cd, ok := cands[id]
		if !ok {
			cd = &candidate{d: d, id: id, order: next, matched: map[string]bool{}}
			next++
			cands[id] = cd
		}
		return cd
	}
	for _, d := range cats {
		switch {
		case spanMode:
			if err := c.searchSpans(ctx, d, a, filter, fargs, fts, limit, get); err != nil {
				return SearchResult{}, err
			}
		case len(terms) > 0:
			if err := searchText(ctx, d, filter, fargs, fts, get); err != nil {
				return SearchResult{}, err
			}
		default:
			rows, err := d.ro.QueryContext(ctx, `SELECT a.id FROM assets a WHERE `+and(filter)+ //nolint:gosec // fixed SQL with ? parameters
				` ORDER BY a.updated DESC, a.id LIMIT `+strconv.Itoa(limit), fargs...)
			if err != nil {
				return SearchResult{}, wrap(err)
			}
			rank := 0
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					_ = rows.Close()
					return SearchResult{}, wrap(err)
				}
				rank++
				get(d, id).score += 1 / (rrfK + float64(rank))
			}
			if err := rows.Close(); err != nil {
				return SearchResult{}, wrap(err)
			}
		}
	}
	if c.o.Vectors != nil && len(terms) > 0 && !spanMode {
		ids, err := c.o.Vectors.Search(ctx, a.Query, 200)
		if err != nil {
			return SearchResult{}, wrap(err)
		}
		for i, id := range ids {
			d, r, err := c.find(ctx, id)
			if err != nil {
				continue
			}
			if cd, ok := cands[r.id]; ok || filter == "" {
				if !ok {
					cd = get(d, r.id)
				}
				cd.score += weightVectors / (rrfK + float64(i+1))
				cd.matched["vector"] = true
			}
		}
	}
	list := make([]*candidate, 0, len(cands))
	for _, cd := range cands {
		list = append(list, cd)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].order < list[j].order
	})
	if len(list) > limit {
		list = list[:limit]
	}
	res := SearchResult{Query: a.Query, Results: []Hit{}}
	for _, cd := range list {
		r, err := cd.d.getRow(ctx, cd.id)
		if err != nil {
			continue
		}
		e, err := cd.d.entry(ctx, r)
		if err != nil {
			return res, wrap(err)
		}
		h := Hit{Asset: e.ID, Kind: e.Kind, Scope: e.Scope, Title: e.Title, Tags: e.Tags, License: e.License.Status,
			Duration: e.Media.Duration, Score: round6(cd.score), Spans: cd.spans, Matched: []string{}}
		for _, k := range []string{PreviewContactSheet, PreviewWaveform, PreviewThumbnail, PreviewSpecimen} {
			if p := e.Previews.Paths[k]; p != "" {
				h.Preview = p
				break
			}
		}
		if len(terms) > 0 {
			textMatches(&h, e, terms, cd.matched)
			if cd.matched["asset"] && !h.matchedAny("title", "tags", "caption") {
				hits, err := cd.d.transcriptHits(ctx, e.ID, terms, 5)
				if err != nil {
					return res, wrap(err)
				}
				if len(hits) > 0 {
					h.Transcript = hits
					h.Matched = append(h.Matched, "transcript")
				}
			}
		}
		if len(cd.spans) > 0 {
			h.Matched = append(h.Matched, "span")
		}
		if cd.matched["vector"] {
			h.Matched = append(h.Matched, "vector")
		}
		res.Results = append(res.Results, h)
	}
	return res, nil
}

func round6(v float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 6, 64), 64)
	return f
}

func (h Hit) matchedAny(fields ...string) bool {
	for _, m := range h.Matched {
		for _, f := range fields {
			if m == f {
				return true
			}
		}
	}
	return false
}

// textMatches says which of an entry's fields hold a query word.
func textMatches(h *Hit, e Entry, terms []string, matched map[string]bool) {
	if !matched["asset"] {
		return
	}
	has := func(s string) bool {
		words := queryTerms(s)
		for _, t := range terms {
			for _, w := range words {
				if strings.HasPrefix(w, t) {
					return true
				}
			}
		}
		return false
	}
	if has(e.Title) {
		h.Matched = append(h.Matched, "title")
	}
	if has(strings.Join(e.Tags, " ")) {
		h.Matched = append(h.Matched, "tags")
	}
	if has(e.Caption) {
		h.Matched = append(h.Matched, "caption")
	}
}

// searchText ranks one catalog's assets (BM25: title > tags > caption > transcript) and spans by the query, and
// adds both lists to the fusion.
func searchText(ctx context.Context, d *db, filter string, fargs []any, fts string,
	get func(*db, string) *candidate) error {
	args := append([]any{fts}, fargs...)
	//nolint:gosec // fixed SQL with ? parameters
	q := `SELECT a.id FROM assets_fts JOIN assets a ON a.id = assets_fts.id
		WHERE assets_fts MATCH ? AND ` + and(filter) + ` ORDER BY bm25(assets_fts, 0, 10.0, 5.0, 2.0, 1.0), a.id LIMIT 500`
	rows, err := d.ro.QueryContext(ctx, q, args...)
	if err != nil {
		return wrap(err)
	}
	rank := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return wrap(err)
		}
		rank++
		cd := get(d, id)
		cd.score += weightAssets / (rrfK + float64(rank))
		cd.matched["asset"] = true
	}
	if err := rows.Close(); err != nil {
		return wrap(err)
	}
	q = `SELECT ` + prefixed("s.", spanCols) + ` FROM spans_fts
		JOIN spans s ON s.id = spans_fts.span JOIN assets a ON a.id = s.asset
		WHERE spans_fts MATCH ? AND ` + and(filter) + ` ORDER BY bm25(spans_fts, 0, 4.0, 2.0, 1.0), s.id LIMIT 1000`
	srows, err := d.ro.QueryContext(ctx, q, args...)
	if err != nil {
		return wrap(err)
	}
	var spans []Span
	for srows.Next() {
		sp, err := d.scanSpan(srows)
		if err != nil {
			_ = srows.Close()
			return wrap(err)
		}
		spans = append(spans, sp)
	}
	if err := srows.Close(); err != nil {
		return wrap(err)
	}
	return addSpans(ctx, d, spans, get, weightSpans)
}

// addSpans groups ranked spans by asset (at most five each) and adds the assets, in order of their best span, to
// the fusion.
func addSpans(ctx context.Context, d *db, spans []Span, get func(*db, string) *candidate, weight float64) error {
	if err := d.decorate(ctx, spans); err != nil {
		return wrap(err)
	}
	rank := 0
	seen := map[string]bool{}
	for _, sp := range spans {
		cd := get(d, sp.Asset)
		if !seen[sp.Asset] {
			seen[sp.Asset] = true
			rank++
			cd.score += weight / (rrfK + float64(rank))
		}
		if len(cd.spans) < 5 {
			cd.spans = append(cd.spans, SpanHit{Span: sp.Span, T0: sp.T0, T1: sp.T1, Type: sp.Type, Label: sp.Label,
				Clocks: sp.Clocks, Strip: sp.Strip})
		}
	}
	return nil
}

// searchSpans is search with span filters (type, where): the matching spans, ranked by the query when there is one,
// grouped by asset.
func (c *Catalog) searchSpans(ctx context.Context, d *db, a SearchArgs, filter string, fargs []any, fts string,
	limit int, get func(*db, string) *candidate) error {
	tc, targs := typeCond("s.type", a.Type)
	// The where condition sees the spans table alone, so its column names are unambiguous.
	from := "spans"
	if a.Where != "" {
		from = "(SELECT * FROM spans WHERE (" + a.Where + "))"
	}
	base := and(filter, tc)
	args := append(append([]any{}, fargs...), targs...)
	var q string
	if fts != "" {
		q = `SELECT ` + prefixed("s.", spanCols) + ` FROM ` + from + ` s JOIN assets a ON a.id = s.asset
			LEFT JOIN (SELECT span, bm25(spans_fts, 0, 4.0, 2.0, 1.0) AS r FROM spans_fts WHERE spans_fts MATCH ?) m
				ON m.span = s.id
			LEFT JOIN (SELECT id, bm25(assets_fts, 0, 10.0, 5.0, 2.0, 1.0) AS r FROM assets_fts WHERE assets_fts MATCH ?) am
				ON am.id = s.asset
			WHERE (m.span IS NOT NULL OR am.id IS NOT NULL) AND ` + base + `
			ORDER BY m.r IS NULL, m.r, am.r, s.asset, s.t0 LIMIT ` + strconv.Itoa(limit*20)
		args = append([]any{fts, fts}, args...)
	} else {
		q = `SELECT ` + prefixed("s.", spanCols) + ` FROM ` + from + ` s JOIN assets a ON a.id = s.asset WHERE ` + base +
			` ORDER BY s.asset, s.t0, s.id LIMIT ` + strconv.Itoa(limit*20)
	}
	rows, err := d.ro.QueryContext(ctx, q, args...)
	if err != nil {
		return errorf(CodeBadRequest, "span query failed: %v", err)
	}
	var spans []Span
	for rows.Next() {
		sp, err := d.scanSpan(rows)
		if err != nil {
			_ = rows.Close()
			return wrap(err)
		}
		spans = append(spans, sp)
	}
	if err := rows.Close(); err != nil {
		return errorf(CodeBadRequest, "span query failed: %v", err)
	}
	return addSpans(ctx, d, spans, get, weightSpans)
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
