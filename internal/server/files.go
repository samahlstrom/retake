package server

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/theme"
)

// mediaTypes maps the extensions of page files and playlist neighbors to content types. It is fixed (not the OS
// mime table) so every platform serves the same types.
var mediaTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
	".m4s":         "video/iso.segment",
	".ts":          "video/mp2t",
	".vtt":         "text/vtt; charset=utf-8",
	".m4a":         "audio/mp4",
	".aac":         "audio/aac",
	".mp3":         "audio/mpeg",
	".ogg":         "audio/ogg",
	".opus":        "audio/ogg",
	".wav":         "audio/wav",
}

func mediaType(name string) string {
	ext := strings.ToLower(family.Suffix(name))
	if t, ok := family.ContentTypes[ext]; ok {
		return t
	}
	if t, ok := mediaTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

var rangeRE = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

// parseNum parses a run of digits, saturating at MaxInt64 instead of failing.
func parseNum(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return math.MaxInt64
	}
	return n
}

// byteRange applies a Range header to a file of size bytes, exactly like the legacy server: one range only
// ("bytes=a-b", "bytes=a-", "bytes=-n"); anything else is ignored (full 200). ok=false means unsatisfiable (416).
func byteRange(header string, size int64) (start, end int64, partial, ok bool) {
	start, end = 0, size-1
	h := strings.TrimSpace(header)
	m := rangeRE.FindStringSubmatch(h)
	if h == "" || m == nil || (m[1] == "" && m[2] == "") {
		return start, end, false, true
	}
	if m[1] == "" {
		start = max(0, size-parseNum(m[2]))
	} else {
		start = parseNum(m[1])
		if m[2] != "" {
			end = min(parseNum(m[2]), size-1)
		}
	}
	if start >= size || start > end {
		return 0, 0, false, false
	}
	return start, end, true, true
}

// serveFile sends a video file with single-range support. Headers match the legacy server's.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, p, ctype string) {
	if s.opts.Media != nil && s.opts.Media(w, r, p, ctype) {
		return
	}
	f, err := os.Open(p) //nolint:gosec // p is a version file of the family or a plain name beside a playlist
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "missing")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		s.fail(w, r, http.StatusNotFound, "missing")
		return
	}
	size := st.Size()
	start, end, partial, ok := byteRange(r.Header.Get("Range"), size)
	h := w.Header()
	if !ok {
		h.Set("Content-Type", "text/plain")
		h.Set("Content-Length", "0")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	length := end - start + 1
	h.Set("Content-Type", ctype)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("Cache-Control", "no-cache")
	if family.IsPlaylist(p) {
		// A streamed render's playlist grows (spec/bridge.md, "Streaming"): nothing between the player and this server
		// may keep a copy, a tunnel's CDN included (Cloudflare reads the targeted headers first).
		h.Set("Cache-Control", "no-cache, private")
		h.Set("CDN-Cache-Control", "no-store")
		h.Set("Cloudflare-CDN-Cache-Control", "no-store")
	}
	code := http.StatusOK
	if partial {
		code = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	w.WriteHeader(code)
	if r.Method == http.MethodHead || length <= 0 {
		return
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return
	}
	// The browser cancels range requests all the time while seeking; a write error just ends the response.
	_, _ = io.CopyN(w, f, length)
}

// plainName reports whether name is a bare file name: no separators, not "." or "..", not hidden, nothing a
// Windows path would read as a drive or device.
func plainName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	if strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	return filepath.IsLocal(name) && filepath.Base(name) == name
}

// playlistFile serves /video/<label>/<file>: an .m3u8 version's playlist or a file beside it (segments, init).
func (s *Server) playlistFile(w http.ResponseWriter, r *http.Request, label, name string) {
	v, ok := s.version(label)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	if !family.IsPlaylist(v.Path) || !plainName(name) {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	s.serveFile(w, r, filepath.Join(filepath.Dir(v.Path), name), mediaType(name))
}

// static serves one of the page's files (/assets/..., icons). Hidden files and folders are never served.
func (s *Server) static(w http.ResponseWriter, r *http.Request, parts []string) {
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || strings.ContainsAny(p, "\\:\x00") {
			s.fail(w, r, http.StatusNotFound, "not found")
			return
		}
	}
	name := path.Join(parts...)
	if s.opts.UI == nil || !fs.ValidPath(name) {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	f, err := s.opts.UI.Open(name)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			s.fail(w, r, http.StatusNotFound, "not found")
			return
		}
		rs = bytes.NewReader(b)
	}
	w.Header().Set("Content-Type", mediaType(name))
	w.Header().Set("Cache-Control", "no-cache")
	theme.Headers(w.Header(), name) // a theme file: its own type, nosniff and a CSP (spec/theme.md)
	http.ServeContent(w, r, name, st.ModTime(), rs)
}
