package worker

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// The agent's progress inside a task (spec/automation.md, "Marking progress"): `retake working <note>` and `retake
// done <note>` reach the worker on the same endpoint as `retake ask`, and the worker passes them on. A done note's
// edit goes in the live target first ("The live version"): a timeline project's next complete render, or a plain
// project's video so far, registered as a revision. Only then does the note show Done.

// ProgressRequest is POST <ask url>/progress from the agent.
type ProgressRequest struct {
	Note  int64  `json:"note"`
	State string `json:"state"`
	Text  string `json:"text"`
}

// waitingMark is a timeline task's done mark whose edit isn't in a complete render yet: Source is the timeline's
// fingerprint at the `done`, At when it was said. It is kept in the attempt's record, so a later worker finishes it.
type waitingMark struct {
	Note   int64     `json:"note"`
	Change string    `json:"change"`
	Source string    `json:"source,omitempty"`
	At     time.Time `json:"at"`
}

// plainKeep is how many of a task's newest plain revisions keep their media; older ones keep only their state.
const plainKeep = 3

// serveProgress is POST /progress: a retake working or retake done from the running attempt's agent.
func (r *runner) serveProgress(w http.ResponseWriter, req *http.Request, fail func(int, string)) {
	given := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if r.chatAsk(given) != nil {
		fail(http.StatusConflict, "this is a chat, not an editing task: there are no notes to mark")
		return
	}
	r.mu.Lock()
	secret, active := r.ask.secret, r.active
	r.mu.Unlock()
	if secret == "" || active == nil || subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
		fail(http.StatusForbidden, "no Retake task is running with this secret")
		return
	}
	var body ProgressRequest
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&body) != nil {
		fail(http.StatusBadRequest, "invalid progress")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Minute)
	defer cancel()
	var out project.ProgressMark
	var err error
	switch body.State {
	case project.ProgressWorking:
		out, err = r.markWorking(ctx, active, body)
	case project.ProgressDone:
		out, err = r.markDone(ctx, active, body)
	default:
		fail(http.StatusBadRequest, `the state is "working" or "done"`)
		return
	}
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			msg := he.message
			var reply struct{ Error string }
			if json.Unmarshal([]byte(msg), &reply) == nil && reply.Error != "" {
				msg = reply.Error
			}
			fail(he.status, msg)
			return
		}
		var refused *refusal
		if errors.As(err, &refused) {
			fail(http.StatusConflict, refused.Error())
			return
		}
		fail(http.StatusBadGateway, "can't reach the review: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// refusal is a done the worker refuses itself, with the reason for the agent.
type refusal struct{ msg string }

func (e *refusal) Error() string { return e.msg }

// tellServer passes a mark on: POST /api/worker/progress, with the notes of the next batch the session was given.
func (r *runner) tellServer(ctx context.Context, t *project.EditingTask, p project.WorkProgress) (project.ProgressMark, error) {
	p.Task, p.Lease = t.ID, t.Lease
	p.Join = r.unsentJoin()
	var out project.ProgressMark
	if err := r.call(ctx, "/api/worker/progress", p, &out); err != nil {
		return out, err
	}
	r.joined(p.Join)
	return out, nil
}

func (r *runner) markWorking(ctx context.Context, t *project.EditingTask, body ProgressRequest) (project.ProgressMark, error) {
	out, err := r.tellServer(ctx, t, project.WorkProgress{Note: body.Note, State: project.ProgressWorking, Text: body.Text})
	if err != nil {
		return out, err
	}
	r.dropWaiting(out.Note) // working on it again: its earlier done is taken back
	r.log("%s: the agent is on #%d: %s", filepath.Base(r.opts.Root), out.Note, oneLine(body.Text))
	return out, nil
}

func (r *runner) markDone(ctx context.Context, t *project.EditingTask, body ProgressRequest) (project.ProgressMark, error) {
	r.recMu.Lock()
	rec := r.rec
	r.recMu.Unlock()
	if rec == nil || rec.Task != t.ID {
		return project.ProgressMark{}, &refusal{"the task's attempt isn't running on this computer"}
	}
	p := project.WorkProgress{Note: body.Note, State: project.ProgressDone, Text: body.Text}
	switch {
	case rec.Doc:
		// A document's comment: nothing to render, it's in as soon as it's saved.
		p.Ready = true
	case rec.Plain != nil:
		// A plain project: the video so far, as the target's newest revision.
		id, err := r.plainRevision(ctx, t, rec, rec.Plain.Output)
		if err != nil {
			return project.ProgressMark{}, err
		}
		p.Render = id
	case r.timeline != "":
		source, _, err := fingerprint(r.opts.Root, r.timeline)
		if err != nil {
			return project.ProgressMark{}, &refusal{"the timeline isn't valid right now (" + err.Error() + "): save it, then say done again"}
		}
		r.mu.Lock()
		last := r.render
		r.mu.Unlock()
		if last.Task == t.ID && last.Complete && last.Error == "" && last.Source == source {
			p.Render = last.ID // its edit is rendered already
		} else {
			// Said now, so the note shows it's being put in; ready once a render has it.
			out, err := r.tellServer(ctx, t, p)
			if err != nil {
				return out, err
			}
			r.update(rec, func(x *runRecord) {
				x.Waiting = slices.DeleteFunc(x.Waiting, func(m waitingMark) bool { return m.Note == out.Note })
				x.Waiting = append(x.Waiting, waitingMark{Note: out.Note, Change: body.Text, Source: source, At: time.Now()})
			})
			r.log("%s: the agent finished #%d; it shows once a render has it", filepath.Base(r.opts.Root), out.Note)
			return out, nil
		}
	}
	out, err := r.tellServer(ctx, t, p)
	if err != nil {
		return out, err
	}
	r.dropWaiting(out.Note)
	r.log("%s: the agent finished #%d: %s", filepath.Base(r.opts.Root), out.Note, oneLine(body.Text))
	return out, nil
}

// dropWaiting forgets a waiting mark of note id.
func (r *runner) dropWaiting(id int64) {
	r.recMu.Lock()
	rec := r.rec
	r.recMu.Unlock()
	if rec == nil {
		return
	}
	r.update(rec, func(x *runRecord) {
		x.Waiting = slices.DeleteFunc(x.Waiting, func(m waitingMark) bool { return m.Note == id })
	})
}

// resolveMarks tells the server the waiting marks a complete render of the task now has: its source is the one at the
// done, or it started after the done.
func (r *runner) resolveMarks(ctx context.Context) {
	r.recMu.Lock()
	rec := r.rec
	var waiting []waitingMark
	if rec != nil {
		waiting = slices.Clone(rec.Waiting)
	}
	r.recMu.Unlock()
	if len(waiting) == 0 {
		return
	}
	t, _ := r.state()
	r.mu.Lock()
	last, started := r.render, r.renderStarted[r.render.ID]
	r.mu.Unlock()
	if t == nil || t.ID != rec.Task || t.Stop || last.Task != t.ID || !last.Complete || last.Error != "" {
		return
	}
	for _, m := range waiting {
		if last.Source != m.Source && (started.IsZero() || started.Before(m.At)) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := r.tellServer(cctx, t, project.WorkProgress{Note: m.Note, State: project.ProgressDone, Text: m.Change, Render: last.ID})
		cancel()
		var he *httpError
		if err != nil && !errors.As(err, &he) {
			continue // offline: the next look tries again
		}
		if err != nil {
			r.log("%s: #%d's mark wasn't taken: %s", filepath.Base(r.opts.Root), m.Note, he.message)
		} else {
			r.log("%s: #%d is in %s", filepath.Base(r.opts.Root), m.Note, t.Target)
		}
		r.update(rec, func(x *runRecord) {
			x.Waiting = slices.DeleteFunc(x.Waiting, func(w waitingMark) bool { return w.Note == m.Note && w.At.Equal(m.At) })
		})
	}
}

// plainRevision registers the video at path as the plain task's newest revision of its target (a complete render
// whose media is video.mp4), and returns its id. Anything that doesn't play is refused, so the agent can fix it.
func (r *runner) plainRevision(ctx context.Context, t *project.EditingTask, rec *runRecord, path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", &refusal{fmt.Sprintf("write the video so far to %s first (the whole video, with this note's edit), then say done again", path)}
	}
	info := r.opts.Prober.Probe(path)
	if !info.HasDuration() {
		return "", &refusal{fmt.Sprintf("%s doesn't play (it has no duration): write a video that plays, then say done again", path)}
	}
	id, err := r.addRevision(ctx, t, path, false)
	if err != nil {
		return "", err
	}
	r.update(rec, func(x *runRecord) { x.Revisions = append(x.Revisions, id) })
	r.pruneRevisions(rec)
	return id, nil
}

// addRevision copies (or with link, hard-links) the video at path into a new render folder as video.mp4 and registers
// it with the server as a complete render of task t: a revision of its live target.
func (r *runner) addRevision(ctx context.Context, t *project.EditingTask, path string, link bool) (string, error) {
	info := r.opts.Prober.Probe(path)
	if !info.HasDuration() {
		return "", fmt.Errorf("%s doesn't play", filepath.Base(path))
	}
	v := project.LiveRender{ID: project.NewWorkID(), Worker: r.identity.ID, Task: t.ID, Media: "video.mp4",
		Duration: *info.Duration, FPS: info.FPS, Complete: true, Done: 1, Total: 1, Seconds: *info.Duration, Width: 1, Height: 1}
	if info.Width != nil && info.Height != nil && *info.Width > 0 && *info.Height > 0 {
		v.Width, v.Height = *info.Width, *info.Height
	}
	if v.FPS <= 0 {
		v.FPS = 30
	}
	sum := sha256.Sum256([]byte(v.ID + path))
	v.Source = hex.EncodeToString(sum[:])
	dir := filepath.Join(r.dir, "renders", v.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, v.Media)
	if err := cloneFile(path, dest, link); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("couldn't keep the video as a revision: %w", err)
	}
	if err := writeJSON(filepath.Join(dir, "state.json"), v); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	var registered project.LiveRender
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := r.call(cctx, "/api/worker/render", struct {
		project.LiveRender
		Lease string `json:"lease"`
	}{v, t.Lease}, &registered); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	v.Label, v.N = registered.Label, registered.N
	_ = writeJSON(filepath.Join(dir, "state.json"), v)
	return v.ID, nil
}

// pruneRevisions deletes the media of the task's plain revisions but the newest few: each is a whole video.
func (r *runner) pruneRevisions(rec *runRecord) {
	r.recMu.Lock()
	revs := slices.Clone(rec.Revisions)
	r.recMu.Unlock()
	if len(revs) <= plainKeep {
		return
	}
	for _, id := range revs[:len(revs)-plainKeep] {
		if project.ValidWorkID(id) {
			_ = os.Remove(filepath.Join(r.dir, "renders", id, "video.mp4"))
		}
	}
}

// startRevision makes a plain task's first revision of its target as the attempt starts: the version the agent starts
// from, so the target is live before any edit (spec/automation.md, "The live version").
func (r *runner) startRevision(ctx context.Context, t *project.EditingTask, rec *runRecord) {
	if rec.Plain == nil || rec.Plain.From == "" {
		return
	}
	id, err := r.addRevision(ctx, t, rec.Plain.From, true)
	if err != nil {
		r.log("%s: couldn't show %s live yet: %v", filepath.Base(r.opts.Root), t.Target, err)
		return
	}
	r.update(rec, func(x *runRecord) { x.Revisions = append(x.Revisions, id) })
}

// cloneFile makes dest a copy of src that never changes when src does: a hard link when link (src is never written
// again: a version file), else a reflink where the file system has them, else a plain copy.
func cloneFile(src, dest string, link bool) error {
	if link && os.Link(src, dest) == nil {
		return nil
	}
	in, err := os.Open(src) //nolint:gosec // the task's own output, or a version file of the project
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // a render folder of this worker
	if err != nil {
		return err
	}
	if !reflink(out, in) {
		_, err = io.Copy(out, in)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// oneLine folds whitespace, for the log.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// docTask reports whether the running attempt of task is a document task (only typed comments on an extension's
// version): it makes no version, so it has no live target to render at its start.
func (r *runner) docTask(task string) bool {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	return r.rec != nil && r.rec.Task == task && r.rec.Doc
}
