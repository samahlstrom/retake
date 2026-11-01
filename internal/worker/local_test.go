package worker

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/project"
)

func TestProfileEnvNamesTheWorkersOwnVariables(t *testing.T) {
	env := agentEnv([]string{"OPENROUTER_API_KEY=sk-or-test", "ANTHROPIC_API_KEY=billing-key", "RETAKE_TOKEN=secret",
		"HOME=/home/someone", "PATH=/bin"}, "context.json", "report.json", map[string]string{
		"ANTHROPIC_BASE_URL":   "https://openrouter.ai/api",
		"ANTHROPIC_AUTH_TOKEN": "$OPENROUTER_API_KEY",
		"ANTHROPIC_API_KEY":    "",
		"CACHE":                "${HOME}/.cache/agent",
		"LEAK":                 "$RETAKE_TOKEN",
		"RETAKE_REPORT":        "elsewhere.json",
	})
	got := map[string][]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	want := map[string]string{"ANTHROPIC_BASE_URL": "https://openrouter.ai/api", "ANTHROPIC_AUTH_TOKEN": "sk-or-test",
		"ANTHROPIC_API_KEY": "", "CACHE": "/home/someone/.cache/agent", "LEAK": "", "RETAKE_REPORT": "report.json",
		"RETAKE_CONTEXT": "context.json", "PATH": "/bin"}
	for k, v := range want {
		if len(got[k]) != 1 || got[k][0] != v {
			t.Errorf("%s = %q, want just %q", k, got[k], v)
		}
	}
	if strings.Contains(strings.Join(env, "\n"), "secret") {
		t.Fatal("worker token reached the agent")
	}
}

func TestRestartedWorkerRemembersItsLastCompleteRender(t *testing.T) {
	dir := t.TempDir()
	write := func(id string, v project.LiveRender, at time.Time) {
		t.Helper()
		p := filepath.Join(dir, "renders", id)
		if e := os.MkdirAll(p, 0o700); e != nil {
			t.Fatal(e)
		}
		if e := writeJSON(filepath.Join(p, "state.json"), v); e != nil {
			t.Fatal(e)
		}
		if e := os.Chtimes(filepath.Join(p, "state.json"), at, at); e != nil {
			t.Fatal(e)
		}
	}
	if got := lastRender(dir); got.ID != "" {
		t.Fatal("a render from nowhere", got)
	}
	now := time.Now()
	source := strings.Repeat("b", 64)
	write("older", project.LiveRender{ID: "older", Source: strings.Repeat("a", 64), Complete: true}, now.Add(-time.Hour))
	write("final", project.LiveRender{ID: "final", Source: source, Complete: true, Label: "v3"}, now.Add(-time.Minute))
	write("stopped", project.LiveRender{ID: "stopped", Source: strings.Repeat("c", 64), Stopped: true}, now)
	write("forged", project.LiveRender{ID: "other", Source: strings.Repeat("d", 64), Complete: true}, now)
	if got := lastRender(dir); got.ID != "final" || got.Source != source {
		t.Fatalf("last render %+v", got)
	}
}

func TestBlockedReasonsAreShortSentences(t *testing.T) {
	root := t.TempDir()
	o := Options{Root: root, Editor: bridge.Editor{Configured: filepath.Join(root, "missing-retake-edit")}}
	// A folder without a timeline is a plain project: it needs an agent, not the renderer.
	if got := Reason(Check(o)); got != "No agent found. Install Claude Code or Codex." {
		t.Fatal(got)
	}
	if e := os.MkdirAll(filepath.Join(root, "edit"), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "edit", "timeline.json"), []byte(`{}`), 0o600); e != nil {
		t.Fatal(e)
	}
	if got := Reason(Check(o)); got != "retake-edit isn't installed, so edits can't be rendered." {
		t.Fatal(got)
	}
	editor := filepath.Join(root, "retake-edit")
	if e := os.WriteFile(editor, []byte("#!/bin/sh\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	o.Editor.Configured = editor
	if got := Reason(Check(o)); got != "No agent found. Install Claude Code or Codex." {
		t.Fatal(got)
	}
	if got := Reason(ErrBusy); got != "Another retake worker is editing this project." {
		t.Fatal(got)
	}
	// Local keeps checking: blocked first, then it runs once an agent is installed.
	old := LocalRetry
	LocalRetry = 20 * time.Millisecond
	t.Cleanup(func() { LocalRetry = old })
	ctx, cancel := context.WithCancel(context.Background())
	reasons := make(chan string, 100)
	done := make(chan struct{})
	var installed atomic.Bool
	profiles := func() []Profile {
		if !installed.Load() {
			return nil
		}
		return []Profile{{AgentProfile: project.AgentProfile{ID: "fake"}, Command: []string{"true"}}}
	}
	go func() {
		defer close(done)
		Local(ctx, o, profiles, http.NotFoundHandler(), func(why string) { reasons <- why })
	}()
	if got := <-reasons; got != "No agent found. Install Claude Code or Codex." {
		t.Fatal(got)
	}
	installed.Store(true)
	for got := range reasons {
		if got == "" {
			break // found it and started
		}
		if got != "No agent found. Install Claude Code or Codex." {
			t.Fatal(got)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Local didn't stop")
	}
}

func TestConfiguredProfilesReplaceTheOnesFoundOnPath(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", bin)
	mine := Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code (OpenRouter)"}, Command: []string{"claude", "-p"},
		Env: map[string]string{"ANTHROPIC_BASE_URL": "https://openrouter.ai/api"}}
	got := Profiles([]Profile{mine}, true)
	ids := []string{}
	for _, p := range got {
		ids = append(ids, p.ID+":"+p.Name)
	}
	if !slices.Equal(ids, []string{"claude:Claude Code (OpenRouter)", "codex:Codex"}) {
		t.Fatal(ids)
	}
	if got = Profiles(nil, false); len(got) != 0 {
		t.Fatal("find=false found agents", got)
	}
}

func TestInProcessTransportGrantsNothingButTheHandler(t *testing.T) {
	var seen *http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	c := http.Client{Transport: handlerTransport{h}}
	req, _ := http.NewRequest(http.MethodPost, "http://retake.invalid/api/worker/poll", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer in-process")
	res, e := c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusTeapot || seen.URL.Path != "/api/worker/poll" || seen.Header.Get("Authorization") != "" {
		t.Fatal(res.StatusCode, seen.URL, seen.Header)
	}
}
