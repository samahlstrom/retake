package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
)

type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d := 10.0
	return probe.Info{Duration: &d, FPS: 30}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newProject makes v7/clip_v7.mp4 with three open notes.
func newProject(t *testing.T) *agent.Project {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "v7", "clip_v7.mp4"), "seven")
	p, err := agent.Open(filepath.Join(root, "v7", "clip_v7.mp4"), fakeProber{})
	if err != nil {
		t.Fatal(err)
	}
	p.Store.Poll = 5 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	var cs []project.Comment
	for i, text := range []string{"title bigger", "cut the pause", "music too loud"} {
		cs = append(cs, project.Comment{Take: "2026-10-29_20-48-01", Seg: "S1", N: i + 1, T0: float64(i), T1: float64(i) + 1,
			Text: text, Author: "Sam"})
	}
	if _, err := p.Store.Sync(project.Run{Version: "v7", Comments: cs}); err != nil {
		t.Fatal(err)
	}
	return p
}

func connect(t *testing.T, p *agent.Project) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := NewServer(p, "test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call calls a tool and decodes its structured result into out. It returns the result's text and IsError.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !res.IsError && out != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v in %s", name, err, b)
		}
	}
	return text.String(), res.IsError
}

func TestToolsOverMCP(t *testing.T) {
	p := newProject(t)
	cs := connect(t, p)
	ctx := context.Background()

	if init := cs.InitializeResult(); init == nil || !strings.Contains(init.Instructions, "version_push") ||
		init.ServerInfo.Name != "retake" {
		t.Fatalf("initialize = %+v", init)
	}
	tools := map[string]*sdk.Tool{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	var names []string
	for n := range tools {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"asset_get", "asset_import", "asset_preview", "asset_search", "license_report",
		"note_ask", "note_set", "notes_list", "notes_wait", "plugin_query", "span_add", "span_find", "version_push",
		"versions_list"}) {
		t.Fatalf("tools = %v", names)
	}
	if d := tools["version_push"].Description; !strings.Contains(d, "PUSH RULE") || !strings.Contains(d, "open on the parent") {
		t.Errorf("version_push doesn't explain the push rule: %s", d)
	}
	for _, n := range names {
		if len(tools[n].Description) < 200 || tools[n].InputSchema == nil || tools[n].OutputSchema == nil {
			t.Errorf("tool %s: thin description or no schemas", n)
		}
	}

	var list agent.ListResult
	if _, isErr := call(t, cs, "notes_list", map[string]any{"status": []string{"open"}}, &list); isErr || len(list.Notes) != 3 ||
		list.Notes[0].Text != "title bigger" || list.Cursor == 0 {
		t.Fatalf("notes_list = %+v", list)
	}
	var waited agent.WaitResult
	if _, isErr := call(t, cs, "notes_wait", map[string]any{"since": 0}, &waited); isErr || len(waited.Notes) != 3 {
		t.Fatalf("notes_wait = %+v", waited)
	}
	if _, isErr := call(t, cs, "notes_wait", map[string]any{"since": waited.Cursor, "timeout": 0.05}, &waited); isErr ||
		!waited.TimedOut {
		t.Fatalf("notes_wait timeout = %+v", waited)
	}

	var q project.Thread
	if _, isErr := call(t, cs, "note_ask", map[string]any{"note": 3, "question": "which track instead?"}, &q); isErr ||
		q.Kind != "question" || *q.Note != 3 {
		t.Fatalf("note_ask = %+v", q)
	}
	var set agent.NoteView
	if _, isErr := call(t, cs, "note_set", map[string]any{"id": 1, "status": "answered", "in": "v7", "change": "x"}, &set); isErr ||
		set.Status != project.StatusAnswered {
		t.Fatalf("note_set = %+v", set)
	}
	text, isErr := call(t, cs, "note_set", map[string]any{"id": 1, "status": "accepted"}, nil)
	if !isErr || !strings.Contains(text, "approving is the reviewer's action") {
		t.Fatalf("note_set accepted: %v %s", isErr, text)
	}

	src := filepath.Join(t.TempDir(), "render.mp4")
	write(t, src, "eight")
	text, isErr = call(t, cs, "version_push", map[string]any{"file": src}, nil)
	if !isErr || !strings.Contains(text, "push rejected: note #2 is open on v7") {
		t.Fatalf("push without handling #2: %v %s", isErr, text)
	}
	var pushed agent.PushResult
	if _, isErr := call(t, cs, "version_push", map[string]any{"file": src,
		"changes": []map[string]any{{"note": 2, "summary": "pause cut"}}}, &pushed); isErr ||
		pushed.Version.Label != "v8" || !slices.Equal(pushed.Answered, []int64{2}) {
		t.Fatalf("version_push = %+v", pushed)
	}
	var versions agent.VersionsResult
	if _, isErr := call(t, cs, "versions_list", nil, &versions); isErr || len(versions.Versions) != 2 ||
		versions.Versions[1].Parent != "v7" || len(versions.Versions[1].Changelog) != 1 {
		t.Fatalf("versions_list = %+v", versions)
	}
	if text, isErr := call(t, cs, "notes_list", map[string]any{"status": []string{"nope"}}, nil); !isErr ||
		!strings.Contains(text, "unknown status") {
		t.Errorf("bad status: %s", text)
	}
}

// Serve speaks newline-delimited JSON-RPC on plain streams, as `retake mcp` does on stdio.
func TestServeOverStreams(t *testing.T) {
	p := newProject(t)
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, p, "test", sr, sw) }()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "1"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list agent.ListResult
	if _, isErr := call(t, cs, "notes_list", nil, &list); isErr || len(list.Notes) != 3 {
		t.Fatalf("notes_list = %+v", list)
	}
	_ = cs.Close()
	_ = cw.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve didn't return")
	}
}
