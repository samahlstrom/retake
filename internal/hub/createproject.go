package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/download"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/team"
)

const maxProjectUpload = int64(50 << 30)

// createProject is the local Home's one-way import: a browser streams a copy into
// a linked root, never names a path for Retake to read or alter.
func (f *Folder) createProject(w http.ResponseWriter, r *http.Request, shared bool, role team.Role) {
	if shared || role != team.Owner || r.Header.Get("Authorization") != "" || hasShareCookie(r) {
		writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "only the local owner creates projects"})
		return
	}
	f.libraryChangeMu.Lock()
	defer f.libraryChangeMu.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectUpload)
	if err := r.ParseMultipartForm(32 << 10); err != nil { //nolint:gosec // MaxBytesReader bounds the complete request.
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "send a project form"})
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	root, code, err := f.projectRoot(r)
	if err != nil {
		writeJSON(w, r, code, map[string]string{"error": err.Error()})
		return
	}
	name, source, err := readProjectForm(r, root)
	if err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer func() {
		if source != "" {
			_ = os.Remove(source) //nolint:gosec // source is this request's temp upload.
		}
	}()
	slug := home.Slug(name)
	if slug == "" || len(slug) > 63 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "give the project a name"})
		return
	}
	dest := filepath.Join(root, slug)
	if _, err := os.Lstat(dest); err == nil {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": "that project folder already exists"})
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "can't create the project"})
		return
	}
	stage, err := os.MkdirTemp(root, ".retake-project-")
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "can't prepare the project"})
		return
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if source == "" {
		if err := writeBlankProject(stage, slug); err != nil {
			writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "can't create the blank project"})
			return
		}
	} else if err := importProjectVideo(r.Context(), stage, slug, source); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "couldn't import that video: " + err.Error()})
		return
	}
	if err := os.Rename(stage, dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeJSON(w, r, http.StatusConflict, map[string]string{"error": "that project folder already exists"})
			return
		}
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "can't create the project"})
		return
	}
	// A request can immediately read the new card; no restart or rescan delay.
	f.mu.Lock()
	f.scanned = time.Time{}
	f.mu.Unlock()
	writeJSON(w, r, http.StatusCreated, map[string]string{"name": slug, "url": "p/" + slug + "/"})
}

func hasShareCookie(r *http.Request) bool { _, err := r.Cookie(ShareCookie); return err == nil }

func (f *Folder) projectRoot(r *http.Request) (string, int, error) {
	if f.libraryError() != nil {
		return "", http.StatusInternalServerError, errors.New("linked-folder library cannot be read")
	}
	id := r.FormValue("root")
	if id == "" {
		return "", http.StatusBadRequest, errors.New("choose a linked folder")
	}
	for _, link := range foldersOf(f.librarySnapshot(), f.opts.Dir) {
		if link.ID == id {
			if !readableDir(link.Path) {
				return "", http.StatusConflict, errors.New("that linked folder is unavailable")
			}
			return link.Path, http.StatusOK, nil
		}
	}
	if f.removedRoots[id] || id == "original" {
		return "", http.StatusConflict, errors.New("that linked folder was removed")
	}
	return "", http.StatusBadRequest, errors.New("unknown linked folder")
}

func readProjectForm(r *http.Request, root string) (string, string, error) {
	// ParseMultipartForm keeps file parts on disk; opening this part does not buffer it.
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len([]rune(name)) > 200 {
		return "", "", errors.New("give the project a name")
	}
	file, header, err := r.FormFile("video")
	if errors.Is(err, http.ErrMissingFile) {
		return name, "", nil
	}
	if err != nil {
		return "", "", errors.New("choose a readable video")
	}
	defer func() { _ = file.Close() }()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	// The suffix only makes ffprobe's container judgement possible; it never grants acceptance.
	if ext == "" || len(ext) > 12 {
		return "", "", errors.New("choose a video file")
	}
	tmp, err := os.CreateTemp(root, ".retake-upload-*"+ext)
	if err != nil {
		return "", "", errors.New("can't receive the video")
	}
	path := tmp.Name()
	if _, err = io.Copy(tmp, file); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		_ = os.Remove(path)
		return "", "", errors.New("couldn't receive the video")
	}
	return name, path, nil
}

func importProjectVideo(ctx context.Context, stage, slug, source string) error {
	m := &download.Manager{}
	check, err := m.Check(ctx, source, true)
	if err != nil {
		return err
	}
	out := filepath.Join(stage, "v1", slug+"_v1.mp4")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a version folder others may read.
		return err
	}
	if strings.Contains(check.Format, "mp4") && check.Video != nil && check.Video.Codec == "h264" && check.Video.PixFmt == "yuv420p" &&
		(check.Audio == nil || check.Audio.Codec == "aac") {
		return copyProjectFile(source, out)
	}
	st := m.Wait(ctx, download.Source{Root: stage, Path: source, Keep: true})
	if st.State != download.StateReady || st.File == "" {
		return errors.New(st.Error)
	}
	return copyProjectFile(st.File, out)
}

func copyProjectFile(source, dest string) error {
	in, err := os.Open(source) //nolint:gosec // temp upload or downloaded output in this project.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // a version file, as a push writes it.
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeBlankProject(stage, slug string) error {
	if err := os.MkdirAll(filepath.Join(stage, "edit"), 0o755); err != nil { //nolint:gosec // project files may be read by local tools.
		return err
	}
	b, err := json.Marshal(map[string]any{"schema": "timeline/0.1", "id": slug, "rev": 0,
		"format": map[string]any{"size": []int{1920, 1080}, "fps": 30}, "assets": map[string]any{}, "spine": []any{}, "scenes": []any{}})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "edit", "timeline.json"), append(b, '\n'), 0o644); err != nil { //nolint:gosec // project file local tools read.
		return err
	}
	// This is the same v0 document shape that the built-in Scripts extension
	// creates. It gives a no-footage project an in-app brief and chat path while
	// retaining the native timeline that later turns the brief into a render.
	dir := filepath.Join(stage, "v0")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // project files may be read by local tools.
		return err
	}
	return os.WriteFile(filepath.Join(dir, slug+"_v0.md"), []byte("# "+slug+"\n\nDescribe the video you want to make.\n"), 0o644) //nolint:gosec // project file local tools read.
}
