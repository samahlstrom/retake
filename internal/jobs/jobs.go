// Package jobs runs a project's queued jobs in the server process (spec/notes.md, "Jobs"): transcribe(take), then
// notes(version). One job runs at a time per kind. A failed attempt is retried up to 3 times with backoff, and the
// jobs a crashed run left running are queued again at startup. Every state change is a "job" event.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// TakeTranscriber transcribes one take: it writes transcript.json in the take folder (the legacy format), skipping
// the audio files already transcribed with the same size and model.
type TakeTranscriber interface {
	Take(ctx context.Context, takeDir string) error
}

// NotesBuilder runs the notes engine for one version (all its takes plus inherited ones). It writes the version's
// notes.md and notes.json and returns the comments for the notes table. Transcripts must already exist.
type NotesBuilder interface {
	Build(ctx context.Context, video family.Version, fam []family.Version) (project.Run, error)
}

// Engines are the implementations the jobs run.
type Engines struct {
	Notes NotesBuilder
	// Transcriber returns the configured transcriber, or an error that says why there is none (for example, none
	// is installed). It is called for every transcribe job, so installing one takes effect without a restart.
	Transcriber func() (TakeTranscriber, error)
}

// fatal marks an error that retrying can't fix.
type fatal struct{ err error }

func (f fatal) Error() string { return f.err.Error() }
func (f fatal) Unwrap() error { return f.err }

// Fatal marks err as one that retrying can't fix: the job fails at once.
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return fatal{err}
}

// IsFatal reports whether err was marked by Fatal.
func IsFatal(err error) bool {
	var f fatal
	return errors.As(err, &f)
}

// Handler runs one job.
type Handler func(ctx context.Context, job project.Job) error

// FamilyFunc returns the current version family.
type FamilyFunc func() []family.Version

// Handlers returns the handlers of the two job kinds. fam is called when a notes job starts, so it sees versions
// pushed since the server started.
func Handlers(st *project.Store, fam FamilyFunc, e Engines) map[string]Handler {
	return map[string]Handler{
		project.JobTranscribe: func(ctx context.Context, job project.Job) error {
			return transcribe(ctx, e, job.Args.Dir)
		},
		project.JobNotes: func(ctx context.Context, job project.Job) error {
			f := fam()
			v, ok := find(f, job.Args.Version)
			if !ok {
				return Fatal(fmt.Errorf("version %s is no longer in the family", job.Args.Version))
			}
			_, err := Notes(ctx, st, v, f, e)
			return err
		},
	}
}

func find(fam []family.Version, label string) (family.Version, bool) {
	for _, v := range fam {
		if v.Label == label {
			return v, true
		}
	}
	return family.Version{}, false
}

func transcribe(ctx context.Context, e Engines, dir string) error {
	if dir == "" {
		return Fatal(errors.New("a transcribe job needs its take folder"))
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Fatal(fmt.Errorf("the take folder %s is gone", dir))
	}
	if e.Transcriber == nil {
		return Fatal(errors.New("no transcriber is set up"))
	}
	t, err := e.Transcriber()
	if err != nil {
		return Fatal(fmt.Errorf("can't transcribe: %w", err))
	}
	return t.Take(ctx, dir)
}

// Notes runs the notes engine for one version and syncs the notes table with what it found.
func Notes(ctx context.Context, st *project.Store, v family.Version, fam []family.Version, e Engines) (project.SyncResult, error) {
	if e.Notes == nil {
		return project.SyncResult{}, Fatal(errors.New("no notes engine is set up"))
	}
	run, err := e.Notes.Build(ctx, v, fam)
	if err != nil {
		return project.SyncResult{}, err
	}
	if run.Version == "" {
		run.Version = v.Label
	}
	return st.Sync(run)
}

// TakeDirs returns the take folders a version's notes read: its own takes, then the ones inherit.json carries
// over.
func TakeDirs(v family.Version) []string {
	var out []string
	for _, s := range store.Sessions(v.Path) {
		out = append(out, store.TakeDir(v.Path, s.ID))
	}
	for _, x := range store.InheritedTakes(v.Path) {
		out = append(out, x.Dir)
	}
	return out
}

// Rebuild is `retake notes build`: it transcribes every take of the version that needs it (when a transcriber is
// set up; otherwise it says so on log and uses the transcripts there are), then runs the notes engine and syncs.
func Rebuild(ctx context.Context, st *project.Store, v family.Version, fam []family.Version, e Engines,
	log io.Writer) (project.SyncResult, error) {
	var t TakeTranscriber
	var terr error
	if e.Transcriber == nil {
		terr = errors.New("no transcriber is set up")
	} else {
		t, terr = e.Transcriber()
	}
	if terr != nil {
		_, _ = fmt.Fprintf(log, "retake: not transcribing (%v); using the transcripts there are\n", terr)
	} else {
		for _, dir := range TakeDirs(v) {
			_, _ = fmt.Fprintf(log, "retake: transcribing %s\n", filepath.Base(dir))
			if err := t.Take(ctx, dir); err != nil {
				return project.SyncResult{}, fmt.Errorf("transcribing %s: %w", dir, err)
			}
		}
	}
	return Notes(ctx, st, v, fam, e)
}

// Defaults of a Runner.
const (
	DefaultMaxAttempts = 3
	DefaultPoll        = time.Second
	DefaultLockRetry   = 2 * time.Second
)

// DefaultBackoff waits 2 s after a first failed attempt and 8 s after a second.
func DefaultBackoff(attempts int) time.Duration {
	d := 2 * time.Second
	for i := 1; i < attempts; i++ {
		d *= 4
	}
	return d
}

// Runner runs the jobs of one project store.
type Runner struct {
	Store       *project.Store
	Handlers    map[string]Handler
	MaxAttempts int                              // 0 = DefaultMaxAttempts
	Backoff     func(attempts int) time.Duration // nil = DefaultBackoff
	Poll        time.Duration                    // how often to look for jobs queued by others; 0 = DefaultPoll
	LockRetry   time.Duration                    // how often to retry the project's job lock; 0 = DefaultLockRetry
	Log         io.Writer                        // job failures; nil = discard
}

func (r *Runner) defaults() {
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = DefaultMaxAttempts
	}
	if r.Backoff == nil {
		r.Backoff = DefaultBackoff
	}
	if r.Poll <= 0 {
		r.Poll = DefaultPoll
	}
	if r.LockRetry <= 0 {
		r.LockRetry = DefaultLockRetry
	}
	if r.Log == nil {
		r.Log = io.Discard
	}
}

// Run runs jobs until ctx is done. It first takes the project's job lock, waiting while another process holds it,
// so one process runs a project's jobs; then it queues again the jobs a crashed run left running. A job running
// when ctx ends is interrupted and queued again, and that attempt doesn't count.
func (r *Runner) Run(ctx context.Context) error {
	r.defaults()
	var unlock func()
	for {
		u, ok, err := project.LockJobs(r.Store.Root())
		if err != nil {
			return fmt.Errorf("job lock: %w", err)
		}
		if ok {
			unlock = u
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.LockRetry):
		}
	}
	defer unlock()
	requeued, err := r.Store.RequeueRunning(r.MaxAttempts)
	if err != nil {
		return fmt.Errorf("requeueing interrupted jobs: %w", err)
	}
	for _, j := range requeued {
		_, _ = fmt.Fprintf(r.Log, "retake: job %d (%s) was interrupted; %s\n", j.ID, j.Kind, j.State)
	}
	var wg sync.WaitGroup
	for kind, h := range r.Handlers {
		wg.Go(func() { r.loop(ctx, kind, h) })
	}
	<-ctx.Done() // hold the lock until the end, even with no handlers
	wg.Wait()
	return nil
}

func (r *Runner) loop(ctx context.Context, kind string, h Handler) {
	tick := time.NewTicker(r.Poll)
	defer tick.Stop()
	for ctx.Err() == nil {
		changed := r.Store.Changed()
		job, ok, err := r.Store.Claim(kind, r.Backoff)
		if err != nil {
			_, _ = fmt.Fprintf(r.Log, "retake: claiming a %s job: %v\n", kind, err)
		}
		if ok {
			r.run(ctx, job, h)
			continue
		}
		select {
		case <-ctx.Done():
		case <-changed:
		case <-tick.C:
		}
	}
}

func (r *Runner) run(ctx context.Context, job project.Job, h Handler) {
	err := call(ctx, h, job)
	outcome, msg := project.Succeeded, ""
	switch {
	case err == nil:
	case ctx.Err() != nil:
		outcome, msg = project.Interrupted, "interrupted: retake stopped while it ran"
	case IsFatal(err):
		outcome, msg = project.Fatal, err.Error()
	default:
		outcome, msg = project.Failed, err.Error()
	}
	j, ferr := r.Store.Finish(job.ID, outcome, msg, r.MaxAttempts)
	if ferr != nil {
		_, _ = fmt.Fprintf(r.Log, "retake: recording job %d: %v\n", job.ID, ferr)
		return
	}
	if err != nil && j.State == project.JobFailed {
		_, _ = fmt.Fprintf(r.Log, "retake: %s job %d failed: %s\n", j.Kind, j.ID, j.Error)
	}
}

// panicSite picks the panicking function out of a stack trace, to keep the job's error to one line.
func panicSite(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "panic(") && i+2 < len(lines) {
			return strings.TrimSpace(lines[i+2])
		}
	}
	return "unknown function"
}

// call runs a handler, turning a panic into an error so one bad job can't stop the server.
func call(ctx context.Context, h Handler, job project.Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v (%s)", p, panicSite(debug.Stack()))
		}
	}()
	return h(ctx, job)
}
