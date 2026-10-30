package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// Summary is one entry of a version's take list (/api/info "sessions", `retake sessions`).
type Summary struct {
	ID        string          `json:"id"`
	Narration json.RawMessage `json:"narration"`
	Segments  int             `json:"segments"`
	Strokes   int             `json:"strokes"`
	Notes     bool            `json:"notes"`
	Saved     json.RawMessage `json:"saved"`
}

// HistoryEntry is one earlier take's marks, as the page draws them on the timeline.
type HistoryEntry struct {
	ID            string          `json:"id"`
	InheritedFrom json.RawMessage `json:"inherited_from"`
	Voice         json.RawMessage `json:"voice"`
	Strokes       []HistoryStroke `json:"strokes"`
}

// HistoryStroke is a stroke reduced to what the page needs to draw it.
type HistoryStroke struct {
	ID     json.RawMessage `json:"id,omitempty"` // the stroke's id in its take: what the eraser names
	VideoT json.RawMessage `json:"video_t"`
	Color  json.RawMessage `json:"color"`
	Points json.RawMessage `json:"points"`
}

var null = json.RawMessage("null")

// readJSON returns the file's JSON, or ok=false if it's missing or not valid JSON.
func readJSON(path string) (json.RawMessage, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // reads take files under the version's .review folder
	if err != nil {
		return nil, false
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !json.Valid(b) {
		return nil, false
	}
	return b, true
}

// readObject returns the file's JSON object, or an empty one.
func readObject(path string) *jsonx.Object {
	raw, ok := readJSON(path)
	if !ok || jsonx.Kind(raw) != "object" {
		return jsonx.NewObject()
	}
	o, err := jsonx.ParseObject(raw)
	if err != nil {
		return jsonx.NewObject()
	}
	return o
}

func field(o *jsonx.Object, key string) json.RawMessage {
	v, ok := o.Get(key)
	if !ok {
		return nil
	}
	return v
}

func asArray(raw json.RawMessage) []json.RawMessage {
	if jsonx.Kind(raw) != "array" {
		return nil
	}
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil
	}
	return a
}

func asObject(raw json.RawMessage) *jsonx.Object {
	if jsonx.Kind(raw) != "object" {
		return nil
	}
	o, err := jsonx.ParseObject(raw)
	if err != nil {
		return nil
	}
	return o
}

// takeDirs lists a version's take folders, sorted by name.
func takeDirs(video string) []string {
	rd := ReviewDir(video)
	entries, err := os.ReadDir(rd)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !ValidTakeID(e.Name()) {
			continue
		}
		d := filepath.Join(rd, e.Name())
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// Sessions summarizes every take of a version, sorted by id.
func Sessions(video string) []Summary {
	out := []Summary{}
	for _, d := range takeDirs(video) {
		out = append(out, Summarize(d))
	}
	return out
}

// Summarize reads one take folder. narration is the sum of t_end - t_start over segments.json; without that file
// it falls back to session.segments, and with no segments at all to session.rec.duration.
func Summarize(dir string) Summary {
	meta := readObject(filepath.Join(dir, "session.json"))
	strokes := 0
	if raw, ok := readJSON(filepath.Join(dir, "strokes.json")); ok {
		strokes = len(asArray(raw))
	}
	segs, ok := readJSON(filepath.Join(dir, "segments.json"))
	if !ok || jsonx.Kind(segs) == "null" {
		segs = field(meta, "segments")
	}
	list := asArray(segs)
	var narration json.RawMessage
	if len(list) > 0 {
		narration = sumTalk(list)
	} else if rec := asObject(field(meta, "rec")); rec != nil {
		narration = field(rec, "duration")
	}
	if narration == nil {
		narration = null
	}
	saved := field(meta, "saved")
	if saved == nil {
		saved = null
	}
	_, err := os.Stat(filepath.Join(dir, "notes.md"))
	return Summary{
		ID: filepath.Base(dir), Narration: narration, Segments: len(list), Strokes: strokes,
		Notes: err == nil, Saved: saved,
	}
}

// sumTalk sums t_end - t_start like Python: the result is an int unless a term was a float.
func sumTalk(segs []json.RawMessage) json.RawMessage {
	var sum float64
	isFloat := false
	term := func(o *jsonx.Object, key string) float64 {
		raw := field(o, key)
		f, ok := jsonx.Number(raw)
		if !ok || f == 0 {
			return 0 // `or 0`: missing, null and zero all count as the integer 0
		}
		if bytes.ContainsAny(raw, ".eE") {
			isFloat = true
		}
		return f
	}
	for _, s := range segs {
		o := asObject(s)
		if o == nil {
			continue
		}
		sum += term(o, "t_end") - term(o, "t_start")
	}
	if isFloat {
		return json.RawMessage(jsonx.FormatFloat(sum))
	}
	return json.RawMessage(strconv.FormatFloat(sum, 'f', -1, 64))
}

// Inherited is one take carried over onto a version by `review inherit` (inherit.json).
type Inherited struct {
	Dir     string
	Version json.RawMessage
	Ranges  [][2]float64
}

// defaultRanges is what an inherit.json entry without ranges covers: everything.
var defaultRanges = [][2]float64{{0, 1e9}}

// InheritedTakes reads <version>.review/inherit.json: {"takes": [{dir, version, ranges, ...}]}. Entries whose dir
// no longer exists are left out. A relative dir is taken relative to the .review folder.
func InheritedTakes(video string) []Inherited {
	rd := ReviewDir(video)
	doc := readObject(filepath.Join(rd, "inherit.json"))
	var out []Inherited
	for _, x := range asArray(field(doc, "takes")) {
		o := asObject(x)
		if o == nil {
			continue
		}
		var dir string
		if raw := field(o, "dir"); jsonx.Kind(raw) == "string" {
			if err := json.Unmarshal(raw, &dir); err != nil {
				continue
			}
		}
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rd, dir)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		ver := field(o, "version")
		if ver == nil {
			ver = null
		}
		ranges := defaultRanges
		if raw := field(o, "ranges"); jsonx.Truthy(raw) {
			ranges = parseRanges(raw)
		}
		out = append(out, Inherited{Dir: filepath.Clean(dir), Version: ver, Ranges: ranges})
	}
	return out
}

func parseRanges(raw json.RawMessage) [][2]float64 {
	out := [][2]float64{}
	for _, r := range asArray(raw) {
		ab := asArray(r)
		if len(ab) != 2 {
			continue
		}
		a, okA := jsonx.Number(ab[0])
		b, okB := jsonx.Number(ab[1])
		if okA && okB {
			out = append(out, [2]float64{a, b})
		}
	}
	return out
}

func inRanges(t float64, ranges [][2]float64) bool {
	for _, r := range ranges {
		if r[0] <= t && t <= r[1] {
			return true
		}
	}
	return false
}

// History returns the strokes and voice coverage of every take of a version, then of every take carried over by
// inherit.json, so the page can show them on the timeline. Carried-over takes keep only the strokes, and the
// voice bins, inside their ranges. Lines erased on this version (erased.json) are left out.
func History(video string) []HistoryEntry {
	out := []HistoryEntry{}
	erased := ReadErased(ReviewDir(video))
	for _, d := range takeDirs(video) {
		out = append(out, historyEntry(d, null, nil, erased[filepath.Base(d)]))
	}
	for _, x := range InheritedTakes(video) {
		out = append(out, historyEntry(x.Dir, x.Version, x.Ranges, erased[filepath.Base(x.Dir)]))
	}
	return out
}

func historyEntry(dir string, from json.RawMessage, ranges [][2]float64, erased map[int64]bool) HistoryEntry {
	inherited := ranges != nil
	var strokes []json.RawMessage
	if raw, ok := readJSON(filepath.Join(dir, "strokes.json")); ok {
		strokes = asArray(raw)
	}
	voice := json.RawMessage("{}")
	if raw, ok := readJSON(filepath.Join(dir, "voice.json")); ok && jsonx.Truthy(raw) {
		voice = raw
	}
	if inherited {
		voice = clipVoice(voice, ranges)
	}
	out := HistoryEntry{ID: filepath.Base(dir), InheritedFrom: from, Voice: voice, Strokes: []HistoryStroke{}}
	for _, s := range strokes {
		o := asObject(s)
		if o == nil {
			continue
		}
		id := field(o, "id")
		if n, ok := jsonx.Number(id); ok && erased[int64(n)] {
			continue
		}
		vt := field(o, "video_t")
		if inherited {
			t, ok := jsonx.Number(vt)
			if !ok || !inRanges(t, ranges) {
				continue
			}
		}
		if vt == nil {
			vt = null
		}
		color, ok := o.Get("color")
		if !ok {
			color = json.RawMessage(`"red"`)
		}
		points := field(o, "points")
		if !jsonx.Truthy(points) {
			points = json.RawMessage("[]")
		}
		out.Strokes = append(out.Strokes, HistoryStroke{ID: id, VideoT: vt, Color: color, Points: points})
	}
	return out
}

// clipVoice keeps the voice bins [i, level] whose time i*bin is inside ranges: {"bin": bin, "bins": [...]}.
func clipVoice(voice json.RawMessage, ranges [][2]float64) json.RawMessage {
	o := asObject(voice)
	if o == nil {
		o = jsonx.NewObject()
	}
	binRaw, ok := o.Get("bin")
	if !ok {
		binRaw = json.RawMessage("0.25")
	}
	bin, binOK := jsonx.Number(binRaw)
	bins := []json.RawMessage{}
	for _, pair := range asArray(field(o, "bins")) {
		iv := asArray(pair)
		if len(iv) != 2 || !binOK {
			continue
		}
		i, ok := jsonx.Number(iv[0])
		if !ok || !inRanges(i*bin, ranges) {
			continue
		}
		b, err := jsonx.Marshal([]json.RawMessage{iv[0], iv[1]})
		if err != nil {
			continue
		}
		bins = append(bins, b)
	}
	res := jsonx.NewObject()
	res.Set("bin", binRaw)
	if err := res.SetValue("bins", bins); err != nil {
		return json.RawMessage(`{"bin":0.25,"bins":[]}`)
	}
	b, err := res.MarshalJSON()
	if err != nil {
		return json.RawMessage(`{"bin":0.25,"bins":[]}`)
	}
	return b
}
