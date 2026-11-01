package hub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/workspace"
)

// shareHost retains the public address of a single-video share when its collection becomes the main app.
// Authentication still comes from that video's store; recognizing a host never grants access by itself.
func (f *Folder) shareHost(host string) bool {
	if f.allow[host] {
		return true
	}
	if st := f.homeStore(); st != nil {
		if allowed, err := st.HostAllowed(host); err == nil && allowed {
			return true
		}
	}
	for _, fd := range f.rescan(false) {
		if video := f.projectOf(fd); video != nil {
			if st := video.projectStore(); st != nil {
				if allowed, err := st.HostAllowed(host); err == nil && allowed {
					return true
				}
			}
		}
	}
	return false
}

func (f *Folder) workspace() (workspace.Layout, error) {
	layout := workspace.Default(filepath.Base(f.opts.Dir))
	if st := f.homeStore(); st != nil {
		data, revision, err := st.Workspace()
		if err != nil {
			return layout, err
		}
		if data != "" {
			if err = json.Unmarshal([]byte(data), &layout); err != nil {
				return layout, err
			}
			layout.Revision = revision
		}
	}
	return layout, nil
}

func (f *Folder) saveWorkspace(w http.ResponseWriter, r *http.Request) {
	var layout workspace.Layout
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := decoder.Decode(&layout); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid project organization"})
		return
	}
	videos := map[string]bool{}
	for _, video := range f.rescan(true) {
		videos[video.Name] = true
	}
	if err := layout.Validate(videos); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	f.storeMu.Lock()
	if f.store == nil {
		st, err := project.OpenHome(f.opts.Dir)
		if err != nil {
			f.storeMu.Unlock()
			f.logf("workspace: %v", err)
			writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "could not save projects"})
			return
		}
		f.store = st
	}
	st := f.store
	f.storeMu.Unlock()
	data, err := json.Marshal(layout)
	if err == nil {
		layout.Revision, err = st.SaveWorkspace(string(data), layout.Revision)
	}
	if errors.Is(err, project.ErrWorkspaceConflict) {
		writeJSON(w, r, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		f.logf("workspace: %v", err)
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "could not save projects"})
		return
	}
	writeJSON(w, r, http.StatusOK, layout)
}
