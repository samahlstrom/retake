package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/sharing"
	"github.com/samahlstrom/retake/internal/trash"
)

// A video's ⋯ on a local home (spec/home.md, "A video's ⋯"): the address its share links use (spec/team.md, "How a
// link reaches people"), Move to Trash with Undo, and Show in folder.

// FolderShare is how links made in the page reach people from this computer.
type FolderShare struct {
	Cloudflared string        // [share] cloudflared; "" = $PATH
	Off         bool          // [share] tunnel = "off"
	LANAddr     func() string // this computer's local network address; nil = sharing.LANAddr
}

// maxCount is how many files Move to Trash's dialog counts at most.
const maxCount = 200_000

// menuState is the folder's share address, its local network listener and the videos moved to the trash.
type menuState struct {
	sharing *sharing.Local

	lanMu   sync.Mutex
	lanHost string       // <ip>:<port> once the folder answers on the local network
	lanSrv  *http.Server // that listener's server

	trashMu sync.Mutex
	trashed map[string]trash.Moved // by id, for Undo
	leaving map[string]bool        // roots being moved: no project is made for them meanwhile
}

type lanKey struct{}

// initMenus sets up the folder's sharing and trash.
func (f *Folder) initMenus() {
	o := f.opts.Share
	lan := o.LANAddr
	if lan == nil {
		lan = sharing.LANAddr
	}
	f.menus.trashed = map[string]trash.Moved{}
	f.menus.leaving = map[string]bool{}
	f.menus.sharing = sharing.New(sharing.Options{Port: f.opts.Port, Allow: f.opts.AllowHosts, Hosts: f.shareHosts,
		AddHost: func(host string) error {
			st, err := project.OpenHome(f.opts.Dir) // the home store: shareHost knows the tunnel's host from it
			if err != nil {
				return err
			}
			f.storeMu.Lock()
			if f.store == nil {
				f.store = st
			} else {
				_ = st.Close()
				st = f.store
			}
			f.storeMu.Unlock()
			return st.AddHost(host, sharing.KindTunnel, os.Getpid())
		},
		RemoveHost: func(host string) {
			if st := f.homeStore(); st != nil {
				_ = st.RemoveHost(host, os.Getpid())
			}
		},
		Cloudflared: o.Cloudflared, Off: o.Off, LANAddr: lan, Listen: f.listenLAN, Log: f.opts.Log})
}

// shareHosts are the home store's hosts: the running shares of this folder.
func (f *Folder) shareHosts() []project.Host {
	st := f.homeStore()
	if st == nil {
		return nil
	}
	hosts, err := st.Hosts()
	if err != nil {
		return nil
	}
	return hosts
}

// closeMenus stops the tunnel the page started and the local network listener.
func (f *Folder) closeMenus() error {
	var errs []error
	if f.menus.sharing != nil {
		errs = append(errs, f.menus.sharing.Close())
	}
	f.menus.lanMu.Lock()
	srv := f.menus.lanSrv
	f.menus.lanSrv, f.menus.lanHost = nil, ""
	f.menus.lanMu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	return errors.Join(errs...)
}

// listenLAN makes the folder answer on the local network too (spec/team.md, "On the local network") and returns
// <ip>:<port>. A request there is always shared: it needs a token, whatever its Host says.
func (f *Folder) listenLAN() (string, error) {
	m := &f.menus
	m.lanMu.Lock()
	defer m.lanMu.Unlock()
	if m.lanHost != "" {
		return m.lanHost, nil
	}
	lan := f.opts.Share.LANAddr
	if lan == nil {
		lan = sharing.LANAddr
	}
	ip := lan()
	if ip == "" {
		return "", errors.New("this computer has no local network address")
	}
	hp := net.JoinHostPort(ip, strconv.Itoa(f.opts.Port))
	ln, err := net.Listen("tcp", hp)
	if err != nil {
		return "", fmt.Errorf("answering on the local network: %w", err)
	}
	host := strings.ToLower(hp)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Host) != host {
			writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "bad host"})
			return
		}
		f.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), lanKey{}, true)))
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(f.opts.Log, "retake: ", 0)}
	go func() { _ = srv.Serve(ln) }()
	m.lanHost, m.lanSrv = host, srv
	f.logf("answering share links on the local network at http://%s/", hp)
	return hp, nil
}

// viaLAN reports a request that came in on the local network listener.
func viaLAN(r *http.Request) bool { return r.Context().Value(lanKey{}) != nil }

// lanOrigin reports the local network's own origin, for a write from a page opened there.
func (f *Folder) lanOrigin(origin string) bool {
	h, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		return false
	}
	f.menus.lanMu.Lock()
	defer f.menus.lanMu.Unlock()
	return f.menus.lanHost != "" && strings.ToLower(h) == f.menus.lanHost
}

// --- Move to Trash and Show in folder

// isVideoMenu reports the routes of a card's ⋯: api/videos/<name>/trash|reveal, api/trash/<id>/restore.
func isVideoMenu(parts []string) bool {
	return len(parts) == 4 && parts[0] == "api" && (parts[1] == "videos" && (parts[3] == "trash" || parts[3] == "reveal") ||
		parts[1] == "trash" && parts[3] == "restore")
}

// trashView is GET api/videos/<name>/trash: what Move to Trash would move.
type trashView struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Files    int    `json:"files"`
	Versions int    `json:"versions"`
	OK       bool   `json:"ok"`
	Why      string `json:"why,omitempty"`
}

// Why a video can't go to the trash.
const (
	whyTop     = "It's the folder Retake opened."
	whyShared  = "Its folder holds other videos too."
	whyWorking = "The agent is working on this video. Stop it first."
)

// videoMenu answers the routes of isVideoMenu, for the owner (ServeHTTP checked).
func (f *Folder) videoMenu(w http.ResponseWriter, r *http.Request, parts []string) {
	if parts[1] == "trash" {
		if r.Method != http.MethodPost {
			writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		f.restore(w, r, parts[2])
		return
	}
	fp := f.projectNamed(parts[2])
	if fp == nil {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no such project"})
		return
	}
	switch {
	case parts[3] == "reveal" && r.Method == http.MethodPost:
		f.reveal(w, r, fp)
	case parts[3] == "trash" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		writeJSON(w, r, http.StatusOK, f.trashInfo(fp, true))
	case parts[3] == "trash" && r.Method == http.MethodPost:
		f.moveToTrash(w, r, fp)
	default:
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// whyNot says why a video can't go to the trash, "" when it can.
func whyNot(fp *folderProject) string {
	fd := fp.found
	switch {
	case fd.Path == "":
		return whyTop
	case fd.Project == fd.Path:
		return whyShared
	}
	if st := fp.projectStore(); st != nil {
		if a, err := st.Automation(); err == nil {
			for _, t := range a.Tasks {
				if t.State == "running" || t.State == "stopping" {
					return whyWorking
				}
			}
		}
	}
	return ""
}

// trashInfo describes the video's folder; count adds its size and files.
func (f *Folder) trashInfo(fp *folderProject, count bool) trashView {
	fd := fp.found
	v := trashView{Name: fd.Name, Title: fd.Title, Path: f.tildePath(fd.Root), Versions: len(fp.srv.Family())}
	v.Why = whyNot(fp)
	v.OK = v.Why == ""
	if count {
		_ = filepath.WalkDir(fd.Root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // an unreadable folder counts as nothing
			}
			if info, err := d.Info(); err == nil && d.Type().IsRegular() {
				v.Size += info.Size()
			}
			v.Files++
			if v.Files >= maxCount {
				return filepath.SkipAll
			}
			return nil
		})
	}
	return v
}

// tildePath shows a path under the home folder as ~/….
func (f *Folder) tildePath(p string) string {
	homeDir := f.opts.Trash.Home
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	return tilde(p, homeDir)
}

// moveToTrash closes the video's server and store, moves its folder to the trash, and remembers the move for Undo.
func (f *Folder) moveToTrash(w http.ResponseWriter, r *http.Request, fp *folderProject) {
	if why := whyNot(fp); why != "" {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": why})
		return
	}
	root := fp.found.Root
	f.mu.Lock()
	f.menus.leaving[root] = true
	if cur, ok := f.open[root]; ok {
		delete(f.open, root)
		f.mu.Unlock()
		_ = cur.close()
	} else {
		f.mu.Unlock()
	}
	defer func() {
		f.mu.Lock()
		delete(f.menus.leaving, root)
		f.mu.Unlock()
		f.rescan(true)
	}()
	moved, err := trash.Move(root, f.opts.Trash)
	bin := f.opts.Trash.Name() // the Trash, or the Recycle Bin on Windows
	if errors.Is(err, trash.ErrOtherDisk) {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": "Can't move it to the " + bin + " from this disk"})
		return
	}
	if err != nil {
		f.logf("move to trash: %v", err)
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "Couldn't move it to the " + bin})
		return
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	f.menus.trashMu.Lock()
	f.menus.trashed[id] = moved
	f.menus.trashMu.Unlock()
	f.logf("moved %s to the trash: %s", root, moved.To)
	writeJSON(w, r, http.StatusOK, map[string]string{"id": id, "path": f.tildePath(root)})
}

// restore is Undo: the folder goes back where it was.
func (f *Folder) restore(w http.ResponseWriter, r *http.Request, id string) {
	f.menus.trashMu.Lock()
	moved, ok := f.menus.trashed[id]
	f.menus.trashMu.Unlock()
	if !ok {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "nothing to undo"})
		return
	}
	if err := trash.Restore(moved); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeJSON(w, r, http.StatusConflict, map[string]string{"error": "Something else is in its place now"})
			return
		}
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "It isn't in the " + f.opts.Trash.Name() + " any more"})
		return
	}
	f.menus.trashMu.Lock()
	delete(f.menus.trashed, id)
	f.menus.trashMu.Unlock()
	name := ""
	for _, fd := range f.rescan(true) {
		if fd.Root == moved.From {
			name = fd.Name
		}
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "name": name})
}

// reveal shows the video's folder in the file manager of this computer.
func (f *Folder) reveal(w http.ResponseWriter, r *http.Request, fp *folderProject) {
	open := f.opts.Reveal
	if open == nil {
		open = RevealDefault
	}
	if err := open(fp.found.Root); err != nil {
		f.logf("show in folder: %v", err)
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "Couldn't open the folder"})
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
}

// RevealDefault opens a folder in the file manager: RETAKE_REVEAL's program when set (tests), else xdg-open on Linux,
// open -R on macOS, explorer on Windows. It doesn't wait for the file manager.
func RevealDefault(dir string) error {
	name, args := "xdg-open", []string{dir}
	switch {
	case os.Getenv("RETAKE_REVEAL") != "":
		name = os.Getenv("RETAKE_REVEAL")
	case runtime.GOOS == "darwin":
		name, args = "open", []string{"-R", dir}
	case runtime.GOOS == "windows":
		name, args = "explorer", []string{"/select," + dir}
	}
	cmd := exec.Command(name, args...) //nolint:gosec // the platform's opener, on a folder of the home
	if name == "explorer" {
		explorerLine(cmd, dir) // explorer reads its own command line: /select,"<folder>", not Go's quoting
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// leavingRoot reports a project whose folder is on its way to the trash (call with f.mu held).
func (f *Folder) leavingRoot(fd home.Found) bool { return f.menus.leaving[fd.Root] }
