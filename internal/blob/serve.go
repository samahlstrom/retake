package blob

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var rangeRE = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

// parseNum parses a run of digits, saturating at MaxInt64 instead of failing.
func parseNum(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return math.MaxInt64
	}
	return n
}

// byteRange applies a Range header to an object of size bytes, with the rules of spec/api.md (internal/server
// serves files the same way): one range only ("bytes=a-b", "bytes=a-", "bytes=-n"); anything else is ignored (a
// full 200). ok=false means unsatisfiable (416).
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

// Serve sends the object at key with single-range support, with the headers internal/server sends for a video
// file. A missing object gives ErrNotFound with nothing written, so the caller can fall back to another copy;
// another store error is returned with nothing written too. Once the headers are out, a failed copy (usually the
// browser cancelling a range while it seeks) just ends the response, and Serve returns nil.
func Serve(w http.ResponseWriter, r *http.Request, s Store, key, contentType string) error {
	ctx := r.Context()
	info, err := s.Stat(ctx, key)
	if err != nil {
		return err
	}
	size := info.Size
	start, end, partial, ok := byteRange(r.Header.Get("Range"), size)
	h := w.Header()
	if !ok {
		h.Set("Content-Type", "text/plain")
		h.Set("Content-Length", "0")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return nil
	}
	length := end - start + 1
	var body io.ReadCloser
	if r.Method != http.MethodHead && length > 0 {
		if body, err = s.Get(ctx, key, start, length); err != nil {
			return err
		}
		defer func() { _ = body.Close() }()
	}
	h.Set("Content-Type", contentType)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(max(length, 0), 10))
	h.Set("Cache-Control", "no-cache")
	code := http.StatusOK
	if partial {
		code = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	w.WriteHeader(code)
	if body == nil {
		return nil
	}
	// A failed copy (the client went away, or the store broke off) just ends the response.
	_, _ = io.CopyN(w, body, length)
	return nil
}
