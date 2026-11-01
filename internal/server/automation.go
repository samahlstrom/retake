package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/store"
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
		s.json(w, r, 200, a)
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
			a.Config = b.AutomationConfig
			if !b.Enabled {
				for i := range a.Tasks {
					t := &a.Tasks[i]
					if t.State == "running" {
						t.State = "stopping"
						t.Stop = true
						t.NextAgent = ""
					}
				}
			}
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
		s.json(w, r, 200, project.WorkAssignment{Task: t, Notes: notes, Config: state.Config})
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
		err = st.ReportWork(b)
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
				if old.Worker != id || old.Source != b.Source || old.Task != b.Task {
					return false, errors.New("immutable render identity changed")
				}
				b.Label, b.N = old.Label, old.N
				*old = b.LiveRender
			} else {
				n := 0
				for _, v := range s.Family() {
					n = max(n, v.N)
				}
				for _, v := range a.Renders {
					n = max(n, v.N)
				}
				b.N = n + 1
				b.Label = fmt.Sprintf("v%d", b.N)
				a.Renders = append(a.Renders, b.LiveRender)
				fresh = true
			}
			return fresh, nil
		})
		if err == nil {
			s.Rescan()
			if b.Manifest != nil {
				v, _ := s.version(b.Label)
				raw, e := json.Marshal(b.Manifest)
				if e == nil {
					e = os.MkdirAll(filepath.Dir(v.Path), 0o700)
				}
				if e == nil {
					e = store.WriteJSON(bridge.ManifestPath(v.Path), raw)
				}
				if e != nil {
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
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, relay.MaxBody))
		if e != nil {
			s.fail(w, r, 413, "media response too large")
			return
		}
		if !s.mediaRelay.Reply(id, p[3], relay.Response{Status: status, Type: r.Header.Get("Content-Type"), Range: r.Header.Get("Content-Range"), Body: body}) {
			s.fail(w, r, 410, "request expired")
			return
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
	for _, v := range a.Renders {
		stream := remoteStream(v)
		duration := jsonx.Float(v.Duration)
		w, h := v.Width, v.Height
		f = append(f, family.Version{Label: v.Label, N: v.N, Name: "index.m3u8", Path: filepath.Join(s.root, ".retake", "remote", v.Label, "index.m3u8"), FPS: jsonx.Float(v.FPS), Duration: &duration, Width: &w, Height: &h, Stream: &stream})
	}
	slices.SortStableFunc(f, func(a, b family.Version) int { return a.N - b.N })
	return f
}

func (s *Server) remoteMedia(w http.ResponseWriter, r *http.Request, label, name string) bool {
	v, ok := s.remoteRender(label)
	if !ok {
		return false
	}
	if !relay.ValidFile(name) {
		s.fail(w, r, 404, "no such media file")
		return true
	}
	st, e := s.store(false)
	if e != nil {
		s.storeFailed(w, r, e)
		return true
	}
	a, e := st.Automation()
	if e != nil {
		s.storeFailed(w, r, e)
		return true
	}
	m := a.Worker(v.Worker)
	if m == nil || m.Seen+10 < st.Now().Unix() {
		w.Header().Set("Retry-After", "3")
		s.fail(w, r, 503, "editing machine is offline")
		return true
	}
	reply, e := s.mediaRelay.Fetch(r.Context(), v.Worker, relay.Request{Render: v.ID, File: name, Range: r.Header.Get("Range"), Head: r.Method == http.MethodHead})
	if e != nil {
		s.fail(w, r, 503, "editing machine did not answer the media request")
		return true
	}
	h := w.Header()
	h.Set("Content-Type", reply.Type)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	if strings.HasSuffix(name, ".m3u8") || reply.Status != 200 && reply.Status != 206 {
		h.Set("Cache-Control", "no-store")
	}
	if reply.Range != "" {
		h.Set("Content-Range", reply.Range)
	}
	w.WriteHeader(reply.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(reply.Body)
	}
	return true
}
