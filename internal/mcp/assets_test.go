package mcp

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/assets"
)

// TestMain keeps every test in this package away from the user's library, plug-ins and config.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "retake-mcp-test-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{"RETAKE_LIBRARY": filepath.Join(dir, "library"), "RETAKE_PLUGIN_PATH": "",
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "RETAKE_EDIT": filepath.Join(dir, "no-retake-edit")} {
		_ = os.Setenv(k, v)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const gamePlugin = `#!/bin/sh
req=$(cat)
case "$req" in
*'"method":"tool"'*)
  echo '{"type":"result","result":{"rows":[{"span_key":"k1","label":"a kill"}]}}' ;;
*)
  echo '{"type":"asset","key":"rec","kind":"video","path":"rec.mp4","title":"Long recording","license":{"status":"owned"},"media":{"duration":300}}'
  echo '{"type":"span","key":"k1","asset":"rec","t0":10,"t1":20,"span_type":"kill","label":"Blue scores","data":{"match_id":7}}'
  echo '{"type":"clock","asset":"rec","domain":"game:match/7","epochs":[{"asset_t":[0,300],"domain_t0":-30,"rate":1}]}'
  echo '{"type":"done"}' ;;
esac
`

func assetServer(t *testing.T) (*sdk.ClientSession, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plug-in is a shell script")
	}
	root := t.TempDir()
	pdir := filepath.Join(root, ".retake", "plugins", "game")
	write(t, filepath.Join(pdir, "plugin.json"), `{"name": "game", "version": "1.0", "exec": ["./p.sh"],
		"span_types": [{"type": "kill", "data_schema": {"type": "object", "required": ["match_id"]}}],
		"indexers": [{"name": "all"}],
		"tools": [{"name": "find", "description": "Find kills.", "input_schema": {"properties": {"hero": {"type": "string"}}}}]}`)
	write(t, filepath.Join(pdir, "p.sh"), gamePlugin)
	if err := os.Chmod(filepath.Join(pdir, "p.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pdir, "rec.mp4"), "not really a video")
	lib := t.TempDir()
	open := func() (*assets.Catalog, error) { return assets.Open(assets.Options{Root: root, Library: lib}) }
	ctx := context.Background()
	s := sdk.NewServer(&sdk.Implementation{Name: "retake", Version: "test"}, nil)
	AddAssetTools(s, open)
	st, ct := sdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, root
}

func pngFile(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestAssetToolsOverMCP(t *testing.T) {
	cs, root := assetServer(t)
	ctx := context.Background()
	tools := map[string]*sdk.Tool{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	for _, n := range []string{"asset_search", "asset_get", "asset_preview", "span_find", "span_add", "asset_import",
		"plugin_query", "license_report"} {
		tool := tools[n]
		if tool == nil || len(tool.Description) < 200 || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Errorf("tool %s: missing, thin description or no schemas: %+v", n, tool)
		}
	}
	if pt := tools["game_find"]; pt == nil || !strings.Contains(pt.Description, "Find kills.") {
		t.Fatalf("plug-in tool = %+v", pt)
	}

	// An agent's import is review-only, whatever it is.
	pngFile(t, filepath.Join(root, "in", "dragon-card.png"), 120, 80)
	var imp assets.ImportResult
	if text, isErr := call(t, cs, "asset_import", map[string]any{"paths": []string{filepath.Join(root, "in")},
		"tags": []string{"cards"}}, &imp); isErr || imp.Imported != 1 {
		t.Fatalf("asset_import = %+v %s", imp, text)
	}
	id := imp.Items[0].ID
	var e assets.Entry
	if _, isErr := call(t, cs, "asset_get", map[string]any{"id": id}, &e); isErr || e.License.Status != assets.LicenseReviewOnly ||
		e.License.Publish || e.Provenance.Importer != "mcp" || e.Kind != assets.KindImage {
		t.Fatalf("asset_get = %+v", e)
	}
	var found assets.SearchResult
	if _, isErr := call(t, cs, "asset_search", map[string]any{"q": "dragon", "tags": []string{"cards"}}, &found); isErr ||
		len(found.Results) != 1 || found.Results[0].Asset != id || found.Results[0].Preview == "" {
		t.Fatalf("asset_search = %+v", found)
	}

	// asset_preview returns the images as content too.
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "asset_preview", Arguments: map[string]any{"id": id}})
	if err != nil || res.IsError {
		t.Fatalf("asset_preview: %v %+v", err, res)
	}
	var images int
	for _, c := range res.Content {
		if ic, ok := c.(*sdk.ImageContent); ok && len(ic.Data) > 100 && ic.MIMEType == "image/jpeg" {
			images++
		}
	}
	if images != 1 || res.StructuredContent == nil {
		t.Fatalf("asset_preview content: %d images", images)
	}

	// The plug-in's index, then spans by type and near a domain time, and the agent's own span.
	if _, isErr := call(t, cs, "asset_import", map[string]any{"via": "game"}, &imp); isErr || imp.Spans != 1 {
		t.Fatalf("index via game = %+v", imp)
	}
	var spans assets.SpanFindResult
	if _, isErr := call(t, cs, "span_find", map[string]any{"near": "game:match/7@0:00", "window": 25}, &spans); isErr ||
		len(spans.Spans) != 1 || spans.Spans[0].Clocks[0].T0 != -20 {
		t.Fatalf("span_find near = %+v", spans)
	}
	var sp assets.Span
	if _, isErr := call(t, cs, "span_add", map[string]any{"asset": "proj:game/rec", "t0": 40, "t1": 44.5,
		"label": "the escape"}, &sp); isErr || sp.SourceOfLabel != "agent" || sp.Span != "sp:2" {
		t.Fatalf("span_add = %+v", sp)
	}
	if text, isErr := call(t, cs, "span_add", map[string]any{"asset": "proj:game/rec", "t0": 1, "t1": 2, "type": "kill"}, nil); !isErr ||
		!strings.Contains(text, "match_id") {
		t.Fatalf("span_add without required data: %v %s", isErr, text)
	}
	if _, isErr := call(t, cs, "span_find", map[string]any{"type": []string{"tag"}}, &spans); isErr || len(spans.Spans) != 1 {
		t.Fatalf("span_find type = %+v", spans)
	}

	// Plug-in tools, both ways, with span ids linked.
	var q assets.PluginQueryResult
	if _, isErr := call(t, cs, "plugin_query", map[string]any{"plugin": "game", "tool": "find"}, &q); isErr {
		t.Fatal("plugin_query failed")
	}
	if row := q.Result.(map[string]any)["rows"].([]any)[0].(map[string]any); row["span"] != "sp:1" {
		t.Fatalf("plugin_query = %+v", q)
	}
	text, isErr := call(t, cs, "game_find", map[string]any{"hero": "x"}, nil)
	if isErr || !strings.Contains(text, `"span":"sp:1"`) {
		t.Fatalf("game_find = %v %s", isErr, text)
	}

	// The license report of a manifest that uses the agent's import: blocked.
	manifest := filepath.Join(root, "v1.manifest.json")
	write(t, manifest, `{"schema": "manifest/0.1", "units": [{"unit": "u1", "t": [0, 2], "elements": [
		{"id": "card", "kind": "overlay", "t": [0.5, 1.5], "sources": [{"asset": "`+id+`", "in": 0, "out": 1}]},
		{"id": "bg", "kind": "shot", "sources": [{"asset": "proj:game/rec", "in": 10, "out": 12}]}]}]}`)
	var rep assets.LicenseReport
	if _, isErr := call(t, cs, "license_report", map[string]any{"path": manifest}, &rep); isErr || rep.Publishable ||
		len(rep.Blockers) != 1 || rep.Blockers[0].Asset != id || rep.Blockers[0].Ranges[0] != [2]float64{0.5, 1.5} {
		t.Fatalf("license_report = %+v", rep)
	}

	if text, isErr := call(t, cs, "asset_get", map[string]any{"id": "proj:nope"}, nil); !isErr || !strings.Contains(text, "no asset proj:nope") {
		t.Fatalf("missing asset: %v %s", isErr, text)
	}
}

// The review server serves the asset tools too.
func TestReviewServerHasAssetTools(t *testing.T) {
	p := newProject(t)
	cs := connect(t, p)
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	for _, want := range []string{"notes_wait", "asset_search", "license_report"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools %v lack %s", names, want)
		}
	}
	// No catalog is created until an asset tool writes.
	var res assets.SearchResult
	if _, isErr := call(t, cs, "asset_search", map[string]any{"q": "x"}, &res); isErr || len(res.Results) != 0 {
		t.Fatalf("asset_search on an empty project = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(p.Root, ".retake", assets.DBName)); err == nil {
		t.Fatal("a search created the catalog")
	}
}
