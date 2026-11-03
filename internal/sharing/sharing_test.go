package sharing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/tunnel"
)

// fakeCloudflared writes a stand-in for cloudflared that prints a quick tunnel's address and waits to be stopped. It
// never reaches Cloudflare. dir/pid holds its pid, dir/args its arguments.
func fakeCloudflared(t *testing.T, host string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$*" > "` + dir + `/args"` + "\n" +
		`echo $$ > "` + dir + `/pid"` + "\n" +
		`echo "INF |  https://` + host + `  |" >&2` + "\n" +
		"exec sleep 30\n"
	bin = filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func gone(t *testing.T, dir string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return !alive(pid)
}

// hostsTable is a hosts table in memory.
type hostsTable struct {
	mu   sync.Mutex
	rows []project.Host
}

func (h *hostsTable) list() []project.Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]project.Host(nil), h.rows...)
}

func (h *hostsTable) add(host string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rows = append(h.rows, project.Host{Host: host, Tunnel: "cloudflare", PID: os.Getpid()})
	return nil
}

func (h *hostsTable) remove(host string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.rows[:0]
	for _, r := range h.rows {
		if r.Host != host {
			out = append(out, r)
		}
	}
	h.rows = out
}

func TestStartsATunnelOnceAndStopsIt(t *testing.T) {
	testmedia.NeedShell(t)
	bin, dir := fakeCloudflared(t, "brave-words.trycloudflare.com")
	hosts := &hostsTable{}
	l := New(Options{Port: 9141, Hosts: hosts.list, AddHost: hosts.add, RemoveHost: hosts.remove, Cloudflared: bin,
		LANAddr: func() string { return "192.168.1.20" }})
	if r := l.Reach(nil); r.Kind != KindTunnel || r.Host != "" || r.Note != "" {
		t.Fatalf("before: %+v", r)
	}
	r, err := l.Ready(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindTunnel || r.Host != "brave-words.trycloudflare.com" ||
		r.Base != "https://brave-words.trycloudflare.com/" {
		t.Fatalf("ready: %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.TrimSpace(string(b)); got != "tunnel --no-autoupdate --url http://127.0.0.1:9141" {
		t.Fatalf("args %q", got)
	}
	if rows := hosts.list(); len(rows) != 1 || rows[0].Host != "brave-words.trycloudflare.com" {
		t.Fatalf("hosts: %+v", rows)
	}
	// A second link uses the same tunnel.
	if r2, err := l.Ready(context.Background(), nil); err != nil || r2.Host != r.Host {
		t.Fatal(r2, err)
	}
	if r := l.Reach(nil); r.Host != "brave-words.trycloudflare.com" {
		t.Fatalf("reach while it runs: %+v", r)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !gone(t, dir) {
		t.Fatal("the tunnel still runs")
	}
	if rows := hosts.list(); len(rows) != 0 {
		t.Fatalf("its host stayed: %+v", rows)
	}
}

func TestUsesARunningShareFirst(t *testing.T) {
	started := false
	hosts := &hostsTable{rows: []project.Host{
		{Host: "dead.trycloudflare.com", Tunnel: "cloudflare", PID: 999999},
		{Host: "192.168.1.20:9141", Tunnel: KindLAN, PID: 1},
		{Host: "live.trycloudflare.com", Tunnel: "cloudflare", PID: 4242},
	}}
	l := New(Options{Port: 9141, Hosts: hosts.list, Alive: func(pid int) bool { return pid == 4242 },
		Allow: []string{"proxy.example.com"}})
	l.o.Start = func(context.Context, tunnel.Options) (*tunnel.Tunnel, error) {
		started = true
		return nil, errors.New("no tunnel in this test")
	}
	r, err := l.Ready(context.Background(), nil)
	if err != nil || r.Kind != KindTunnel || r.Base != "https://live.trycloudflare.com/" || started {
		t.Fatalf("%+v %v", r, err)
	}
	// A share in this same process (retake share <folder> serves the folder itself) counts too.
	l = New(Options{Port: 9141, Hosts: func() []project.Host {
		return []project.Host{{Host: "same.trycloudflare.com", Tunnel: "cloudflare", PID: os.Getpid()}}
	}})
	if r := l.Reach(nil); r.Host != "same.trycloudflare.com" {
		t.Fatalf("a share of this process: %+v", r)
	}
	// No live share: the --allow-host name.
	l = New(Options{Port: 9141, Hosts: func() []project.Host { return nil }, Allow: []string{"Proxy.Example.com"}})
	if r := l.Reach(nil); r.Host != "proxy.example.com" || r.Base != "https://proxy.example.com/" {
		t.Fatalf("allow-host: %+v", r)
	}
}

func TestLocalNetworkWhenNoTunnel(t *testing.T) {
	listened := 0
	listen := func() (string, error) { listened++; return "192.168.1.20:9141", nil }
	lan := func() string { return "192.168.1.20" }

	// cloudflared missing.
	l := New(Options{Port: 9141, Cloudflared: filepath.Join(t.TempDir(), "no-cloudflared"), LANAddr: lan,
		Listen: listen})
	if r := l.Reach(nil); r.Kind != KindLAN || r.Host != "192.168.1.20:9141" || r.Note != NoteInstall {
		t.Fatalf("reach: %+v", r)
	}
	r, err := l.Ready(context.Background(), nil)
	if err != nil || r.Base != "http://192.168.1.20:9141/" || r.Note != NoteInstall || listened != 1 {
		t.Fatalf("ready: %+v %v", r, err)
	}

	// Tunnels turned off, cloudflared or not.
	bin, _ := fakeCloudflared(t, "never.trycloudflare.com")
	l = New(Options{Port: 9141, Cloudflared: bin, Off: true, LANAddr: lan, Listen: listen})
	if r, err := l.Ready(context.Background(), nil); err != nil || r.Kind != KindLAN || r.Note != NoteOff {
		t.Fatalf("off: %+v %v", r, err)
	}

	// No local network either: no address at all.
	l = New(Options{Port: 9141, Off: true, LANAddr: func() string { return "" }, Listen: listen})
	if _, err := l.Ready(context.Background(), nil); err == nil {
		t.Fatal("no address, but a link")
	}
}

func TestTunnelThatFailsFallsBackToTheNetwork(t *testing.T) {
	testmedia.NeedShell(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'ERR failed to reach Cloudflare' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := New(Options{Port: 9141, Cloudflared: bin, LANAddr: func() string { return "10.0.0.5" },
		Listen: func() (string, error) { return "10.0.0.5:9141", nil }})
	r, err := l.Ready(context.Background(), nil)
	if err != nil || r.Kind != KindLAN || r.Note != NoteFailed || r.Base != "http://10.0.0.5:9141/" {
		t.Fatalf("%+v %v", r, err)
	}
}
