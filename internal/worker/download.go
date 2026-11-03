package worker

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/download"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// Downloads of this computer's versions for a hosted server (spec/automation.md, "The media relay": Downloads): the
// server asks where one is (and to get it ready), then reads the ready file through the relay like a version file.

// downloadSource is the download a relay request names: a render of this worker, or a version of the family on disk.
func (r *runner) downloadSource(request relay.Request) (download.Source, bool) {
	switch {
	case request.Render != "":
		if !project.ValidWorkID(request.Render) {
			return download.Source{}, false
		}
		dir := filepath.Join(r.dir, "renders", request.Render)
		if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
			return download.Source{}, false
		}
		return download.ForRender(r.opts.Root, dir, r.timeline, r.editor, r.opts.Editor), true
	case request.Version != "":
		for _, v := range r.onDisk(true) {
			if v.Label == request.Version {
				return download.ForVersion(r.opts.Root, v, r.opts.Editor), true
			}
		}
	}
	return download.Source{}, false
}

// downloadAnswer is the "download" request: where the download is, got ready first with Start.
func (r *runner) downloadAnswer(ctx context.Context, request relay.Request) media {
	src, ok := r.downloadSource(request)
	if !ok {
		return missing()
	}
	if request.Start {
		return jsonAnswer(http.StatusOK, download.Default.Start(ctx, src))
	}
	return jsonAnswer(http.StatusOK, download.Default.Status(ctx, src))
}

// readDownload reads the ready download's bytes, one chunk as for any file; 404 while it isn't ready.
func (r *runner) readDownload(request relay.Request) media {
	src, ok := r.downloadSource(request)
	if !ok {
		return missing()
	}
	st := download.Default.Ready(src)
	if st.State != download.StateReady {
		return missing()
	}
	f, err := os.Open(st.File)
	if err != nil {
		return missing()
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return missing()
	}
	return part(f, request, fi.ModTime().UnixMilli())
}
