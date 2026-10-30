package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/project"
)

const agentTake = "2026-10-29_20-48-01"

// seedNotes opens the world's project store and adds three open notes on v6 (the opened version).
func (w *world) seedNotes(t *testing.T) *project.Store {
	t.Helper()
	st, err := project.Open(w.root)
	if err != nil {
		t.Fatal(err)
	}
	st.Poll = 5 * time.Millisecond
	t.Cleanup(func() { _ = st.Close() })
	var cs []project.Comment
	for i, text := range []string{"title bigger", "cut the pause", "music too loud"} {
		cs = append(cs, project.Comment{Take: agentTake, Seg: "S1", N: i + 1, T0: float64(i * 2), T1: float64(i*2 + 1),
			Text: text, Author: "Sam"})
	}
	if _, err := st.Sync(project.Run{Version: "v6", Comments: cs}); err != nil {
		t.Fatal(err)
	}
	return st
}

// out runs a command that doesn't serve and returns its exit code, stdout and stderr.
func (w *world) out(args ...string) (int, string, string) {
	w.stdout.mu.Lock()
	w.stdout.b.Reset()
	w.stdout.mu.Unlock()
	w.stderr.mu.Lock()
	w.stderr.b.Reset()
	w.stderr.mu.Unlock()
	code := w.run(args...)
	return code, w.stdout.String(), w.stderr.String()
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

func TestAgentCommands(t *testing.T) {
	w := newWorld(t)
	w.seedNotes(t)

	code, out, errOut := w.out("notes", "list", w.video, "--json")
	list := decode[agent.ListResult](t, out)
	if code != 0 || len(list.Notes) != 3 || list.Notes[1].Text != "cut the pause" || list.Cursor != 1 {
		t.Fatalf("notes list --json: %d %s %s", code, out, errOut)
	}
	code, out, _ = w.out("notes", "list", "--status", "open,question", w.video)
	if code != 0 || !strings.Contains(out, "#1   v6   0:00.0-0:01.0  open      Sam: title bigger") ||
		!strings.HasSuffix(out, "cursor 1\n") {
		t.Fatalf("notes list:\n%s", out)
	}
	if code, _, errOut = w.out("notes", "list", w.video, "--status", "bogus"); code != 1 ||
		!strings.Contains(errOut, `retake notes list: unknown status "bogus"`) {
		t.Errorf("bad status: %d %s", code, errOut)
	}
	code, out, _ = w.out("notes", "list", w.video, "--version", "v9", "--json")
	if e := decode[agent.Error](t, out); code != 1 || e.Code != agent.CodeNotFound {
		t.Errorf("unknown version --json: %d %s", code, out)
	}

	code, out, _ = w.out("notes", "wait", w.video, "--json")
	if wait := decode[agent.WaitResult](t, out); code != 0 || len(wait.Notes) != 3 || wait.Cursor != 1 {
		t.Fatalf("notes wait --json: %s", out)
	}
	if code, out, _ = w.out("notes", "wait", w.video, "--since", "1", "--timeout", "0.05"); code != 0 ||
		out != "nothing new (cursor 1)\n" {
		t.Errorf("notes wait timeout: %d %q", code, out)
	}

	code, out, _ = w.out("ask", w.video, "3", "Which track instead?", "--option", "v5", "--option", "v7", "--json")
	if q := decode[project.Thread](t, out); code != 0 || *q.Note != 3 || !slices.Equal(q.Options, []string{"v5", "v7"}) {
		t.Fatalf("ask: %s", out)
	}
	if code, out, _ = w.out("ask", w.video, "0", "Too long overall?"); code != 0 ||
		out != "asked about version v7 (question 2): Too long overall?\n" {
		t.Errorf("ask about the version: %d %q", code, out)
	}
	if code, out, _ = w.out("note", w.video, "#1", "--status", "answered", "--in", "v7", "--change", "title at 120%"); code != 0 ||
		!strings.Contains(out, "answered  Sam: title bigger  -> v7: title at 120%") {
		t.Errorf("note: %d %q", code, out)
	}
	if code, _, errOut = w.out("note", w.video, "1", "--status", "accepted"); code != 1 ||
		!strings.Contains(errOut, "approving is the reviewer's action") {
		t.Errorf("note accepted: %d %s", code, errOut)
	}

	render := filepath.Join(t.TempDir(), "render.mp4")
	if err := os.WriteFile(render, []byte("new render"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = w.out("push", w.video, render, "--parent", "v6")
	if code != 1 || !strings.Contains(errOut, "retake push: push rejected: note #2 is open on v6") {
		t.Fatalf("push without handling #2: %d %s", code, errOut)
	}
	code, out, _ = w.out("push", w.video, render, "--parent", "v6", "--json")
	if e := decode[agent.Error](t, out); code != 1 || e.Code != agent.CodePushRule || !slices.Equal(e.Notes, []int64{2}) {
		t.Fatalf("push --json rejection: %s", out)
	}
	code, out, errOut = w.out("push", w.video, render, "--parent=v6", "--change", `2=pause cut to half a second`)
	if code != 0 || !strings.Contains(out, "pushed v8 from v6: "+filepath.Join(w.root, "v8", "clip_v8.mp4")) ||
		!strings.Contains(out, "answered #2") {
		t.Fatalf("push: %d %s %s", code, out, errOut)
	}
	code, out, _ = w.out("versions", w.video, "--json")
	vs := decode[agent.VersionsResult](t, out)
	if code != 0 || len(vs.Versions) != 4 || vs.Versions[3].Label != "v8" || vs.Versions[3].Parent != "v6" {
		t.Fatalf("versions --json: %s", out)
	}
	if code, out, _ = w.out("versions", w.video); code != 0 || !strings.Contains(out, "(from v6 by agent, 1 change)") ||
		!strings.Contains(out, "#2 pause cut to half a second") {
		t.Errorf("versions:\n%s", out)
	}

	for _, args := range [][]string{
		{"notes", "frob", w.video}, // a bare `notes` prints help (TestNotesBuildErrors)
		{"notes", "list"},
		{"notes", "wait", w.video, "--since", "x"},
		{"notes", "wait", w.video, "--timeout", "-1"},
		{"ask", w.video, "3"},
		{"ask", w.video, "x", "why?"},
		{"note", w.video, "1"},
		{"push", w.video},
		{"push", w.video, render, "--change", "2"},
		{"push", w.video, render, "--ask", "x=why"},
		{"push", w.video, render, "--frob"},
		{"versions"},
		{"mcp"},
		{"watch", w.video},
	} {
		if code, _, errOut := w.out(args...); code != ExitUsage || !strings.Contains(errOut, "usage:") {
			t.Errorf("%v: exit %d, want %d with usage\n%s", args, code, ExitUsage, errOut)
		}
	}
	if code, _, errOut := w.out("versions", filepath.Join(w.root, "nope_v1.mp4")); code != ExitUsage ||
		!strings.Contains(errOut, "no such file") {
		t.Errorf("missing video: %d %s", code, errOut)
	}
}

type fakeNotes struct{}

func (fakeNotes) Build(_ context.Context, v family.Version, _ []family.Version) (project.Run, error) {
	return project.Run{Comments: []project.Comment{{Take: agentTake, Seg: "S1", N: 1, T0: 1, T1: 2, Text: "from " + v.Label}}}, nil
}

func TestNotesBuildCommand(t *testing.T) {
	w := newWorld(t)
	env := w.environment(nil)
	env.Engines = func(config.Config) jobs.Engines { return jobs.Engines{Notes: fakeNotes{}} }
	code := Main(context.Background(), []string{"notes", "sync", w.video, "--json"}, env)
	if code != 0 || !strings.Contains(w.stdout.String(), `"version": "v6"`) || !strings.Contains(w.stdout.String(), `"Added": [`) {
		t.Fatalf("notes sync: %d %s %s", code, w.stdout.String(), w.stderr.String())
	}
	if !strings.Contains(w.stderr.String(), "not transcribing (no transcriber is set up)") {
		t.Errorf("stderr = %s", w.stderr.String())
	}
}

func TestMCPCommand(t *testing.T) {
	w := newWorld(t)
	w.seedNotes(t)
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	env := w.environment(nil)
	env.Stdin, env.Stdout = sr, sw
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- Main(ctx, []string{"mcp", w.video}, env) }()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "notes_list", Arguments: map[string]any{"status": []string{"open"}}})
	if err != nil || res.IsError {
		t.Fatalf("notes_list: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if l := decode[agent.ListResult](t, string(b)); len(l.Notes) != 3 {
		t.Fatalf("notes_list = %s", b)
	}
	_ = cs.Close()
	_ = cw.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("retake mcp exited %d: %s", code, w.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retake mcp didn't stop when the client hung up")
	}
}

func TestWatch(t *testing.T) {
	w := newWorld(t)
	st := w.seedNotes(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	gate := filepath.Join(t.TempDir(), "slow")
	// Each run logs its start and end; while the gate file exists a run takes long.
	hook := fmt.Sprintf(`echo "start $RETAKE_SINCE $RETAKE_VIDEO $RETAKE_PROJECT" >> %[1]q; `+
		`if [ -e %[2]q ]; then sleep 30; fi; sleep 0.2; echo end >> %[1]q`, log, gate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := w.environment(nil)
	done := make(chan int, 1)
	go func() { done <- Main(ctx, []string{"watch", w.video, "--exec", hook, "--debounce", "0.15"}, env) }()
	waitUntil(t, "watch to start", func() bool { return strings.Contains(w.stderr.String(), "after cursor 1") })

	lines := func() []string {
		b, _ := os.ReadFile(log)
		return strings.Fields(strings.ReplaceAll(string(b), "\n", " | "))
	}
	add := func(n int, text string) {
		t.Helper()
		var cs []project.Comment
		for i := range n {
			cs = append(cs, project.Comment{Key: fmt.Sprintf("%s/S9/%s%d", agentTake, text, i), Take: agentTake,
				Seg: "S9", T0: 1, T1: 2, Text: text, Version: "v6"})
		}
		// Keep v6's earlier notes in the run so nothing is retracted.
		old, _ := st.Notes(project.NoteFilter{Scope: &project.Scope{Version: "v6"}})
		for _, n := range old {
			cs = append(cs, project.Comment{Key: n.Key, Take: n.Take, Seg: n.Seg, T0: n.T0, T1: n.T1, Text: n.Text,
				Author: n.Author, Version: n.Version})
		}
		if _, err := st.Sync(project.Run{Version: "v6", Comments: cs}); err != nil {
			t.Fatal(err)
		}
	}

	// Two syncs in quick succession: one run (debounced), with the cursor from before them.
	add(1, "a")
	add(1, "b")
	waitUntil(t, "the first run", func() bool { return strings.Count(strings.Join(lines(), " "), "end") == 1 })
	first := strings.Join(lines(), " ")
	if !strings.HasPrefix(first, "start 1 "+w.video+" "+w.root+" |") {
		t.Fatalf("first run: %q", first)
	}
	time.Sleep(300 * time.Millisecond)
	if n := strings.Count(strings.Join(lines(), " "), "start"); n != 1 {
		t.Fatalf("the burst ran %d times", n)
	}

	// Notes landing during a run trigger the next run after it, never alongside it.
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	add(1, "c")
	waitUntil(t, "the second run to start", func() bool { return strings.Count(strings.Join(lines(), " "), "start") == 2 })
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	add(1, "d") // lands while run 2 sleeps behind the (now removed) gate
	time.Sleep(200 * time.Millisecond)
	if got := strings.Join(lines(), " "); strings.Count(got, "start") != 2 {
		t.Fatalf("a run started while another was running: %q", got)
	}

	// Ctrl+C: the running command is interrupted and watch exits cleanly.
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("watch exited %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch didn't stop")
	}
	got := strings.Join(lines(), " ")
	if strings.Count(got, "start") != 2 || strings.Count(got, "end") != 1 {
		t.Errorf("runs: %q", got)
	}
	if !strings.Contains(w.stderr.String(), "retake watch: stopped") {
		t.Errorf("stderr: %s", w.stderr.String())
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
