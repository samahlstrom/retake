package relay

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The byte-range math of the relay (spec/automation.md, "Chunks"): one range per request, never more than a Chunk.

var rangeRE = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

// Range is one parsed Range header. Suffix ranges ("bytes=-n") have Suffix set and Start = n; open-ended ones
// ("bytes=a-") have End = -1.
type Range struct {
	Start, End int64
	Suffix     bool
}

// ParseRange reads a Range header of one range. ok is false for none, several, or a malformed one: those are read
// as the whole file.
func ParseRange(header string) (r Range, ok bool) {
	m := rangeRE.FindStringSubmatch(strings.TrimSpace(header))
	if m == nil || m[1] == "" && m[2] == "" {
		return Range{}, false
	}
	if m[1] == "" {
		return Range{Start: num(m[2]), End: -1, Suffix: true}, true
	}
	r = Range{Start: num(m[1]), End: -1}
	if m[2] != "" {
		r.End = num(m[2])
		if r.End < r.Start {
			return Range{}, false // invalid: ignored, as HTTP says
		}
	}
	return r, true
}

// num parses a run of digits, saturating at MaxInt64 instead of failing.
func num(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return math.MaxInt64
	}
	return n
}

// Resolve applies a parsed range to a file of size bytes: the first and last byte it asks for. ok is false when
// nothing of it is in the file (416).
func (r Range) Resolve(size int64) (start, end int64, ok bool) {
	start, end = r.Start, r.End
	if r.Suffix {
		start, end = max(0, size-r.Start), size-1
		if r.Start == 0 {
			return 0, 0, false
		}
	}
	if end < 0 || end > size-1 {
		end = size - 1
	}
	if start >= size || start > end {
		return 0, 0, false
	}
	return start, end, true
}

// Plan is how a worker answers a Range header for a file of size bytes: the bytes it sends ([start, end], never
// more than limit of them) and the status. No range (or an unusable one) is read as "bytes=0-": 200 when the whole
// file fits, else its first limit bytes as 206. A range past the end is 416.
func Plan(header string, size, limit int64) (start, end int64, status int) {
	r, ranged := ParseRange(header)
	if !ranged {
		r = Range{End: -1}
	}
	start, end, ok := r.Resolve(size)
	if !ok {
		if !ranged && size == 0 {
			return 0, -1, 200 // an empty file, asked for whole
		}
		return 0, 0, 416
	}
	end = min(end, start+limit-1)
	if !ranged && start == 0 && end == size-1 {
		return start, end, 200
	}
	return start, end, 206
}

// ContentRange is the Content-Range of a 206: "bytes start-end/size".
func ContentRange(start, end, size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", start, end, size)
}

var contentRangeRE = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+)$`)

// ParseContentRange reads "bytes start-end/size" (not "bytes */size").
func ParseContentRange(s string) (start, end, size int64, ok bool) {
	m := contentRangeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, 0, false
	}
	start, end, size = num(m[1]), num(m[2]), num(m[3])
	if start > end || end >= size || size == math.MaxInt64 {
		return 0, 0, 0, false
	}
	return start, end, size, true
}

// ChunkRange is the Range header that asks for aligned chunk k.
func ChunkRange(k int64) string { return fmt.Sprintf("bytes=%d-%d", k*Chunk, (k+1)*Chunk-1) }
