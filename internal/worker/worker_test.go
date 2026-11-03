package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// fakeProber says every file that isn't "broken" is a 10 s video.
type fakeProber struct{}

func (fakeProber) Probe(path string) probe.Info {
	if strings.Contains(filepath.Base(path), "broken") {
		return probe.Info{}
	}
	d, w, h := 10.0, 64, 36
	return probe.Info{Duration: &d, FPS: 30, Width: &w, Height: &h}
}

func body(t *testing.T, m media) string {
	t.Helper()
	defer m.close()
	if m.Body == nil {
		return ""
	}
	b, e := io.ReadAll(m.Body)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestMediaReadsRangesButNeverSymlinksOrSource(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".retake", "worker")
	render := filepath.Join(dir, "renders", "revision")
	if e := os.MkdirAll(render, 0o700); e != nil {
		t.Fatal(e)
	}
	if e := writeJSON(filepath.Join(render, "state.json"), project.LiveRender{ID: "revision"}); e != nil {
		t.Fatal(e)
	}
	write := func(p, data string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(p), 0o700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(render, "seg_00000.m4s"), "0123456789")
	r := &runner{opts: Options{Root: root, Prober: fakeProber{}}, dir: dir}
	m := r.readMedia(context.Background(), relay.Request{Render: "revision", File: "seg_00000.m4s", Range: "bytes=2-5"})
	if m.Status != 206 || m.Range != "bytes 2-5/10" || m.Length != 4 || body(t, m) != "2345" {
		t.Fatal(m.Status, m.Range, m.Length)
	}
	secret := filepath.Join(root, "secret.mp4")
	write(secret, "private source")
	if e := os.Symlink(secret, filepath.Join(render, "escape.mp4")); e != nil {
		t.Fatal(e)
	}
	// The project's version files: v1/clip_v1.mp4, and v2/ with an HLS playlist beside its segments.
	write(filepath.Join(root, "v1", "clip_v1.mp4"), "version one bytes")
	write(filepath.Join(root, "v2", "clip_v2.m3u8"), "#EXTM3U\n")
	write(filepath.Join(root, "v2", "seg_000.m4s"), "segment zero")
	write(filepath.Join(root, "v2", "notes.txt"), "not media")
	write(filepath.Join(root, "edit", "timeline.mp4"), "not a version")
	if e := os.Symlink(secret, filepath.Join(root, "v2", "seg_001.m4s")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(root, "v2"), filepath.Join(root, "v2", "sub.mp4")); e != nil {
		t.Fatal(e)
	}
	m = r.readMedia(context.Background(), relay.Request{Version: "v1", Range: "bytes=8-"})
	if m.Status != 206 || m.Range != "bytes 8-16/17" || body(t, m) != "one bytes" || m.Modified == 0 {
		t.Fatal(m.Status, m.Range, m.Modified)
	}
	if m = r.readMedia(context.Background(), relay.Request{Version: "v1", File: "clip_v1.mp4"}); m.Status != 200 || body(t, m) != "version one bytes" {
		t.Fatal("the version by its name", m.Status)
	}
	if m = r.readMedia(context.Background(), relay.Request{Version: "v1", File: "clip_v1.mp4", Head: true}); m.Status != 200 || m.Length != 17 || m.Body != nil {
		t.Fatal("HEAD", m.Status, m.Length)
	}
	if m = r.readMedia(context.Background(), relay.Request{Version: "v2", File: "seg_000.m4s"}); m.Status != 200 || body(t, m) != "segment zero" {
		t.Fatal("a segment beside the playlist", m.Status)
	}
	if m = r.readMedia(context.Background(), relay.Request{Version: "v1", Range: "bytes=17-"}); m.Status != 416 || m.Range != "bytes */17" {
		t.Fatal("past the end", m.Status, m.Range)
	}
	for _, req := range []relay.Request{
		{Render: "revision", File: "escape.mp4"}, {Render: "revision", File: "state.json"},
		{Render: "../revision", File: "seg_00000.m4s"},
		{Version: "v1", File: "secret.mp4"},           // not the version's file, and v1 isn't a playlist
		{Version: "v1", File: "../secret.mp4"},        // traversal
		{Version: "v2", File: "../v1/clip_v1.mp4"},    // out of the playlist's folder
		{Version: "v2", File: "../../secret.mp4"},     // further out
		{Version: "v2", File: "/etc/passwd"},          // absolute
		{Version: "v2", File: "notes.txt"},            // not media
		{Version: "v2", File: "seg_001.m4s"},          // a symlink out
		{Version: "v2", File: "sub.mp4"},              // a symlink to a folder
		{Version: "v3", File: "clip_v3.mp4"},          // no such version
		{Version: "../v1"}, {Version: "timeline"}, {}, // not versions
	} {
		if got := r.readMedia(context.Background(), req); got.Status != 404 {
			body(t, got)
			t.Fatal("unsafe read", req, got.Status)
		}
	}
	// What the server is told: metadata only, of the versions that play.
	write(filepath.Join(root, "v3", "clip_v3_broken.mp4"), "x")
	files := fileVersions(r.onDisk(false))
	if len(files) != 2 || files[0].Label != "v1" || files[0].Name != "clip_v1.mp4" || files[0].Size != 17 ||
		files[0].Duration != 10 || files[0].Width != 64 || files[1].Label != "v2" || files[1].Name != "clip_v2.m3u8" {
		t.Fatalf("%+v", files)
	}
}

func TestWatchIgnoresOwnOutputAndDetectsExternalAssetChanges(t *testing.T) {
	root := t.TempDir()
	asset := filepath.Join(t.TempDir(), "voice.wav")
	if e := os.WriteFile(asset, []byte("audio"), 0o600); e != nil {
		t.Fatal(e)
	}
	tl := filepath.Join(root, "timeline.json")
	if e := writeJSON(tl, map[string]any{"assets": map[string]any{"vo": map[string]any{"path": asset}}}); e != nil {
		t.Fatal(e)
	}
	a, _, e := fingerprint(root, tl)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(root, ".retake", "worker"), 0o700); e != nil {
		t.Fatal(e)
	}
	if e = writeJSON(filepath.Join(root, ".retake", "worker", "state.json"), map[string]int{"progress": 1}); e != nil {
		t.Fatal(e)
	}
	b, _, e := fingerprint(root, tl)
	if e != nil || a != b {
		t.Fatal("feedback loop from own state", e)
	}
	if e = os.WriteFile(asset, []byte("different audio"), 0o600); e != nil {
		t.Fatal(e)
	}
	c, _, e := fingerprint(root, tl)
	if e != nil || b == c {
		t.Fatal("external dependency ignored", e)
	}
	if e = os.WriteFile(tl, []byte("{"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = fingerprint(root, tl); e == nil {
		t.Fatal("partial save accepted")
	}
}

func TestProviderTelemetryDoesNotConfuseQuotedFeedbackWithAnError(t *testing.T) {
	u, limited := telemetry([]byte(`{"type":"result","usage":{"input_tokens":120,"output_tokens":24},"total_cost_usd":0.04}`))
	if limited || u == nil || u.Input != 120 || u.Cost == nil || *u.Cost != .04 {
		t.Fatal(u, limited)
	}
	_, limited = telemetry([]byte(`{"type":"turn.failed","error":{"message":"You have hit your usage limit"}}`))
	if !limited {
		t.Fatal("usage limit not recognized")
	}
	_, limited = telemetry([]byte(`{"type":"assistant","message":"Add the words usage limit to the title"}`))
	if limited {
		t.Fatal("ordinary output became a failure")
	}
	env := agentEnv([]string{"RETAKE_TOKEN=secret", "PATH=/bin", "OTHER=value"}, "context.json", "report.json", nil)
	if strings.Contains(strings.Join(env, "\n"), "secret") {
		t.Fatal("worker token passed to agent")
	}
}

func TestLocalProjectLockExcludesAnotherWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	un, e := lockProject(path)
	if e != nil {
		t.Fatal(e)
	}
	defer un()
	second, e := lockProject(path)
	if e == nil {
		second()
		t.Fatal("a second worker acquired the same project")
	}
}

func TestCancellingAgentStopsItsChildBeforeHandoff(t *testing.T) {
	root := t.TempDir()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	r := &runner{opts: Options{Root: root, Profiles: []Profile{{AgentProfile: project.AgentProfile{ID: "slow"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "slow"}}}}, dir: filepath.Join(root, ".retake", "worker"), timeline: filepath.Join(root, "timeline.json")}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan outcome, 1)
	go func() {
		done <- r.execute(ctx, project.WorkAssignment{Task: &project.EditingTask{ID: "task", Agent: "slow", Attempt: 1}})
	}()
	heartbeat := filepath.Join(root, "child-heartbeat")
	agentLog := func() string {
		b, _ := os.ReadFile(filepath.Join(r.dir, "tasks", "task", "agent-1.log"))
		_, started := os.Stat(filepath.Join(root, "agent-started"))
		return fmt.Sprintf("agent started: %v\nagent log:\n%s", started == nil, b)
	}
	// Two starts of the test binary can take a while on a busy (Windows) runner.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, e = os.Stat(heartbeat); e == nil {
			break
		}
		select {
		case o := <-done:
			t.Fatalf("the attempt ended before the agent's child started: %+v\n%s", o, agentLog())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never started\n%s", agentLog())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel(errStop) // a person's pause or switch
	select {
	case result := <-done:
		if result.state != "stopped" {
			t.Fatal(result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop")
	}
	before, e := os.ReadFile(heartbeat)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(150 * time.Millisecond)
	after, e := os.ReadFile(heartbeat)
	if e != nil || string(before) != string(after) {
		t.Fatal("old agent's child kept writing after handoff", e)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestAgentProcess is a deterministic stand-in for a provider, used by integration tests. It consumes the same
// context/report contract as Claude and Codex without using a subscription or pretending to transcribe audio.
func TestAgentProcess(t *testing.T) {
	contextPath := os.Getenv("RETAKE_CONTEXT")
	if contextPath == "" {
		return
	}
	b, e := os.ReadFile(contextPath)
	if e != nil {
		t.Fatal(e)
	}
	var assignment project.WorkAssignment
	if e = json.Unmarshal(b, &assignment); e != nil {
		t.Fatal(e)
	}
	mode := os.Args[len(os.Args)-1]
	if docAgent(t, mode, assignment) {
		return
	}
	switch mode {
	case "child":
		for {
			if e = os.WriteFile("child-heartbeat", []byte(time.Now().String()), 0o600); e != nil {
				t.Fatal(e)
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "limited":
		os.Stdout.WriteString("{\"type\":\"turn.failed\",\"error\":{\"message\":\"usage limit reached\"}}\n")
		os.Exit(1)
	case "question":
		if assignment.Task.Attempt == 1 {
			if e = writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "question", Message: "Blue or teal?"}); e != nil {
				t.Fatal(e)
			}
			return
		}
	case "batch": // a plain project's batch: logs its launch and notes, waits for $BATCH_DIR/go and $BATCH_PEERS launches
		var c plainContext
		if e = json.Unmarshal(b, &c); e != nil || len(c.Family) == 0 {
			t.Fatal("plain context", e)
		}
		dir, cwd := os.Getenv("BATCH_DIR"), filepath.Base(must(os.Getwd()))
		log, e := os.OpenFile(filepath.Join(dir, "launches"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = fmt.Fprintf(log, "%s %v\n", cwd, c.Task.Notes)
		_ = log.Close()
		peers, _ := strconv.Atoi(os.Getenv("BATCH_PEERS"))
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			got, _ := os.ReadFile(filepath.Join(dir, "launches"))
			if _, e = os.Stat(filepath.Join(dir, "go")); e == nil && strings.Count(string(got), "\n") >= peers {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the batch's gate never opened")
			}
		}
		video, e := os.ReadFile(c.Family[len(c.Family)-1].Path)
		if e == nil {
			e = os.WriteFile(os.Getenv("RETAKE_OUTPUT"), video, 0o600)
		}
		if e != nil {
			t.Fatal(e)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "launches"))
		round := strings.Count(string(got), cwd+" ")
		report := agentResult{State: "completed", Message: "Round " + strconv.Itoa(round)}
		if slices.Contains(c.Task.Notes, 1) {
			report.Notes = map[string]string{"1": "Fixed note 1", "x": "not a note"}
		}
		if e = writeJSON(os.Getenv("RETAKE_REPORT"), report); e != nil {
			t.Fatal(e)
		}
		return
	case "durable": // a plain project's agent that outlives its worker: waits for $DURABLE_DIR/go, then makes the next version
		var c plainContext
		if e = json.Unmarshal(b, &c); e != nil || len(c.Family) == 0 {
			t.Fatal("plain context", e)
		}
		dir := os.Getenv("DURABLE_DIR")
		log, e := os.OpenFile(filepath.Join(dir, "launches"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = fmt.Fprintf(log, "%v\n", c.Task.Notes)
		_ = log.Close()
		fmt.Println(`{"type":"system","subtype":"init"}`)
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if _, e = os.Stat(filepath.Join(dir, "go")); e == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("never told to go on")
			}
		}
		video, e := os.ReadFile(c.Family[len(c.Family)-1].Path)
		if e == nil {
			e = os.WriteFile(os.Getenv("RETAKE_OUTPUT"), video, 0o600)
		}
		if e == nil {
			e = writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "Fixed it while Retake restarted"})
		}
		if e != nil {
			t.Fatal(e)
		}
		fmt.Println(`{"type":"result","usage":{"input_tokens":120,"output_tokens":24},"total_cost_usd":0.04}`)
		return
	case "plain-lazy": // a plain project's agent that says it's done without a video
		if e = writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "Done, I think"}); e != nil {
			t.Fatal(e)
		}
		return
	case "plain-still": // checks its note's still is one the worker made, keeps a copy, then edits like plain-edit
		var c plainContext
		if e = json.Unmarshal(b, &c); e != nil || len(c.Notes) == 0 {
			t.Fatal("plain context", e)
		}
		n := c.Notes[0]
		if n.Detail.Snapshot != "note-1.png" || len(n.Missing) != 1 || !strings.Contains(n.Missing[0], "this computer made of v1") {
			t.Fatalf("note 1: snapshot %q, missing %q", n.Detail.Snapshot, n.Missing)
		}
		still, e := os.ReadFile(filepath.Join(filepath.Dir(contextPath), "note-1.png"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile("seen-still.png", still, 0o600); e != nil {
			t.Fatal(e)
		}
		fallthrough
	case "plain-edit": // a plain project's agent: cuts the newest version's first second, into $RETAKE_OUTPUT
		var c plainContext
		if e = json.Unmarshal(b, &c); e != nil || len(c.Family) == 0 || c.Output != os.Getenv("RETAKE_OUTPUT") {
			t.Fatal("plain context", c, e)
		}
		src := c.Family[len(c.Family)-1].Path
		cut := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-threads", "2", "-ss", "1", "-i", src,
			"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-movflags", "+faststart", os.Getenv("RETAKE_OUTPUT"))
		if out, e := cut.CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
		if e = writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "Cut the first second of " + c.Family[len(c.Family)-1].Label}); e != nil {
			t.Fatal(e)
		}
		return
	case "slow":
		bin, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		child := exec.Command(bin, "-test.run=^TestAgentProcess$", "--", "child")
		if e = child.Start(); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile("agent-started", []byte("running"), 0o600); e != nil {
			t.Fatal(e)
		}
		// Wait to be stopped. A sleep, not a receive that never ends: with no timer pending, Go's runtime calls that a
		// deadlock and exits (it does on Windows, where nothing else keeps the process looking busy).
		time.Sleep(time.Hour)
	}
	b, e = os.ReadFile("timeline.json")
	if e != nil {
		t.Fatal(e)
	}
	b = []byte(strings.Replace(string(b), "#aa3322", "#2255dd", 1))
	if e = os.WriteFile("timeline.json", b, 0o600); e != nil {
		t.Fatal(e)
	}
	if e = writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "Changed the first shot from red to blue; the other two shots remain intact."}); e != nil {
		t.Fatal(e)
	}
	time.Sleep(50 * time.Millisecond)
}
