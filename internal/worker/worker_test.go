package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

func TestMediaReadsRangesButNeverSymlinksOrSource(t *testing.T) {
	dir := t.TempDir()
	render := filepath.Join(dir, "renders", "revision")
	if e := os.MkdirAll(render, 0o700); e != nil {
		t.Fatal(e)
	}
	if e := writeJSON(filepath.Join(render, "state.json"), project.LiveRender{ID: "revision"}); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(render, "seg_00000.m4s"), []byte("0123456789"), 0o600); e != nil {
		t.Fatal(e)
	}
	r := readMedia(dir, relay.Request{Render: "revision", File: "seg_00000.m4s", Range: "bytes=2-5"})
	if r.Status != 206 || string(r.Body) != "2345" || r.Range != "bytes 2-5/10" {
		t.Fatal(r)
	}
	secret := filepath.Join(dir, "secret.mp4")
	if e := os.WriteFile(secret, []byte("private source"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(secret, filepath.Join(render, "escape.mp4")); e != nil {
		t.Fatal(e)
	}
	for _, req := range []relay.Request{{Render: "revision", File: "escape.mp4"}, {Render: "revision", File: "state.json"}, {Render: "../revision", File: "seg_00000.m4s"}} {
		if got := readMedia(dir, req); got.Status != 404 {
			t.Fatal("unsafe read", req, got)
		}
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
	r := &runner{opts: Options{Root: root, Profiles: []Profile{{AgentProfile: project.AgentProfile{ID: "slow"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "slow"}}}}, dir: filepath.Join(root, ".retake", "worker")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan outcome, 1)
	go func() {
		done <- r.execute(ctx, project.WorkAssignment{Task: &project.EditingTask{ID: "task", Agent: "slow", Attempt: 1}})
	}()
	heartbeat := filepath.Join(root, "child-heartbeat")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e = os.Stat(heartbeat); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
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
		<-context.Background().Done()
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
