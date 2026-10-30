package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// Job kinds.
const (
	JobTranscribe = "transcribe" // transcribe(take): writes the take's transcript.json
	JobNotes      = "notes"      // notes(version): runs the notes engine for a version and syncs the notes table
)

// Job states.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// JobArgs are a job's arguments.
type JobArgs struct {
	Version string  `json:"version"`         // the version label
	Take    string  `json:"take,omitempty"`  // transcribe: the take id
	Dir     string  `json:"dir,omitempty"`   // transcribe: the take folder
	After   []int64 `json:"after,omitempty"` // run only once these jobs are done; fail if one of them failed
}

// Job is one row of the jobs table.
type Job struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"`
	Args     JobArgs `json:"args"`
	State    string  `json:"state"`
	Attempts int     `json:"attempts"`
	Error    string  `json:"error,omitempty"`
	Hint     string  `json:"hint,omitempty"` // a failed job's one-line fix
	Created  string  `json:"created"`
	Updated  string  `json:"updated"`
}

const jobCols = `id, kind, args, state, attempts, error, hint, created, updated`

func scanJob(r scanner) (Job, error) {
	var j Job
	var args string
	if err := r.Scan(&j.ID, &j.Kind, &args, &j.State, &j.Attempts, &j.Error, &j.Hint, &j.Created, &j.Updated); err != nil {
		return j, err
	}
	_ = json.Unmarshal([]byte(args), &j.Args) // malformed args fail the job when it runs
	return j, nil
}

func queryJobs(q querier, where string, args ...any) ([]Job, error) {
	rows, err := q.Query(`SELECT `+jobCols+` FROM jobs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func jobByID(q querier, id int64) (Job, error) {
	j, err := scanJob(q.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, fmt.Errorf("job %d: %w", id, ErrNotFound)
	}
	return j, err
}

// Job returns one job.
func (s *Store) Job(id int64) (Job, error) { return jobByID(s.db, id) }

// Jobs returns the jobs, newest first, up to limit (<= 0: all).
func (s *Store) Jobs(limit int) ([]Job, error) {
	if limit <= 0 {
		limit = math.MaxInt64 // every row; the same SQL on SQLite and Postgres
	}
	return queryJobs(s.db, `ORDER BY id DESC LIMIT ?`, limit)
}

func (j Job) event() JobEvent {
	return JobEvent{ID: j.ID, Kind: j.Kind, State: j.State, Attempts: j.Attempts, Error: j.Error, Hint: j.Hint, Take: j.Args.Take,
		Version: j.Args.Version}
}

func (t *tx) insertJob(kind string, args JobArgs) (Job, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return Job{}, err
	}
	now := t.s.stamp()
	var id int64
	if err := t.QueryRow(`INSERT INTO jobs (kind, args, state, created, updated) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		kind, string(b), JobQueued, now, now).Scan(&id); err != nil {
		return Job{}, err
	}
	j, err := jobByID(t, id)
	if err == nil {
		t.emit(EventJob, j.event())
	}
	return j, err
}

// QueueTake queues the jobs for a closed take: transcribe(take), then notes(version) after it. It is idempotent:
// a take whose transcription is queued, running or done keeps that job, and a notes job that already waits for it
// is kept too. A take whose last transcription failed gets a new one, so pressing Done again retries.
func (s *Store) QueueTake(version, take, dir string) ([]Job, error) {
	var out []Job
	err := s.write(func(t *tx) error {
		out = nil
		tj, err := t.findJob(JobTranscribe, func(j Job) bool {
			return j.Args.Take == take && j.Args.Version == version && j.State != JobFailed
		})
		if err != nil {
			return err
		}
		if tj == nil {
			j, err := t.insertJob(JobTranscribe, JobArgs{Version: version, Take: take, Dir: dir})
			if err != nil {
				return err
			}
			tj = &j
		}
		nj, err := t.findJob(JobNotes, func(j Job) bool {
			return j.Args.Version == version && j.State != JobFailed && slices.Contains(j.Args.After, tj.ID)
		})
		if err != nil {
			return err
		}
		if nj == nil {
			// A notes run for this version that hasn't started yet can wait for this take too.
			nj, err = t.findJob(JobNotes, func(j Job) bool { return j.Args.Version == version && j.State == JobQueued })
			if err != nil {
				return err
			}
			if nj != nil {
				nj.Args.After = append(nj.Args.After, tj.ID)
				b, err := json.Marshal(nj.Args)
				if err != nil {
					return err
				}
				if _, err := t.Exec(`UPDATE jobs SET args = ? WHERE id = ?`, string(b), nj.ID); err != nil {
					return err
				}
			}
		}
		if nj == nil {
			j, err := t.insertJob(JobNotes, JobArgs{Version: version, After: []int64{tj.ID}})
			if err != nil {
				return err
			}
			nj = &j
		}
		out = []Job{*tj, *nj}
		return nil
	})
	return out, err
}

// QueueNotes queues a notes(version) job unless one for that version is already waiting to run.
func (s *Store) QueueNotes(version string) (Job, error) {
	var out Job
	err := s.write(func(t *tx) error {
		nj, err := t.findJob(JobNotes, func(j Job) bool { return j.Args.Version == version && j.State == JobQueued })
		if err != nil || nj != nil {
			if nj != nil {
				out = *nj
			}
			return err
		}
		out, err = t.insertJob(JobNotes, JobArgs{Version: version})
		return err
	})
	return out, err
}

// findJob returns the newest job of a kind that matches, or nil.
func (t *tx) findJob(kind string, match func(Job) bool) (*Job, error) {
	jobs, err := queryJobs(t, `WHERE kind = ? ORDER BY id DESC`, kind)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if match(j) {
			return &j, nil
		}
	}
	return nil, nil
}

// Claim takes the oldest queued job of a kind that may run now and marks it running (one more attempt). A job
// that failed waits backoff(attempts) after its last attempt; a job waits for the jobs in its After list, and fails
// at once if one of them failed. ok is false when nothing may run.
func (s *Store) Claim(kind string, backoff func(attempts int) time.Duration) (job Job, ok bool, err error) {
	err = s.write(func(t *tx) error {
		ok = false
		jobs, err := queryJobs(t, `WHERE kind = ? AND state = ? ORDER BY id`, kind, JobQueued)
		if err != nil {
			return err
		}
		now := s.Now()
	next:
		for _, j := range jobs {
			if j.Attempts > 0 && backoff != nil {
				last, err := ParseTime(j.Updated)
				if err == nil && now.Before(last.Add(backoff(j.Attempts))) {
					continue
				}
			}
			for _, dep := range j.Args.After {
				d, err := jobByID(t, dep)
				if errors.Is(err, ErrNotFound) {
					continue // a vanished dependency doesn't block
				}
				if err != nil {
					return err
				}
				switch d.State {
				case JobFailed:
					msg := fmt.Sprintf("%s job #%d failed: %s", d.Kind, d.ID, d.Error)
					if d.Args.Take != "" {
						msg = fmt.Sprintf("transcribing take %s failed: %s", d.Args.Take, d.Error)
					}
					if err := t.finish(j.ID, JobFailed, j.Attempts, msg, d.Hint); err != nil {
						return err
					}
					continue next
				case JobDone:
				default:
					continue next
				}
			}
			if _, err := t.Exec(`UPDATE jobs SET state = ?, attempts = attempts + 1, updated = ? WHERE id = ?`,
				JobRunning, s.stamp(), j.ID); err != nil {
				return err
			}
			if job, err = jobByID(t, j.ID); err != nil {
				return err
			}
			t.emit(EventJob, job.event())
			ok = true
			return nil
		}
		return nil
	})
	return job, ok, err
}

func (t *tx) finish(id int64, state string, attempts int, msg, hint string) error {
	if _, err := t.Exec(`UPDATE jobs SET state = ?, attempts = ?, error = ?, hint = ?, updated = ? WHERE id = ?`, state,
		attempts, msg, hint, t.s.stamp(), id); err != nil {
		return err
	}
	j, err := jobByID(t, id)
	if err != nil {
		return err
	}
	t.emit(EventJob, j.event())
	return nil
}

// Outcome is how a job attempt ended.
type Outcome int

// The outcomes.
const (
	Succeeded   Outcome = iota // done
	Failed                     // retried while attempts remain, else failed
	Fatal                      // failed now: retrying can't help
	Interrupted                // the server stopped: queued again, and the attempt doesn't count
)

// Finish records the end of a running job's attempt. maxAttempts bounds retries of a Failed attempt; hint is a
// one-line fix for a failure (kept only while the job isn't done).
func (s *Store) Finish(id int64, outcome Outcome, msg, hint string, maxAttempts int) (Job, error) {
	var out Job
	err := s.write(func(t *tx) error {
		j, err := jobByID(t, id)
		if err != nil {
			return err
		}
		state, attempts := JobDone, j.Attempts
		switch outcome {
		case Succeeded:
			msg, hint = "", ""
		case Failed:
			state = JobFailed
			if j.Attempts < maxAttempts {
				state = JobQueued
			}
		case Fatal:
			state = JobFailed
		case Interrupted:
			state, attempts = JobQueued, max(0, j.Attempts-1)
		}
		if err := t.finish(id, state, attempts, msg, hint); err != nil {
			return err
		}
		out, err = jobByID(t, id)
		return err
	})
	return out, err
}

// RequeueRunning puts the jobs that a crashed run left running back in the queue; one that has used all its
// attempts fails instead. Call it once, from the process that runs the jobs, before it claims any.
func (s *Store) RequeueRunning(maxAttempts int) ([]Job, error) {
	var out []Job
	err := s.write(func(t *tx) error {
		out = nil
		jobs, err := queryJobs(t, `WHERE state = ? ORDER BY id`, JobRunning)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			state, msg := JobQueued, "interrupted: retake stopped while it ran"
			if j.Attempts >= maxAttempts {
				state = JobFailed
				msg = fmt.Sprintf("interrupted on its last attempt (%d of %d)", j.Attempts, maxAttempts)
			}
			if err := t.finish(j.ID, state, j.Attempts, msg, ""); err != nil {
				return err
			}
			j, err := jobByID(t, j.ID)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return nil
	})
	return out, err
}
