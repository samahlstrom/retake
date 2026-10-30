package team

import (
	"sync"
	"time"
)

// Limiter defaults (spec/team.md, "Rate limiting").
const (
	DefaultMaxFails   = 10
	DefaultFailWindow = time.Minute
	DefaultMaxClients = 4096
)

// Limiter counts failed token attempts per client. A client with Max failures inside Window is blocked until its
// oldest one leaves the window. The zero value is not usable; use NewLimiter.
type Limiter struct {
	Max     int
	Window  time.Duration
	Clients int // most clients tracked; the stalest is dropped first

	mu    sync.Mutex
	fails map[string][]time.Time // per client, oldest first, at most Max
}

// NewLimiter returns a limiter with the defaults.
func NewLimiter() *Limiter {
	return &Limiter{Max: DefaultMaxFails, Window: DefaultFailWindow, Clients: DefaultMaxClients,
		fails: map[string][]time.Time{}}
}

// prune drops a client's failures that left the window at now.
func (l *Limiter) prune(key string, now time.Time) []time.Time {
	f := l.fails[key]
	i := 0
	for i < len(f) && now.Sub(f[i]) >= l.Window {
		i++
	}
	if i == len(f) {
		delete(l.fails, key)
		return nil
	}
	f = f[i:]
	l.fails[key] = f
	return f
}

// Blocked reports whether the client may not try a token now, and if so for how long.
func (l *Limiter) Blocked(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if len(f) < l.Max {
		return false, 0
	}
	return true, l.Window - now.Sub(f[0])
}

// Fail counts one failed attempt of the client at now.
func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if f == nil && len(l.fails) >= l.Clients {
		l.dropStalest()
	}
	f = append(f, now)
	if len(f) > l.Max {
		f = f[len(f)-l.Max:]
	}
	l.fails[key] = f
}

// dropStalest forgets the client whose last failure is the oldest.
func (l *Limiter) dropStalest() {
	var key string
	var last time.Time
	for k, f := range l.fails {
		if t := f[len(f)-1]; key == "" || t.Before(last) {
			key, last = k, t
		}
	}
	delete(l.fails, key)
}

// Len returns how many clients are tracked (tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.fails)
}
