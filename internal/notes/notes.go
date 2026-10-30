// Package notes is the notes engine: it turns a version's review takes (voice transcripts, pen strokes, images,
// the clocks in events.json) into notes.md and notes.json beside them. It is a rule-for-rule port of the legacy
// `review notes` (notes.py), plus images, answers and stable comment keys (spec/notes.md).
//
// Clocks: inside a take, session_t = seconds since the take started (first R). Each R-on/R-off segment is its own
// audio file starting at the segment's t_start. events.json maps session_t to video_t.
//   - Sentences stay whole (they end at punctuation, a >1.5 s gap or a segment boundary) and are filed under the
//     moment where they START; if the playhead moved while he talked, the line says where it went.
//   - Comments: a paused stop gives one comment at its frame (everything said, drawn and dropped there). A playing
//     stretch gives one comment per sentence, over the video range it was said over. A stroke or image added while
//     playing joins the nearest sentence (<= 3 s), else stands alone.
//   - Moments: comments chain while their anchors are <= window s apart and the moment spans <= max-span s. Each
//     moment gets one snapshot: the frame plus its strokes, numbered, and its images, framed.
//   - Flags: likely retractions ("scrap that") and reinstatements ("actually, add that back") are flagged with the
//     comments they probably refer to. Nothing is deleted.
//
// The engine never transcribes: it reads transcript.json and reports the segments that have none yet.
package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/store"
)

// Defaults for Options.Window and Options.MaxSpan.
const (
	DefaultWindow  = 2.0 // s between comments that still count as one moment
	DefaultMaxSpan = 8.0 // longest a moment may get, in video s
)

// ErrNoTakes is returned when there is nothing to build notes from.
var ErrNoTakes = errors.New("no review takes")

// Options tune a build.
type Options struct {
	Window          float64 // 0 = DefaultWindow
	MaxSpan         float64 // 0 = DefaultMaxSpan
	VideoTranscript string  // the edit's own VO words (JSON), quoted under (audio) comments
	NoSnapshots     bool    // skip the frame grabs (snaps/ is still cleared)
	FFmpeg          string  // the ffmpeg binary; "" = "ffmpeg" on PATH
	Log             io.Writer
	Now             func() time.Time // notes.json "generated"; nil = time.Now
	Prober          family.Prober    // Run: probes the video and its family; nil = ffprobe
}

func (o Options) window() float64 {
	if o.Window == 0 {
		return DefaultWindow
	}
	return o.Window
}

func (o Options) maxSpan() float64 {
	if o.MaxSpan == 0 {
		return DefaultMaxSpan
	}
	return o.MaxSpan
}

func (o Options) logf(format string, args ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintf(o.Log, format, args...)
	}
}

// Result is what a build produced. Its files are already written.
type Result struct {
	Dir      string // the folder notes.md, notes.json and snaps/ went into
	MDPath   string
	JSONPath string
	Markdown string
	Doc      *Doc       // notes.json
	Comments []*Comment // in spoken order (the same values as in Doc.Moments)
	// Untranscribed lists recorded segments without a (current) transcript. Their words are missing from the notes.
	Untranscribed []Untranscribed
}

// Summary is the one-line report, like the legacy engine's.
func (r *Result) Summary() string {
	return fmt.Sprintf("%s  (%s: %s, %s, %s, %s, %s)", r.MDPath, r.Doc.Version, plural(len(r.Doc.Moments), "moment"),
		plural(len(r.Comments), "comment"), plural(len(r.Doc.Segments), "segment"), plural(len(r.Doc.Flags), "flag"),
		plural(len(r.Doc.Takes), "take"))
}

// inheritEntry is one inherit.json take: recorded on another version, valid here inside its ranges.
type inheritEntry struct {
	dir     string
	version json.RawMessage
	ranges  json.RawMessage
	note    json.RawMessage
}

type input struct {
	own     []string
	out     string
	inherit []inheritEntry
	video   *jsonx.Object // nil = the first own take's session.json video
	fam     []family.Version
}

// Build runs the notes engine for one version (like legacy `review notes <video>`): every take in the video's
// <stem>.review folder plus the takes inherit.json carries over. It writes <stem>.review/notes.md, notes.json and
// snaps/, and returns the result. fam is the version family (its labels are used when no take recorded them).
// Transcripts must already exist; Build never transcribes.
func Build(ctx context.Context, video family.Version, fam []family.Version, opts Options) (*Result, error) {
	rd := store.ReviewDir(video.Path)
	in := input{out: rd, fam: fam, video: videoObject(video)}
	in.own, in.inherit = reviewTakes(rd)
	if len(in.own) == 0 && len(in.inherit) == 0 {
		return nil, fmt.Errorf("%w at %s", ErrNoTakes, rd)
	}
	return run(ctx, in, opts)
}

// Run builds notes for a target given on the command line, like legacy `review notes`: a video file (its
// version's takes), its <stem>.review folder, or one take folder (that take alone, written into the folder).
func Run(ctx context.Context, target string, opts Options) (*Result, error) {
	p, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if isDir(p) && isFile(filepath.Join(p, "session.json")) {
		return run(ctx, input{own: []string{p}, out: p}, opts)
	}
	videoFile := ""
	switch {
	case isFile(p):
		videoFile = p
	case strings.HasSuffix(filepath.Base(p), ".review"):
		stem := strings.TrimSuffix(filepath.Base(p), ".review")
		for _, ext := range []string{".mp4", ".webm", ".mov", ".m4v", ".mkv", ".m3u8"} {
			if f := filepath.Join(filepath.Dir(p), stem+ext); isFile(f) {
				videoFile = f
				break
			}
		}
	}
	if videoFile != "" {
		pr := opts.Prober
		if pr == nil {
			pr = probe.New(opts.Log)
		}
		fam := family.Detect(videoFile, nil, pr)
		for _, v := range fam {
			if v.Current {
				return Build(ctx, v, fam, opts)
			}
		}
		return nil, fmt.Errorf("%w at %s", ErrNoTakes, store.ReviewDir(videoFile))
	}
	in := input{out: p}
	in.own, in.inherit = reviewTakes(p)
	if len(in.own) == 0 && len(in.inherit) == 0 {
		return nil, fmt.Errorf("%w at %s", ErrNoTakes, p)
	}
	return run(ctx, in, opts)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// reviewTakes lists a .review folder's own take folders (sorted) and the existing takes of its inherit.json. A
// relative inherit dir is relative to the .review folder.
func reviewTakes(rd string) ([]string, []inheritEntry) {
	var own []string
	if entries, err := os.ReadDir(rd); err == nil {
		for _, e := range entries {
			d := filepath.Join(rd, e.Name())
			if store.ValidTakeID(e.Name()) && isDir(d) && isFile(filepath.Join(d, "session.json")) {
				own = append(own, d)
			}
		}
	}
	sort.Strings(own)
	var inh []inheritEntry
	raw, ok, _ := readJSON(filepath.Join(rd, "inherit.json"))
	if ok {
		for _, x := range readObjects(field(objectOf(raw), "takes")) {
			dir := str(x["dir"])
			if dir == "" {
				continue
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(rd, dir)
			}
			if isDir(dir) {
				inh = append(inh, inheritEntry{dir: dir, version: x["version"], ranges: x["ranges"], note: x["note"]})
			}
		}
	}
	return own, inh
}

// videoObject is the notes.json "video" of a probed version: {path, name, duration, fps, width, height}.
func videoObject(v family.Version) *jsonx.Object {
	o := jsonx.NewObject()
	_ = o.SetValue("path", v.Path)
	_ = o.SetValue("name", v.Name)
	_ = o.SetValue("duration", v.Duration)
	_ = o.SetValue("fps", v.FPS)
	_ = o.SetValue("width", v.Width)
	_ = o.SetValue("height", v.Height)
	return o
}

func run(ctx context.Context, in input, opts Options) (*Result, error) {
	var takes []*take
	for _, d := range in.own {
		t, err := loadTake(d)
		if err != nil {
			opts.logf("notes: skipping take %s: %v\n", d, err)
			continue
		}
		takes = append(takes, t)
	}
	for _, x := range in.inherit {
		t, err := loadTake(x.dir)
		if err != nil {
			opts.logf("notes: skipping inherited take %s: %v\n", x.dir, err)
			continue
		}
		if truthy(x.version) {
			v := str(x.version)
			t.version = &v
		}
		t.inherited = true
		t.rangesRaw = x.ranges
		if !truthy(x.ranges) {
			t.rangesRaw = json.RawMessage("[[0, 1000000000.0]]")
		}
		var rs [][]float64
		_ = json.Unmarshal(t.rangesRaw, &rs)
		for _, r := range rs {
			if len(r) == 2 {
				t.ranges = append(t.ranges, [2]float64{r[0], r[1]})
			}
		}
		t.inheritNote = x.note
		t.id = fmt.Sprintf("%s (%s)", t.id, pyStr(x.version))
		takes = append(takes, t)
	}
	if len(takes) == 0 {
		return nil, fmt.Errorf("%w at %s", ErrNoTakes, in.out)
	}
	sort.SliceStable(takes, func(i, j int) bool { return takes[i].id < takes[j].id })

	video := in.video
	if video == nil {
		video = jsonx.NewObject()
		for _, t := range takes {
			if !t.inherited {
				video = t.video
				break
			}
		}
	}
	version := ""
	if l := verLabel(str(field(video, "name"))); l != nil {
		version = *l
	}
	for _, t := range takes {
		if version == "" && t.version != nil && *t.version != "" {
			version = *t.version
		}
	}
	if version == "" {
		version = "current"
	}

	window, maxSpan := opts.window(), opts.maxSpan()
	moments, comments, flags := build(takes, video, window, maxSpan)
	dropped := 0
	for _, t := range takes {
		if !t.inherited {
			continue
		}
		keep := comments[:0:0]
		for _, c := range comments {
			if c.Take != t.id || inRanges(float64(c.VideoT), t.ranges) {
				keep = append(keep, c)
			}
		}
		dropped += len(comments) - len(keep)
		comments = keep
	}
	if dropped > 0 {
		moments, comments, flags = rebuild(comments, window, maxSpan)
	}
	if opts.VideoTranscript != "" {
		vw, err := loadVideoWords(opts.VideoTranscript)
		if err != nil {
			return nil, fmt.Errorf("--video-transcript: %w", err)
		}
		attachVO(comments, vw)
	}

	if err := snapshots(ctx, in.out, video, moments, opts); err != nil {
		return nil, err
	}

	res := &Result{Dir: in.out, MDPath: filepath.Join(in.out, "notes.md"), JSONPath: filepath.Join(in.out, "notes.json"),
		Comments: comments}
	for _, t := range takes {
		res.Untranscribed = append(res.Untranscribed, t.missing...)
	}
	fam := familyLabels(takes, in.fam)
	res.Markdown = renderMD(moments, comments, flags, video, takes, version, fam)
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	res.Doc = &Doc{Video: video, Version: version, Family: fam, Window: jsonFloat(window), MaxSpan: jsonFloat(maxSpan),
		Generated: now().Format("2006-01-02T15:04:05-0700"), Rules: docRules, Clock: docClock,
		Takes: []TakeInfo{}, Segments: []*jsonx.Object{}, Flags: flags, Moments: moments, Chronological: []int{}}
	for _, t := range takes {
		models := make([]string, 0, len(t.models))
		for m := range t.models {
			models = append(models, m)
		}
		sort.Strings(models)
		warnings := append([]string{}, t.warnings...)
		info := TakeInfo{Take: t.id, Version: t.version, Recorded: jsonFloat(r3(t.recorded)), Ranges: t.rangesRaw,
			InheritNote: t.inheritNote, Models: models, Warnings: warnings, SmallStrokesDropped: t.dropped}
		res.Doc.Takes = append(res.Doc.Takes, info)
		for _, sg := range t.segments {
			o := jsonx.NewObject()
			_ = o.SetValue("label", sg.label)
			_ = o.SetValue("take", t.id)
			for _, k := range sg.obj.Keys() {
				if k != "label" {
					o.Set(k, field(sg.obj, k))
				}
			}
			res.Doc.Segments = append(res.Doc.Segments, o)
		}
	}
	for _, c := range comments {
		res.Doc.Chronological = append(res.Doc.Chronological, c.Order)
	}
	raw, err := jsonx.Marshal(res.Doc)
	if err == nil {
		raw, err = jsonx.Indent1(raw)
	}
	if err != nil {
		return nil, fmt.Errorf("notes.json: %w", err)
	}
	if err := store.WriteFileAtomic(res.MDPath, []byte(res.Markdown)); err != nil {
		return nil, err
	}
	if err := store.WriteFileAtomic(res.JSONPath, raw); err != nil {
		return nil, err
	}
	return res, nil
}

func inRanges(t float64, ranges [][2]float64) bool {
	for _, r := range ranges {
		if r[0] <= t && t <= r[1] {
			return true
		}
	}
	return false
}

// attachVO quotes the edit's own voice-over around every (audio) comment.
func attachVO(comments []*Comment, vw []voWord) {
	for _, c := range comments {
		if !c.Audio {
			continue
		}
		lo, hi := float64(c.VideoT)-1.5, float64(c.VideoTEnd)+1.5
		var ws []voWord
		for _, w := range vw {
			if w.e >= lo && w.s <= hi {
				ws = append(ws, w)
			}
		}
		if len(ws) == 0 {
			continue
		}
		parts := make([]string, len(ws))
		for i, w := range ws {
			parts[i] = w.w
		}
		text := strings.Join(parts, " ")
		if utf8.RuneCountInString(text) > 240 {
			text = string([]rune(text)[:240])
		}
		c.VO = &VO{Text: text, Start: jsonFloat(r3(ws[0].s)), End: jsonFloat(r3(ws[len(ws)-1].e))}
	}
}

// familyLabels are the version labels the takes recorded (sorted by number), else the given family's.
func familyLabels(takes []*take, fam []family.Version) []string {
	seen := map[string]bool{}
	var labels []string
	for _, t := range takes {
		for _, x := range readObjects(field(t.meta, "versions")) {
			l := str(x["label"])
			if !seen[l] {
				seen[l] = true
				labels = append(labels, l)
			}
		}
	}
	if len(labels) == 0 {
		for _, v := range fam {
			labels = append(labels, v.Label)
		}
	}
	sort.Strings(labels) // set order is arbitrary in legacy; ties (non-numbered labels) go by name here
	sort.SliceStable(labels, func(i, j int) bool { return labelNum(labels[i]) < labelNum(labels[j]) })
	if labels == nil {
		labels = []string{}
	}
	return labels
}

// labelNum is legacy's sort key: the number after the first character when it's all digits, else 0.
func labelNum(l string) int {
	_, size := utf8.DecodeRuneInString(l)
	rest := l[size:]
	if rest == "" {
		return 0
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return n
}
