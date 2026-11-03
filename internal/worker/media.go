package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// media is a worker's answer to one relay request: the part of the file it sends.
type media struct {
	relay.Response
	file *os.File // nil when there is no body
}

func (m media) close() {
	if m.file != nil {
		_ = m.file.Close()
	}
}

func missing() media { return media{Response: relay.Response{Status: http.StatusNotFound}} }

// readMedia opens what a relay request asks for (spec/automation.md, "What a worker reads"): a retained render's
// flat media file, with traversal and symlink containment enforced by os.Root, or a version file of the project's
// current family, or a regular media file beside a playlist version. Anything else is 404.
func (r *runner) readMedia(ctx context.Context, request relay.Request) media {
	switch {
	case strings.HasPrefix(request.Op, "ext."):
		return r.extAnswer(ctx, request)
	case request.Op == "download":
		return r.downloadAnswer(ctx, request)
	case request.Op != "":
		return missing()
	case request.Download:
		return r.readDownload(request)
	case request.Render != "":
		return readRender(r.dir, request)
	case request.Version != "":
		return r.readVersion(request)
	}
	return missing()
}

func readRender(dir string, request relay.Request) media {
	if !project.ValidWorkID(request.Render) || !relay.ValidFile(request.File) {
		return missing()
	}
	root, e := os.OpenRoot(filepath.Join(dir, "renders", request.Render))
	if e != nil {
		return missing()
	}
	defer func() { _ = root.Close() }()
	// state.json is written locally before registration, so arbitrary directories cannot be exposed.
	state, e := root.Open("state.json")
	if e != nil {
		return missing()
	}
	var v project.LiveRender
	e = json.NewDecoder(io.LimitReader(state, 8<<20)).Decode(&v)
	_ = state.Close()
	if e != nil || v.ID != request.Render {
		return missing()
	}
	stat, e := root.Lstat(request.File)
	if e != nil || !stat.Mode().IsRegular() {
		return missing()
	}
	f, e := root.Open(request.File)
	if e != nil {
		return missing()
	}
	return part(f, request, 0)
}

// readVersion opens a file of a version of the project's family as it is now.
func (r *runner) readVersion(request relay.Request) media {
	var v *family.Version
	for _, x := range r.onDisk(true) {
		if x.Label == request.Version {
			v = &x
			break
		}
	}
	if v == nil {
		return missing()
	}
	var f *os.File
	var e error
	if request.File == "" || request.File == filepath.Base(v.Path) {
		f, e = os.Open(v.Path) // the version's own file, as retake open serves it
	} else {
		if !family.IsPlaylist(v.Path) || !relay.ValidFile(request.File) {
			return missing()
		}
		var root *os.Root
		if root, e = os.OpenRoot(filepath.Dir(v.Path)); e != nil {
			return missing()
		}
		defer func() { _ = root.Close() }()
		stat, err := root.Lstat(request.File)
		if err != nil || !stat.Mode().IsRegular() {
			return missing() // never a symlink or a folder beside a playlist
		}
		f, e = root.Open(request.File)
	}
	if e != nil {
		return missing()
	}
	stat, e := f.Stat()
	if e != nil || !stat.Mode().IsRegular() {
		_ = f.Close()
		return missing()
	}
	return part(f, request, stat.ModTime().UnixMilli())
}

// part answers the request's range of an open file: at most one chunk (relay.Plan). A HEAD gets the headers only,
// with the whole file's length when it asked for no range.
func part(f *os.File, request relay.Request, modified int64) media {
	stat, e := f.Stat()
	if e != nil {
		_ = f.Close()
		return missing()
	}
	size := stat.Size()
	m := media{Response: relay.Response{Type: contentType(request.File), Modified: modified}}
	if _, ranged := relay.ParseRange(request.Range); request.Head && !ranged {
		_ = f.Close()
		m.Status, m.Length = http.StatusOK, size
		return m
	}
	start, end, status := relay.Plan(request.Range, size, relay.Chunk)
	m.Status = status
	switch status {
	case http.StatusRequestedRangeNotSatisfiable:
		_ = f.Close()
		m.Range = "bytes */" + strconv.FormatInt(size, 10)
		return m
	case http.StatusPartialContent:
		m.Range = relay.ContentRange(start, end, size)
	}
	m.Length = end - start + 1
	if request.Head || m.Length == 0 {
		_ = f.Close()
		return m
	}
	m.file = f
	m.Body = io.NewSectionReader(f, start, m.Length)
	return m
}

// contentType is a relayed file's type; the server sets its own by name, so this is only informative.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/mp2t"
	case ".m4s":
		return "video/iso.segment"
	}
	if t, ok := family.ContentTypes[strings.ToLower(filepath.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

func (r *runner) mediaLoop(ctx context.Context) {
	// An answer can take longer than a poll's timeout to upload: its own client, bounded by its context.
	client := *r.opts.HTTP
	client.Timeout = 0
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
		if request.ID == "" || !project.ValidWorkID(request.ID) {
			continue
		}
		r.answer(ctx, &client, request)
	}
}

// answer sends one relay request's answer, its body streamed from the file.
func (r *runner) answer(ctx context.Context, client *http.Client, request relay.Request) {
	m := r.readMedia(ctx, request)
	defer m.close()
	ctx, cancel := context.WithTimeout(ctx, relay.Hold)
	defer cancel()
	body := io.Reader(http.NoBody)
	if m.Body != nil {
		body = m.Body
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.opts.URL, "/")+"/api/worker/media/"+request.ID, body)
	if e != nil {
		return
	}
	if m.Body != nil {
		req.ContentLength = m.Length
	} else {
		req.ContentLength = 0
	}
	req.Header.Set("Authorization", "Bearer "+r.opts.Token)
	req.Header.Set("X-Retake-Worker", r.identity.ID)
	req.Header.Set("X-Retake-Session", r.identity.Session)
	req.Header.Set("X-Media-Status", strconv.Itoa(m.Status))
	req.Header.Set("X-Media-Length", strconv.FormatInt(m.Length, 10))
	if m.Modified > 0 {
		req.Header.Set("X-Media-Modified", strconv.FormatInt(m.Modified, 10))
	}
	req.Header.Set("Content-Type", m.Type)
	if m.Range != "" {
		req.Header.Set("Content-Range", m.Range)
	}
	if m.ETag != "" {
		req.Header.Set("X-Media-ETag", m.ETag)
	}
	resp, e := client.Do(req)
	if e == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
}
