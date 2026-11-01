package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

const take = "2026-10-29_20-48-01"

type world struct {
	t     *testing.T
	st    *project.Store
	video family.Version
	dir   string // the take folder
	mu    sync.Mutex
	log   []string // what ran, in order
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	st.Poll = 5 * time.Millisecond
	t.Cleanup(func() { _ = st.Close() })
	video := filepath.Join(root, "v7", "clip_v7.mp4")
	dir := filepath.Join(root, "v7", "clip_v7.review", take)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &world{t: t, st: st, video: family.Version{Label: "v7", N: 7, Path: video, Name: "clip_v7.mp4"}, dir: dir}
}

func (w *world) note(s string) {
	w.mu.Lock()
	w.log = append(w.log, s)
	w.mu.Unlock()
}

func (w *world) ran() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.log)
}

type fakeTranscriber struct {
	w     *world
	fails int32 // fail this many times first
	calls atomic.Int32
	block chan struct{} // if set, wait for it (or ctx)
}

func (f *fakeTranscriber) Take(ctx context.Context, dir string) error {
	n := f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if n <= f.fails {
		f.w.note("transcribe failed")
		return fmt.Errorf("whisper crashed (attempt %d)", n)
	}
	f.w.note("transcribe " + filepath.Base(dir))
	return nil
}

type fakeNotes struct{ w *world }

func (f fakeNotes) Build(_ context.Context, v family.Version, _ []family.Version) (project.Run, error) {
	f.w.note("notes " + v.Label)
	return project.Run{Comments: []project.Comment{{Take: take, Seg: "S1", N: 0, T0: 1, T1: 2, Text: "bigger"}}}, nil
}

func (w *world) runner(e Engines) *Runner {
	fam := func() []family.Version { return []family.Version{w.video} }
	return &Runner{Store: w.st, Handlers: Handlers(w.st, fam, e), Backoff: func(int) time.Duration { return time.Millisecond },
		Poll: 5 * time.Millisecond, LockRetry: 5 * time.Millisecond}
}

func start(t *testing.T, r *Runner) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run didn't stop")
		}
	}
}

func waitState(t *testing.T, st *project.Store, id int64, state string) project.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := st.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == state {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d is %s (%s), want %s", id, j.State, j.Error, state)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestTranscribeThenNotes(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w}
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}))
	defer stop()
	jobs, err := w.st.QueueTake("v7", take, w.dir)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, w.st, jobs[1].ID, project.JobDone)
	if got := w.ran(); !slices.Equal(got, []string{"transcribe " + take, "notes v7"}) {
		t.Fatalf("ran %v", got)
	}
	notes, _ := w.st.Notes(project.NoteFilter{})
	if len(notes) != 1 || notes[0].Version != "v7" || notes[0].Text != "bigger" || notes[0].Status != project.StatusOpen {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestRetriesWithBackoffThenSucceeds(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w, fails: 2}
	r := w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }})
	var waits []int
	var mu sync.Mutex
	r.Backoff = func(n int) time.Duration {
		mu.Lock()
		waits = append(waits, n)
		mu.Unlock()
		return 20 * time.Millisecond
	}
	stop := start(t, r)
	defer stop()
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	j := waitState(t, w.st, jobs[0].ID, project.JobDone)
	if j.Attempts != 3 || tr.calls.Load() != 3 {
		t.Fatalf("attempts = %d, calls = %d", j.Attempts, tr.calls.Load())
	}
	waitState(t, w.st, jobs[1].ID, project.JobDone)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(waits, 1) || !slices.Contains(waits, 2) {
		t.Errorf("backoff asked for %v", waits)
	}
}

func TestFailsAfterThreeAttempts(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w, fails: 100}
	var log strings.Builder
	var logMu sync.Mutex
	r := w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }})
	r.Log = writerFunc(func(p []byte) (int, error) {
		logMu.Lock()
		defer logMu.Unlock()
		return log.Write(p)
	})
	stop := start(t, r)
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	j := waitState(t, w.st, jobs[0].ID, project.JobFailed)
	nj := waitState(t, w.st, jobs[1].ID, project.JobFailed)
	stop()
	if j.Attempts != 3 || tr.calls.Load() != 3 || j.Error != "whisper crashed (attempt 3)" {
		t.Fatalf("job = %+v, calls %d", j, tr.calls.Load())
	}
	if !strings.Contains(nj.Error, "transcribing take "+take+" failed: whisper crashed") || nj.Attempts != 0 {
		t.Fatalf("notes job = %+v", nj)
	}
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(log.String(), "transcribe job 1 failed: whisper crashed (attempt 3)") {
		t.Errorf("log = %q", log.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// With no transcriber installed, done still queues the jobs; the transcription fails at once with a clear error
// (no pointless retries), and so does the notes run that waits for it.
func TestNoTranscriberFailsClearly(t *testing.T) {
	w := newWorld(t)
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) {
		return nil, errors.New("whisper.cpp isn't installed")
	}}))
	defer stop()
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	j := waitState(t, w.st, jobs[0].ID, project.JobFailed)
	if j.Attempts != 1 || j.Error != "can't transcribe: whisper.cpp isn't installed" || j.Hint != TranscribeHint {
		t.Fatalf("job = %+v", j)
	}
	nj := waitState(t, w.st, jobs[1].ID, project.JobFailed)
	if !strings.Contains(nj.Error, "whisper.cpp isn't installed") || nj.Hint != TranscribeHint {
		t.Fatalf("notes job = %+v", nj)
	}
	if got := w.ran(); len(got) != 0 {
		t.Errorf("ran %v", got)
	}
}

// A transcriber's own hint wins over the default one, and a later success clears it.
func TestHintsComeFromTheError(t *testing.T) {
	w := newWorld(t)
	fail := true
	var mu sync.Mutex
	tr := &fakeTranscriber{w: w}
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return nil, Hinted(errors.New("no model"), "run retake setup")
		}
		return tr, nil
	}}))
	defer stop()
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	if j := waitState(t, w.st, jobs[0].ID, project.JobFailed); j.Hint != "run retake setup" {
		t.Fatalf("hint = %q", j.Hint)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	retry, _ := w.st.QueueTake("v7", take, w.dir)
	if j := waitState(t, w.st, retry[0].ID, project.JobDone); j.Hint != "" || j.Error != "" {
		t.Fatalf("done job = %+v", j)
	}
	if HintOf(errors.New("plain")) != "" || HintOf(fmt.Errorf("wrapped: %w", Hinted(errors.New("x"), "h"))) != "h" {
		t.Error("HintOf")
	}
}

func TestPanicFailsTheJobNotTheServer(t *testing.T) {
	w := newWorld(t)
	r := w.runner(Engines{})
	r.Handlers[project.JobNotes] = func(context.Context, project.Job) error { panic("engine bug") }
	stop := start(t, r)
	defer stop()
	j, _ := w.st.QueueNotes("v7")
	j = waitState(t, w.st, j.ID, project.JobFailed)
	if !strings.HasPrefix(j.Error, "panic: engine bug (") || strings.Contains(j.Error, "\n") {
		t.Fatalf("error = %q", j.Error)
	}
}

func TestCrashedJobsAreRequeuedAtStartup(t *testing.T) {
	w := newWorld(t)
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	// A previous server claimed the transcription and died.
	if _, ok, err := w.st.Claim(project.JobTranscribe, nil); !ok || err != nil {
		t.Fatal("claim", err)
	}
	if j, _ := w.st.Job(jobs[0].ID); j.State != project.JobRunning {
		t.Fatalf("state = %s", j.State)
	}
	tr := &fakeTranscriber{w: w}
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}))
	defer stop()
	j := waitState(t, w.st, jobs[0].ID, project.JobDone)
	if j.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (the crashed one counts)", j.Attempts)
	}
	waitState(t, w.st, jobs[1].ID, project.JobDone)
}

func TestShutdownInterruptsAndRequeues(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w, block: make(chan struct{})}
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}))
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	waitState(t, w.st, jobs[0].ID, project.JobRunning)
	stop()
	j, _ := w.st.Job(jobs[0].ID)
	if j.State != project.JobQueued || j.Attempts != 0 {
		t.Fatalf("after shutdown: %+v", j)
	}
}

func TestOneJobAtATimePerKind(t *testing.T) {
	w := newWorld(t)
	var running, most atomic.Int32
	r := w.runner(Engines{})
	r.Handlers[project.JobNotes] = func(context.Context, project.Job) error {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return nil
	}
	var ids []int64
	for i := range 5 {
		j, err := w.st.QueueNotes(fmt.Sprintf("v%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	stop := start(t, r)
	defer stop()
	for _, id := range ids {
		waitState(t, w.st, id, project.JobDone)
	}
	if most.Load() != 1 {
		t.Fatalf("%d notes jobs ran at once", most.Load())
	}
}

func TestOneRunnerPerProject(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w}
	e := Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}
	other, err := project.Open(w.st.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	other.Poll = 5 * time.Millisecond

	// Runner A holds the lock but has no handlers; runner B (another "process") must wait for it.
	a := &Runner{Store: w.st, Handlers: map[string]Handler{}, Poll: 5 * time.Millisecond}
	stopA := start(t, a)
	time.Sleep(20 * time.Millisecond)
	b := w.runner(e)
	b.Store = other
	b.Handlers = Handlers(other, func() []family.Version { return []family.Version{w.video} }, e)
	stopB := start(t, b)
	defer stopB()
	jobs, _ := w.st.QueueTake("v7", take, w.dir)
	time.Sleep(50 * time.Millisecond)
	if j, _ := w.st.Job(jobs[0].ID); j.State != project.JobQueued {
		t.Fatalf("runner B ran a job while A held the lock: %s", j.State)
	}
	stopA()
	waitState(t, w.st, jobs[1].ID, project.JobDone)
}

func TestRebuild(t *testing.T) {
	w := newWorld(t)
	tr := &fakeTranscriber{w: w}
	// The version has one take of its own and one carried over by inherit.json.
	if err := os.WriteFile(filepath.Join(w.dir, "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(filepath.Dir(filepath.Dir(w.dir)), "..", "v6", "clip_v6.review", "2026-10-01_10-00-00")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	inherit := fmt.Sprintf(`{"takes": [{"dir": %q, "version": "v6"}]}`, old)
	if err := os.WriteFile(filepath.Join(filepath.Dir(w.dir), "inherit.json"), []byte(inherit), 0o600); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	res, err := Rebuild(context.Background(), w.st, w.video, nil,
		Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}, &log)
	if err != nil || len(res.Added) != 1 {
		t.Fatalf("Rebuild = %+v %v", res, err)
	}
	if got := w.ran(); !slices.Equal(got, []string{"transcribe " + take, "transcribe 2026-10-01_10-00-00", "notes v7"}) {
		t.Fatalf("ran %v", got)
	}
	// Without a transcriber it says so and builds from the transcripts there are.
	log.Reset()
	w.log = nil
	_, err = Rebuild(context.Background(), w.st, w.video, nil,
		Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return nil, errors.New("not installed") }}, &log)
	if err != nil || !strings.Contains(log.String(), "not transcribing (not installed)") {
		t.Fatalf("Rebuild without transcriber: %v %q", err, log.String())
	}
	if got := w.ran(); !slices.Equal(got, []string{"notes v7"}) {
		t.Fatalf("ran %v", got)
	}
	if _, err := Rebuild(context.Background(), w.st, w.video, nil, Engines{}, &log); err == nil || !IsFatal(err) {
		t.Errorf("Rebuild without a notes engine: %v", err)
	}
}

func TestVersionOfTake(t *testing.T) {
	fam := []family.Version{
		{Label: "v6", Path: filepath.FromSlash("/p/v6/clip_v6.mp4")},
		{Label: "v7", Path: filepath.FromSlash("/p/v7/clip_v7.mp4")},
	}
	for dir, want := range map[string]string{
		"/p/v6/clip_v6.review/" + take:       "v6",
		"/p/v7/clip_v7.review/" + take:       "v7",
		"/p/v7/clip_v7.review/" + take + "/": "v7",
		"/elsewhere/x.review/" + take:        "",
	} {
		if got := VersionOfTake(fam, filepath.FromSlash(dir)); got != want {
			t.Errorf("VersionOfTake(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestDefaultBackoff(t *testing.T) {
	if DefaultBackoff(1) != 2*time.Second || DefaultBackoff(2) != 8*time.Second {
		t.Fatalf("backoff = %v, %v", DefaultBackoff(1), DefaultBackoff(2))
	}
}

type clipTranscriber struct{ *fakeTranscriber }

func (f clipTranscriber) CompletedTake(_ context.Context, dir string) error {
	f.w.note("clip " + filepath.Base(dir))
	return nil
}

func TestClipJobUsesCompletedAudioAndFinalJobUsesWholeTake(t *testing.T) {
	w := newWorld(t)
	tr := clipTranscriber{&fakeTranscriber{w: w}}
	stop := start(t, w.runner(Engines{Notes: fakeNotes{w}, Transcriber: func() (TakeTranscriber, error) { return tr, nil }}))
	defer stop()
	first, err := w.st.QueueClips("v7", take, w.dir, "first-clip")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, w.st, first[1].ID, project.JobDone)
	final, err := w.st.QueueTake("v7", take, w.dir)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, w.st, final[1].ID, project.JobDone)
	if got := w.ran(); !slices.Equal(got, []string{"clip " + take, "notes v7", "transcribe " + take, "notes v7"}) {
		t.Fatalf("wrong pipeline: %v", got)
	}
}
