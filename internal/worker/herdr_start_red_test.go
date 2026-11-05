package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func managedHerdr(t *testing.T, ready bool) string {
	t.Helper()
	dir := fakeViews(t)
	t.Setenv("FAKE_HERDR_MANAGE_SERVER", "1")
	if ready {
		if err := os.WriteFile(filepath.Join(dir, "herdr", "server-ready"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHerdrStartsServerAndPollsStatusBeforeCreatingWorkspace(t *testing.T) {
	dir := managedHerdr(t, false)
	r, _ := viewRunner(t, "startup", claudeInHerdr)
	got := r.execute(context.Background(), task("start"))
	if got.state != "question" {
		t.Fatalf("startup result: %+v", got)
	}
	all := calls(t, filepath.Join(dir, "herdr", "argv.log"))
	if len(all) < 4 {
		t.Fatalf("herdr calls: %#v", all)
	}
	sameCalls(t, "startup before workspace", all[:4], [][]string{
		{"status", "server", "--json"},
		{"server"},
		{"status", "server", "--json"},
		{"workspace", "create", "--cwd", r.opts.Root, "--label", "retake · startup", "--no-focus", "--env", "SHOT_COLOUR=blue"},
	})
}

func TestReadyHerdrDoesNotStartAgain(t *testing.T) {
	dir := managedHerdr(t, true)
	r, _ := viewRunner(t, "ready", claudeInHerdr)
	if got := r.execute(context.Background(), task("ready")); got.state != "question" {
		t.Fatalf("ready result: %+v", got)
	}
	all := calls(t, filepath.Join(dir, "herdr", "argv.log"))
	for _, call := range all {
		if strings.Join(call, " ") == "server" {
			t.Fatalf("ready server was started: %#v", all)
		}
	}
	if len(all) < 2 {
		t.Fatalf("ready server calls: %#v", all)
	}
	sameCalls(t, "ready server before workspace", all[:2], [][]string{{"status", "server", "--json"}, {"workspace", "create", "--cwd", r.opts.Root, "--label", "retake · ready", "--no-focus", "--env", "SHOT_COLOUR=blue"}})
}

func TestConcurrentHerdrStartsShareOneHeadlessStart(t *testing.T) {
	dir := managedHerdr(t, false)
	first, _ := viewRunner(t, "first", claudeInHerdr)
	second, _ := viewRunner(t, "second", claudeInHerdr)
	errs := make(chan string, 2)
	var group sync.WaitGroup
	for _, run := range []*runner{first, second} {
		group.Add(1)
		go func(run *runner) {
			defer group.Done()
			if result := run.execute(context.Background(), task(filepath.Base(run.opts.Root))); result.state != "question" {
				errs <- result.message
			}
		}(run)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent startup failed: %s", err)
	}
	starts := 0
	for _, call := range calls(t, filepath.Join(dir, "herdr", "argv.log")) {
		if strings.Join(call, " ") == "server" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("headless starts = %d, want 1", starts)
	}
}

func TestHerdrStartupFailureIsClearAndMakesNoWorkspace(t *testing.T) {
	dir := managedHerdr(t, false)
	t.Setenv("FAKE_HERDR_START_FAIL", "1")
	r, _ := viewRunner(t, "failure", claudeInHerdr)
	got := r.execute(context.Background(), task("failure"))
	if got.state != "paused" || !strings.Contains(got.message, "Couldn't start Herdr. Retry.") {
		t.Fatalf("startup failure: %+v", got)
	}
	for _, call := range calls(t, filepath.Join(dir, "herdr", "argv.log")) {
		if len(call) >= 2 && call[0] == "workspace" && call[1] == "create" {
			t.Fatalf("workspace created after startup failure: %#v", call)
		}
	}
}

// A newer server belongs to the user. An old bundled client must not try to replace it or create a workspace.
func TestIncompatibleHerdrDoesNotStartOrUseWorkspace(t *testing.T) {
	dir := managedHerdr(t, true)
	t.Setenv("FAKE_HERDR_INCOMPATIBLE", "1")
	r, _ := viewRunner(t, "incompatible", claudeInHerdr)
	got := r.execute(context.Background(), task("incompatible"))
	if got.state != "paused" || !strings.Contains(got.message, "Herdr") {
		t.Fatalf("incompatible server should explain why it cannot be used: %+v", got)
	}
	all := calls(t, filepath.Join(dir, "herdr", "argv.log"))
	sameCalls(t, "only inspect the incompatible server", all, [][]string{{"status", "server", "--json"}})
}
