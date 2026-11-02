// Package extwork is the work core does for extensions next to their owned files (spec/extensions.md, "Files it
// owns"): transcribing an owned audio file, and answering the relay's ext.* requests on the editing computer. The
// local server and a connected computer's worker share it, so the files are handled the same way in both modes.
package extwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// KindTranscribe is a transcription job's kind.
const KindTranscribe = "transcribe-file"

// The states of a job.
const (
	Queued  = "queued"
	Running = "running"
	Done    = "done"
	Failed  = "failed"
)

// Hint is the fix shown when there is no transcriber (as for a take).
const Hint = "Install whisper.cpp, or set [transcribe] command in config.toml."

// Job is one transcription of an owned audio file: GET api/ext/<id>/jobs/<job>.
type Job struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Path  string `json:"path"`
	Words string `json:"words_path,omitempty"` // the words file it writes: <path without extension>.words.json
	Error string `json:"error,omitempty"`
	Hint  string `json:"hint,omitempty"`
}

// Jobs runs transcriptions one at a time, and keeps the last 200 jobs' states in memory.
type Jobs struct {
	// Transcriber is the configured transcriber, or why there is none; it is asked for every job.
	Transcriber func() (transcribe.Transcriber, error)
	// Changed is told every state change (the server turns it into a "job" event); nil: nobody.
	Changed func(Job)
	// Timeout bounds one transcription; 0 = 30 minutes.
	Timeout time.Duration

	mu    sync.Mutex
	jobs  map[string]*Job
	order []string
	run   sync.Mutex // one transcription at a time
}

// MaxJobs is how many jobs' states are kept.
const MaxJobs = 200

// WordsPath is where a transcription of path writes its words: the same name with .words.json for its extension.
func WordsPath(path string) string {
	if i := strings.LastIndexByte(path, '.'); i > strings.LastIndexByte(path, '/') {
		path = path[:i]
	}
	return path + ".words.json"
}

// Start queues the transcription of an owned audio file of folder, and returns its job.
func (j *Jobs) Start(ctx context.Context, folder ext.Folder, path string) (Job, error) {
	if _, err := folder.Stat(path); err != nil {
		return Job{}, err
	}
	if !folderOwns(folder, WordsPath(path)) {
		return Job{}, ext.ErrNotOwned
	}
	job := &Job{ID: project.NewWorkID()[:16], Kind: KindTranscribe, State: Queued, Path: path, Words: WordsPath(path)}
	j.mu.Lock()
	if j.jobs == nil {
		j.jobs = map[string]*Job{}
	}
	j.jobs[job.ID] = job
	j.order = append(j.order, job.ID)
	for len(j.order) > MaxJobs {
		delete(j.jobs, j.order[0])
		j.order = j.order[1:]
	}
	out := *job
	j.mu.Unlock()
	j.tell(out)
	go j.transcribe(context.WithoutCancel(ctx), folder, job.ID)
	return out, nil
}

func folderOwns(f ext.Folder, path string) bool {
	for _, g := range f.Owns {
		if ext.MatchGlob(g, path) {
			return true
		}
	}
	return false
}

// Get returns a job's state.
func (j *Jobs) Get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *job, true
}

func (j *Jobs) set(id string, fn func(*Job)) {
	j.mu.Lock()
	job, ok := j.jobs[id]
	if ok {
		fn(job)
	}
	var out Job
	if ok {
		out = *job
	}
	j.mu.Unlock()
	if ok {
		j.tell(out)
	}
}

func (j *Jobs) tell(job Job) {
	if j.Changed != nil {
		j.Changed(job)
	}
}

// word is one word of a words file (spec/extensions.md): {w, s, e, p}.
type word struct {
	W string  `json:"w"`
	S float64 `json:"s"`
	E float64 `json:"e"`
	P float64 `json:"p"`
}

func (j *Jobs) transcribe(ctx context.Context, folder ext.Folder, id string) {
	j.run.Lock()
	defer j.run.Unlock()
	j.set(id, func(job *Job) { job.State = Running })
	job, _ := j.Get(id)
	fail := func(err error, hint string) {
		j.set(id, func(job *Job) { job.State, job.Error, job.Hint = Failed, err.Error(), hint })
	}
	if j.Transcriber == nil {
		fail(errors.New("no transcriber is set up"), Hint)
		return
	}
	tr, err := j.Transcriber()
	if err != nil {
		fail(err, Hint)
		return
	}
	// Stat checks the file is an owned regular file inside the folder (no symlink out of it).
	if _, err := folder.Stat(job.Path); err != nil {
		fail(err, "")
		return
	}
	abs := filepath.Join(folder.Root, filepath.FromSlash(job.Path))
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	words, err := tr.Transcribe(tctx, abs, transcribe.Options{})
	if err != nil {
		fail(fmt.Errorf("transcribing %s: %w", job.Path, err), "")
		return
	}
	out := struct {
		Model string `json:"model"`
		Words []word `json:"words"`
	}{Model: tr.Name(), Words: make([]word, 0, len(words))}
	for _, w := range words {
		out.Words = append(out.Words, word{W: w.Text, S: w.Start, E: w.End, P: w.Prob})
	}
	b, err := json.Marshal(out)
	if err != nil {
		fail(err, "")
		return
	}
	if _, err := folder.Write(job.Words, strings.NewReader(string(b)), ""); err != nil {
		fail(err, "")
		return
	}
	j.set(id, func(job *Job) { job.State = Done })
}
