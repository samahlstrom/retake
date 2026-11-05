package worker

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/testmedia"
)

// A startup that never reaches readiness belongs to Retake, not to an existing user's server.
// It must not survive a failed request and race the next request to bind the same socket.
func TestUnreadyHerdrIsReapedAfterStartupEnds(t *testing.T) {
	testmedia.NeedShell(t)
	for _, reason := range []string{"canceled", "timed out"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "pid")
			t.Setenv("TEST_HERDR_PID", pidPath)
			bin := filepath.Join(dir, "herdr")
			script := "#!/bin/sh\ncase \"$1\" in\nstatus) echo '{\"status\":\"not_running\",\"running\":false}' ;;\nserver) echo $$ > \"$TEST_HERDR_PID\"; exec sleep 60 ;;\nesac\n"
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if reason == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				// Cancel only after the child exists; a busy machine must not cancel the initial status probe.
				go func() {
					tick := time.NewTicker(10 * time.Millisecond)
					defer tick.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
							if _, err := os.Stat(pidPath); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			v := view{bin: bin}
			if err := v.ensureHerdr(ctx); err == nil {
				t.Fatal("unready server unexpectedly succeeded")
			}
			b, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			p, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Kill(); _ = p.Release() })
			if err := p.Signal(syscall.Signal(0)); err == nil {
				t.Fatal("Retake left its unready Herdr process alive after startup ended")
			}
		})
	}
}

func TestReadyHerdrStartedByRetakeSurvivesStartup(t *testing.T) {
	testmedia.NeedShell(t)
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	t.Setenv("TEST_HERDR_PID", pidPath)
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\ncase \"$1\" in\nstatus) if [ -f \"$TEST_HERDR_PID\" ]; then echo '{\"status\":\"running\",\"running\":true,\"compatible\":true}'; else echo '{\"status\":\"not_running\",\"running\":false}'; fi ;;\nserver) echo $$ > \"$TEST_HERDR_PID\"; exec sleep 60 ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	v := view{bin: bin}
	if err := v.ensureHerdr(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Release() })
	if err := p.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("ready server was stopped after successful startup: %v", err)
	}
}
