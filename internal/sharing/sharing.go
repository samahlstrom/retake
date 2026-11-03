// Package sharing gives the share links made in the page an address on this computer (spec/team.md, "How a link
// reaches people"): a tunnel that already runs for the server, else a Cloudflare quick tunnel started now, else the
// local network.
package sharing

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/tunnel"
)

// The kinds of reach.
const (
	KindHosted = "hosted"
	KindTunnel = "tunnel"
	KindLAN    = "lan"
)

// The notes a local-network link carries: how to reach it from outside.
const (
	NoteInstall = "Only people on your network can open it. To share it with anyone, install cloudflared."
	NoteOff     = "Only people on your network can open it. To share it with anyone, turn on sharing through a " +
		"tunnel in Settings."
	NoteFailed = "The tunnel didn't start, so only people on your network can open it."
)

// Options configure a Local.
type Options struct {
	Port  int      // the server's port: what a tunnel forwards to, and the local network's port
	Allow []string // --allow-host names: a reverse proxy or tunnel someone runs for this server
	// Hosts returns the hosts tables' rows (the folder's, or the video's): the hostnames of running shares.
	Hosts func() []project.Host
	// AddHost records the hostname of a tunnel this process started (with its own pid); RemoveHost forgets it.
	AddHost    func(host string) error
	RemoveHost func(host string)
	// Cloudflared is the cloudflared binary ([share] cloudflared); "" = cloudflared on $PATH.
	Cloudflared string
	// Off is [share] tunnel = "off": never start a tunnel.
	Off bool
	// LANAddr is this computer's local network address (an IP), "" without one; nil = none. Listen makes the server
	// answer there too, and returns <ip>:<port>; nil = no local-network links.
	LANAddr func() string
	Listen  func() (string, error)
	Log     io.Writer // the tunnel's output; nil = discard
	// Start starts a tunnel (tests); nil = tunnel.Start.
	Start func(ctx context.Context, o tunnel.Options) (*tunnel.Tunnel, error)
	// Alive reports whether a hosts row's process still runs; nil = by its pid.
	Alive func(pid int) bool
}

// Local is a server's sharing on this computer. It implements server.Sharing.
type Local struct {
	o Options

	startMu sync.Mutex // one tunnel start at a time

	mu  sync.Mutex
	tun *tunnel.Tunnel // the tunnel this process started, while it runs
}

// New returns a Local.
func New(o Options) *Local {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Start == nil {
		o.Start = tunnel.Start
	}
	if o.Alive == nil {
		o.Alive = alive
	}
	return &Local{o: o}
}

var _ server.Sharing = (*Local)(nil)

// running is the hostname of a tunnel that runs for this server now: a live share's (this process's own tunnel is
// one), else an --allow-host name. "" when there is none.
func (l *Local) running() string {
	l.mu.Lock()
	if l.tun != nil {
		select {
		case <-l.tun.Done(): // it died: forget it, so the next link starts another
			if l.o.RemoveHost != nil {
				l.o.RemoveHost(l.tun.Host)
			}
			l.tun = nil
		default:
			h := l.tun.Host
			l.mu.Unlock()
			return h
		}
	}
	l.mu.Unlock()
	if l.o.Hosts != nil {
		for _, h := range l.o.Hosts() {
			switch {
			case h.Tunnel == KindLAN: // the local network's, not a tunnel
			case h.PID == os.Getpid(): // this process's own: only a running l.tun counts
			case h.PID > 0 && !l.o.Alive(h.PID): // a share that died
			default:
				return h.Host
			}
		}
	}
	for _, h := range l.o.Allow {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			return h
		}
	}
	return ""
}

// SetOff turns starting a tunnel off or on ([share] tunnel, changed in Settings). A running tunnel keeps running.
func (l *Local) SetOff(off bool) {
	l.mu.Lock()
	l.o.Off = off
	l.mu.Unlock()
}

// canTunnel says whether a tunnel may be started now, and the local network's note when it can't.
func (l *Local) canTunnel() (bool, string) {
	l.mu.Lock()
	off := l.o.Off
	l.mu.Unlock()
	if off {
		return false, NoteOff
	}
	bin := l.o.Cloudflared
	if bin == "" {
		bin = "cloudflared"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return false, NoteInstall
	}
	return true, ""
}

// Reach says what a new link would use, without starting anything.
func (l *Local) Reach(*http.Request) server.Reach {
	if h := l.running(); h != "" {
		return server.Reach{Kind: KindTunnel, Host: h, Base: "https://" + h + "/"}
	}
	if ok, note := l.canTunnel(); !ok {
		return l.lanReach(note)
	}
	return server.Reach{Kind: KindTunnel}
}

func (l *Local) lanReach(note string) server.Reach {
	ip := ""
	if l.o.LANAddr != nil {
		ip = l.o.LANAddr()
	}
	if ip == "" {
		return server.Reach{Kind: KindLAN, Note: note}
	}
	hp := net.JoinHostPort(ip, strconv.Itoa(l.o.Port))
	return server.Reach{Kind: KindLAN, Host: hp, Note: note, Base: "http://" + hp + "/"}
}

// Ready returns the address a link uses now, starting a tunnel when that's how it reaches people.
func (l *Local) Ready(ctx context.Context, r *http.Request) (server.Reach, error) {
	if h := l.running(); h != "" {
		return server.Reach{Kind: KindTunnel, Host: h, Base: "https://" + h + "/"}, nil
	}
	ok, note := l.canTunnel()
	if ok {
		h, err := l.startTunnel(ctx)
		if err == nil {
			return server.Reach{Kind: KindTunnel, Host: h, Base: "https://" + h + "/"}, nil
		}
		if ctx.Err() != nil {
			return server.Reach{}, err
		}
		note = NoteFailed
	}
	return l.listenLAN(note)
}

// startTunnel starts this process's Cloudflare quick tunnel, once at a time, and records its hostname.
func (l *Local) startTunnel(ctx context.Context) (string, error) {
	l.startMu.Lock()
	defer l.startMu.Unlock()
	if h := l.running(); h != "" { // another link started one meanwhile
		return h, nil
	}
	t, err := l.o.Start(ctx, tunnel.Options{Kind: tunnel.Cloudflare, Bin: l.o.Cloudflared, Port: l.o.Port, Log: l.o.Log})
	if err != nil {
		return "", err
	}
	if l.o.AddHost != nil {
		if err := l.o.AddHost(t.Host); err != nil {
			_ = t.Stop()
			return "", err
		}
	}
	l.mu.Lock()
	l.tun = t
	l.mu.Unlock()
	return t.Host, nil
}

func (l *Local) listenLAN(note string) (server.Reach, error) {
	if l.o.Listen == nil || l.o.LANAddr == nil || l.o.LANAddr() == "" {
		return server.Reach{}, errors.New("this computer has no address other people can reach: install cloudflared " +
			"to share through a tunnel")
	}
	hp, err := l.o.Listen()
	if err != nil {
		return server.Reach{}, err
	}
	return server.Reach{Kind: KindLAN, Host: hp, Note: note, Base: "http://" + hp + "/"}, nil
}

// Close stops the tunnel this process started, and forgets its hostname.
func (l *Local) Close() error {
	l.mu.Lock()
	t := l.tun
	l.tun = nil
	l.mu.Unlock()
	if t == nil {
		return nil
	}
	if l.o.RemoveHost != nil {
		l.o.RemoveHost(t.Host)
	}
	return t.Stop()
}

// Tunnel is the hostname of the tunnel this process started, "" without one.
func (l *Local) Tunnel() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tun == nil {
		return ""
	}
	return l.tun.Host
}

// LANAddr is this computer's address on its local network: the first private IPv4 address of an interface that is
// up and isn't a loopback. "" without one.
func LANAddr() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil && ip4.IsPrivate() {
				return ip4.String()
			}
		}
	}
	return ""
}
