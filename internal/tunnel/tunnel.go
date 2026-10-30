// Package tunnel starts the HTTPS tunnel of `retake share` (spec/team.md): a Cloudflare quick tunnel through
// cloudflared, or Tailscale's serve or funnel, and reads the public hostname it prints.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Kinds of tunnel.
const (
	Cloudflare = "cloudflare"
	Tailscale  = "tailscale"
	None       = "none"
)

// DefaultStart is how long Start waits for the tunnel to print its hostname.
const DefaultStart = 60 * time.Second

// Options configure a tunnel.
type Options struct {
	Kind   string // Cloudflare or Tailscale
	Bin    string // the binary; "" = cloudflared or tailscale on $PATH
	Port   int    // the local port it forwards to (127.0.0.1)
	Funnel bool   // Tailscale: funnel (the internet) instead of serve (the tailnet)
	Log    io.Writer
	// Start is how long to wait for the hostname; 0 = DefaultStart.
	Start time.Duration
}

// Tunnel is a running tunnel process.
type Tunnel struct {
	Host string // the public hostname, lower case
	cmd  *exec.Cmd
	done chan struct{}
	err  error // why the process ended (nil after a clean exit or Stop); read after done

	mu    sync.Mutex
	tail  []string // the last lines it printed, for error messages
	stops bool
}

var (
	cloudflareRE = regexp.MustCompile(`https://([a-z0-9-]+\.trycloudflare\.com)\b`)
	tailscaleRE  = regexp.MustCompile(`https://([a-z0-9-]+(?:\.[a-z0-9-]+)*\.ts\.net)\b`)
)

// Command returns the command line of a tunnel to 127.0.0.1:<port>.
func Command(o Options) (bin string, args []string, err error) {
	target := fmt.Sprintf("http://127.0.0.1:%d", o.Port)
	switch o.Kind {
	case Cloudflare:
		bin = "cloudflared"
		args = []string{"tunnel", "--no-autoupdate", "--url", target}
	case Tailscale:
		bin, args = "tailscale", []string{"serve", target}
		if o.Funnel {
			args[0] = "funnel"
		}
	default:
		return "", nil, fmt.Errorf("no tunnel kind %q (cloudflare or tailscale)", o.Kind)
	}
	if o.Bin != "" {
		bin = o.Bin
	}
	return bin, args, nil
}

// hostIn finds the public hostname in a line of the tunnel's output.
func hostIn(kind, line string) string {
	re := cloudflareRE
	if kind == Tailscale {
		re = tailscaleRE
	}
	for _, m := range re.FindAllStringSubmatch(strings.ToLower(line), -1) {
		if m[1] != "api.trycloudflare.com" { // cloudflared names its API in error messages
			return m[1]
		}
	}
	return ""
}

// Start starts the tunnel and waits until it prints its public hostname, or ctx ends. The process keeps running
// until Stop.
func Start(ctx context.Context, o Options) (*Tunnel, error) {
	bin, args, err := Command(o)
	if err != nil {
		return nil, err
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%s not found: install it, or point --tunnel-bin (or [share] %s in config.toml) at it",
			bin, map[string]string{Cloudflare: "cloudflared", Tailscale: "tailscale"}[o.Kind])
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Start <= 0 {
		o.Start = DefaultStart
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...) //nolint:gosec // the configured tunnel binary
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	_ = pw.Close() // the child has its own copy
	t := &Tunnel{cmd: cmd, done: make(chan struct{})}
	found := make(chan string, 1)
	drained := make(chan struct{}) // closed once everything it printed has been read
	go func() {                    // read everything it prints: the hostname once, then just drain it into the log
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			_, _ = fmt.Fprintln(o.Log, line)
			t.remember(line)
			if !sent {
				if h := hostIn(o.Kind, line); h != "" {
					found <- h
					sent = true
				}
			}
		}
		_ = pr.Close()
		close(drained)
	}()
	go func() {
		err := cmd.Wait()
		// Its last lines (often the reason it stopped) may still be in the pipe: read them before reporting. A
		// grandchild that kept the pipe open can't hold this up for long.
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
		}
		t.mu.Lock()
		if !t.stops {
			t.err = err
			if err == nil {
				t.err = errors.New("it exited")
			}
		}
		t.mu.Unlock()
		close(t.done)
	}()
	timer := time.NewTimer(o.Start)
	defer timer.Stop()
	select {
	case h := <-found:
		t.Host = h
		return t, nil
	case <-t.done:
		return nil, fmt.Errorf("%s stopped before it printed a public URL (%w)%s", bin, t.err, t.lastLines())
	case <-timer.C:
		_ = t.Stop()
		return nil, fmt.Errorf("%s printed no public URL within %s%s", bin, o.Start, t.lastLines())
	case <-ctx.Done():
		_ = t.Stop()
		return nil, ctx.Err()
	}
}

func (t *Tunnel) remember(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tail = append(t.tail, line)
	if len(t.tail) > 8 {
		t.tail = t.tail[len(t.tail)-8:]
	}
}

func (t *Tunnel) lastLines() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tail) == 0 {
		return ""
	}
	return ":\n  " + strings.Join(t.tail, "\n  ")
}

// Done is closed when the tunnel process has ended.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Err says why the tunnel ended on its own (with its last output); nil before it ends or after Stop.
func (t *Tunnel) Err() error {
	select {
	case <-t.done:
	default:
		return nil
	}
	t.mu.Lock()
	err := t.err
	t.mu.Unlock()
	if err == nil {
		return nil
	}
	return fmt.Errorf("the tunnel stopped (%w)%s", err, t.lastLines())
}

// Stop asks the tunnel to exit (an interrupt, as Ctrl+C would), kills it after 5 s, and waits for it.
func (t *Tunnel) Stop() error {
	t.mu.Lock()
	t.stops = true
	t.mu.Unlock()
	select {
	case <-t.done:
		return nil
	default:
	}
	_ = interrupt(t.cmd.Process)
	select {
	case <-t.done:
	case <-time.After(5 * time.Second):
		_ = t.cmd.Process.Kill()
		<-t.done
	}
	return nil
}
