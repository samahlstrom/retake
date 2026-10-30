package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/lib"
)

// libServer serves the library tools of a fresh timeline project, with the starter registry as $RETAKE_REGISTRY.
func libServer(t *testing.T, within bool) (*sdk.ClientSession, string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "edit", "timeline.json"), `{"schema": "timeline/0.1", "id": "t", "rev": 0,
		"format": {"size": [1280, 720], "fps": 30}, "assets": {}, "spine": [], "scenes": [{"id": "s", "shots": [],
		"overlays": [{"id": "pop/a", "use": "pop-text@1.0.0", "from": "t:0", "to": "t:1", "props": {"text": "A"}}]}]}`)
	reg, err := filepath.Abs(filepath.Join("..", "..", "library", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"RETAKE_REGISTRY": reg, "RETAKE_LIBRARY": filepath.Join(root, "library")}
	o := LibOptions{Root: root, Getenv: func(k string) string { return vars[k] },
		Editor: bridge.Editor{Configured: filepath.Join(root, "no-retake-edit")}}
	if within {
		o.Within = root
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "retake", Version: "test"}, nil)
	AddLibTools(s, o)
	ctx := context.Background()
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

func toolNames(t *testing.T, cs *sdk.ClientSession) map[string]*sdk.Tool {
	t.Helper()
	out := map[string]*sdk.Tool{}
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

func TestLibraryToolsOverMCP(t *testing.T) {
	cs, root := libServer(t, false)
	tools := toolNames(t, cs)
	for _, n := range []string{"component_list", "component_describe", "lib_search", "lib_add", "lint"} {
		tool := tools[n]
		if tool == nil || len(tool.Description) < 200 || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Errorf("tool %s: missing, thin description or no schemas: %+v", n, tool)
		}
	}

	var found lib.SearchResult
	if text, isErr := call(t, cs, "lib_search", map[string]any{"q": "karaoke"}, &found); isErr || len(found.Hits) != 1 ||
		found.Hits[0].Name != "caption" || found.Hits[0].Installed {
		t.Fatalf("lib_search = %+v %s", found, text)
	}
	var added lib.AddResult
	if text, isErr := call(t, cs, "lib_add", map[string]any{"name": "pop-text"}, &added); isErr ||
		added.Packages[0].Ref != "pop-text@1.0.0" || added.Packages[0].Status != "added" {
		t.Fatalf("lib_add = %+v %s", added, text)
	}
	if lock, err := lib.ReadLock(filepath.Join(root, "edit", "timeline.lock")); err != nil || len(lock.Packages) != 1 {
		t.Fatalf("lock = %+v %v", lock, err)
	}
	var list lib.ListResult
	if text, isErr := call(t, cs, "component_list", map[string]any{}, &list); isErr || len(list.Components) != 1 ||
		!list.Components[0].Locked || list.Components[0].Used[0] != "pop/a" {
		t.Fatalf("component_list = %+v %s", list, text)
	}

	// describe returns the schema and the sheet as an image the agent sees.
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "component_describe",
		Arguments: map[string]any{"name": "pop-text"}})
	if err != nil || res.IsError {
		t.Fatalf("component_describe: %v %+v", err, res)
	}
	var image bool
	for _, c := range res.Content {
		if ic, ok := c.(*sdk.ImageContent); ok && ic.MIMEType == "image/png" && len(ic.Data) > 1000 {
			image = true
		}
	}
	var d lib.Description
	if _, isErr := call(t, cs, "component_describe", map[string]any{"name": "pop-text@1.0.0"}, &d); isErr || !image ||
		d.Source != "project" || d.Props == nil || d.Use["use"] != "pop-text@1.0.0" || len(d.Examples) == 0 {
		t.Fatalf("component_describe = %+v (image %v)", d, image)
	}
	// Not in the project yet: described from the registry.
	if _, isErr := call(t, cs, "component_describe", map[string]any{"name": "lower-third"}, &d); isErr ||
		d.Source != "registry" || d.Name != "lower-third" {
		t.Fatalf("component_describe from the registry = %+v", d)
	}

	// lint needs retake-edit; without it the error says so.
	if text, isErr := call(t, cs, "lint", map[string]any{}, nil); !isErr || !strings.Contains(text, "retake-edit") {
		t.Fatalf("lint without retake-edit: %v %s", isErr, text)
	}
	if text, isErr := call(t, cs, "lib_add", map[string]any{"name": "no-such-thing"}, nil); !isErr ||
		!strings.Contains(text, "no package no-such-thing") {
		t.Fatalf("lib_add unknown: %s", text)
	}
}

func TestLibraryToolsForARemoteAgent(t *testing.T) {
	cs, root := libServer(t, true)
	tools := toolNames(t, cs)
	if tools["lib_add"] != nil {
		t.Fatal("a remote agent can't add packages")
	}
	if tools["lib_search"] == nil || tools["component_describe"] == nil || tools["lint"] == nil {
		t.Fatalf("tools = %v", tools)
	}
	// A timeline outside the project is refused.
	if text, isErr := call(t, cs, "lint", map[string]any{"timeline": "/etc/hostname"}, nil); !isErr ||
		!strings.Contains(text, "inside the project folder") {
		t.Fatalf("lint outside: %s", text)
	}
	// The registry is the configured one, whatever the agent names.
	var found lib.SearchResult
	if _, isErr := call(t, cs, "lib_search", map[string]any{"q": "counter", "registry": filepath.Join(root, "x")},
		&found); isErr || len(found.Hits) != 1 {
		t.Fatalf("lib_search = %+v", found)
	}
}
