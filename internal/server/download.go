package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samahlstrom/retake/internal/download"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/team"
)

// Downloads (spec/api.md, "Download"): any version that plays, as one MP4 that YouTube takes as it is. The versions on
// this server's disk are checked and made here (internal/download); a relayed one on its computer, asked through the
// relay's download request, and its file comes through the relay in chunks like a version file.

// Who may download ([server] downloads, spec/server.md).
const (
	DownloadsEveryone  = "everyone"  // anyone who can watch (the default)
	DownloadsReviewers = "reviewers" // anyone who can record
	DownloadsOff       = "off"       // nobody
)

// ValidDownloads reports whether s is a [server] downloads value ("" is the default).
func ValidDownloads(s string) bool {
	return s == "" || s == DownloadsEveryone || s == DownloadsReviewers || s == DownloadsOff
}

// downloadResponse is GET /api/download's answer.
type downloadResponse struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	download.Status
}

func (s *Server) downloader() *download.Manager {
	if s.opts.Downloads != nil {
		return s.opts.Downloads
	}
	return download.Default
}

// mayDownload says whether the request may download, and if not, the error to answer.
func (s *Server) mayDownload(r *http.Request) (bool, string) {
	role := accessOf(r).role
	switch s.opts.WhoDownloads {
	case DownloadsOff:
		return false, "downloads are off"
	case DownloadsReviewers:
		return role.Can(team.PermRecord), "not allowed"
	}
	return role.Can(team.PermWatch), "not allowed"
}

// downloadState is GET (start false) and POST (start true) /api/download?v=<label>.
func (s *Server) downloadState(w http.ResponseWriter, r *http.Request, start bool) {
	if ok, msg := s.mayDownload(r); !ok {
		s.fail(w, r, http.StatusForbidden, msg)
		return
	}
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	st, _, err := s.downloadOf(r.Context(), v, start)
	if err != nil {
		s.relayFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, downloadResponse{Label: v.Label, Name: s.downloadName(v), Status: st})
}

// downloadFile is GET/HEAD /video/<label>/download: the file as an attachment, once it's ready.
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, label string) {
	if ok, msg := s.mayDownload(r); !ok {
		s.fail(w, r, http.StatusForbidden, msg)
		return
	}
	v, ok := s.version(label)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	st, target, err := s.downloadOf(r.Context(), v, false)
	if err == nil && st.State != download.StateReady && st.State != download.StateUnavailable {
		st, target, err = s.downloadOf(r.Context(), v, true)
	}
	if err != nil {
		s.relayFailed(w, r, err)
		return
	}
	if st.State != download.StateReady {
		msg := "not ready"
		if st.State == download.StateUnavailable {
			msg = st.Error
		} else {
			w.Header().Set("Retry-After", "5")
		}
		s.json(w, r, http.StatusConflict, map[string]any{"error": msg, "state": st.State, "progress": st.Progress})
		return
	}
	w.Header().Set("Content-Disposition", contentDisposition(s.downloadName(v)))
	if target != nil {
		s.relayMedia(w, r, *target)
		return
	}
	s.serveFile(w, r, st.File, "video/mp4")
}

// downloadOf is where a version's download is (starting it with start). A relayed version asks its computer, and
// returns the relay target of its file once it's ready.
func (s *Server) downloadOf(ctx context.Context, v family.Version, start bool) (download.Status, *mediaTarget, error) {
	if !v.Relay {
		src := download.ForVersion(s.root, v, s.opts.Editor)
		if start {
			return s.downloader().Start(ctx, src), nil, nil
		}
		return s.downloader().Status(ctx, src), nil, nil
	}
	st, err := s.store(false)
	if err != nil {
		return download.Status{}, nil, err
	}
	a, err := st.Automation()
	if err != nil {
		return download.Status{}, nil, err
	}
	req := relay.Request{Op: "download", Start: start}
	worker := ""
	switch {
	case a.Render(v.Label) != nil:
		rv := a.Render(v.Label)
		if !rv.Complete {
			return download.Status{State: download.StateUnavailable, Error: v.Label + " is still rendering"}, nil, nil
		}
		worker, req.Render = rv.Worker, rv.ID
	case a.FileVersion(v.Label) != nil && v.Kind == "":
		fv := a.FileVersion(v.Label)
		worker, req.Version = fv.Worker, fv.Label
	default:
		return download.Status{State: download.StateUnavailable, Error: v.Label + " isn't a video"}, nil, nil
	}
	reply, err := s.ask(ctx, mediaTarget{worker: worker, req: req}, "", false)
	if err != nil {
		return download.Status{}, nil, err
	}
	defer reply.Close()
	body, err := io.ReadAll(io.LimitReader(reply.Body, 64<<10))
	if err != nil {
		return download.Status{}, nil, fmt.Errorf("%w: %w", errNoAnswer, err)
	}
	var ds download.Status
	switch {
	case reply.Status == http.StatusNotFound:
		return download.Status{State: download.StateUnavailable, Error: v.Label + " isn't on the editing computer"}, nil,
			nil
	case reply.Status != http.StatusOK || json.Unmarshal(body, &ds) != nil:
		return download.Status{}, nil, errNoAnswer
	}
	if ds.State != download.StateReady {
		return ds, nil, nil
	}
	file := req
	file.Op, file.Start, file.Download = "", false, true
	t := &mediaTarget{worker: worker, req: file, ctype: "video/mp4", cache: "private, no-cache",
		key:  fmt.Sprintf("download/%s/%s/%s/%d/%d/%s", worker, req.Render, req.Version, ds.Size, ds.Modified, v.Label),
		size: ds.Size, modified: ds.Modified}
	return ds, t, nil
}

// downloadName is the file a version downloads as: <video>_<label>.mp4, or the stem of a version without a number.
func (s *Server) downloadName(v family.Version) string {
	if versionLabel.MatchString(v.Label) {
		return s.videoName() + "_" + v.Label + ".mp4"
	}
	stem := family.Stem(v.Name)
	if stem == "" || family.IsPlaylist(v.Name) {
		stem = s.videoName()
	}
	return stem + ".mp4"
}

// versionLabel is a version's or a draft's label: v5, v5-draft2.
var versionLabel = regexp.MustCompile(`^[vV]\d{1,4}(-draft\d+)?$`)

// videoName is the family's name: the part before _v<N> of its file names, else the opened file's stem, else the
// project's name.
func (s *Server) videoName() string {
	var names []string
	if s.video() != "" {
		names = append(names, filepath.Base(s.video()))
	}
	for _, v := range s.Family() {
		names = append(names, v.Name)
	}
	for _, n := range names {
		if nm, ok := family.ParseStem(family.Stem(n)); ok && nm.Prefix != "" {
			return nm.Prefix
		}
	}
	for _, p := range []string{s.video(), s.opts.BasePath, s.root} {
		if st := family.Stem(filepath.Base(p)); p != "" && st != "" && st != "." && st != "/" {
			return st
		}
	}
	return "video"
}

// contentDisposition makes a download an attachment named name: a plain ASCII name for every browser, and the exact
// one (RFC 5987) for those that read it.
func contentDisposition(name string) string {
	plain := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '/' || r == ';' || r == '%' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, plain, url.PathEscape(name))
}
