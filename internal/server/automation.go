package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/store"
	"github.com/samahlstrom/retake/internal/team"
)

func (s *Server) automationRoute(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		s.fail(w, r, 405, "method not allowed")
		return
	}
	if !s.originOK(r) {
		s.fail(w, r, 403, "bad origin")
		return
	}
	st, err := s.store(true)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	if p[1] == "worker" {
		s.workerRoute(w, r, p, st)
		return
	}
	if len(p) == 2 && r.Method == http.MethodGet {
		if err := st.ReconcileWork(); err != nil {
			s.storeFailed(w, r, err)
			return
		}
		a, err := st.Automation()
		if err != nil {
			s.storeFailed(w, r, err)
			return
		}
		redactAutomation(&a)
		view := automationView{AutomationState: a, Local: s.opts.LocalWorker != nil}
		if view.Local {
			view.Blocked = s.inproc.reason()
		}
		s.json(w, r, 200, view)
		return
	}
	if r.Method != http.MethodPost {
		s.fail(w, r, 404, "not found")
		return
	}
	var b struct {
		project.AutomationConfig
		Notes   []int64 `json:"notes"`
		Message string  `json:"message"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b); err != nil {
		s.fail(w, r, 400, "invalid request")
		return
	}
	switch {
	case len(p) == 2:
		err = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
			worker := a.Worker(b.Worker)
			if b.Enabled && (worker == nil || !worker.HasAgent(b.Agent) || b.Fallback != "" && !worker.HasAgent(b.Fallback)) {
				return false, errors.New("choose a connected machine and an available agent")
			}
			if a.Config.Worker != b.Worker && slices.ContainsFunc(a.Tasks, func(t project.EditingTask) bool { return t.State == "running" || t.State == "stopping" }) {
				return false, errors.New("pause the active task before changing machines")
			}
			// Enabled only decides whether finished reviews go out by themselves: turning it off stops nothing.
			a.Config = b.AutomationConfig
			return true, nil
		})
	case is(p, "api", "automation", "tasks"):
		// Reviewers may submit notes, but only owners choose executable profiles.
		_, err = st.QueueFeedback(b.Notes, "")
	case len(p) == 5 && p[2] == "tasks":
		err = st.ControlTask(p[3], p[4], b.Agent, b.Message)
	default:
		s.fail(w, r, 404, "not found")
		return
	}
	if err != nil {
		s.fail(w, r, 409, err.Error())
		return
	}
	s.json(w, r, 200, map[string]bool{"ok": true})
}

// automationView is GET /api/automation: the state, and in Local mode whether its in-process worker can run.
type automationView struct {
	project.AutomationState
	Local   bool   `json:"local"`
	Blocked string `json:"blocked,omitempty"`
}

func redactAutomation(a *project.AutomationState) {
	for i := range a.Workers {
		a.Workers[i].Session = ""
	}
	for i := range a.Tasks {
		a.Tasks[i].Lease = ""
	}
	for i := range a.Renders {
		a.Renders[i].Manifest = nil
		a.Renders[i].Source = ""
	}
	a.Submissions = nil
}

func (s *Server) workerRoute(w http.ResponseWriter, r *http.Request, p []string, st *project.Store) {
	if is(p, "api", "worker", "poll") && r.Method == http.MethodPost {
		var b struct {
			Worker project.EditingWorker `json:"worker"`
			Task   string                `json:"task"`
			Lease  string                `json:"lease"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || !project.ValidWorkID(b.Worker.ID) || !project.ValidWorkID(b.Worker.Session) || len(b.Worker.Name) > 100 || len(b.Worker.Agents) > 16 {
			s.fail(w, r, 400, "invalid worker")
			return
		}
		for _, a := range b.Worker.Agents {
			if !project.ValidWorkID(a.ID) || len(a.Name) > 100 || len(a.Model) > 100 {
				s.fail(w, r, 400, "invalid agent profile")
				return
			}
		}
		if err := st.ReadyFeedback(); err != nil {
			s.storeFailed(w, r, err)
			return
		}
		t, err := st.PollWork(b.Worker, b.Task, b.Lease)
		if err != nil {
			s.fail(w, r, 409, err.Error())
			return
		}
		var notes []project.WorkNote
		if t != nil {
			ns, err := st.Notes(project.NoteFilter{})
			if err != nil {
				s.storeFailed(w, r, err)
				return
			}
			images, err := st.ImagesOf(ns, s.takeDirOf(s.Family()))
			if err != nil {
				s.storeFailed(w, r, err)
				return
			}
			for _, n := range ns {
				if slices.Contains(t.Notes, n.ID) || n.Status == project.StatusOpen {
					detail := n.Detail
					if detail.Snapshot != "" {
						detail.Snapshot = "note-" + strconv.FormatInt(n.ID, 10) + ".png"
					}
					notes = append(notes, project.WorkNote{Note: n, Detail: detail, Images: images[n.ID]})
				}
			}
		}
		state, err := st.Automation()
		if err != nil {
			s.storeFailed(w, r, err)
			return
		}
		disk, _ := s.diskVersions(&state)
		versions := disk
		for _, v := range state.Renders {
			if !v.Draft {
				versions++
			}
		}
		s.json(w, r, 200, project.WorkAssignment{Task: t, Notes: notes, Config: state.Config, Versions: versions})
		return
	}
	// Every request after registration belongs to the live worker session. A token alone cannot impersonate
	// another session's media response or mutate a render after its worker has gone away.
	a, err := st.Automation()
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	id, session := r.Header.Get("X-Retake-Worker"), r.Header.Get("X-Retake-Session")
	machine := a.Worker(id)
	if machine == nil || machine.Session != session || machine.Seen+int64(project.WorkerLease.Seconds()) <= st.Now().Unix() {
		s.fail(w, r, 409, "worker must reconnect")
		return
	}
	switch {
	case (len(p) == 5 && p[2] == "notes" && p[4] == "snapshot" || len(p) == 6 && p[2] == "notes" && p[4] == "images") && r.Method == http.MethodGet:
		noteID, e := strconv.ParseInt(p[3], 10, 64)
		if e != nil {
			s.fail(w, r, 404, "no such note")
			return
		}
		n, e := st.Note(noteID)
		if e != nil {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		// Notes originate in project-owned takes; never let an imported note read an arbitrary host path.
		path := n.Detail.Snapshot
		if len(p) == 6 {
			images, e := st.ImagesOf([]project.Note{n}, s.takeDirOf(s.Family()))
			if e != nil || !slices.ContainsFunc(images[n.ID], func(im project.TakeImage) bool { return im.File == p[5] }) {
				s.fail(w, r, 404, "no image")
				return
			}
			path = filepath.Join(s.takeDirOf(s.Family())(n), p[5])
		}
		if path == "" {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		rel, e := filepath.Rel(s.root, path)
		if e != nil || !filepath.IsLocal(rel) {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		root, e := os.OpenRoot(s.root)
		if e != nil {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		defer func() { _ = root.Close() }()
		file, e := root.Open(rel)
		if e != nil {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		defer func() { _ = file.Close() }()
		stat, e := file.Stat()
		if e != nil || !stat.Mode().IsRegular() || stat.Size() > MaxImage {
			s.fail(w, r, 404, "no snapshot")
			return
		}
		w.Header().Set("Content-Type", project.ImageTypes[filepath.Ext(path)])
		http.ServeContent(w, r, filepath.Base(path), stat.ModTime(), file)
		return
	case is(p, "api", "worker", "report") && r.Method == http.MethodPost:
		var b project.WorkReport
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || b.Worker != id {
			s.fail(w, r, 400, "invalid report")
			return
		}
		if b.State == "completed" && b.Result == "" && b.Version != "" {
			// A plain project's new version file: it must be a version of the family now, and not a render.
			v, ok := s.familyVersion(b.Version)
			b.VersionChecked = ok && a.Render(b.Version) == nil && (!v.Relay || a.FileVersion(b.Version) != nil &&
				a.FileVersion(b.Version).Worker == id)
		}
		_, floor := s.diskVersions(&a)
		var v project.LiveRender
		if v, err = st.ReportWork(b, floor); err == nil && v.ID != "" {
			// The task's final draft is a version now: the family gains its label.
			s.Rescan()
			if v.Manifest != nil {
				if e := s.writeRemoteManifest(v.Label, v.Manifest); e != nil {
					s.storeFailed(w, r, e)
					return
				}
			}
			stream := remoteStream(v)
			err = st.EmitVersion(project.VersionEvent{Label: v.Label, Why: "push", Stream: &stream})
		}
	case is(p, "api", "worker", "render") && r.Method == http.MethodPost:
		var b struct {
			project.LiveRender
			Lease string `json:"lease"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&b) != nil || !project.ValidWorkID(b.ID) || b.Worker != id || len(b.Source) != 64 || b.Duration <= 0 || b.FPS <= 0 || b.Width <= 0 || b.Height <= 0 {
			s.fail(w, r, 400, "invalid render")
			return
		}
		fresh := false
		err = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
			if a.Config.Worker != id {
				return false, errors.New("this is not the designated machine")
			}
			if b.Task != "" {
				t, e := a.WorkAttempt(b.Task, id, b.Lease, st.Now().Unix())
				if e != nil {
					return false, e
				}
				if t.Stop && (!b.Stopped || b.Complete) {
					return false, errors.New("task is stopping")
				}
			}
			old := a.Render(b.ID)
			if old != nil {
				if old.ID != b.ID || old.Worker != id || old.Source != b.Source || old.Task != b.Task {
					return false, errors.New("immutable render identity changed")
				}
				b.Label, b.N, b.Draft, b.Alias = old.Label, old.N, old.Draft, old.Alias
				*old = b.LiveRender
			} else {
				// Only a task's completion or an empty project's first render makes a version; the rest are drafts.
				disk, floor := s.diskVersions(a)
				b.LiveRender = a.AddRender(b.LiveRender, disk, floor)
				fresh = true
			}
			return fresh, nil
		})
		if err == nil {
			s.Rescan()
			if b.Manifest != nil {
				if e := s.writeRemoteManifest(b.Label, b.Manifest); e != nil {
					s.storeFailed(w, r, e)
					return
				}
			}
			why := project.WhyProgress
			if fresh {
				why = "push"
			}
			if b.Complete {
				why = project.WhyComplete
			}
			if b.Stopped {
				why = project.WhyStopped
			}
			if b.Error != "" {
				why = project.WhyFailed
			}
			stream := remoteStream(b.LiveRender)
			err = st.EmitVersion(project.VersionEvent{Label: b.Label, Why: why, Stream: &stream, Error: b.Error})
			if err == nil {
				s.json(w, r, 200, b.LiveRender)
				return
			}
		}
	case is(p, "api", "worker", "files") && r.Method == http.MethodPost:
		var b struct {
			Files []project.FileVersion `json:"files"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b) != nil || !validFiles(b.Files) {
			s.fail(w, r, 400, "invalid version files")
			return
		}
		var added []string
		changed := false
		err = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
			before := map[string]bool{}
			for _, f := range a.ListedFiles() {
				before[f.Label] = true
			}
			changed = a.SetFiles(id, b.Files)
			for _, f := range a.ListedFiles() {
				if changed && !before[f.Label] {
					added = append(added, f.Label)
				}
			}
			return changed, nil
		})
		if err == nil && changed {
			s.Rescan()
			if len(added) == 0 {
				added = []string{""} // a file changed or went: the pages read the family again
			}
			for _, label := range added {
				if err = st.EmitVersion(project.VersionEvent{Label: label, Why: "push"}); err != nil {
					break
				}
			}
		}
	case is(p, "api", "worker", "media") && r.Method == http.MethodGet:
		request, e := s.mediaRelay.Next(r.Context(), id)
		if e != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.json(w, r, 200, request)
		return
	case len(p) == 4 && p[2] == "media" && r.Method == http.MethodPost:
		status, e := strconv.Atoi(r.Header.Get("X-Media-Status"))
		if e != nil || status != 200 && status != 206 && status != 404 && status != 416 && status != 503 {
			s.fail(w, r, 400, "invalid media status")
			return
		}
		length, e1 := strconv.ParseInt(r.Header.Get("X-Media-Length"), 10, 64)
		modified, _ := strconv.ParseInt(r.Header.Get("X-Media-Modified"), 10, 64)
		if e1 != nil || length < 0 || r.ContentLength < 0 || r.ContentLength > relay.Chunk {
			s.fail(w, r, 413, "a media answer is one chunk at most, with its length")
			return
		}
		// The body streams through to the browser: this request stays open until it has been read.
		body := http.MaxBytesReader(w, r.Body, relay.Chunk)
		done, ok := s.mediaRelay.Reply(id, p[3], relay.Response{Status: status, Type: r.Header.Get("Content-Type"),
			Range: r.Header.Get("Content-Range"), Length: length, Modified: modified, Body: body})
		if !ok {
			s.fail(w, r, 410, "request expired")
			return
		}
		select {
		case <-done:
		case <-r.Context().Done():
		case <-time.After(relay.Hold):
		}
	default:
		s.fail(w, r, 404, "not found")
		return
	}
	if err != nil {
		s.fail(w, r, 409, err.Error())
		return
	}
	s.json(w, r, 200, map[string]bool{"ok": true})
}

// validFiles checks a worker's version files (spec/automation.md, "Version files").
func validFiles(fs []project.FileVersion) bool {
	if len(fs) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range fs {
		n, err := strconv.Atoi(strings.TrimPrefix(f.Label, "v"))
		ok := err == nil && n == f.N && n >= 0 && f.Label == "v"+strconv.Itoa(n) && !seen[f.Label] &&
			plainName(f.Name) && len(f.Name) <= 255 && family.IsVideo(f.Name) && f.Size >= 0 && f.Modified >= 0 &&
			f.Duration >= 0 && f.Duration < 1e7 && f.FPS >= 0 && f.FPS <= 1000 && f.Width >= 0 && f.Width <= 1<<15 &&
			f.Height >= 0 && f.Height <= 1<<15
		if !ok {
			return false
		}
		seen[f.Label] = true
	}
	return true
}

func remoteStream(v project.LiveRender) family.StreamState {
	state := family.StatePending
	if v.Done > 0 {
		state = family.StateRendering
	}
	if v.Complete {
		state = family.StateComplete
	}
	if v.Stopped || v.Error != "" {
		state = "stopped"
	}
	return family.StreamState{State: state, Done: v.Done, Total: v.Total, Seconds: jsonx.Float(v.Seconds)}
}

func (s *Server) remoteRender(label string) (project.LiveRender, bool) {
	st, e := s.store(false)
	if e != nil {
		return project.LiveRender{}, false
	}
	a, e := st.Automation()
	if e != nil {
		return project.LiveRender{}, false
	}
	v := a.Render(label)
	if v == nil {
		return project.LiveRender{}, false
	}
	return *v, true
}

func (s *Server) remoteFamily(f []family.Version) []family.Version {
	st, e := s.store(false)
	if e != nil {
		return f
	}
	a, e := st.Automation()
	if e != nil {
		return f
	}
	// A connected computer's version files, unless the server has that version itself or a render has its label.
	have := map[string]bool{}
	for _, v := range f {
		have[v.Label] = true
	}
	for _, fv := range a.ListedFiles() {
		if !have[fv.Label] && a.Render(fv.Label) == nil {
			f = append(f, s.fileVersion(fv))
			have[fv.Label] = true
		}
	}
	floor := 0
	for _, v := range f {
		floor = max(floor, v.N)
	}
	for i, v := range a.Renders {
		if a.Listed(i, floor) {
			f = append(f, s.remoteVersion(v))
		}
	}
	slices.SortStableFunc(f, func(a, b family.Version) int { return a.N - b.N })
	return f
}

// remoteVersion is a relayed render as a version of the family.
func (s *Server) remoteVersion(v project.LiveRender) family.Version {
	stream := remoteStream(v)
	duration := jsonx.Float(v.Duration)
	w, h := v.Width, v.Height
	return family.Version{Label: v.Label, N: v.N, Name: "index.m3u8", Path: s.remotePath(v.Label), FPS: jsonx.Float(v.FPS),
		Duration: &duration, Width: &w, Height: &h, Stream: &stream, Relay: true}
}

// fileVersion is a connected computer's version file as a version of the family. Its takes live on the host, under
// .retake/remote/<label>/.
func (s *Server) fileVersion(fv project.FileVersion) family.Version {
	v := family.Version{Label: fv.Label, N: fv.N, Name: fv.Name, FPS: jsonx.Float(fv.FPS), Relay: true,
		Path: filepath.Join(s.root, ".retake", "remote", fv.Label, fv.Name)}
	d := jsonx.Float(fv.Duration)
	v.Duration = &d
	if fv.Width > 0 && fv.Height > 0 {
		w, h := fv.Width, fv.Height
		v.Width, v.Height = &w, &h
	}
	return v
}

// familyVersion finds a version in the family as it is now (scanned again).
func (s *Server) familyVersion(label string) (family.Version, bool) {
	for _, v := range s.Rescan() {
		if v.Label == label {
			return v, true
		}
	}
	return family.Version{}, false
}

// remotePath is where a relayed version's takes and manifest live on the host.
func (s *Server) remotePath(label string) string {
	return filepath.Join(s.root, ".retake", "remote", label, "index.m3u8")
}

// unlistedRender finds a relayed render the family doesn't list (an older draft, or a promoted draft's old label).
func (s *Server) unlistedRender(label string) (family.Version, bool) {
	if label == "" {
		return family.Version{}, false
	}
	v, ok := s.remoteRender(label)
	if !ok {
		return family.Version{}, false
	}
	return s.remoteVersion(v), true
}

func (s *Server) writeRemoteManifest(label string, m *bridge.Manifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	path := s.remotePath(label)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // label is one the server made (v<N>, v<N>-draft<K>)
		return err
	}
	return store.WriteJSON(bridge.ManifestPath(path), raw)
}

// diskVersions counts the family's versions that aren't relayed renders, and their highest number.
func (s *Server) diskVersions(a *project.AutomationState) (count, floor int) {
	for _, v := range s.Family() {
		if a.Render(v.Label) == nil {
			count++
			floor = max(floor, v.N)
		}
	}
	return count, floor
}

// localWorker is the state of Options.LocalWorker: why it can't run, and when it has stopped.
type localWorker struct {
	mu      sync.Mutex
	blocked string
	done    chan struct{} // closed when it returns; nil without one
}

func (l *localWorker) setReason(why string) {
	l.mu.Lock()
	l.blocked = why
	l.mu.Unlock()
}

func (l *localWorker) reason() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blocked
}

func (l *localWorker) wait() {
	if l.done != nil {
		<-l.done
	}
}

// startLocalWorker starts Options.LocalWorker, once, for the life of the server.
func (s *Server) startLocalWorker() {
	if s.opts.LocalWorker == nil {
		return
	}
	s.inproc.done = make(chan struct{})
	go func() {
		defer close(s.inproc.done)
		s.opts.LocalWorker(s.ctx, s.root, s.WorkerHandler(), s.inproc.setReason)
	}()
}

// WorkerHandler serves the project's /api/worker/* routes to an editing worker inside this process (Local mode), as
// the agent role. Nothing outside the process can reach it, so there are no Host, origin or token checks.
func (s *Server) WorkerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := splitPath(r.URL.Path)
		if len(parts) < 3 || parts[0] != "api" || parts[1] != "worker" {
			s.fail(w, r, http.StatusNotFound, "not found")
			return
		}
		s.route(w, withAccess(r, access{role: team.Agent}), parts)
	})
}

// Busy reports whether this process's own worker has an agent at work, which keeps an idle server from exiting.
func (s *Server) Busy() bool {
	if s.opts.LocalWorker == nil {
		return false
	}
	s.projMu.Lock()
	st := s.proj // a worker at work has opened it; checking doesn't open it
	s.projMu.Unlock()
	if st == nil {
		return false
	}
	a, err := st.Automation()
	return err == nil && slices.ContainsFunc(a.Tasks, func(t project.EditingTask) bool {
		return t.State == "running" || t.State == "stopping"
	})
}
