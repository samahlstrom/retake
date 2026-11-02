package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/extwork"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/notes"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/team"
)

// Extensions (spec/extensions.md): the page's files of each extension, its list, and the routes over its owned files
// of this video's folder. Locally the files are on this computer's disk; hosted (Options.RemoteFiles) they are on the
// editing computer and every request goes through its worker's relay.

type extKey struct{}

// GrantExts puts the extensions in effect for the person behind r on it (the team server: its allowed ones minus the
// ones they turned off). Without it, the Server's own Options.Exts apply.
func GrantExts(r *http.Request, set *ext.Set) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), extKey{}, set))
}

// extsOf is the extensions in effect for r.
func (s *Server) extsOf(r *http.Request) *ext.Set {
	if set, ok := r.Context().Value(extKey{}).(*ext.Set); ok {
		return set
	}
	return s.opts.Exts
}

// isExtRoute reports whether parts is one of the extension routes: ext/… or api/ext/….
func isExtRoute(parts []string) bool {
	return len(parts) >= 1 && parts[0] == "ext" || len(parts) >= 2 && parts[0] == "api" && parts[1] == "ext"
}

// extPermFor is what an extension route needs at the door; the manifest's own roles are checked inside.
func extPermFor(method string, parts []string) team.Perm {
	if method == http.MethodGet || method == http.MethodHead {
		return team.PermWatch
	}
	if len(parts) == 4 && parts[3] == "watch" {
		return team.PermWatch // a viewer's page watches the files it reads
	}
	return team.PermRecord
}

// permOf maps a manifest role ("watch", "record", "approve", "owner") to its permission; "" is def.
func permOf(role string, def team.Perm) team.Perm {
	switch role {
	case "watch":
		return team.PermWatch
	case "record":
		return team.PermRecord
	case "approve":
		return team.PermApprove
	case "owner":
		return team.PermManage
	}
	return def
}

// extRoute serves ext/… and api/ext/… under the video.
func (s *Server) extRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	set := s.extsOf(r)
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case is(parts, "api", "ext") && get:
		s.json(w, r, http.StatusOK, set.APIList(s.machineHas))
		return
	case is(parts, "ext", "styles.css") && get:
		ServeStyles(w, r, set)
		return
	case len(parts) >= 3 && parts[0] == "ext" && get:
		set.ServeUI(w, r, parts[1], strings.Join(parts[2:], "/"))
		return
	case len(parts) < 4 || parts[0] != "api":
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	e := set.Get(parts[2])
	if e == nil || e.Files == nil {
		s.fail(w, r, http.StatusNotFound, "no such extension")
		return
	}
	role := accessOf(r).role
	read, write := permOf(e.Files.Read, team.PermWatch), permOf(e.Files.Write, team.PermRecord)
	need := read
	if !get && parts[3] != "watch" {
		need = write
	}
	if !role.Can(need) {
		s.fail(w, r, http.StatusForbidden, "not allowed")
		return
	}
	if !get && !s.originOK(r) {
		s.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	files := s.extFiles(e)
	rest := parts[3:]
	switch {
	case rest[0] == "files" && len(rest) == 1 && get:
		list, err := files.list(r.Context(), r.URL.Query().Get("glob"))
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		s.json(w, r, http.StatusOK, map[string]any{"files": list})
	case rest[0] == "files" && len(rest) > 1:
		p := strings.Join(rest[1:], "/")
		if !ext.ValidPath(p) || !e.Owns(p) {
			s.fail(w, r, http.StatusNotFound, "not an owned file")
			return
		}
		s.extFile(w, r, files, p)
	case is(rest, "watch") && r.Method == http.MethodPost:
		s.watchExt(e, files)
		s.json(w, r, http.StatusOK, map[string]any{"ok": true, "lease": int(extLease / time.Second)})
	case is(rest, "transcribe") && r.Method == http.MethodPost:
		var b struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || !ext.ValidPath(b.Path) ||
			!e.Owns(b.Path) {
			s.fail(w, r, http.StatusBadRequest, "path is an owned audio file")
			return
		}
		job, err := files.transcribe(r.Context(), b.Path)
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		s.json(w, r, http.StatusAccepted, map[string]string{"job": job.ID})
	case len(rest) == 2 && rest[0] == "jobs" && get:
		job, err := files.job(r.Context(), rest[1])
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		s.json(w, r, http.StatusOK, job)
	case is(rest, "history") && get:
		p := r.URL.Query().Get("path")
		if p != "" && (!ext.ValidPath(p) || !e.Owns(p)) {
			s.fail(w, r, http.StatusNotFound, "not an owned file")
			return
		}
		edits, err := files.history(r.Context(), p)
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		if edits == nil {
			edits = []ext.Edit{}
		}
		s.json(w, r, http.StatusOK, map[string]any{"edits": edits})
	case is(rest, "history", "undo") && r.Method == http.MethodPost:
		var b struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || b.ID == "" ||
			!ext.ValidPath(b.Path) || !e.Owns(b.Path) {
			s.fail(w, r, http.StatusBadRequest, "undo needs an edit's id and its owned file")
			return
		}
		fi, err := files.undo(r.Context(), b.ID, b.Path)
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		s.json(w, r, http.StatusOK, fi)
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}

// ServeStyles answers ext/styles.css: one layer per extension, after the built-in CSS and before the theme.
func ServeStyles(w http.ResponseWriter, r *http.Request, set *ext.Set) {
	body := set.StylesCSS()
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // our own @import lines, as text/css with nosniff
	}
}

// extFile is GET, PUT and DELETE api/ext/<id>/files/<path>.
func (s *Server) extFile(w http.ResponseWriter, r *http.Request, files extFiles, p string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		files.serve(w, r, p)
	case http.MethodPut:
		if r.ContentLength > ext.MaxWrite {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "a file is 25 MiB at most")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ext.MaxWrite))
		if err != nil {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "a file is 25 MiB at most")
			return
		}
		fi, err := files.write(r.Context(), p, body, ifMatch(r))
		if err != nil {
			s.extFailed(w, r, err)
			return
		}
		s.json(w, r, http.StatusOK, fi)
	case http.MethodDelete:
		if err := files.remove(r.Context(), p, ifMatch(r)); err != nil {
			s.extFailed(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	default:
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ifMatch is the If-Match header without its quotes ("*" stays).
func ifMatch(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	v = strings.TrimPrefix(v, "W/")
	return strings.Trim(v, `"`)
}

// extFailed answers an owned-file error.
func (s *Server) extFailed(w http.ResponseWriter, r *http.Request, err error) {
	var changed *ext.ChangedError
	var he *extHTTPError
	switch {
	case errors.As(err, &changed):
		s.json(w, r, http.StatusConflict, map[string]string{"error": "changed", "sha256": changed.SHA256})
	case errors.As(err, &he):
		s.json(w, r, he.status, json.RawMessage(he.body))
	case errors.Is(err, ext.ErrTooLarge):
		s.fail(w, r, http.StatusRequestEntityTooLarge, "a file is 25 MiB at most")
	case errors.Is(err, ext.ErrNotFound), errors.Is(err, ext.ErrNotOwned), errors.Is(err, errMissing):
		s.fail(w, r, http.StatusNotFound, "no such file")
	case errors.Is(err, errOffline), errors.Is(err, errNoAnswer):
		w.Header().Set("Retry-After", "3")
		s.fail(w, r, http.StatusServiceUnavailable, errOffline.Error())
	case errors.Is(err, errNoJob):
		s.fail(w, r, http.StatusNotFound, "no such job")
	default:
		_, _ = fmt.Fprintf(s.opts.Log, "retake: extension files: %v\n", err)
		s.fail(w, r, http.StatusInternalServerError, "couldn't do that with the file")
	}
}

var errNoJob = errors.New("no such job")

// extHTTPError is an answer from the editing computer to pass on as it is.
type extHTTPError struct {
	status int
	body   []byte
}

func (e *extHTTPError) Error() string { return fmt.Sprintf("editing machine answered %d", e.status) }

// extFiles is one extension's owned files of this video: on this computer's disk, or on the editing computer.
type extFiles interface {
	list(ctx context.Context, glob string) ([]ext.FileInfo, error)
	serve(w http.ResponseWriter, r *http.Request, path string)
	write(ctx context.Context, path string, body []byte, ifMatch string) (ext.FileInfo, error)
	remove(ctx context.Context, path, ifMatch string) error
	history(ctx context.Context, path string) ([]ext.Edit, error)
	undo(ctx context.Context, id, path string) (ext.FileInfo, error)
	transcribe(ctx context.Context, path string) (extwork.Job, error)
	job(ctx context.Context, id string) (extwork.Job, error)
}

// extFiles returns where e's files of this video are.
func (s *Server) extFiles(e *ext.Ext) extFiles {
	if s.opts.RemoteFiles {
		return &relayFiles{s: s, ext: e.ID}
	}
	return &diskFiles{s: s, folder: ext.Folder{Root: s.root, Owns: e.Files.Owns}}
}

// diskFiles is the local case: the video's folder is on this computer.
type diskFiles struct {
	s      *Server
	folder ext.Folder
}

func (d *diskFiles) list(_ context.Context, glob string) ([]ext.FileInfo, error) {
	return d.folder.List(glob)
}

func (d *diskFiles) serve(w http.ResponseWriter, r *http.Request, p string) {
	f, fi, err := d.folder.Open(p)
	if err != nil {
		d.s.extFailed(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", extType(p))
	h.Set("ETag", `"`+fi.SHA256+`"`)
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.UnixMilli(fi.Modified), f)
}

func (d *diskFiles) write(_ context.Context, p string, body []byte, ifMatch string) (ext.FileInfo, error) {
	return d.folder.Write(p, bytes.NewReader(body), ifMatch)
}

func (d *diskFiles) remove(_ context.Context, p, ifMatch string) error {
	return d.folder.Remove(p, ifMatch)
}

func (d *diskFiles) history(_ context.Context, p string) ([]ext.Edit, error) {
	return d.folder.History(p)
}

func (d *diskFiles) undo(_ context.Context, id, p string) (ext.FileInfo, error) {
	return d.folder.Undo(id, p)
}

func (d *diskFiles) transcribe(ctx context.Context, p string) (extwork.Job, error) {
	return d.s.extJobs().Start(ctx, d.folder, p)
}

func (d *diskFiles) job(_ context.Context, id string) (extwork.Job, error) {
	j, ok := d.s.extJobs().Get(id)
	if !ok {
		return j, errNoJob
	}
	return j, nil
}

// extJobs is this server's transcriptions of owned files, made on first use. Their states go out as "job" events.
func (s *Server) extJobs() *extwork.Jobs {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	if s.jobsExt == nil {
		s.jobsExt = &extwork.Jobs{Transcriber: s.opts.FileTranscriber, Changed: func(j extwork.Job) {
			if st, err := s.store(true); err == nil {
				_ = st.Emit(project.EventJob, map[string]any{"id": j.ID, "kind": j.Kind, "state": j.State,
					"error": j.Error, "hint": j.Hint, "path": j.Path})
			}
		}}
	}
	return s.jobsExt
}

// extType is an owned file's content type, by its extension.
func extType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".webm":
		return "audio/webm"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	}
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(p))); t != "" && !strings.Contains(t, "html") &&
		!strings.Contains(t, "javascript") && !strings.Contains(t, "svg") {
		return t
	}
	return "application/octet-stream"
}

// relayFiles is the hosted case: the files are on the editing computer, asked through its worker.
type relayFiles struct {
	s   *Server
	ext string
}

// worker is the editing computer that has the video's folder: the one that sent its versions, else the chosen one.
func (rf *relayFiles) worker() (string, error) {
	st, err := rf.s.store(false)
	if err != nil {
		return "", errOffline
	}
	a, err := st.Automation()
	if err != nil {
		return "", err
	}
	id := a.FileSource()
	if id == "" {
		id = a.Config.Worker
	}
	if m := a.Worker(id); m == nil || m.Seen+int64(project.WorkerLease.Seconds()) <= st.Now().Unix() {
		return "", errOffline
	}
	return id, nil
}

// do sends one ext.* request and reads its answer (at most 8 MiB). An answer other than want is an error.
func (rf *relayFiles) do(ctx context.Context, req relay.Request, out any, want ...int) error {
	id, err := rf.worker()
	if err != nil {
		return err
	}
	req.Ext = rf.ext
	reply, err := rf.s.mediaRelay.Fetch(ctx, id, req)
	if err != nil {
		return fmt.Errorf("%w: %w", errNoAnswer, err)
	}
	defer reply.Close()
	body, err := io.ReadAll(io.LimitReader(reply.Body, relay.Chunk))
	if err != nil {
		return fmt.Errorf("%w: %w", errNoAnswer, err)
	}
	if !slices.Contains(want, reply.Status) {
		switch reply.Status {
		case http.StatusNotFound:
			return ext.ErrNotFound
		case http.StatusRequestEntityTooLarge:
			return ext.ErrTooLarge
		case http.StatusConflict:
			var c struct {
				SHA256 string `json:"sha256"`
			}
			_ = json.Unmarshal(body, &c)
			return &ext.ChangedError{SHA256: c.SHA256}
		}
		if json.Valid(body) && len(body) > 0 {
			return &extHTTPError{status: reply.Status, body: body}
		}
		return fmt.Errorf("%w: status %d", errNoAnswer, reply.Status)
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (rf *relayFiles) list(ctx context.Context, glob string) ([]ext.FileInfo, error) {
	var out struct {
		Files []ext.FileInfo `json:"files"`
	}
	err := rf.do(ctx, relay.Request{Op: "ext.list", Glob: glob}, &out, http.StatusOK)
	return out.Files, err
}

func (rf *relayFiles) serve(w http.ResponseWriter, r *http.Request, p string) {
	var fi ext.FileInfo
	if err := rf.do(r.Context(), relay.Request{Op: "ext.stat", Path: p}, &fi, http.StatusOK); err != nil {
		rf.s.extFailed(w, r, err)
		return
	}
	id, err := rf.worker()
	if err != nil {
		rf.s.extFailed(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+fi.SHA256+`"`)
	rf.s.relayMedia(w, r, mediaTarget{worker: id, req: relay.Request{Op: "ext.read", Ext: rf.ext, Path: p},
		ctype: extType(p), cache: "no-cache"})
}

func (rf *relayFiles) write(ctx context.Context, p string, body []byte, ifMatch string) (ext.FileInfo, error) {
	var fi ext.FileInfo
	err := rf.do(ctx, relay.Request{Op: "ext.write", Path: p, IfMatch: ifMatch, HasBody: true, Body: body}, &fi,
		http.StatusOK)
	return fi, err
}

func (rf *relayFiles) remove(ctx context.Context, p, ifMatch string) error {
	return rf.do(ctx, relay.Request{Op: "ext.delete", Path: p, IfMatch: ifMatch}, nil, http.StatusNoContent,
		http.StatusOK)
}

func (rf *relayFiles) history(ctx context.Context, p string) ([]ext.Edit, error) {
	var out struct {
		Edits []ext.Edit `json:"edits"`
	}
	err := rf.do(ctx, relay.Request{Op: "ext.history", Path: p}, &out, http.StatusOK)
	return out.Edits, err
}

func (rf *relayFiles) undo(ctx context.Context, id, p string) (ext.FileInfo, error) {
	var fi ext.FileInfo
	err := rf.do(ctx, relay.Request{Op: "ext.undo", Path: p, Edit: id}, &fi, http.StatusOK)
	return fi, err
}

func (rf *relayFiles) transcribe(ctx context.Context, p string) (extwork.Job, error) {
	var j extwork.Job
	err := rf.do(ctx, relay.Request{Op: "ext.transcribe", Path: p}, &j, http.StatusAccepted, http.StatusOK)
	return j, err
}

func (rf *relayFiles) job(ctx context.Context, id string) (extwork.Job, error) {
	var j extwork.Job
	err := rf.do(ctx, relay.Request{Op: "ext.job", Job: id}, &j, http.StatusOK)
	if errors.Is(err, ext.ErrNotFound) {
		return j, errNoJob
	}
	return j, err
}

// --- the watch (spec/extensions.md, "Files it owns"): while a page holds a lease, the files are compared every 2 s
// and the changes go out as "ext.files" events.

// extLease is how long one watch request keeps the files watched.
const extLease = 30 * time.Second

// extWatch is one extension's watch of this video's files.
type extWatch struct {
	until time.Time
	last  map[string]ext.FileInfo // nil before the first look
}

// watchExt takes or renews the lease on e's files, starting the comparing when it isn't running.
func (s *Server) watchExt(e *ext.Ext, files extFiles) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	if s.watches == nil {
		s.watches = map[string]*extWatch{}
	}
	w := s.watches[e.ID]
	if w != nil {
		w.until = time.Now().Add(extLease)
		return
	}
	w = &extWatch{until: time.Now().Add(extLease)}
	s.watches[e.ID] = w
	go s.watchLoop(e.ID, w, files)
}

func (s *Server) watchLoop(id string, w *extWatch, files extFiles) {
	every := s.opts.ExtWatchEvery
	if every <= 0 {
		every = 2 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		s.extMu.Lock()
		over := time.Now().After(w.until)
		if over {
			delete(s.watches, id)
		}
		s.extMu.Unlock()
		if over {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		list, err := files.list(ctx, "")
		cancel()
		if err == nil {
			s.compareFiles(id, w, list)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// compareFiles sends what changed since the last look (nothing on the first).
func (s *Server) compareFiles(id string, w *extWatch, list []ext.FileInfo) {
	now := make(map[string]ext.FileInfo, len(list))
	for _, f := range list {
		now[f.Path] = f
	}
	if w.last == nil {
		w.last = now
		return
	}
	var changed []ext.FileInfo
	for _, f := range list {
		if old, ok := w.last[f.Path]; !ok || old.SHA256 != f.SHA256 {
			changed = append(changed, f)
		}
	}
	for p, f := range w.last {
		if _, ok := now[p]; !ok {
			f.Deleted = true
			changed = append(changed, f)
		}
	}
	w.last = now
	if len(changed) == 0 {
		return
	}
	slices.SortFunc(changed, func(a, b ext.FileInfo) int { return strings.Compare(a.Path, b.Path) })
	if st, err := s.store(true); err == nil {
		_ = st.Emit("ext.files", map[string]any{"ext": id, "files": changed})
	}
}

// machineHas reports whether the editing computer of this video has an extension (api/ext's "machine"). Locally the
// worker is this process, so it does.
func (s *Server) machineHas(id string) bool {
	if !s.opts.RemoteFiles {
		return true
	}
	st, err := s.store(false)
	if err != nil {
		return false
	}
	a, err := st.Automation()
	if err != nil {
		return false
	}
	w := a.Worker(a.FileSource())
	if w == nil {
		w = a.Worker(a.Config.Worker)
	}
	return w != nil && slices.ContainsFunc(w.Extensions, func(x project.ExtRef) bool { return x.ID == id })
}

// --- typed comments: POST api/notes (spec/extensions.md, "Comments and questions on an extension's version", and
// spec/notes.md, "Typed comments on a video").

// addNote is POST api/notes: a typed comment, on a version of an extension's kind (anchored) or on a video (at a time,
// with its pen lines and maybe a still of its moment).
func (s *Server) addNote(w http.ResponseWriter, r *http.Request, body []byte) {
	var b struct {
		Version string          `json:"version"`
		Text    string          `json:"text"`
		Anchor  *project.Anchor `json:"anchor"`
		Wants   string          `json:"wants"`
		T       *float64        `json:"t"`
		Strokes []struct {
			Color  string       `json:"color"`
			Points [][2]float64 `json:"points"`
			T      float64      `json:"t"`
		} `json:"strokes"`
		Frame    *[2]float64 `json:"frame"`
		Snapshot string      `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	v, ok := s.version(b.Version)
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "no such version")
		return
	}
	in := project.TypedNote{Version: v.Label, Text: b.Text, Author: s.authorOf(r), Anchor: b.Anchor, Wants: b.Wants}
	if v.Kind == "" {
		if b.T == nil {
			s.fail(w, r, http.StatusBadRequest, "a comment on a video needs its time (t)")
			return
		}
		if d := v.Duration; d != nil && *b.T > float64(*d)+1 {
			s.fail(w, r, http.StatusBadRequest, "t is past the end of "+v.Label)
			return
		}
		if len(b.Strokes) > project.MaxTypedStrokes {
			s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("a comment has at most %d lines", project.MaxTypedStrokes))
			return
		}
		fw, fh := 1920.0, 1080.0
		if f := b.Frame; f != nil && f[0] >= 1 && f[1] >= 1 && f[0] <= 1<<15 && f[1] <= 1<<15 {
			fw, fh = f[0], f[1]
		}
		in.Video, in.At = true, *b.T
		for i, st := range b.Strokes {
			if len(st.Points) == 0 || len(st.Points) > maxTypedPoints {
				s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("line %d: 1 to %d points", i+1, maxTypedPoints))
				return
			}
			_, g := notes.Describe(st.Points, fw, fh)
			in.Strokes = append(in.Strokes, project.Stroke{Shape: g.Shape, Color: st.Color,
				Box:    [4]float64{g.BBox[0], g.BBox[1], g.BBox[2] - g.BBox[0], g.BBox[3] - g.BBox[1]},
				Points: project.FewerPoints(st.Points), T: math.Round(st.T*1e3) / 1e3})
		}
		if b.Snapshot != "" {
			img, ext, err := stillOf(b.Snapshot)
			if err != nil {
				s.fail(w, r, http.StatusBadRequest, err.Error())
				return
			}
			in.Snapshot, in.SnapshotExt = img, ext
		}
	} else if b.T != nil || len(b.Strokes) > 0 || b.Snapshot != "" {
		s.fail(w, r, http.StatusBadRequest, "a comment on an extension's version has an anchor, not a time or lines")
		return
	}
	st, err := s.store(true)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	n, err := st.AddTyped(in)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	s.json(w, r, http.StatusCreated, project.ItemOf(n))
}

// maxTypedPoints is the most points a typed comment's line may bring; it is kept with at most 64 (FewerPoints).
const maxTypedPoints = 2000

// stillOf reads a typed comment's still: a data:image/jpeg or data:image/png base64 URL whose bytes are that format.
func stillOf(dataURL string) ([]byte, string, error) {
	bad := errors.New("snapshot is a data:image/jpeg or data:image/png base64 URL")
	head, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, "", bad
	}
	var ext string
	var magic []byte
	switch head {
	case "data:image/jpeg;base64":
		ext, magic = ".jpg", []byte{0xff, 0xd8, 0xff}
	case "data:image/png;base64":
		ext, magic = ".png", []byte("\x89PNG\r\n\x1a\n")
	default:
		return nil, "", bad
	}
	if base64.StdEncoding.DecodedLen(len(data)) > project.MaxTypedSnapshot+3 {
		return nil, "", fmt.Errorf("the still is over %d MiB", project.MaxTypedSnapshot>>20)
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || !bytes.HasPrefix(b, magic) {
		return nil, "", bad
	}
	return b, ext, nil
}

// kindVersion is the family's version of an extension kind on this computer's disk, when the video's folder has one.
func (s *Server) kindVersion() (family.Version, bool) {
	set := s.opts.Exts
	kind := set.KindAt(s.root)
	if kind == "" {
		return family.Version{}, false
	}
	_, k, ok := set.Kind(kind)
	if !ok {
		return family.Version{}, false
	}
	return family.KindVersion(s.root, k.Folder, kind), true
}

// extState is the Server's extension state: its watches and its transcriptions.
type extState struct {
	extMu   sync.Mutex
	watches map[string]*extWatch
	jobsExt *extwork.Jobs
}
