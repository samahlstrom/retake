package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UseRef is one place a timeline or manifest uses an asset.
type UseRef struct {
	Element string            `json:"element"`
	Ptr     string            `json:"ptr,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Anchors map[string]string `json:"anchors,omitempty"` // as written, when there are no resolved times
	T       *[2]float64       `json:"t,omitempty"`       // output seconds, when known
	Source  *[2]float64       `json:"source,omitempty"`  // source seconds, when known
}

// AssetUse is one asset in a license report.
type AssetUse struct {
	Asset      string       `json:"asset"`                 // the catalog id ("" when it isn't cataloged)
	TimelineID string       `json:"timeline_id,omitempty"` // the id the timeline or manifest uses, when different
	Title      string       `json:"title,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	License    *License     `json:"license"` // null: not in the catalog
	Ranges     [][2]float64 `json:"ranges"`  // output seconds, merged
	Uses       []UseRef     `json:"uses"`
	MatchedBy  string       `json:"matched_by,omitempty"` // id, path or hash
}

// Blocker is an asset that keeps a video from being published.
type Blocker struct {
	Asset  string       `json:"asset"`
	Reason string       `json:"reason"`
	Ranges [][2]float64 `json:"ranges"`
}

// LicenseReport is license_report's answer (spec/assets.md, "License report").
type LicenseReport struct {
	Source      string     `json:"source"`
	Kind        string     `json:"kind"` // timeline or manifest
	Publishable bool       `json:"publishable"`
	Assets      []AssetUse `json:"assets"`
	Blockers    []Blocker  `json:"blockers"`
	Warnings    []string   `json:"warnings"`
	Credits     string     `json:"credits"`
	Resolved    bool       `json:"resolved"` // timeline: output times came from retake-edit resolve
}

// LicenseReportArgs name the timeline or manifest, and optionally retake-edit resolve's output for a timeline.
type LicenseReportArgs struct {
	Path     string
	Resolved string
}

// use is one asset reference found in a document.
type use struct {
	id     string
	ref    UseRef
	path   string // the timeline's path for it, resolved
	ranges [][2]float64
}

// LicenseReport lists every asset a timeline or render manifest uses, by output time, with the publish blockers and
// the description credits.
func (c *Catalog) LicenseReport(ctx context.Context, a LicenseReportArgs) (LicenseReport, error) {
	path, err := filepath.Abs(a.Path)
	if err != nil {
		return LicenseReport{}, wrap(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the timeline or manifest the user named
	if err != nil {
		return LicenseReport{}, errorf(CodeNotFound, "can't read %s", path)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return LicenseReport{}, errorf(CodeBadRequest, "%s isn't JSON: %v", path, err)
	}
	rep := LicenseReport{Source: path, Assets: []AssetUse{}, Blockers: []Blocker{}, Warnings: []string{}}
	var uses []use
	schema, _ := doc["schema"].(string)
	switch {
	case strings.HasPrefix(schema, "manifest/"):
		rep.Kind = "manifest"
		uses = manifestUses(doc)
	case strings.HasPrefix(schema, "timeline/"):
		rep.Kind = "timeline"
		resolved, err := c.resolvedTimes(ctx, path, a.Resolved)
		if err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
		}
		rep.Resolved = resolved != nil
		uses = timelineUses(doc, filepath.Dir(path), resolved)
	default:
		return rep, errorf(CodeBadRequest, "%s is neither a timeline (timeline/…) nor a render manifest (manifest/…)", path)
	}
	// Group by the id the document uses, in order of first use.
	byID := map[string]*AssetUse{}
	var order []string
	for _, u := range uses {
		au, ok := byID[u.id]
		if !ok {
			au = &AssetUse{TimelineID: u.id}
			byID[u.id] = au
			order = append(order, u.id)
			c.catalogLookup(ctx, au, u.path)
		}
		au.Uses = append(au.Uses, u.ref)
		au.Ranges = append(au.Ranges, u.ranges...)
	}
	var credits []string
	creditSeen := map[string]bool{}
	for _, id := range order {
		au := byID[id]
		au.Ranges = mergeRanges(au.Ranges)
		if au.Asset == au.TimelineID {
			au.TimelineID = ""
		}
		name := firstNonEmpty(au.Asset, au.TimelineID)
		switch {
		case au.License == nil:
			rep.Blockers = append(rep.Blockers, Blocker{Asset: name, Ranges: au.Ranges,
				Reason: "not in the catalog: no license on record (import it, then set its license)"})
		case au.License.Status == LicenseReviewOnly:
			rep.Blockers = append(rep.Blockers, Blocker{Asset: name, Ranges: au.Ranges,
				Reason: "review-only: fine in drafts, blocked at publish"})
		case au.License.Status == LicenseBlocked:
			rep.Blockers = append(rep.Blockers, Blocker{Asset: name, Ranges: au.Ranges, Reason: "blocked: never use"})
		case au.License.Status == LicenseAttribution && au.License.Credit == "":
			rep.Blockers = append(rep.Blockers, Blocker{Asset: name, Ranges: au.Ranges,
				Reason: "attribution needed but no credit line is stored (retake assets set --credit)"})
		}
		if au.License != nil {
			if r := au.License.ContentIDRisk; r == "medium" || r == "high" {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: content_id_risk %s", name, r))
			}
			if cr := strings.TrimSpace(au.License.Credit); cr != "" && publishable(au.License.Status) && !creditSeen[cr] {
				creditSeen[cr] = true
				credits = append(credits, cr)
			}
		}
		rep.Assets = append(rep.Assets, *au)
	}
	rep.Credits = strings.Join(credits, "\n")
	rep.Publishable = len(rep.Blockers) == 0
	return rep, nil
}

// catalogLookup fills an asset use's catalog fields: by id, else by the timeline's path, else by the file's hash.
func (c *Catalog) catalogLookup(ctx context.Context, au *AssetUse, path string) {
	set := func(d *db, r row, by string) {
		e, err := d.entry(ctx, r)
		if err != nil {
			return
		}
		au.Asset, au.Title, au.Kind, au.MatchedBy = e.ID, e.Title, e.Kind, by
		lic := e.License
		au.License = &lic
	}
	if strings.HasPrefix(au.TimelineID, "font:display") && path == "" {
		au.Title, au.Kind, au.MatchedBy = "Baloo 2 ExtraBold (built in)", KindFont, "builtin"
		au.License = &License{Status: LicenseCleared, Publish: true, Note: "vendored with the editor, SIL Open Font License"}
		return
	}
	if !strings.HasPrefix(au.TimelineID, "font:") {
		if d, r, err := c.find(ctx, au.TimelineID); err == nil {
			set(d, r, "id")
			return
		}
	}
	if path == "" {
		return
	}
	cats, err := c.visible()
	if err != nil {
		return
	}
	loc := fileLocator(path)
	for _, d := range cats {
		if r, err := scanRow(d.rw.QueryRowContext(ctx, `SELECT `+rowCols+` FROM assets WHERE locator = ? LIMIT 1`, loc)); err == nil {
			set(d, r, "path")
			return
		}
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	for _, d := range cats {
		h, _, _, err := d.fileHash(ctx, path)
		if err != nil {
			return
		}
		if r, err := scanRow(d.rw.QueryRowContext(ctx, `SELECT `+rowCols+` FROM assets WHERE hash = ? LIMIT 1`, h)); err == nil {
			set(d, r, "hash")
			return
		}
	}
}

// mergeRanges sorts ranges and merges the ones that touch.
func mergeRanges(rs [][2]float64) [][2]float64 {
	if len(rs) == 0 {
		return [][2]float64{}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i][0] < rs[j][0] })
	out := [][2]float64{rs[0]}
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1]+0.001 {
			last[1] = max(last[1], r[1])
			continue
		}
		out = append(out, r)
	}
	for i := range out {
		out[i] = [2]float64{round3(out[i][0]), round3(out[i][1])}
	}
	return out
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func pair(v any) (*[2]float64, bool) {
	l, ok := v.([]any)
	if !ok || len(l) != 2 {
		return nil, false
	}
	a, ok1 := num(l[0])
	b, ok2 := num(l[1])
	if !ok1 || !ok2 {
		return nil, false
	}
	return &[2]float64{a, b}, true
}

// manifestUses reads every element source of a render manifest (spec/manifest.md), with output times.
func manifestUses(doc map[string]any) []use {
	var out []use
	units, _ := doc["units"].([]any)
	for _, u := range units {
		unit, _ := u.(map[string]any)
		ut, _ := pair(unit["t"])
		elems, _ := unit["elements"].([]any)
		for _, e := range elems {
			el, _ := e.(map[string]any)
			t, ok := pair(el["t"])
			if !ok {
				t = ut
			}
			srcs, _ := el["sources"].([]any)
			for _, s := range srcs {
				src, _ := s.(map[string]any)
				id, _ := src["asset"].(string)
				if id == "" {
					continue
				}
				ref := UseRef{Element: str(el["id"]), Ptr: str(el["ptr"]), Kind: str(el["kind"]), T: t}
				if a, ok := num(src["in"]); ok {
					if b, ok := num(src["out"]); ok {
						ref.Source = &[2]float64{a, b}
					}
				}
				u := use{id: id, ref: ref}
				if t != nil {
					u.ranges = [][2]float64{*t}
				}
				out = append(out, u)
			}
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// resolvedSpans is the part of retake-edit resolve --json a report needs: each element's output times.
type resolvedSpans struct {
	times map[string][2]float64 // element id -> [start, end]
	end   float64
}

// resolvedTimes reads retake-edit resolve --json output: from a file, or by running retake-edit when it is
// installed. nil without either.
func (c *Catalog) resolvedTimes(ctx context.Context, timeline, file string) (*resolvedSpans, error) {
	var data []byte
	var err error
	if file != "" {
		if data, err = os.ReadFile(file); err != nil { //nolint:gosec // the file the user named
			return nil, fmt.Errorf("can't read %s: output times are anchors as written", file)
		}
	} else {
		bin := c.o.EditBin
		if bin == "" {
			bin = c.o.Getenv("RETAKE_EDIT")
		}
		if bin == "" {
			if p, err := exec.LookPath("retake-edit"); err == nil {
				bin = p
			}
		}
		if bin == "" {
			return nil, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "resolve", timeline, "--json") //nolint:gosec // argv only
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if data, err = cmd.Output(); err != nil && len(data) == 0 {
			return nil, fmt.Errorf("retake-edit resolve failed (%w): output times are anchors as written", err)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errors.New("retake-edit resolve's output isn't JSON: output times are anchors as written")
	}
	if ok, present := doc["ok"].(bool); present && !ok {
		return nil, errors.New("the timeline doesn't resolve (retake-edit validate): output times are anchors as written")
	}
	if r, ok := doc["result"].(map[string]any); ok {
		doc = r
	}
	rs := &resolvedSpans{times: map[string][2]float64{}}
	rs.end, _ = num(doc["end"])
	for _, list := range []struct{ key, a, b string }{
		{"spine", "start", "end"}, {"shots", "start", "end"}, {"overlays", "from", "to"}, {"audio", "from", "to"},
		{"scenes", "start", "end"},
	} {
		items, _ := doc[list.key].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			a, ok1 := num(m[list.a])
			b, ok2 := num(m[list.b])
			if id := str(m["id"]); id != "" && ok1 && ok2 {
				rs.times[id] = [2]float64{a, b}
			}
		}
	}
	return rs, nil
}

// timelineUses walks a timeline (spec/timeline.md) for asset references and the fonts its text layers use. Times
// are the containing element's resolved output times when known, else its anchors as written.
func timelineUses(doc map[string]any, dir string, rs *resolvedSpans) []use {
	assetPaths := map[string]string{}
	if am, ok := doc["assets"].(map[string]any); ok {
		for id, v := range am {
			if m, ok := v.(map[string]any); ok {
				if p := str(m["path"]); p != "" {
					assetPaths[id] = expandPath(p, dir)
				}
			}
		}
	}
	fontPaths := map[string]string{}
	if fm, ok := doc["fonts"].(map[string]any); ok {
		for name, v := range fm {
			if m, ok := v.(map[string]any); ok {
				fontPaths[name] = expandPath(str(m["path"]), dir)
			}
		}
	}
	var out []use
	fontsUsed := map[string]bool{}
	add := func(id, container, ptr, kind string, anchors map[string]string, src *[2]float64) {
		ref := UseRef{Element: container, Ptr: ptr, Kind: kind, Source: src}
		u := use{id: id, path: assetPaths[id]}
		if rs != nil {
			if t, ok := rs.times[container]; ok {
				ref.T = &t
				u.ranges = [][2]float64{t}
			}
		}
		if ref.T == nil && len(anchors) > 0 {
			ref.Anchors = anchors
		}
		u.ref = ref
		out = append(out, u)
	}
	anchorsOf := func(m map[string]any, keys ...string) map[string]string {
		a := map[string]string{}
		for _, k := range keys {
			switch v := m[k].(type) {
			case string:
				a[k] = v
			case float64:
				a[k] = fmt.Sprintf("t:%g", v)
			}
		}
		if len(a) == 0 {
			return nil
		}
		return a
	}
	// walkLayers finds asset and font references inside a composition.
	var walkLayers func(v any, ptr, container string, anchors map[string]string)
	walkLayers = func(v any, ptr, container string, anchors map[string]string) {
		switch x := v.(type) {
		case map[string]any:
			if t := str(x["type"]); t == "text" {
				f := str(x["font"])
				if f == "" {
					f = "display"
				}
				fontsUsed[f] = true
			}
			if id := str(x["asset"]); id != "" {
				kind := str(x["type"])
				var src *[2]float64
				if in, ok := num(x["in"]); ok {
					src = &[2]float64{in, in}
				}
				add(id, container, ptr, kind, anchors, src)
			}
			for _, k := range sortedMapKeys(x) {
				if k == "asset" {
					continue
				}
				walkLayers(x[k], ptr+"/"+escapePtr(k), container, anchors)
			}
		case []any:
			for i, e := range x {
				walkLayers(e, fmt.Sprintf("%s/%d", ptr, i), container, anchors)
			}
		}
	}
	spine, _ := doc["spine"].([]any)
	for i, v := range spine {
		m, _ := v.(map[string]any)
		id := str(m["id"])
		ptr := fmt.Sprintf("/spine/%d", i)
		kind := str(m["kind"])
		if kind == "" {
			kind = "vo"
		}
		if a := str(m["asset"]); a != "" {
			var src *[2]float64
			in, ok1 := num(m["in"])
			outT, ok2 := num(m["out"])
			if ok1 && ok2 {
				src = &[2]float64{in, outT}
			}
			add(a, id, ptr, kind, anchorsOf(m, "in", "out", "dur", "frames"), src)
		}
		if p, ok := m["picture"]; ok {
			walkLayers(p, ptr+"/picture", id, nil)
		}
	}
	scenes, _ := doc["scenes"].([]any)
	for i, v := range scenes {
		sc, _ := v.(map[string]any)
		shots, _ := sc["shots"].([]any)
		for j, s := range shots {
			m, _ := s.(map[string]any)
			walkLayers(m["picture"], fmt.Sprintf("/scenes/%d/shots/%d/picture", i, j), str(m["id"]), anchorsOf(m, "at"))
		}
		overlays, _ := sc["overlays"].([]any)
		for j, o := range overlays {
			m, _ := o.(map[string]any)
			walkLayers(m["layers"], fmt.Sprintf("/scenes/%d/overlays/%d/layers", i, j), str(m["id"]),
				anchorsOf(m, "from", "to"))
			// A component use passes its assets in props (spec/components.md).
			walkLayers(m["props"], fmt.Sprintf("/scenes/%d/overlays/%d/props", i, j), str(m["id"]),
				anchorsOf(m, "from", "to"))
		}
	}
	audio, _ := doc["audio"].([]any)
	for i, v := range audio {
		m, _ := v.(map[string]any)
		if a := str(m["asset"]); a != "" {
			add(a, str(m["id"]), fmt.Sprintf("/audio/%d", i), "audio", anchorsOf(m, "from", "to"), nil)
		}
	}
	for _, f := range sortedMapKeys(fontsUsed) {
		u := use{id: "font:" + f, path: fontPaths[f], ref: UseRef{Element: "text layers", Kind: "font"}}
		if rs != nil && rs.end > 0 {
			t := [2]float64{0, rs.end}
			u.ref.T = &t
			u.ranges = [][2]float64{t}
		}
		out = append(out, u)
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func escapePtr(s string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(s) }

// expandPath resolves a timeline asset path: ~/, $VAR and ${VAR}, relative to the timeline's folder.
func expandPath(p, dir string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	p = os.ExpandEnv(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}
