package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// The milestone 2 routes of spec/notes.md: images, Done, the sidebar, reviewer actions and the event stream.

// MaxImage is the largest image POST /api/session/<take>/image accepts (25 MiB).
const MaxImage int64 = 25 << 20

const (
	defaultKeepAlive = 15 * time.Second
	sseRetryMillis   = 2000
)

func (s *Server) storeFailed(w http.ResponseWriter, r *http.Request, err error) {
	_, _ = fmt.Fprintf(s.opts.Log, "retake: project store: %v\n", err)
	s.fail(w, r, http.StatusInternalServerError, "project store failed")
}

// takeClosed reports whether a take's session.json has closed: true.
func takeClosed(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "session.json")) //nolint:gosec // a take folder of the family
	if err != nil {
		return false
	}
	var v struct {
		Closed bool `json:"closed"`
	}
	return json.Unmarshal(b, &v) == nil && v.Closed
}

// markClosed sets closed: true in a take's session.json, keeping everything else as it is.
func markClosed(dir string) error {
	p := filepath.Join(dir, "session.json")
	b, err := os.ReadFile(p) //nolint:gosec // a take folder of the family
	if err != nil {
		return err
	}
	o, err := jsonx.ParseObject(b)
	if err != nil {
		return err
	}
	o.Set("closed", json.RawMessage("true"))
	raw, err := o.MarshalJSON()
	if err != nil {
		return err
	}
	return store.WriteJSON(p, raw)
}

type jobRef struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}

// done is POST /api/session/<take>/done: it marks the take closed and queues transcribe(take), then notes(version).
// It is idempotent. A body of {"send": true} sends the take's notes to the agent once they're processed
// (spec/automation.md); any other body, or none, leaves that to the automation config.
func (s *Server) done(w http.ResponseWriter, r *http.Request, v family.Version, take, dir string, body []byte) {
	var opts struct {
		Send  bool   `json:"send"`
		Agent string `json:"agent"` // this send's agent, when the selected worker offers it
	}
	_ = json.Unmarshal(body, &opts) // not JSON: as if absent
	s.writeMu.Lock()
	if _, err := os.Stat(filepath.Join(dir, "session.json")); err != nil {
		s.writeMu.Unlock()
		s.fail(w, r, http.StatusNotFound, "no such take")
		return
	}
	var err error
	if !takeClosed(dir) {
		err = markClosed(dir)
	}
	s.writeMu.Unlock()
	if err != nil {
		_, _ = fmt.Fprintf(s.opts.Log, "retake: closing %s failed: %v\n", dir, err)
		s.fail(w, r, http.StatusInternalServerError, "save failed")
		return
	}
	st, err := s.store(true)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	queued, err := st.QueueTake(v.Label, take, dir)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	refs := make([]jobRef, len(queued))
	dependencies := make([]int64, len(queued))
	for i, j := range queued {
		refs[i] = jobRef{j.ID, j.Kind, j.State}
		dependencies[i] = j.ID
	}
	if err := st.SubmitReview(v.Label, take, dependencies, opts.Send, offeredAgent(st, opts.Agent)); err != nil {
		s.storeFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, struct {
		OK   bool     `json:"ok"`
		Jobs []jobRef `json:"jobs"`
	}{true, refs})
}

// transcribeClips starts work as soon as a completed recording is safely saved.
func (s *Server) transcribeClips(w http.ResponseWriter, r *http.Request, v family.Version, take, dir string) {
	s.writeMu.Lock()
	raw, err := os.ReadFile(filepath.Join(dir, "segments.json")) //nolint:gosec // validated take folder of the family
	var segments []struct {
		File      string `json:"file"`
		Recording bool   `json:"recording"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &segments)
	}
	hash := sha256.New()
	count := 0
	if err == nil {
		for _, seg := range segments {
			if seg.Recording || !store.ValidSegName(seg.File) {
				continue
			}
			info, statErr := os.Stat(filepath.Join(dir, seg.File))
			if statErr != nil || info.Size() == 0 {
				continue
			}
			_, _ = fmt.Fprintf(hash, "%s:%d\n", seg.File, info.Size())
			count++
		}
	}
	s.writeMu.Unlock()
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no saved clips")
		return
	}
	if count == 0 {
		s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	st, err := s.store(true)
	if err == nil {
		_, err = st.QueueClips(v.Label, take, dir, fmt.Sprintf("%x", hash.Sum(nil)))
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
}

// saveImage is POST /api/session/<take>/image?name=img_NN.ext: one image dropped on the video while recording.
func (s *Server) saveImage(w http.ResponseWriter, r *http.Request, dir string, body []byte) {
	name := query(r, "name")
	if !project.ValidImageName(name) {
		s.fail(w, r, http.StatusBadRequest, "bad name")
		return
	}
	want := project.ImageTypes[filepath.Ext(name)]
	got, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || got != want || http.DetectContentType(body) != want {
		s.fail(w, r, http.StatusUnsupportedMediaType, "bad type")
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = store.MkdirTake(dir)
	if err == nil {
		err = store.WriteFileAtomic(filepath.Join(dir, name), body)
	}
	if err != nil {
		_, _ = fmt.Fprintf(s.opts.Log, "retake: writing %s failed: %v\n", filepath.Join(dir, name), err)
		s.fail(w, r, http.StatusInternalServerError, "save failed")
		return
	}
	s.json(w, r, http.StatusOK, struct {
		OK   bool   `json:"ok"`
		File string `json:"file"`
		Size int    `json:"size"`
	}{true, name, len(body)})
}

// historyImage is an image of an earlier take, for the page to show near its time like a stroke.
type historyImage struct {
	URL    string     `json:"url"`
	VideoT float64    `json:"video_t"`
	Box    [4]float64 `json:"box"`
}

// historyEntry is a store.HistoryEntry plus the take's images (take format v1.1). Takes without images look exactly
// as in milestone 1.
type historyEntry struct {
	store.HistoryEntry
	Images        []historyImage      `json:"images,omitempty"`
	SourceVersion string              `json:"source_version,omitempty"`
	Transcribed   bool                `json:"transcribed,omitempty"`
	VoiceTracks   []historyVoiceTrack `json:"voice_tracks,omitempty"`
}

// history is store.History with each take's images. Carried-over takes keep the images inside their ranges, like
// their strokes; their URLs name the version the take was recorded on.
func (s *Server) history(v family.Version) []historyEntry {
	entries := store.History(v.Path)
	inherited := store.InheritedTakes(v.Path)
	own := len(entries) - len(inherited) // History lists the version's own takes, then the carried-over ones
	out := make([]historyEntry, len(entries))
	for i, e := range entries {
		out[i] = historyEntry{HistoryEntry: e}
		dir, label, ranges := store.TakeDir(v.Path, e.ID), v.Label, [][2]float64(nil)
		if i >= own && own >= 0 {
			x := inherited[i-own]
			dir, label, ranges = x.Dir, jobs.VersionOfTake(s.Family(), x.Dir), x.Ranges
		}
		if label == "" || !store.ValidTakeID(e.ID) {
			continue // not a take the /api/takes route can serve
		}
		out[i].Transcribed, out[i].VoiceTracks = completedVoice(dir)
		for _, img := range project.ReadImages(dir) {
			if ranges != nil && !inAny(img.VideoT, ranges) {
				continue
			}
			out[i].Images = append(out[i].Images, historyImage{
				URL:    s.opts.BasePath + "/api/takes/" + e.ID + "/" + img.File + "?v=" + url.QueryEscape(label),
				VideoT: img.VideoT, Box: img.Box,
			})
		}
	}
	return out
}

func inAny(t float64, ranges [][2]float64) bool {
	for _, r := range ranges {
		if r[0] <= t && t <= r[1] {
			return true
		}
	}
	return false
}

// takeFile is GET /api/takes/<take>/<file>?v=: one image of a take, for the sidebar's thumbnails.
func (s *Server) takeFile(w http.ResponseWriter, r *http.Request, take, name string) {
	if !store.ValidTakeID(take) || !project.ValidImageName(name) {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	s.serveFile(w, r, filepath.Join(store.TakeDir(v.Path, take), name), project.ImageTypes[filepath.Ext(name)])
}

// noteSnapshot is GET /api/notes/<id>/snapshot: the still of a note's moment with its strokes, for its card's drawing
// (spec/notes.md, "What the sidebar shows").
func (s *Server) noteSnapshot(w http.ResponseWriter, r *http.Request, id string) {
	noteID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || noteID <= 0 {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	st, err := s.store(false)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	n, err := st.Note(noteID)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	s.serveSnapshot(w, r, st, n)
}

// serveSnapshot serves a note's snapshot. Only a regular file inside the project is served, through os.Root, so an
// imported note can't name any other file of the host (spec/notes.md, "Paths").
func (s *Server) serveSnapshot(w http.ResponseWriter, r *http.Request, st *project.Store, n project.Note) {
	rel, _, ok := st.SnapshotFile(n)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	root, err := os.OpenRoot(st.Root())
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(rel)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	ctype := project.ImageTypes[strings.ToLower(filepath.Ext(rel))]
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxImage || ctype == "" {
		s.fail(w, r, http.StatusNotFound, "no snapshot")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(rel), stat.ModTime(), file)
}

type jobView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Take     string `json:"take,omitempty"`
}

type sidebarResponse struct {
	Scope   string         `json:"scope,omitempty"`
	Version string         `json:"version"`
	Newest  bool           `json:"newest"`
	Items   []project.Item `json:"items"`
	Jobs    []jobView      `json:"jobs"`
}

// sidebar is GET /api/notes?v=: the sidebar of a version, computed here so the page only draws it.
func (s *Server) sidebar(w http.ResponseWriter, r *http.Request) {
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	fam := s.Family()
	resp := sidebarResponse{Version: v.Label, Items: []project.Item{}, Jobs: []jobView{}}
	if scope := query(r, "scope"); scope == "video" || scope == "review" {
		resp.Scope = scope
	}
	// An extension's version (v0) shows its comments as an older version does: To do and Done, never only new ones.
	resp.Newest = (len(fam) == 0 || fam[len(fam)-1].Label == v.Label) && v.Kind == ""
	st, err := s.store(false)
	if errors.Is(err, project.ErrNoProject) {
		s.json(w, r, http.StatusOK, resp)
		return
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	q := project.SidebarQuery{Version: v.Label, Newest: resp.Newest, TakeDir: s.takeDirOf(fam),
		ImageURL: func(n project.Note, img project.TakeImage) string {
			return s.opts.BasePath + "/api/takes/" + n.Take + "/" + img.File + "?v=" + url.QueryEscape(n.Version)
		},
		SnapshotURL: func(n project.Note, modified time.Time) string {
			// The file's time in the URL: a notes rebuild redraws it, and the card loads the new one.
			return s.opts.BasePath + "/api/notes/" + strconv.FormatInt(n.ID, 10) + "/snapshot?m=" + strconv.FormatInt(modified.UnixMilli(), 10)
		}}
	if resp.Scope != "" {
		seen := map[string]bool{}
		target := s.manifestOf(v)
		sources := s.withLeftDrafts(st, fam)
		q.TakeDir = s.takeDirOf(sources)
		for _, source := range sources {
			if resp.Scope == "review" && source.N > v.N {
				continue
			}
			q.Version, q.Newest = source.Label, false
			items, readErr := st.Sidebar(q)
			if readErr != nil {
				s.storeFailed(w, r, readErr)
				return
			}
			var tm *bridge.TimeMap
			if source.Label != v.Label && target != nil {
				if origin := s.manifestOf(source); origin != nil {
					m := bridge.BuildTimeMap(source.Label, v.Label, origin, target)
					tm = &m
				}
			}
			for _, item := range items {
				if resp.Scope == "review" && source.Label != v.Label && item.Status != project.StatusOpen && item.Status != project.StatusQuestion && item.AnsweredIn != v.Label {
					continue
				}
				key := fmt.Sprintf("%s:%d", item.Kind, item.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				if tm != nil {
					if item.T0 != nil {
						t := tm.Map(*item.T0)
						item.T0 = &t
					}
					if item.T1 != nil {
						t := tm.Map(*item.T1)
						item.T1 = &t
					}
				}
				resp.Items = append(resp.Items, item)
			}
			pending, readErr := pendingJobs(st, source.Label)
			if readErr != nil {
				s.storeFailed(w, r, readErr)
				return
			}
			resp.Jobs = append(resp.Jobs, pending...)
		}

		q.Version, q.Newest = v.Label, true
		questions, readErr := st.Sidebar(q)
		if readErr != nil {
			s.storeFailed(w, r, readErr)
			return
		}
		for _, item := range questions {
			if resp.Scope == "review" {
				if origin, ok := s.version(item.Version); ok && origin.N > v.N {
					continue
				}
				if item.Note != nil && !seen[fmt.Sprintf("note:%d", *item.Note)] {
					continue
				}
			}
			key := fmt.Sprintf("%s:%d", item.Kind, item.ID)
			if item.Kind != "question" || seen[key] {
				continue
			}
			if item.Note != nil {
				for _, note := range resp.Items {
					if note.Kind == "note" && note.ID == *item.Note {
						item.T0, item.T1 = note.T0, note.T1
						break
					}
				}
			}
			seen[key] = true
			resp.Items = append(resp.Items, item)
		}
	} else {
		if resp.Newest {
			if row, err := st.Version(v.Label); err == nil {
				q.PushedAt, _ = project.ParseTime(row.Created)
			}
		}
		if resp.Items, err = st.Sidebar(q); err != nil {
			s.storeFailed(w, r, err)
			return
		}
		if resp.Newest {
			if resp.Items, err = s.carry(st, v, fam, resp.Items); err != nil {
				s.storeFailed(w, r, err)
				return
			}
		}
		if resp.Jobs, err = pendingJobs(st, v.Label); err != nil {
			s.storeFailed(w, r, err)
			return
		}
	}
	if automation, e := st.Automation(); e == nil {
		for i := range resp.Items {
			item := &resp.Items[i]
			if item.Kind != "note" {
				continue
			}
			for j := len(automation.Tasks) - 1; j >= 0; j-- {
				task := automation.Tasks[j]
				if item.Status == project.StatusOpen && task.State == "completed" {
					continue
				}
				if slices.Contains(task.Notes, item.ID) {
					task.Lease = ""
					item.Work = &task
					break
				}
			}
		}
		resp.Items = s.attachAsks(st, v, resp.Items, automation)
	}
	s.json(w, r, http.StatusOK, resp)
}

// draftLabel is a draft's label, v<N>-draft<K>: the draft K heading for version N.
var draftLabel = regexp.MustCompile(`^v(\d+)-draft\d+$`)

// withLeftDrafts is the family plus the drafts it no longer lists that comments were recorded on (older drafts, and a
// promoted draft's old label), each as version N: those comments belong to the version they headed for (spec/notes.md,
// "Comments belong to their version"), so they don't vanish once it exists.
func (s *Server) withLeftDrafts(st *project.Store, fam []family.Version) []family.Version {
	notes, err := st.Notes(project.NoteFilter{})
	if err != nil {
		return fam
	}
	listed := map[string]bool{}
	for _, v := range fam {
		listed[v.Label] = true
	}
	out := slices.Clone(fam)
	for _, n := range notes {
		m := draftLabel.FindStringSubmatch(n.Version)
		if m == nil || listed[n.Version] || !listed["v"+m[1]] {
			continue
		}
		listed[n.Version] = true
		num, _ := strconv.Atoi(m[1])
		out = append(out, family.Version{Label: n.Version, N: num, Path: s.remotePath(n.Version)})
	}
	return out
}

// takeDirOf finds a note's take folder through the family: <version's dir>/<stem>.review/<take>.
func (s *Server) takeDirOf(fam []family.Version) func(project.Note) string {
	paths := map[string]string{}
	for _, v := range fam {
		paths[v.Label] = v.Path
	}
	return func(n project.Note) string {
		p, ok := paths[n.Version]
		if !ok || !store.ValidTakeID(n.Take) {
			return ""
		}
		return store.TakeDir(p, n.Take)
	}
}

// pendingJobs returns a version's jobs that aren't done: queued, running, or failed and not redone since. The
// sidebar shows them, so a missing transcriber is visible.
func pendingJobs(st *project.Store, version string) ([]jobView, error) {
	all, err := st.Jobs(500)
	if err != nil {
		return nil, err
	}
	out := []jobView{}
	seen := map[string]bool{}
	for _, j := range all { // newest first: the first of each kind and take is its latest
		if j.Args.Version != version {
			continue
		}
		key := j.Kind + "/" + j.Args.Take
		if seen[key] {
			continue
		}
		seen[key] = true
		if j.State != project.JobDone {
			out = append(out, jobView{j.ID, j.Kind, j.State, j.Attempts, j.Error, j.Hint, j.Args.Take})
		}
	}
	return out, nil
}

// noteID parses a note id from the path.
func noteID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == s
}

func (s *Server) transitionFailed(w http.ResponseWriter, r *http.Request, err error) {
	var te *project.TransitionError
	switch {
	case errors.Is(err, project.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "no such note")
	case errors.As(err, &te):
		s.fail(w, r, http.StatusConflict, err.Error())
	default:
		s.storeFailed(w, r, err)
	}
}

// reopen is POST /api/notes/<id>/reopen: the reviewer reopens an answered note.
func (s *Server) reopen(w http.ResponseWriter, r *http.Request, idText string) {
	id, ok := noteID(idText)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such note")
		return
	}
	st, err := s.store(false)
	if errors.Is(err, project.ErrNoProject) {
		s.fail(w, r, http.StatusNotFound, "no such note")
		return
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	n, err := st.Reopen(id)
	if err != nil {
		s.transitionFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, struct {
		OK     bool           `json:"ok"`
		ID     int64          `json:"id"`
		Status project.Status `json:"status"`
	}{true, n.ID, n.Status})
}

// approve is POST /api/versions/<label>/approve: the reviewer approves a version, and the notes it answered are
// accepted.
func (s *Server) approve(w http.ResponseWriter, r *http.Request, label string) {
	_, known := s.version(label)
	st, err := s.store(false)
	if err != nil && !errors.Is(err, project.ErrNoProject) {
		s.storeFailed(w, r, err)
		return
	}
	if !known && st != nil {
		_, verr := st.Version(label)
		known = verr == nil
	}
	if !known || label == "" {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	accepted := []int64{}
	if st != nil {
		ids, err := st.Approve(label)
		if err != nil {
			s.storeFailed(w, r, err)
			return
		}
		accepted = append(accepted, ids...)
	}
	s.json(w, r, http.StatusOK, struct {
		OK       bool    `json:"ok"`
		Accepted []int64 `json:"accepted"`
	}{true, accepted})
}

// parseSince reads the resume point of an event stream: Last-Event-ID (set by the browser when it reconnects) wins
// over ?since=. given is false when neither is there; ok is false when one is malformed.
func parseSince(r *http.Request) (since int64, given, ok bool) {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = query(r, "since")
	}
	if raw == "" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, true, false
	}
	return n, true, true
}

// events is GET /api/events?since=<seq>: server-sent events from the project's event log. Each event carries its
// seq as the SSE id, so a reconnecting browser resumes after the last one it got (Last-Event-ID). Without a resume
// point the stream starts with the events that come next.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	since, given, ok := parseSince(r)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "bad since")
		return
	}
	st, err := s.store(false)
	if err != nil && !errors.Is(err, project.ErrNoProject) {
		s.storeFailed(w, r, err)
		return
	}
	if !given && st != nil {
		if since, err = st.LastSeq(); err != nil {
			s.storeFailed(w, r, err)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	rc := http.NewResponseController(w)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis); err != nil || rc.Flush() != nil {
		return
	}
	poll := project.DefaultPoll
	if s.opts.EventPoll > 0 {
		poll = s.opts.EventPoll
	}
	keep := defaultKeepAlive
	if s.opts.KeepAlive > 0 {
		keep = s.opts.KeepAlive
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	alive := time.NewTicker(keep)
	defer alive.Stop()
	for {
		var changed <-chan struct{}
		if st == nil {
			if st, err = s.store(false); err != nil && !errors.Is(err, project.ErrNoProject) {
				return
			}
		}
		if st != nil {
			changed = st.Changed()
			evs, err := st.Events(since, 200)
			if err != nil {
				return
			}
			for _, e := range evs {
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, e.Data); err != nil {
					return
				}
				since = e.Seq
			}
			if len(evs) > 0 {
				if rc.Flush() != nil {
					return
				}
				if len(evs) == 200 {
					continue // more are waiting
				}
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		case <-changed:
		case <-tick.C:
		case <-alive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
