package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// readMedia opens only retained render output, with traversal and symlink containment enforced by os.Root.
func readMedia(dir string, request relay.Request) relay.Response {
	missing := relay.Response{Status: 404, Type: "text/plain", Body: []byte("media is not available yet")}
	if !project.ValidWorkID(request.Render) || !relay.ValidFile(request.File) {
		return missing
	}
	root, e := os.OpenRoot(filepath.Join(dir, "renders", request.Render))
	if e != nil {
		return missing
	}
	defer func() { _ = root.Close() }()
	// state.json is written locally before registration, so arbitrary directories cannot be exposed.
	state, e := root.Open("state.json")
	if e != nil {
		return missing
	}
	var v project.LiveRender
	e = json.NewDecoder(io.LimitReader(state, 8<<20)).Decode(&v)
	_ = state.Close()
	if e != nil || v.ID != request.Render {
		return missing
	}
	stat, e := root.Lstat(request.File)
	if e != nil || !stat.Mode().IsRegular() || stat.Size() > relay.MaxBody {
		return missing
	}
	f, e := root.Open(request.File)
	if e != nil {
		return missing
	}
	defer func() { _ = f.Close() }()
	method := http.MethodGet
	if request.Head {
		method = http.MethodHead
	}
	req := httptest.NewRequest(method, "/"+request.File, nil)
	req.Header.Set("Range", request.Range)
	w := httptest.NewRecorder()
	ctype := "video/mp4"
	if strings.HasSuffix(request.File, ".m3u8") {
		ctype = "application/vnd.apple.mpegurl"
	}
	if strings.HasSuffix(request.File, ".ts") {
		ctype = "video/mp2t"
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, req, request.File, stat.ModTime(), f)
	return relay.Response{Status: w.Code, Type: w.Header().Get("Content-Type"), Range: w.Header().Get("Content-Range"), Body: w.Body.Bytes()}
}

func (r *runner) mediaLoop(ctx context.Context) {
	for ctx.Err() == nil {
		response, e := r.request(ctx, http.MethodGet, "/api/worker/media", nil)
		var request relay.Request
		if e == nil {
			if response.StatusCode == http.StatusOK {
				e = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&request)
			} else if response.StatusCode != http.StatusNoContent {
				e = io.ErrUnexpectedEOF
			}
			_ = response.Body.Close()
		}
		if e != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if request.ID == "" {
			continue
		}
		body := readMedia(r.dir, request)
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.opts.URL, "/")+"/api/worker/media/"+request.ID, bytes.NewReader(body.Body))
		if e != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+r.opts.Token)
		req.Header.Set("X-Retake-Worker", r.identity.ID)
		req.Header.Set("X-Retake-Session", r.identity.Session)
		req.Header.Set("X-Media-Status", strconv.Itoa(body.Status))
		req.Header.Set("Content-Type", body.Type)
		req.Header.Set("Content-Range", body.Range)
		resp, e := r.opts.HTTP.Do(req)
		if e == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
}
