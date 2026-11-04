package hub

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

const libraryManifest = "library.json"

type libraryFolder struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Subfolders bool   `json:"subfolders"`
	Available  bool   `json:"available,omitempty"`
}

type libraryState struct {
	Revision   int64             `json:"revision"`
	Folders    []libraryFolder   `json:"folders"`
	Names      map[string]string `json:"names,omitempty"`
	Relative   map[string]string `json:"relative_names,omitempty"`
	Configured bool              `json:"-"`
	LoadErr    error             `json:"-"`
}

type libraryResponse struct {
	Computer struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
	} `json:"computer"`
	NativePicker bool  `json:"native_picker"`
	Revision     int64 `json:"revision"`
	Folders      []struct {
		ID         string `json:"id"`
		Path       string `json:"path"`
		Subfolders bool   `json:"subfolders"`
		Available  bool   `json:"available"`
	} `json:"folders"`
}

func libraryPath(configPath, original string) string {
	if configPath == "" {
		return filepath.Join(original, project.Dir, libraryManifest)
	}
	key := sha256.Sum256([]byte(stablePath(original)))
	return filepath.Join(filepath.Dir(configPath), project.Dir, "library-"+hex.EncodeToString(key[:8])+".json")
}

func (f *Folder) libraryPath() string {
	path := ""
	if f.opts.Settings != nil {
		path = f.opts.Settings.ConfigPath
	}
	return libraryPath(path, f.opts.Dir)
}

// HasLibrary reports whether the selected library has persisted folder links.
func HasLibrary(configPath, original string) bool {
	_, err := os.Stat(libraryPath(configPath, original))
	return err == nil
}

func stablePath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/private/tmp", "/private/var", "/private/etc"} {
			if p == alias || strings.HasPrefix(p, alias+"/") {
				return strings.TrimPrefix(p, "/private")
			}
		}
	}
	return p
}

func cloneLibrary(s libraryState) libraryState {
	c := s
	c.Folders = slices.Clone(s.Folders)
	c.Names, c.Relative = maps.Clone(s.Names), maps.Clone(s.Relative)
	return c
}

func (f *Folder) loadLibrary() libraryState {
	b, err := os.ReadFile(f.libraryPath())
	if errors.Is(err, os.ErrNotExist) {
		return libraryState{Folders: []libraryFolder{}, Names: map[string]string{}, Relative: map[string]string{}}
	}
	if err != nil {
		return invalidLibrary(err)
	}
	var s libraryState
	if err := json.Unmarshal(b, &s); err != nil {
		return invalidLibrary(err)
	}
	if s.Revision < 1 || s.Folders == nil {
		return invalidLibrary(errors.New("missing revision or folders"))
	}
	s.Configured = true
	if s.Names == nil {
		s.Names = map[string]string{}
	}
	if s.Relative == nil {
		s.Relative = map[string]string{}
	}
	return s
}

func invalidLibrary(err error) libraryState {
	return libraryState{Configured: true, LoadErr: err, Folders: []libraryFolder{}, Names: map[string]string{}, Relative: map[string]string{}}
}

func (f *Folder) saveLibrary(s libraryState) error {
	s.Configured = false
	if s.Folders == nil {
		s.Folders = []libraryFolder{}
	}
	for i := range s.Folders {
		s.Folders[i].Available = false
	}
	if err := os.MkdirAll(filepath.Dir(f.libraryPath()), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := f.libraryPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.libraryPath())
}

func (f *Folder) librarySnapshot() libraryState {
	f.libraryMu.Lock()
	defer f.libraryMu.Unlock()
	return cloneLibrary(f.library)
}

func foldersOf(s libraryState, original string) []libraryFolder {
	if s.Configured || s.LoadErr != nil {
		return slices.Clone(s.Folders)
	}
	return []libraryFolder{{ID: "original", Path: original, Subfolders: true}}
}

func readableDir(dir string) bool { _, err := os.ReadDir(dir); return err == nil }

func (f *Folder) libraryResponse() libraryResponse {
	s := f.librarySnapshot()
	var v libraryResponse
	v.Computer.Name, _ = os.Hostname()
	if v.Computer.Name == "" {
		v.Computer.Name = "this computer"
	}
	v.Computer.Platform, v.NativePicker, v.Revision = runtime.GOOS, f.opts.NativePicker != nil, s.Revision
	links := foldersOf(s, f.opts.Dir)
	v.Folders = make([]struct {
		ID         string `json:"id"`
		Path       string `json:"path"`
		Subfolders bool   `json:"subfolders"`
		Available  bool   `json:"available"`
	}, 0, len(links))
	for _, link := range links {
		v.Folders = append(v.Folders, struct {
			ID         string `json:"id"`
			Path       string `json:"path"`
			Subfolders bool   `json:"subfolders"`
			Available  bool   `json:"available"`
		}{link.ID, link.Path, link.Subfolders, readableDir(link.Path)})
	}
	return v
}

func (f *Folder) libraryError() error { return f.librarySnapshot().LoadErr }

func (f *Folder) libraryUnavailable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "linked-folder library cannot be read; repair its manifest before changing folders"})
}

func (f *Folder) libraryRoute(w http.ResponseWriter, r *http.Request, shared bool, role team.Role, pick bool) {
	if shared || role != team.Owner {
		writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "only the local owner manages linked folders"})
		return
	}
	if f.libraryError() != nil {
		f.libraryUnavailable(w, r)
		return
	}
	if pick {
		if r.Method != http.MethodPost || f.opts.NativePicker == nil {
			writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		f.pickerMu.Lock()
		if f.picking {
			f.pickerMu.Unlock()
			writeJSON(w, r, http.StatusConflict, map[string]string{"error": "folder picker is already open"})
			return
		}
		f.picking = true
		f.pickerMu.Unlock()
		paths, err := f.opts.NativePicker(r.Context())
		f.pickerMu.Lock()
		f.picking = false
		f.pickerMu.Unlock()
		if err != nil {
			writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, r, http.StatusOK, map[string][]string{"paths": paths})
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, r, http.StatusOK, f.libraryResponse())
	case http.MethodPost:
		f.replaceLibrary(w, r)
	default:
		writeJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}

type libraryChange struct {
	Revision *int64          `json:"revision"`
	Folders  json.RawMessage `json:"folders"`
}

func (f *Folder) replaceLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryChange
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() || req.Revision == nil || *req.Revision < 0 || req.Folders == nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "give revision and folders"})
		return
	}
	var input []libraryFolder
	if string(req.Folders) != "null" {
		folders := json.NewDecoder(bytes.NewReader(req.Folders))
		folders.DisallowUnknownFields()
		if err := folders.Decode(&input); err != nil || folders.More() {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "folders must be an array"})
			return
		}
	}
	f.libraryChangeMu.Lock()
	defer f.libraryChangeMu.Unlock()
	old := f.librarySnapshot()
	if old.LoadErr != nil {
		f.libraryUnavailable(w, r)
		return
	}
	if *req.Revision != old.Revision {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": "library changed; reload and try again"})
		return
	}
	links, err := f.validLibrary(input, old)
	if err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	next := cloneLibrary(old)
	next.Configured, next.Folders, next.Revision = true, links, old.Revision+1
	candidate := f.scanState(&next)
	if f.removesBusyProject(candidate.Videos) {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": "a linked folder has an edit in progress"})
		return
	}
	if err := f.saveLibrary(next); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "saving linked folders"})
		return
	}
	f.libraryMu.Lock()
	f.library = next
	f.libraryMu.Unlock()
	f.mu.Lock()
	f.found, f.scanned, f.took = candidate.Videos, f.opts.Now(), candidate.Took
	f.mu.Unlock()
	f.releaseMissing()
	writeJSON(w, r, http.StatusOK, f.libraryResponse())
}

func (f *Folder) validLibrary(input []libraryFolder, old libraryState) ([]libraryFolder, error) {
	if len(input) > 128 {
		return nil, errors.New("too many linked folders")
	}
	known := map[string]libraryFolder{}
	for _, link := range foldersOf(old, f.opts.Dir) {
		known[link.ID] = link
	}
	seenID, byReal := map[string]bool{}, map[string]int{}
	links := make([]libraryFolder, 0, len(input))
	for _, in := range input {
		if in.ID != "" && (seenID[in.ID] || known[in.ID].ID == "") {
			return nil, errors.New("unknown or duplicate linked-folder id")
		}
		p, resolved, err := f.cleanLibraryPath(in.Path, in.ID, known)
		if err != nil {
			return nil, err
		}
		if i, ok := byReal[resolved]; ok {
			if !isSymlink(p) && isSymlink(links[i].Path) {
				links[i].Path = p
			}
			continue
		}
		if in.ID == "" {
			in.ID = libraryID()
		}
		in.Path, in.Available = p, false
		seenID[in.ID], byReal[resolved] = true, len(links)
		links = append(links, in)
	}
	for i := range links {
		for j := range links[:i] {
			if pathWithin(stablePath(links[i].Path), stablePath(links[j].Path)) || pathWithin(stablePath(links[j].Path), stablePath(links[i].Path)) {
				return nil, errors.New("linked folders cannot overlap")
			}
		}
	}
	return links, nil
}

func (f *Folder) cleanLibraryPath(p, id string, known map[string]libraryFolder) (string, string, error) {
	if strings.HasPrefix(p, "~/") {
		if f.opts.Settings == nil || f.opts.Settings.Home == "" {
			return "", "", errors.New("home-relative path is unavailable")
		}
		p = filepath.Join(f.opts.Settings.Home, p[2:])
	}
	if p == "" {
		return "", "", errors.New("folder path is required")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(f.opts.Dir, p)
	}
	p = filepath.Clean(p)
	resolved := stablePath(p)
	if readableDir(p) {
		return p, resolved, nil
	}
	if old, ok := known[id]; ok && stablePath(old.Path) == resolved {
		return old.Path, resolved, nil
	}
	return "", "", fmt.Errorf("%q is not a readable folder", p)
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func libraryID() string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("folder-%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}

func (f *Folder) scanState(s *libraryState) home.Scan {
	if !s.Configured {
		return home.ScanDir(f.opts.Dir, f.opts.Exts.KindAt)
	}
	links, groups := foldersOf(*s, f.opts.Dir), libraryGroups(foldersOf(*s, f.opts.Dir))
	all := home.Scan{Videos: []home.Found{}, Empty: []home.Found{}}
	for _, link := range links {
		if !readableDir(link.Path) {
			continue
		}
		scan := home.ScanLinkedDir(link.Path, link.Subfolders, f.opts.Exts.KindAt)
		all.Dirs, all.Took, all.Capped = all.Dirs+scan.Dirs, all.Took+scan.Took, all.Capped || scan.Capped
		for _, fd := range slices.Concat(scan.Videos, scan.Empty) {
			fd.Path, fd.Project = joinLibraryPath(groups[link.ID], fd.Path), joinLibraryPath(groups[link.ID], fd.Project)
			if fd.Anchor == "" {
				all.Empty = append(all.Empty, fd)
			} else {
				all.Videos = append(all.Videos, fd)
			}
		}
	}
	f.assignNames(s, links, &all)
	return all
}

func libraryGroups(links []libraryFolder) map[string]string {
	ordered := slices.Clone(links)
	slices.SortFunc(ordered, func(a, b libraryFolder) int { return strings.Compare(a.ID, b.ID) })
	used, out := map[string]bool{}, map[string]string{}
	for _, link := range ordered {
		base := filepath.Base(filepath.Clean(link.Path))
		if base == string(filepath.Separator) {
			base = "root"
		}
		name := base
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name], out[link.ID] = true, name
	}
	return out
}

func joinLibraryPath(root, child string) string {
	if child == "" {
		return root
	}
	return root + "/" + child
}

func (f *Folder) assignNames(s *libraryState, links []libraryFolder, scan *home.Scan) {
	if s.Names == nil {
		s.Names = map[string]string{}
	}
	if s.Relative == nil {
		s.Relative = map[string]string{}
	}
	groups, linkByGroup := libraryGroups(links), map[string]libraryFolder{}
	for _, link := range links {
		linkByGroup[groups[link.ID]] = link
	}
	reserved, used := map[string]bool{}, map[string]bool{}
	for _, name := range s.Names {
		reserved[name] = true
	}
	for _, name := range s.Relative {
		reserved[name] = true
	}
	entries := slices.Concat(scan.Videos, scan.Empty)
	slices.SortFunc(entries, func(a, b home.Found) int { return strings.Compare(stablePath(a.Root), stablePath(b.Root)) })
	names := make(map[string]string, len(entries))
	for _, fd := range entries {
		group, rel, _ := strings.Cut(fd.Path, "/")
		link, ok := linkByGroup[group]
		if !ok {
			continue
		}
		key, root := link.ID+"\x00"+rel, stablePath(fd.Root)
		name := names[root]
		if name == "" {
			name = s.Names[root]
		}
		if name == "" {
			name = s.Relative[key]
		}
		if name == "" {
			base, candidate := fd.Name, fd.Name
			for n := 2; reserved[candidate] || used[candidate]; n++ {
				candidate = fmt.Sprintf("%s-%d", base, n)
			}
			name, reserved[name] = candidate, true
		}
		names[root], s.Names[root], s.Relative[key] = name, name, name
		used[name] = true
	}
	for i := range scan.Videos {
		scan.Videos[i].Name = names[stablePath(scan.Videos[i].Root)]
	}
	for i := range scan.Empty {
		scan.Empty[i].Name = names[stablePath(scan.Empty[i].Root)]
	}
}

func (f *Folder) scanLibraryLocked() home.Scan {
	s := f.librarySnapshot()
	scan := f.scanState(&s)
	if !s.Configured {
		for _, fd := range slices.Concat(scan.Videos, scan.Empty) {
			s.Names[stablePath(fd.Root)] = fd.Name
		}
	}
	if s.Configured {
		old := f.librarySnapshot()
		if !sameNames(old, s) && f.saveLibrary(s) == nil {
			f.libraryMu.Lock()
			f.library.Names, f.library.Relative = s.Names, s.Relative
			f.libraryMu.Unlock()
		}
	} else {
		f.libraryMu.Lock()
		f.library.Names, f.library.Relative = s.Names, s.Relative
		f.libraryMu.Unlock()
	}
	return scan
}

func sameNames(a, b libraryState) bool {
	return maps.Equal(a.Names, b.Names) && maps.Equal(a.Relative, b.Relative)
}

func (f *Folder) removesBusyProject(next []home.Found) bool {
	kept := map[string]bool{}
	for _, fd := range next {
		kept[stablePath(fd.Root)] = true
	}
	for _, fd := range f.currentFound() {
		if kept[stablePath(fd.Root)] {
			continue
		}
		f.mu.Lock()
		fp := f.open[fd.Root]
		f.mu.Unlock()
		if fp != nil && fp.srv.Busy() {
			return true
		}
		st, err := project.OpenExisting(fd.Root)
		if err != nil {
			continue
		}
		a, err := st.Automation()
		_ = st.Close()
		if err == nil && slices.ContainsFunc(a.Tasks, func(task project.EditingTask) bool {
			return task.State != "done" && task.State != "cancelled" && task.State != "failed" && task.State != "paused"
		}) {
			return true
		}
	}
	return false
}

func (f *Folder) releaseMissing() {
	current := map[string]bool{}
	for _, fd := range f.currentFound() {
		current[fd.Root] = true
	}
	f.mu.Lock()
	var closing []*folderProject
	for root, fp := range f.open {
		if !current[root] {
			delete(f.open, root)
			closing = append(closing, fp)
		}
	}
	f.mu.Unlock()
	for _, fp := range closing {
		_ = fp.close()
	}
}

func (f *Folder) legacyProject(fd home.Found) bool {
	return pathWithin(stablePath(fd.Root), stablePath(f.opts.Dir))
}
func pathWithin(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (f *Folder) currentFound() []home.Found {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.found)
}

func (f *Folder) libraryTreeProjects() []string {
	s := f.librarySnapshot()
	if !s.Configured {
		return nil
	}
	links := foldersOf(s, f.opts.Dir)
	groups := libraryGroups(links)
	projects := make([]string, 0, len(links))
	for _, link := range links {
		projects = append(projects, groups[link.ID])
	}
	return projects
}
