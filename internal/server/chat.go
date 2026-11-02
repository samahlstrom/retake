package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// The chat with the agent (spec/chat.md): the page's routes under api/chat, and the worker's under api/worker/chat.

// chatKind is the video's version of a kind with a chat, its extension and kind; ok is false when it has none.
func (s *Server) chatKind(r *http.Request) (e *ext.Ext, k ext.Kind, v family.Version, ok bool) {
	set := s.extsOf(r)
	for _, fv := range s.Family() {
		if fv.Kind == "" {
			continue
		}
		if e, k, ok = set.Kind(fv.Kind); ok && k.Chat {
			return e, k, fv, true
		}
	}
	return nil, ext.Kind{}, family.Version{}, false
}

// chatRoute serves api/chat, api/chat/stop, api/chat/send and api/chat/files/<message>/<file>.
func (s *Server) chatRoute(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodPost && !s.originOK(r) {
		s.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	st, err := s.store(true)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	get := r.Method != http.MethodPost
	switch {
	case len(p) == 2 && get:
		v, err := st.ChatView()
		if err != nil {
			s.storeFailed(w, r, err)
			return
		}
		if s.opts.LocalWorker != nil {
			v.Blocked = s.inproc.reason()
		}
		s.json(w, r, http.StatusOK, v)
	case len(p) == 2:
		s.chatPost(w, r, st)
	case len(p) == 3 && p[2] == "stop" && !get:
		if err := st.StopChat(); err != nil {
			s.fail(w, r, http.StatusConflict, err.Error())
			return
		}
		s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
	case len(p) == 3 && p[2] == "send" && !get:
		s.chatSend(w, r, st)
	case len(p) == 5 && p[2] == "files" && get:
		s.chatFile(w, r, st, p[3], p[4])
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}

// chatPost is POST api/chat: a person's message.
func (s *Server) chatPost(w http.ResponseWriter, r *http.Request, st *project.Store) {
	var b struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		Voice  string   `json:"voice"`
		Answer int64    `json:"answer"`
		Agent  string   `json:"agent"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&b) != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid message")
		return
	}
	e, _, _, ok := s.chatKind(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "this video has no chat")
		return
	}
	// Images and the recording are the kind's extension's own files (v0/images/…, v0/voice/…).
	for _, path := range append(append([]string(nil), b.Images...), b.Voice) {
		if path != "" && (!ext.ValidPath(path) || !e.Owns(path)) {
			s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("%q isn't one of the video's own files", path))
			return
		}
	}
	m, err := st.PostChat(project.ChatPost{Text: b.Text, By: s.authorOf(r), Images: b.Images, Voice: b.Voice,
		Answer: b.Answer, Agent: offeredAgent(st, b.Agent)})
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	s.json(w, r, http.StatusCreated, map[string]any{"message": m})
}

// chatSend is POST api/chat/send: Send on the chat's version, "I like it, make the video" (spec/chat.md).
func (s *Server) chatSend(w http.ResponseWriter, r *http.Request, st *project.Store) {
	var b struct {
		File  string `json:"file"`
		Agent string `json:"agent"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid request")
		return
	}
	e, k, kv, ok := s.chatKind(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "this video has no chat")
		return
	}
	if !ext.ValidPath(b.File) || !e.Owns(b.File) {
		s.fail(w, r, http.StatusBadRequest, "file is the video's document")
		return
	}
	list, err := s.extFiles(e).list(r.Context(), b.File)
	if err != nil {
		s.extFailed(w, r, err)
		return
	}
	var doc *ext.FileInfo
	for i := range list {
		if list[i].Path == b.File {
			doc = &list[i]
		}
	}
	if doc == nil {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	a, err := st.Automation()
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	agent := offeredAgent(st, b.Agent)
	if a.Chat != nil && a.Chat.Sent != nil && a.Chat.Sent.File == doc.Path && a.Chat.Sent.SHA256 == doc.SHA256 {
		sent := *a.Chat.Sent
		if n, err := st.Note(sent.Note); err == nil {
			if !a.ChatSentOpen(n) {
				s.fail(w, r, http.StatusConflict, "Nothing changed since the last Send")
				return
			}
			// Its comment is open again (a paused batch): send it again, which resumes it.
			if _, err = st.QueueFeedback([]int64{n.ID}, agent); err != nil {
				s.fail(w, r, http.StatusConflict, err.Error())
				return
			}
			s.json(w, r, http.StatusOK, map[string]any{"note": n.ID, "target": n.Target()})
			return
		}
	}
	word := strings.ToLower(k.Label)
	if word == "" {
		word = "document"
	}
	in := project.TypedNote{Version: kv.Label, Author: s.authorOf(r), Wants: project.WantsVersion,
		Text: fmt.Sprintf("Build the first cut (v1) from the %s.", word), Anchor: &project.Anchor{File: doc.Path, Line: 1}}
	if newest := newestCut(s.Family()); newest != "" {
		in = project.TypedNote{Version: newest, Author: s.authorOf(r), Video: true,
			Text: fmt.Sprintf("Bring the video in line with the %s's changes (%s).", word, doc.Path)}
	}
	n, err := st.AddTyped(in)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if _, err = st.QueueFeedback([]int64{n.ID}, agent); err != nil {
		s.fail(w, r, http.StatusConflict, err.Error())
		return
	}
	target := n.Target()
	if err = st.SetChatSent(project.ChatSent{File: doc.Path, SHA256: doc.SHA256, Note: n.ID, Target: target,
		At: time.Now().UnixMilli()}); err != nil {
		s.storeFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, map[string]any{"note": n.ID, "target": target})
}

// newestCut is the label of the family's newest video version (not a draft, not an extension's kind), or "".
func newestCut(fam []family.Version) string {
	best, label := 0, ""
	for _, v := range fam {
		if n := project.LabelNumber(v.Label); v.Kind == "" && n > best {
			best, label = n, v.Label
		}
	}
	return label
}

// chatFile serves one of a chat question's images.
func (s *Server) chatFile(w http.ResponseWriter, r *http.Request, st *project.Store, id, file string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || !project.QuestionFile(file) || file == "frame.jpg" {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	if m, err := st.ChatMessage(n); err != nil || m.Role != project.ChatQuestion {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	root, err := os.OpenRoot(st.ChatDir(n))
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(file)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		s.fail(w, r, http.StatusNotFound, "no such file")
		return
	}
	ext := filepath.Ext(file)
	w.Header().Set("Content-Type", project.ImageTypes[ext])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, file, stat.ModTime(), f)
}

// workerChat serves the worker's chat routes: POST /api/worker/chat (a turn's stream) and /api/worker/chat/ask.
func (s *Server) workerChat(w http.ResponseWriter, r *http.Request, p []string, st *project.Store, worker string) {
	switch {
	case len(p) == 3:
		var b project.ChatProgress
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&b) != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid chat progress")
			return
		}
		stop, err := st.ChatProgress(worker, b)
		if errors.Is(err, project.ErrStaleChat) {
			s.fail(w, r, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, err.Error())
			return
		}
		s.json(w, r, http.StatusOK, map[string]bool{"stop": stop})
	case len(p) == 4 && p[3] == "ask":
		var b project.WorkAsk
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&b) != nil {
			s.fail(w, r, http.StatusBadRequest, "invalid question")
			return
		}
		b.Worker = worker
		q, err := st.AskInChat(b)
		if err != nil {
			s.fail(w, r, http.StatusConflict, err.Error())
			return
		}
		s.json(w, r, http.StatusOK, q)
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}
