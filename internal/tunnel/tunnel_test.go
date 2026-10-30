//go:build unix

package tunnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fake writes a shell script that stands in for cloudflared or tailscale: it records its arguments and its pid,
// prints body, and then runs tail (a shell snippet; by default it sleeps until it is stopped).
func fake(t *testing.T, body, tail string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	if tail == "" {
		tail = "exec sleep 30"
	}
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$*" > "` + dir + `/args"` + "\n" +
		`echo $$ > "` + dir + `/pid"` + "\n" +
		body + "\n" + tail + "\n"
	bin = filepath.Join(dir, "fake-tunnel")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func gone(t *testing.T, dir string) bool {
	t.Helper()
	pid, err := strconv.Atoi(read(t, filepath.Join(dir, "pid")))
	if err != nil {
		t.Fatal(err)
	}
	return syscall.Kill(pid, 0) != nil
}

const cloudflaredOutput = `echo "2026-10-30T12:00:00Z INF Requesting new quick Tunnel on trycloudflare.com..." >&2
echo "2026-10-30T12:00:00Z ERR retrying: Post \"https://api.trycloudflare.com/tunnel\": EOF" >&2
echo "2026-10-30T12:00:01Z INF +--------------------------------------------------------------------------------------------+" >&2
echo "2026-10-30T12:00:01Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |" >&2
echo "2026-10-30T12:00:01Z INF |  https://Fake-Words-Here.trycloudflare.com                                                 |" >&2`

func TestCloudflare(t *testing.T) {
	bin, dir := fake(t, cloudflaredOutput, "")
	var log strings.Builder
	tun, err := Start(context.Background(), Options{Kind: Cloudflare, Bin: bin, Port: 8901, Log: &logWriter{b: &log}})
	if err != nil {
		t.Fatal(err)
	}
	if tun.Host != "fake-words-here.trycloudflare.com" {
		t.Errorf("host %q", tun.Host)
	}
	if got := read(t, filepath.Join(dir, "args")); got != "tunnel --no-autoupdate --url http://127.0.0.1:8901" {
		t.Errorf("args %q", got)
	}
	if tun.Err() != nil {
		t.Errorf("running tunnel has an error: %v", tun.Err())
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tun.Done():
	default:
		t.Fatal("Stop returned before the process ended")
	}
	if !gone(t, dir) {
		t.Error("the tunnel process is still running")
	}
	if tun.Err() != nil {
		t.Errorf("a stopped tunnel reports an error: %v", tun.Err())
	}
}

type logWriter struct{ b *strings.Builder }

func (l *logWriter) Write(p []byte) (int, error) { return l.b.Write(p) }

func TestTailscale(t *testing.T) {
	body := `echo "Available on the internet:"
echo ""
echo "https://box.tail1234.ts.net/"
echo "|-- proxy http://127.0.0.1:8901"`
	for _, funnel := range []bool{false, true} {
		bin, dir := fake(t, body, "")
		tun, err := Start(context.Background(), Options{Kind: Tailscale, Bin: bin, Port: 8901, Funnel: funnel})
		if err != nil {
			t.Fatal(err)
		}
		want := "serve http://127.0.0.1:8901"
		if funnel {
			want = "funnel http://127.0.0.1:8901"
		}
		if tun.Host != "box.tail1234.ts.net" || read(t, filepath.Join(dir, "args")) != want {
			t.Errorf("funnel %v: host %q, args %q", funnel, tun.Host, read(t, filepath.Join(dir, "args")))
		}
		_ = tun.Stop()
	}
}

func TestStartFailures(t *testing.T) {
	bin, _ := fake(t, `echo "error: you must log in first" >&2`, "exit 1")
	_, err := Start(context.Background(), Options{Kind: Cloudflare, Bin: bin, Port: 8901})
	if err == nil || !strings.Contains(err.Error(), "stopped before it printed a public URL") ||
		!strings.Contains(err.Error(), "you must log in first") {
		t.Errorf("early exit: %v", err)
	}

	bin, dir := fake(t, `echo "still connecting"`, "")
	_, err = Start(context.Background(), Options{Kind: Cloudflare, Bin: bin, Port: 8901, Start: 300 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "printed no public URL within") ||
		!strings.Contains(err.Error(), "still connecting") {
		t.Errorf("no URL: %v", err)
	}
	if !gone(t, dir) {
		t.Error("the silent tunnel was left running")
	}

	if _, err := Start(context.Background(), Options{Kind: Cloudflare, Bin: filepath.Join(t.TempDir(), "nope"), Port: 1}); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("missing binary: %v", err)
	}
	if _, err := Start(context.Background(), Options{Kind: "ngrok", Port: 1}); err == nil {
		t.Error("unknown kind")
	}

	// Ctrl+C while it connects stops it.
	bin, dir = fake(t, `echo "still connecting"`, "")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Start(ctx, Options{Kind: Cloudflare, Bin: bin, Port: 8901}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled start: %v", err)
	}
	if !gone(t, dir) {
		t.Error("the cancelled tunnel was left running")
	}
}

// A tunnel that dies on its own says so through Done and Err.
func TestTunnelDies(t *testing.T) {
	bin, _ := fake(t, `echo "https://quick-one.trycloudflare.com"`, "sleep 0.2; echo 'connection lost' >&2; exit 3")
	tun, err := Start(context.Background(), Options{Kind: Cloudflare, Bin: bin, Port: 8901})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tun.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	if err := tun.Err(); err == nil || !strings.Contains(err.Error(), "exit status 3") ||
		!strings.Contains(err.Error(), "connection lost") {
		t.Errorf("Err = %v", err)
	}
}

func TestHostIn(t *testing.T) {
	for _, c := range []struct{ kind, line, want string }{
		{Cloudflare, "|  https://a-b-c.trycloudflare.com  |", "a-b-c.trycloudflare.com"},
		{Cloudflare, `Post "https://api.trycloudflare.com/tunnel": EOF`, ""},
		{Cloudflare, "https://evil.com/x.trycloudflare.com", ""},
		{Tailscale, "https://box.tail1.ts.net/", "box.tail1.ts.net"},
		{Tailscale, "https://a-b-c.trycloudflare.com", ""},
	} {
		if got := hostIn(c.kind, c.line); got != c.want {
			t.Errorf("hostIn(%s, %q) = %q, want %q", c.kind, c.line, got, c.want)
		}
	}
}
