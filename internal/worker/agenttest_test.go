package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeAgent writes an executable script named name and returns its path.
func fakeAgent(t *testing.T, name, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTryAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	ctx := context.Background()
	// Claude Code: its flags, then one JSON result line.
	claude := fakeAgent(t, "claude", `case "$*" in *"-p --model haiku --tools  --no-session-persistence --output-format json Reply with the single word: ready"*) ;; *) echo "bad args: $*" >&2; exit 2;; esac
[ -n "$RETAKE_AGENT_TEST" ] || exit 3
echo '{"type":"result","is_error":false,"result":"ready\n"}'
`)
	r := TryAgent(ctx, Profile{Command: []string{claude, "-p"}})
	if !r.OK || r.Reply != "ready" || r.Error != "" {
		t.Errorf("claude: %+v", r)
	}
	// Not signed in.
	claude = fakeAgent(t, "claude", `echo 'Invalid API key · Please run /login'; exit 1`)
	if r := TryAgent(ctx, Profile{Command: []string{claude}}); r.OK || !strings.Contains(r.Error, "Not signed in: run claude") {
		t.Errorf("claude, signed out: %+v", r)
	}
	claude = fakeAgent(t, "claude", `echo '{"type":"result","is_error":true,"result":"Credit balance is too low"}'; exit 1`)
	if r := TryAgent(ctx, Profile{Command: []string{claude}}); r.OK || !strings.Contains(r.Error, "Credit balance is too low") {
		t.Errorf("claude, an error result: %+v", r)
	}

	// Codex writes its last message to the -o file.
	codex := fakeAgent(t, "codex", `while [ $# -gt 1 ]; do [ "$1" = -o ] && out=$2; shift; done
[ "$1" = "Reply with the single word: ready" ] || exit 2
printf 'ready\n' > "$out"; echo 'some progress'`)
	if r := TryAgent(ctx, Profile{Command: []string{codex, "exec"}}); !r.OK || r.Reply != "ready" {
		t.Errorf("codex: %+v", r)
	}
	codex = fakeAgent(t, "codex", `echo 'Error: Not logged in. Run codex login.' >&2; exit 1`)
	if r := TryAgent(ctx, Profile{Command: []string{codex}}); r.OK || !strings.Contains(r.Error, "Not signed in: run codex") {
		t.Errorf("codex, signed out: %+v", r)
	}

	// Any other agent: the prompt on stdin, its env, and passing on exit 0.
	mine := fakeAgent(t, "my-agent", `read line; echo "got: $line ($MY_KEY)"; [ -n "$RETAKE_REPORT" ]`)
	t.Setenv("RETAKE_TEST_KEY", "k1")
	r = TryAgent(ctx, Profile{Command: []string{mine, "--flag"}, Env: map[string]string{"MY_KEY": "$RETAKE_TEST_KEY"}})
	if !r.OK || r.Reply != "got: Reply with the single word: ready (k1)" {
		t.Errorf("custom: %+v", r)
	}
	mine = fakeAgent(t, "my-agent", `echo working; echo 'it broke' >&2; exit 4`)
	if r := TryAgent(ctx, Profile{Command: []string{mine}}); r.OK || r.Error != "it broke" {
		t.Errorf("custom failing: %+v", r)
	}

	if r := TryAgent(ctx, Profile{Command: []string{filepath.Join(t.TempDir(), "claude")}}); r.OK ||
		!strings.Contains(r.Error, "Claude Code isn't installed here") {
		t.Errorf("missing: %+v", r)
	}

	old := TryTimeout
	TryTimeout = 300 * time.Millisecond
	t.Cleanup(func() { TryTimeout = old })
	slow := fakeAgent(t, "slow", `exec sleep 30`)
	start := time.Now()
	if r := TryAgent(ctx, Profile{Command: []string{slow}}); r.OK || !strings.Contains(r.Error, "No answer in") ||
		time.Since(start) > 10*time.Second {
		t.Errorf("slow: %+v after %v", r, time.Since(start))
	}
}
