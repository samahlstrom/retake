package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Statuses are the catalog's license statuses (spec/assets.md, "Licenses").
var statuses = map[string]bool{"owned": true, "cleared": true, "attribution": true, "review-only": true, "blocked": true}

// columns maps a catalog field to the CSV header names that can hold it, in order of preference.
var columns = map[string][]string{
	"path":    {"path", "file", "filename", "file_name"},
	"title":   {"title", "name"},
	"tags":    {"tags", "tags_when_to_use", "keywords"},
	"license": {"license", "licence", "license_status"},
	"credit":  {"credit", "attribution"},
	"source":  {"source_url", "source", "url"},
	"type":    {"type"},
}

// fields are the mapped fields, in the order `columns` reports them.
var fields = []string{"path", "title", "tags", "license", "credit", "source", "type"}

// table is a parsed CSV: its header and rows, each row as header name -> value.
type table struct {
	header []string
	rows   []map[string]string
}

// readTable parses a CSV file: quoted fields, ragged rows and a UTF-8 BOM are fine.
func readTable(path string) (*table, error) {
	f, err := os.Open(path) //nolint:gosec // the path the user asked to index
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseTable(f)
}

func parseTable(r io.Reader) (*table, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("the CSV is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("reading the header: %w", err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}
	t := &table{header: header}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading row %d: %w", len(t.rows)+2, err)
		}
		row := map[string]string{}
		for i, v := range rec {
			if i < len(header) && header[i] != "" {
				row[header[i]] = v
			}
		}
		t.rows = append(t.rows, row)
	}
	return t, nil
}

// mapping finds the header column for each field. overrides (field -> header name) win when the header has them.
func mapping(header []string, overrides map[string]string) (map[string]string, error) {
	byLower := map[string]string{}
	for _, h := range header {
		if _, ok := byLower[strings.ToLower(h)]; !ok {
			byLower[strings.ToLower(h)] = h
		}
	}
	m := map[string]string{}
	for _, f := range fields {
		if o := overrides[f]; o != "" {
			h, ok := byLower[strings.ToLower(o)]
			if !ok {
				return nil, fmt.Errorf("no column %q in the CSV (columns: %s)", o, strings.Join(header, ", "))
			}
			m[f] = h
			continue
		}
		for _, name := range columns[f] {
			if h, ok := byLower[name]; ok {
				m[f] = h
				break
			}
		}
	}
	if m["path"] == "" {
		return nil, fmt.Errorf("no path column (path, file, filename or file_name; or set path_column); columns: %s",
			strings.Join(header, ", "))
	}
	return m, nil
}

// titleFromFile derives a title from a file name: the part before the first "__", hyphens and underscores to spaces.
func titleFromFile(p string) string {
	base := filepath.Base(filepath.FromSlash(p))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if i := strings.Index(base, "__"); i > 0 {
		base = base[:i]
	}
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	return strings.Join(strings.Fields(base), " ")
}

// splitTags splits on / ; , | and returns trimmed, lowercased, deduplicated tags.
func splitTags(s string, extra ...string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == ';' || r == ',' || r == '|' })
	parts = append(parts, extra...)
	seen := map[string]bool{}
	out := []string{}
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// kindHint maps a type column value to a catalog kind, or "" to let the core decide.
func kindHint(typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch {
	case t == "":
		return ""
	case t == "sfx" || t == "music" || t == "audio" || strings.Contains(t, "audio"):
		return "audio"
	case strings.Contains(t, "video"):
		return "video"
	case strings.Contains(t, "image") || strings.Contains(t, "icon") || strings.Contains(t, "card") ||
		strings.Contains(t, "map") || strings.Contains(t, "art"):
		return "image"
	case strings.Contains(t, "font"):
		return "font"
	}
	return ""
}

// license is the license object of an asset record.
type license struct {
	Status        string `json:"status"`
	Credit        string `json:"credit,omitempty"`
	Source        string `json:"source,omitempty"`
	Note          string `json:"note,omitempty"`
	ContentIDRisk string `json:"content_id_risk,omitempty"`
}

// mapLicense turns free license text into a status (spec/assets.md, "Licenses"). def is the status for empty or
// unknown text; the original text is kept as the note.
func mapLicense(text, credit, source, title, def string) license {
	l := license{Credit: strings.TrimSpace(credit), Source: strings.TrimSpace(source), Note: strings.TrimSpace(text)}
	t := strings.ToLower(l.Note)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case statuses[t]:
		l.Status = t
	// The "no license" words come first: "copyrighted-meme risk, not CC" mentions CC, and "Unknown ... Original
	// clip" mentions an original.
	case has("not cc", "no license", "not licensed", "unknown", "copyrighted"):
		l.Status = def
	case has("cc0", "public domain"):
		l.Status = "cleared"
	case has("cc-by", "cc by", "attribution", "credit required"):
		l.Status = "attribution"
		if l.Credit == "" {
			l.Credit = strings.TrimPrefix(strings.TrimSpace(title+", "+l.Note), ", ")
		}
	case has("ofl", "open font licen", "font licen"):
		l.Status = "cleared"
	case has("owned", "own work", " own ", "original"):
		l.Status = "owned"
	case has("blocked", "do not use"):
		l.Status = "blocked"
	case has("epidemic", "artlist", "licensed", "royalty-free", "royalty free"):
		l.Status = "cleared"
	default:
		l.Status = def
	}
	// Material without a usable license that names a rights holder or a meme source is likely to be claimed.
	publishable := l.Status == "owned" || l.Status == "cleared" || l.Status == "attribution"
	if !publishable && has("copyright", "meme", "unknown") {
		l.ContentIDRisk = "high"
	}
	return l
}
