// Package stream renders streamed versions on demand (spec/bridge.md, "Streaming"): a version renders while someone
// watches it, stops when the last viewer leaves, and resumes from the cache when someone watches it again. The review
// server (one project) and a home that serves many call the same small API: Watch, Unwatch and State.
package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// Defaults.
const (
	DefaultLease    = 25 * time.Second // a viewer counts this long after its last watch
	DefaultProgress = time.Second      // the least time between two progress events
)

// ErrNotStreamed is Watch's error for a version that has no stream record.
var ErrNotStreamed = errors.New("not a streamed version")

// Options configure a Manager.
type Options struct {
	Editor   bridge.Editor // finds and runs retake-edit
	Lease    time.Duration // 0 = DefaultLease
	Progress time.Duration // 0 = DefaultProgress
	Log      io.Writer     // nil = discard
	Now      func() time.Time
}

// Target is one streamed version of one project.
type Target struct {
	Label    string
	Playlist string // the version's playlist, absolute
	Root     string // the project root: the record's timeline is relative to it
	// Emit sends a version event to the project's pages (its store's event log); nil sends none.
	Emit func(project.VersionEvent)
}

// Status is a version's stream state.
type Status = family.StreamState

type job struct {
	viewers map[string]time.Time
	cancel  context.CancelFunc // set while this Manager renders it
	done    chan struct{}      // closed when that render has ended
}

// Manager runs the renders of the versions being watched.
type Manager struct {
	o      Options
	ctx    context.Context
	stop   context.CancelFunc
	mu     sync.Mutex
	jobs   map[string]*job // by playlist
	wg     sync.WaitGroup
	reaped chan struct{}
}

// New starts a Manager; Close stops it and every render it runs.
func New(o Options) *Manager {
	if o.Lease <= 0 {
		o.Lease = DefaultLease
	}
	if o.Progress <= 0 {
		o.Progress = DefaultProgress
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Manager{o: o, jobs: map[string]*job{}, reaped: make(chan struct{})}
	m.ctx, m.stop = context.WithCancel(context.Background())
	go m.reap()
	return m
}

// Close stops every render and waits for them.
func (m *Manager) Close() {
	m.stop()
	<-m.reaped
	m.wg.Wait()
}

// State is a version's state now: complete, rendering (whoever renders it), or pending.
func (m *Manager) State(playlist string) (Status, error) {
	rec := family.ReadStreamRecord(playlist)
	if rec == nil {
		return Status{}, ErrNotStreamed
	}
	return family.State(playlist, rec), nil
}

// Watch says viewer shows the version: it renders while it has a viewer, unless it's complete or another process
// renders it. The returned state is the version's after the call.
func (m *Manager) Watch(t Target, viewer string) (Status, error) {
	st, err := m.State(t.Playlist)
	if err != nil {
		return st, err
	}
	m.mu.Lock()
	j := m.jobs[t.Playlist]
	if j == nil {
		j = &job{viewers: map[string]time.Time{}}
		m.jobs[t.Playlist] = j
	}
	j.viewers[viewer] = m.o.Now()
	start := st.State != family.StateComplete && j.cancel == nil && m.ctx.Err() == nil
	if start {
		var ctx context.Context
		ctx, j.cancel = context.WithCancel(m.ctx)
		j.done = make(chan struct{})
		m.wg.Add(1)
		go m.render(ctx, j, t)
	}
	m.mu.Unlock()
	if start {
		return m.State(t.Playlist)
	}
	return st, nil
}

// Unwatch says viewer stopped showing the version. The render stops when it was the last one.
func (m *Manager) Unwatch(playlist, viewer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[playlist]; j != nil {
		delete(j.viewers, viewer)
		m.settle(playlist, j)
	}
}

// Rendering reports whether this Manager is rendering the version now (for tests and status lines).
func (m *Manager) Rendering(playlist string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[playlist]
	return j != nil && j.cancel != nil
}

// settle stops a render nobody watches, and forgets an idle job. With m.mu held.
func (m *Manager) settle(playlist string, j *job) {
	if len(j.viewers) > 0 {
		return
	}
	if j.cancel != nil {
		j.cancel()
		return // the render forgets the job when it ends
	}
	delete(m.jobs, playlist)
}

// reap drops the viewers whose lease ran out.
func (m *Manager) reap() {
	defer close(m.reaped)
	tick := time.NewTicker(max(m.o.Lease/10, 50*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
		}
		now := m.o.Now()
		m.mu.Lock()
		for pl, j := range m.jobs {
			for v, at := range j.viewers {
				if now.Sub(at) > m.o.Lease {
					delete(j.viewers, v)
				}
			}
			m.settle(pl, j)
		}
		m.mu.Unlock()
	}
}

// render renders one version until it's complete, fails, or ctx ends (nobody watches it any more).
func (m *Manager) render(ctx context.Context, j *job, t Target) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		j.cancel = nil
		close(j.done)
		if len(j.viewers) == 0 {
			delete(m.jobs, t.Playlist)
		}
		m.mu.Unlock()
	}()
	emit := func(why string, errText string) {
		if t.Emit == nil {
			return
		}
		ev := project.VersionEvent{Why: why, Label: t.Label, Path: t.Playlist, Error: errText}
		if st, err := m.State(t.Playlist); err == nil {
			ev.Stream = &st
		}
		t.Emit(ev)
	}
	unlock, err := family.LockRender(t.Playlist)
	if err != nil {
		return // another process renders it: its events reach the pages too
	}
	defer unlock()
	if pl, err := family.ReadPlaylist(t.Playlist); err == nil && pl.Ended {
		return // completed meanwhile (by another process): nothing to render, and a render would start it over
	}
	rec, err := bridge.ReadPushRecord(t.Playlist)
	if err != nil || rec == nil || rec.Stream == nil {
		m.logf("retake: %s: no stream record: %v\n", t.Label, err)
		return
	}
	s := rec.Stream
	s.State, s.Error = family.StateRendering, ""
	s.Renders++
	m.write(t, rec)
	emit(project.WhyRendering, "")

	err = m.run(ctx, t, rec)
	switch {
	case err == nil:
		s.State = family.StateComplete
		pl, _ := family.ReadPlaylist(t.Playlist)
		s.Done, s.Seconds = s.Total, pl.Seconds
		m.write(t, rec)
		emit(project.WhyComplete, "")
	case ctx.Err() != nil:
		s.State = family.StatePending
		m.write(t, rec)
		emit(project.WhyStopped, "")
	default:
		s.State, s.Error = family.StatePending, err.Error()
		m.write(t, rec)
		m.logf("retake: rendering %s: %v\n", t.Label, err)
		emit(project.WhyFailed, err.Error())
	}
}

// run runs retake-edit for the version, keeping its record and the pages up to date as units publish.
func (m *Manager) run(ctx context.Context, t Target, rec *bridge.PushRecord) error {
	s := rec.Stream
	bin, err := m.o.Editor.Find()
	if err != nil {
		return err
	}
	dir := filepath.Dir(t.Playlist)
	timeline := rec.Timeline
	if !filepath.IsAbs(timeline) {
		timeline = filepath.Join(t.Root, filepath.FromSlash(timeline))
	}
	args := bridge.StreamArgs{Timeline: timeline, Doc: filepath.Join(dir, s.Doc), Playlist: t.Playlist,
		Draft: rec.Draft}
	if s.Export != "" {
		args.Export = filepath.Join(dir, s.Export)
	}
	var mu sync.Mutex // progress comes in on the editor's output goroutine
	var last time.Time
	var trailing *time.Timer
	finished := false
	emitProgress := func() {
		last = m.o.Now()
		if t.Emit != nil {
			st := family.State(t.Playlist, s)
			t.Emit(project.VersionEvent{Why: project.WhyProgress, Label: t.Label, Path: t.Playlist, Stream: &st})
		}
	}
	first := true
	rep, err := m.o.Editor.RenderStream(ctx, bin, args, func(p bridge.Progress) {
		if p.Progress != "publish" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		s.Done, s.Total, s.Seconds = p.Done, p.Total, p.Seconds
		if first {
			el := p.Elapsed
			s.FirstS, first = &el, false
		}
		m.write(t, rec)
		if p.Done == p.Total || trailing == nil && m.o.Now().Sub(last) >= m.o.Progress {
			emitProgress()
		} else if trailing == nil {
			// Cached units arrive in a burst. Publish its final progress even if the next uncached unit takes minutes.
			trailing = time.AfterFunc(m.o.Progress-m.o.Now().Sub(last), func() {
				mu.Lock()
				defer mu.Unlock()
				if finished {
					return
				}
				trailing = nil
				emitProgress()
			})
		}
	})
	mu.Lock()
	defer mu.Unlock()
	finished = true
	if trailing != nil {
		trailing.Stop()
	}
	if err != nil {
		return err
	}
	if rep.Stream != nil {
		d := rep.Stream.DoneS
		s.DoneS = &d
	}
	return nil
}

func (m *Manager) write(t Target, rec *bridge.PushRecord) {
	if err := bridge.WritePushRecord(t.Playlist, *rec); err != nil {
		m.logf("retake: the record of %s: %v\n", t.Label, err)
	}
}

func (m *Manager) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(m.o.Log, format, args...)
}
