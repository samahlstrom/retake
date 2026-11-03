// Command csv-index is a retake catalog plug-in (spec/assets.md, "Plug-ins") that imports any CSV with path, title,
// tags and license columns: one asset per row, the license text mapped to a catalog status. It speaks
// retake-plugin/0.1: one JSON request on stdin, JSON lines on stdout.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// protocol is the plug-in protocol version this build speaks.
const protocol = "retake-plugin/0.1"

// request is one call from the catalog.
type request struct {
	Protocol string         `json:"protocol"`
	Method   string         `json:"method"`
	Indexer  string         `json:"indexer"`
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args"`
	Path     string         `json:"path"`
	Context  struct {
		ProjectRoot string `json:"project_root"`
		PluginDir   string `json:"plugin_dir"`
	} `json:"context"`
}

// failure is a call's error, written as {"type":"error"}.
type failure struct {
	code, message string
}

func (f *failure) Error() string { return f.message }

func badRequest(format string, a ...any) error {
	return &failure{code: "bad_request", message: fmt.Sprintf(format, a...)}
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

// run handles one request and returns the exit code. Errors the catalog should show go to stdout as an error line.
func run(stdin io.Reader, stdout, stderr io.Writer) int {
	out := json.NewEncoder(stdout)
	out.SetEscapeHTML(false)
	emit := func(v any) error { return out.Encode(v) }
	var req request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		_ = emit(map[string]any{"type": "error", "code": "bad_request", "message": "reading the request: " + err.Error()})
		return 0
	}
	var err error
	switch {
	case req.Protocol != "" && req.Protocol != protocol:
		err = badRequest("protocol %q: this plug-in speaks %s", req.Protocol, protocol)
	case req.Method == "index" && (req.Indexer == "" || req.Indexer == "csv"):
		err = index(req, emit)
	case req.Method == "index":
		err = badRequest("unknown indexer %q (this plug-in has csv)", req.Indexer)
	case req.Method == "tool" && req.Tool == "columns":
		err = columnsTool(req, emit)
	case req.Method == "tool":
		err = badRequest("unknown tool %q (this plug-in has columns)", req.Tool)
	default:
		err = badRequest("unknown method %q (index or tool)", req.Method)
	}
	if err != nil {
		var f *failure
		if !errors.As(err, &f) {
			f = &failure{code: "failed", message: err.Error()}
		}
		if e := emit(map[string]any{"type": "error", "code": f.code, "message": f.message}); e != nil {
			_, _ = fmt.Fprintln(stderr, "csv-index:", e)
			return 1
		}
	}
	return 0
}

// options are the indexer's args.
type options struct {
	root      string
	full      bool
	limit     int
	defStatus string
	overrides map[string]string
}

func parseOptions(args map[string]any) (options, error) {
	o := options{full: true, defStatus: "review-only", overrides: map[string]string{}}
	str := func(k string) (string, error) {
		v, ok := args[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", badRequest("arg %s must be a string", k)
		}
		return strings.TrimSpace(s), nil
	}
	var err error
	if o.root, err = str("root"); err != nil {
		return o, err
	}
	imp, err := str("import")
	if err != nil {
		return o, err
	}
	switch imp {
	case "", "full":
	case "metadata":
		o.full = false
	default:
		return o, badRequest(`arg import must be "full" or "metadata", not %q`, imp)
	}
	if v, ok := args["limit"]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			o.limit = int(n)
		case string:
			if o.limit, err = strconv.Atoi(n); err != nil {
				return o, badRequest("arg limit %q is not a number", n)
			}
		default:
			return o, badRequest("arg limit must be a number")
		}
		if o.limit < 0 {
			return o, badRequest("arg limit must be 0 or more")
		}
	}
	def, err := str("license_default")
	if err != nil {
		return o, err
	}
	if def != "" {
		if !statuses[def] {
			return o, badRequest("arg license_default %q is not a status (owned, cleared, attribution, review-only, blocked)", def)
		}
		o.defStatus = def
	}
	for _, f := range []string{"path", "title", "tags", "license"} {
		v, err := str(f + "_column")
		if err != nil {
			return o, err
		}
		if v != "" {
			o.overrides[f] = v
		}
	}
	return o, nil
}

// assetRecord is one {"type":"asset"} line.
type assetRecord struct {
	Type    string         `json:"type"`
	Key     string         `json:"key"`
	Kind    string         `json:"kind,omitempty"`
	Path    string         `json:"path"`
	Import  string         `json:"import"`
	Title   string         `json:"title,omitempty"`
	Tags    []string       `json:"tags"`
	License license        `json:"license"`
	Meta    map[string]any `json:"meta"`
}

func index(req request, emit func(any) error) error {
	if req.Path == "" {
		return badRequest("give the CSV file to index (retake assets import FILE.csv --via csv-index)")
	}
	o, err := parseOptions(req.Args)
	if err != nil {
		return err
	}
	t, err := readTable(req.Path)
	if err != nil {
		return badRequest("%s: %v", req.Path, err)
	}
	m, err := mapping(t.header, o.overrides)
	if err != nil {
		return badRequest("%s: %v", req.Path, err)
	}
	root := o.root
	if root == "" {
		root = filepath.Dir(req.Path)
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	imp := "full"
	if !o.full {
		imp = "metadata"
	}
	assets, skipped := 0, 0
	for i, row := range t.rows {
		if o.limit > 0 && assets >= o.limit {
			break
		}
		p := strings.TrimSpace(row[m["path"]])
		if p == "" {
			skipped++
			if err := emit(map[string]any{"type": "log", "message": fmt.Sprintf("row %d has no %s: skipped", i+2, m["path"])}); err != nil {
				return err
			}
			continue
		}
		// Paths go out absolute: a relative one is relative to root (default: the CSV's folder). On Windows a rooted
		// one (\x, /x) is on root's drive.
		path := filepath.FromSlash(p)
		switch {
		case filepath.IsAbs(path):
		case filepath.VolumeName(path) == "" && strings.HasPrefix(path, string(filepath.Separator)):
			path = filepath.VolumeName(root) + path
		default:
			path = filepath.Join(root, path)
		}
		title := strings.TrimSpace(row[m["title"]])
		if m["title"] == "" || title == "" {
			title = titleFromFile(p)
		}
		typ := strings.TrimSpace(row[m["type"]])
		var extra []string
		if m["type"] != "" && typ != "" {
			extra = append(extra, typ)
		}
		rowObj := map[string]any{}
		for k, v := range row {
			rowObj[k] = v
		}
		rec := assetRecord{
			Type: "asset", Key: p, Kind: kindHint(typ), Path: path, Import: imp, Title: title,
			Tags:    splitTags(row[m["tags"]], extra...),
			License: mapLicense(row[m["license"]], row[m["credit"]], row[m["source"]], title, o.defStatus),
			Meta:    map[string]any{"row": rowObj},
		}
		if err := emit(rec); err != nil {
			return err
		}
		assets++
	}
	return emit(map[string]any{"type": "done", "stats": map[string]int{"rows": len(t.rows), "assets": assets, "skipped": skipped}})
}

// columnsTool reports a CSV's header and the column each field would come from, so an agent can check a file before
// importing it.
func columnsTool(req request, emit func(any) error) error {
	p, _ := req.Args["path"].(string)
	if p == "" {
		p = req.Path
	}
	if p == "" {
		return badRequest(`give the CSV as args {"path": "..."}`)
	}
	o, err := parseOptions(req.Args)
	if err != nil {
		return err
	}
	t, err := readTable(p)
	if err != nil {
		return badRequest("%s: %v", p, err)
	}
	m, mapErr := mapping(t.header, o.overrides)
	res := map[string]any{"path": p, "header": t.header, "rows": len(t.rows), "mapping": m}
	if mapErr != nil {
		res["mapping"] = map[string]string{}
		res["problem"] = mapErr.Error()
	}
	return emit(map[string]any{"type": "result", "result": res})
}
