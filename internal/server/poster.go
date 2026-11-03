package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/thumb"
)

// poster is GET /api/poster?v=<label>: the version's first frame as a JPEG, for the page's <video poster> and the home
// screen's cards (spec/home.md, "Thumbnails and the poster"). The streaming side's poster beside the file wins; else
// a frame made once with ffmpeg and cached under the project root's .retake/thumbs/. A relayed version's frame is
// made from the start of its media, asked from the computer that has it. A version with no frame of its own (yet)
// answers with the newest version before it that has one, so a card always has a picture.
func (s *Server) poster(w http.ResponseWriter, r *http.Request) {
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	var p string
	var err error
	for _, c := range append([]family.Version{v}, posterFallbacks(s.Family(), v)...) {
		if p, err = s.posterOf(r.Context(), c); err == nil || r.Context().Err() != nil {
			break
		}
		if !errors.Is(err, thumb.ErrNone) {
			_, _ = fmt.Fprintf(s.opts.Log, "retake: poster of %s: %v\n", c.Label, err)
		}
	}
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	f, err := os.Open(p) //nolint:gosec // the version's poster or our own cached frame
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "no poster")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", st.ModTime().Truncate(time.Second), f)
}

// posterFallbacks are the versions whose frame stands in for v's, newest first: the family's video versions up to
// v's number, a live version's other revisions first. An extension's version (v0) has none of its own.
func posterFallbacks(fam []family.Version, v family.Version) []family.Version {
	var out []family.Version
	for _, o := range slices.Backward(fam) {
		if o.Kind == "" && o.Label != v.Label && o.N <= v.N {
			out = append(out, o)
		}
	}
	return out
}

// thumbs is the maker of the versions' posters.
func (s *Server) thumbs() *thumb.Maker {
	if s.opts.Thumbs != nil {
		return s.opts.Thumbs
	}
	return thumb.Default
}

// posterOf is one version's own poster: thumb.ErrNone when it has none (yet).
func (s *Server) posterOf(ctx context.Context, v family.Version) (string, error) {
	switch {
	case v.Kind != "":
		return "", thumb.ErrNone // an extension's version isn't a video
	case v.Relay:
		return s.relayPoster(ctx, v)
	}
	return s.thumbs().For(ctx, s.root, v.Path)
}

// maxPosterMedia bounds what one relayed poster fetches: a playlist, an init segment, a first segment.
const maxPosterMedia = 64 << 20

// relayPoster makes a relayed version's first frame (a worker's render, live revisions included, or a connected
// computer's version file) from the start of its media, asked through the relay, and caches it by what names that
// media for good: a render's id, a version file's size and time. An offline computer gives ErrNone, asked again next
// time.
func (s *Server) relayPoster(ctx context.Context, v family.Version) (string, error) {
	st, err := s.store(false)
	if err != nil {
		return "", fmt.Errorf("%w: %w", thumb.ErrNone, err)
	}
	a, err := st.Automation()
	if err != nil {
		return "", fmt.Errorf("%w: %w", thumb.ErrNone, err)
	}
	t, ok := relayTarget(&a, v.Label, "")
	if !ok {
		return "", thumb.ErrNone
	}
	key := relayKey(&a, v.Label)
	if key == "" {
		return "", thumb.ErrNone
	}
	return s.thumbs().Keyed(ctx, s.root, key, func(ctx context.Context, dir string) (string, error) {
		if family.IsPlaylist(t.req.File) {
			return s.firstSegment(ctx, &a, v.Label, t, dir)
		}
		return s.fileEnds(ctx, t, dir)
	})
}

// relayKey names a relayed version's media for good: "" when it isn't relayed.
func relayKey(a *project.AutomationState, label string) string {
	if r := a.Render(label); r != nil {
		return "render/" + r.ID + "/" + r.MediaName()
	}
	if fv := a.FileVersion(label); fv != nil {
		return fmt.Sprintf("file/%s/%s/%d/%d/%s", fv.Worker, fv.Label, fv.Size, fv.Modified, fv.Name)
	}
	return ""
}

// firstSegment fetches a relayed playlist's first segment, with the init segment before it when it has one (an fMP4
// stream: the two together are a playable MP4), into dir.
func (s *Server) firstSegment(ctx context.Context, a *project.AutomationState, label string, pl mediaTarget,
	dir string,
) (string, error) {
	var list bytes.Buffer
	if err := s.relayRead(ctx, pl, &list, 1<<20); err != nil {
		return "", err
	}
	initName, seg := firstMedia(list.String())
	if seg == "" {
		return "", errors.New("no segment yet")
	}
	out := filepath.Join(dir, "first"+strings.ToLower(filepath.Ext(seg)))
	if initName != "" {
		out = filepath.Join(dir, "first.mp4")
	}
	f, err := os.Create(out) //nolint:gosec // our scratch folder
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	for _, name := range []string{initName, seg} {
		if name == "" {
			continue
		}
		t, ok := relayTarget(a, label, name)
		if !ok {
			return "", fmt.Errorf("the playlist names %q", name)
		}
		if err := s.relayRead(ctx, t, f, maxPosterMedia); err != nil {
			return "", err
		}
	}
	return out, f.Close()
}

// firstMedia reads a playlist: its first segment, and the init segment (EXT-X-MAP) in effect for it. Only plain names
// beside the playlist count.
func firstMedia(playlist string) (initName, seg string) {
	sc := bufio.NewScanner(strings.NewReader(playlist))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "#EXT-X-MAP:"); ok {
			initName = ""
			for attr := range strings.SplitSeq(rest, ",") {
				if k, val, ok := strings.Cut(attr, "="); ok && strings.TrimSpace(k) == "URI" {
					initName = strings.Trim(strings.TrimSpace(val), `"`)
				}
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !relay.ValidFile(line) || initName != "" && !relay.ValidFile(initName) {
			return "", ""
		}
		return initName, line
	}
	return "", ""
}

// fileEnds fetches a relayed file's first chunk and, for a longer file, its last one, into a sparse copy in dir: an
// MP4's index (moov) is at one end or the other, and its first frame near the start.
func (s *Server) fileEnds(ctx context.Context, t mediaTarget, dir string) (string, error) {
	out := filepath.Join(dir, "first"+strings.ToLower(filepath.Ext(t.req.File)))
	f, err := os.Create(out) //nolint:gosec // our scratch folder
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	c, err := s.openChunk(ctx, t, 0)
	if err != nil {
		return "", err
	}
	total := c.Total
	err = c.copyTo(f, 0, c.Start+c.Length-1)
	c.close()
	if err != nil {
		return "", err
	}
	if last := (total - 1) / relay.Chunk; last > 0 {
		if err := f.Truncate(total); err != nil {
			return "", err
		}
		c, err := s.openChunk(ctx, t, last)
		if err != nil {
			return "", err
		}
		defer c.close()
		if c.Total != total {
			return "", errors.New("the file changed meanwhile")
		}
		if _, err := f.Seek(c.Start, io.SeekStart); err != nil {
			return "", err
		}
		if err := c.copyTo(f, c.Start, c.Start+c.Length-1); err != nil {
			return "", err
		}
	}
	return out, f.Close()
}

// relayRead copies a whole relayed file to w, chunk after chunk; a file longer than limit is an error.
func (s *Server) relayRead(ctx context.Context, t mediaTarget, w io.Writer, limit int64) error {
	for k, pos, total := int64(0), int64(0), int64(-1); total < 0 || pos < total; k++ {
		c, err := s.openChunk(ctx, t, k)
		if err != nil {
			return err
		}
		if total < 0 {
			total = c.Total
			if total > limit {
				c.close()
				return fmt.Errorf("%s is %d bytes", t.req.File, total)
			}
		}
		end := c.Start + c.Length - 1
		err = c.copyTo(w, c.Start, end)
		c.close()
		if err != nil {
			return err
		}
		if c.Length <= 0 {
			break
		}
		pos = end + 1
	}
	return nil
}
