// Package setup is the first-run page of retake start (spec/setup.md): your videos folder, your agent and where it
// works, saved to config.toml, then the folder's home on the same address. Settings (internal/hub) reuses its folder
// list and agent test.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/ui"
	"github.com/samahlstrom/retake/internal/worker"
)

// Page is the setup page's file in the page build.
const Page = "setup.html"

// MaxVideos is where the setup page stops counting a folder's videos.
const MaxVideos = 99

// Options are what the setup server needs. The CLI fills them in.
type Options struct {
	UI         fs.FS  // the page build (setup.html, assets); nil serves the "page not built" stub
	Port       int    // the port it listens on: only 127.0.0.1:<port> and localhost:<port> are answered
	ConfigPath string // config.toml, where the answers go
	Home       string // the home folder (paths show as ~/…)
	GOOS       string // "" = runtime.GOOS
	// Agents are the agents this computer offers now (the worker's profiles, config read again).
	Agents func() []project.AgentProfile
	// Try runs an agent's test (worker.TryAgent).
	Try func(ctx context.Context, id string) worker.TryResult
	// Check reads the login shell's PATH again, before the agents are found again ("Check again"). nil: nothing.
	Check func(ctx context.Context)
	// Find finds a program: herdr and tmux, and ffmpeg (desktop.Find). bundled is the copy next to retake.
	Find func(name string) (path string, bundled bool, err error)
	// Done is called once, after Done saved the answers, with the folder to serve.
	Done func(folder string)
}

// Handler is the setup server's http.Handler.
type Handler struct {
	o      Options
	local  map[string]bool
	origin map[string]bool
	mu     sync.Mutex
	done   bool
	trying map[string]bool
}

// New makes the setup server.
func New(o Options) *Handler {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	h := &Handler{o: o, local: map[string]bool{}, origin: map[string]bool{}, trying: map[string]bool{}}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		hp := host + ":" + strconv.Itoa(o.Port)
		h.local[hp], h.origin["http://"+hp] = true, true
	}
	return h
}

// State is GET api/setup.
type State struct {
	Folder       string                 `json:"folder"`      // shown with ~
	FolderPath   string                 `json:"folder_path"` // absolute
	FolderExists bool                   `json:"folder_exists"`
	Videos       *int                   `json:"videos"` // nil when it isn't a folder
	Agents       []project.AgentProfile `json:"agents"`
	Agent        string                 `json:"agent"`
	OpenIn       string                 `json:"open_in"`
	Places       []Place                `json:"places"`
	FFmpeg       bool                   `json:"ffmpeg"`
	OS           string                 `json:"os"`
	Config       string                 `json:"config"`
}

// Place is one place agents can work.
type Place struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Bundled   bool   `json:"bundled,omitempty"`
}

// Answers are POST api/setup and api/setup/done: only the fields given change.
type Answers struct {
	Folder *string `json:"folder"`
	Agent  *string `json:"agent"`
	OpenIn *string `json:"open_in"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.local[strings.ToLower(r.Host)] || server.Forwarded(r) {
		WriteJSON(w, http.StatusForbidden, map[string]string{"error": "bad host"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Values("Origin"); len(o) > 1 || len(o) == 1 && !h.origin[o[0]] {
			WriteJSON(w, http.StatusForbidden, map[string]string{"error": "bad origin"})
			return
		}
	}
	get, post := r.Method == http.MethodGet || r.Method == http.MethodHead, r.Method == http.MethodPost
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case get && (p == "" || p == "/index.html"):
		h.page(w, r)
	case get && p == "/api/ping":
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true, "setup": true})
	case p == "/api/home":
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "setting up"})
	case get && p == "/api/setup":
		h.state(w)
	case post && p == "/api/setup":
		h.save(w, r, false)
	case get && p == "/api/setup/folders":
		FoldersRoute(w, r, h.o.Home)
	case post && p == "/api/setup/check":
		if h.o.Check != nil {
			h.o.Check(r.Context())
		}
		h.state(w)
	case post && p == "/api/setup/test":
		h.try(w, r)
	case post && p == "/api/setup/done":
		h.save(w, r, true)
	case strings.HasPrefix(p, "/api/"):
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case get:
		h.static(w, r)
	default:
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}

// page is GET /: setup.html.
func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	body := []byte(ui.NotBuiltHTML)
	if h.o.UI != nil {
		if b, err := fs.ReadFile(h.o.UI, Page); err == nil {
			body = b
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// static serves one of the page's files (/assets/...). Hidden files are never served.
func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if h.o.UI == nil || !fs.ValidPath(name) || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	b, err := fs.ReadFile(h.o.UI, name)
	if err != nil {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", server.MediaType(name))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// config reads config.toml now.
func (h *Handler) config() (config.Config, error) {
	if h.o.ConfigPath == "" {
		return config.Config{}, nil
	}
	c, _, err := config.Load(h.o.ConfigPath)
	return c, err
}

func (h *Handler) agents() []project.AgentProfile {
	out := []project.AgentProfile{}
	if h.o.Agents != nil {
		for _, a := range h.o.Agents() {
			out = append(out, project.AgentProfile{ID: a.ID, Name: a.Name})
		}
	}
	return out
}

func (h *Handler) find(name string) (bool, bool) {
	if h.o.Find == nil {
		return false, false
	}
	_, bundled, err := h.o.Find(name)
	return err == nil, err == nil && bundled
}

// places are where agents can work, and the default: herdr when it's there, else the background.
func (h *Handler) places() ([]Place, string) {
	herdr, bundled := h.find("herdr")
	tmux, _ := h.find("tmux")
	ps := []Place{{ID: config.OpenHerdr, Available: herdr, Bundled: bundled}, {ID: config.OpenTmux, Available: tmux},
		{ID: config.OpenBackground, Available: true}}
	if herdr {
		return ps, config.OpenHerdr
	}
	return ps, config.OpenBackground
}

func available(ps []Place, id string) bool {
	return slices.ContainsFunc(ps, func(p Place) bool { return p.ID == id && p.Available })
}

// Suggest is the videos folder to offer first: ~/Movies on a Mac, else ~/Videos when it exists, else the home folder.
func Suggest(homeDir, goos string) string {
	if goos == "darwin" {
		return filepath.Join(homeDir, "Movies")
	}
	if v := filepath.Join(homeDir, "Videos"); isDir(v) {
		return v
	}
	return filepath.Join(homeDir, "Videos")
}

// StateOf is the setup's state now.
func (h *Handler) StateOf() (State, error) {
	c, err := h.config()
	if err != nil {
		return State{}, fmt.Errorf("%s: %w", Tilde(h.o.ConfigPath, h.o.Home), err)
	}
	folder := config.FolderPath(c, h.o.Home)
	if folder == "" {
		folder = Suggest(h.o.Home, h.o.GOOS)
	}
	s := State{Folder: Tilde(folder, h.o.Home), FolderPath: folder, FolderExists: isDir(folder), Agents: h.agents(),
		OS: h.o.GOOS, Config: Tilde(h.o.ConfigPath, h.o.Home)}
	if s.FolderExists {
		n := min(len(home.Discover(resolve(folder))), MaxVideos)
		s.Videos = &n
	}
	if c.DefaultAgent != nil && slices.ContainsFunc(s.Agents, func(a project.AgentProfile) bool { return a.ID == *c.DefaultAgent }) {
		s.Agent = *c.DefaultAgent
	} else if len(s.Agents) > 0 {
		s.Agent = s.Agents[0].ID
	}
	var def string
	s.Places, def = h.places()
	s.OpenIn = def
	if c.OpenAgentsIn != nil && available(s.Places, *c.OpenAgentsIn) {
		s.OpenIn = *c.OpenAgentsIn
	}
	s.FFmpeg, _ = h.find("ffprobe")
	return s, nil
}

func (h *Handler) state(w http.ResponseWriter) {
	s, err := h.StateOf()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, s)
}

// save is POST api/setup (done false) and api/setup/done (done true): check every answer, then write them.
func (h *Handler) save(w http.ResponseWriter, r *http.Request, done bool) {
	var a Answers
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": `give {"folder", "agent", "open_in"}`})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		WriteJSON(w, http.StatusConflict, map[string]string{"error": "setup is done"})
		return
	}
	cur, err := h.StateOf()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	folder := ""
	if a.Folder != nil || done {
		f := cur.FolderPath
		if a.Folder != nil {
			f = *a.Folder
		}
		if folder, err = CheckFolder(f, h.o.Home, done); err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if a.Agent != nil && *a.Agent != "" && !slices.ContainsFunc(cur.Agents, func(p project.AgentProfile) bool { return p.ID == *a.Agent }) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("no agent %q here", *a.Agent)})
		return
	}
	openIn := ""
	if a.OpenIn != nil || done {
		openIn = cur.OpenIn
		if a.OpenIn != nil {
			openIn = *a.OpenIn
		}
		if !config.ValidOpenIn(openIn) || !available(cur.Places, openIn) {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("agents can't work in %q here", openIn)})
			return
		}
	}
	// Valid: write it.
	if h.o.ConfigPath == "" {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "there is no config file to write"})
		return
	}
	if folder != "" {
		if done && !isDir(folder) {
			if err := os.Mkdir(folder, 0o755); err != nil { //nolint:gosec // the folder the person chose for their videos
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "can't make " + Tilde(folder, h.o.Home) + ": " + err.Error()})
				return
			}
		}
		if err := config.SetTop(h.o.ConfigPath, "folder", Tilde(folder, h.o.Home)); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	agent := a.Agent
	if agent == nil && done && cur.Agent != "" {
		agent = &cur.Agent
	}
	if agent != nil {
		if err := config.SetTop(h.o.ConfigPath, "default_agent", *agent); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if openIn != "" {
		v := openIn
		if v == config.OpenBackground {
			v = "" // the default: no line
		}
		if err := config.SetTop(h.o.ConfigPath, "open_agents_in", v); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if done {
		h.done = true
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
		if h.o.Done != nil {
			go h.o.Done(folder) // after the answer is on its way
		}
		return
	}
	s, err := h.StateOf()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, s)
}

// try is POST api/setup/test.
func (h *Handler) try(w http.ResponseWriter, r *http.Request) {
	TryRoute(w, r, h.agents(), h.o.Try, h.trying, &h.mu)
}

// TryRoute is POST …/test {"agent": id}: one test at a time per agent (spec/setup.md, "The agent test").
func TryRoute(w http.ResponseWriter, r *http.Request, offered []project.AgentProfile,
	try func(context.Context, string) worker.TryResult, busy map[string]bool, mu *sync.Mutex) {
	var b struct {
		Agent string `json:"agent"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": `give {"agent"}`})
		return
	}
	if !slices.ContainsFunc(offered, func(a project.AgentProfile) bool { return a.ID == b.Agent }) || try == nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("no agent %q here", b.Agent)})
		return
	}
	mu.Lock()
	if busy[b.Agent] {
		mu.Unlock()
		WriteJSON(w, http.StatusConflict, map[string]string{"error": "already testing it"})
		return
	}
	busy[b.Agent] = true
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(busy, b.Agent)
		mu.Unlock()
	}()
	WriteJSON(w, http.StatusOK, try(r.Context(), b.Agent))
}

// CheckFolder checks a videos folder the person chose: absolute or starting with ~, and a folder (or, when it may be
// made, one whose parent is a folder). It returns the absolute path.
func CheckFolder(p, homeDir string, mayMake bool) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("choose a folder")
	}
	p = fullPath(p, homeDir)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q isn't a full path", p)
	}
	p = filepath.Clean(p)
	if isDir(p) {
		return p, nil
	}
	if _, err := os.Stat(p); err == nil {
		return "", fmt.Errorf("%s isn't a folder", Tilde(p, homeDir))
	}
	if mayMake && isDir(filepath.Dir(p)) {
		return p, nil
	}
	return "", fmt.Errorf("there's no folder %s", Tilde(p, homeDir))
}

// fullPath expands a leading ~ and, on Windows, puts a path rooted on no drive (\Videos, or / for the top) on the home
// folder's drive.
func fullPath(p, homeDir string) string {
	p = config.ExpandHome(p, homeDir)
	if runtime.GOOS == "windows" && filepath.VolumeName(p) == "" && (strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/")) {
		p = filepath.VolumeName(homeDir) + p
	}
	return p
}

// FolderList is GET …/folders.
type FolderList struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent"`
	Folders []string `json:"folders"`
}

// MaxFolders is the most folders a list shows.
const MaxFolders = 500

// ListFolders lists the folders in p (absolute or ~): never a file, never a hidden folder.
func ListFolders(p, homeDir string) (FolderList, error) {
	if strings.TrimSpace(p) == "" {
		p = homeDir
	}
	dir := filepath.Clean(fullPath(strings.TrimSpace(p), homeDir))
	if !filepath.IsAbs(dir) || !isDir(dir) {
		return FolderList{}, fs.ErrNotExist
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return FolderList{}, err
	}
	out := FolderList{Path: Tilde(dir, homeDir), Folders: []string{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = Tilde(parent, homeDir)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !isDir(filepath.Join(dir, name)) || strings.HasSuffix(name, ".review") {
			continue
		}
		out.Folders = append(out.Folders, name)
	}
	slices.SortFunc(out.Folders, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	if len(out.Folders) > MaxFolders {
		out.Folders = out.Folders[:MaxFolders]
	}
	return out, nil
}

// FoldersRoute is GET …/folders?path=P.
func FoldersRoute(w http.ResponseWriter, r *http.Request, homeDir string) {
	l, err := ListFolders(r.URL.Query().Get("path"), homeDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no such folder"})
	case err != nil:
		WriteJSON(w, http.StatusForbidden, map[string]string{"error": "can't read that folder"})
	default:
		WriteJSON(w, http.StatusOK, l)
	}
}

// Tilde shows a path under the home folder with ~.
func Tilde(p, homeDir string) string {
	if homeDir == "" || p == "" {
		return p
	}
	if p == homeDir {
		return "~"
	}
	if rel, err := filepath.Rel(homeDir, p); err == nil && filepath.IsLocal(rel) {
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}

func isDir(p string) bool {
	st, err := os.Stat(p) //nolint:gosec // only whether it's a folder: the person picks any folder of theirs
	return err == nil && st.IsDir()
}

// resolve resolves links, as the home's discovery wants.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// WriteJSON answers with v as JSON.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(append(b, '\n'))
}
