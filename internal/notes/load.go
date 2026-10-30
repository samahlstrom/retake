package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
)

// take is one take folder, loaded.
type take struct {
	id          string // the folder name; "<name> (<version>)" for an inherited take
	folder      string // the folder name
	dir         string
	meta        *jsonx.Object
	video       *jsonx.Object // session.json "video"
	version     *string
	events      []map[string]json.RawMessage
	strokes     []map[string]json.RawMessage
	images      []map[string]json.RawMessage
	segments    []*segment
	words       []word
	models      map[string]bool
	recorded    float64
	warnings    []string
	dropped     int
	author      string
	ranges      [][2]float64 // inherited takes only
	rangesRaw   json.RawMessage
	inheritNote json.RawMessage
	inherited   bool
	missing     []Untranscribed

	segMap  map[string]string // pyKey(segment id) -> label (S1..Sn across all takes)
	segIDs  map[string]string // label -> the segment's own id
	segSpan []segSpan
}

// segment is one segments.json entry; obj keeps every field for notes.json.
type segment struct {
	obj   *jsonx.Object
	key   string // pyKey of its id
	id    string // its id as text
	label string
}

func (sg *segment) get(k string) json.RawMessage {
	v, _ := sg.obj.Get(k)
	return v
}

type segSpan struct {
	a, e  float64
	label string
}

// Untranscribed is a recorded segment that has no transcript yet (or one for an older, shorter file).
type Untranscribed struct {
	Take string `json:"take"` // the take folder
	Seg  string `json:"seg"`  // the segment's id in the take
	File string `json:"file"` // the audio file
}

// readJSON reads a JSON file. ok is false when it's missing; a file that isn't valid JSON is an error.
func readJSON(path string) (json.RawMessage, bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // reads take files the caller pointed us at
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !json.Valid(b) {
		return nil, false, fmt.Errorf("%s: not valid JSON", path)
	}
	return b, true, nil
}

func readObjects(raw json.RawMessage) []map[string]json.RawMessage {
	var list []json.RawMessage
	if jsonx.Kind(raw) != "array" || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([]map[string]json.RawMessage, 0, len(list))
	for _, x := range list {
		var m map[string]json.RawMessage
		if jsonx.Kind(x) == "object" && json.Unmarshal(x, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func objectOf(raw json.RawMessage) *jsonx.Object {
	if jsonx.Kind(raw) == "object" {
		if o, err := jsonx.ParseObject(raw); err == nil {
			return o
		}
	}
	return jsonx.NewObject()
}

func field(o *jsonx.Object, k string) json.RawMessage {
	v, _ := o.Get(k)
	return v
}

// verLabel is the legacy ver_label: "v7" for clip_v7_draft.mp4, nil without a version number.
func verLabel(name string) *string {
	nm, ok := family.ParseStem(family.Stem(filepath.Base(name)))
	if !ok {
		return nil
	}
	l := fmt.Sprintf("v%d", nm.N)
	return &l
}

func filepathJoin(dir, name string) string { return filepath.Join(dir, name) }

func parsePoints(raw json.RawMessage) [][2]float64 {
	var list []json.RawMessage
	if jsonx.Kind(raw) != "array" || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([][2]float64, 0, len(list))
	for _, p := range list {
		if xy := parseFloats(p, 2); len(xy) == 2 {
			out = append(out, [2]float64{xy[0], xy[1]})
		}
	}
	return out
}

// parseFloats reads a JSON array of n numbers, or returns nil.
func parseFloats(raw json.RawMessage, n int) []float64 {
	var list []json.RawMessage
	if jsonx.Kind(raw) != "array" || json.Unmarshal(raw, &list) != nil || len(list) != n {
		return nil
	}
	out := make([]float64, n)
	for i, x := range list {
		f, ok := num(x)
		if !ok {
			return nil
		}
		out[i] = f
	}
	return out
}

// loadTake reads a take folder. Transcripts are taken from transcript.json when its entry matches the audio
// file's size (any model: the transcriber decides which one); segments without one are reported in missing.
func loadTake(dir string) (*take, error) {
	metaRaw, ok, err := readJSON(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	if !ok || jsonx.Kind(metaRaw) != "object" {
		return nil, fmt.Errorf("%s: no session.json", dir)
	}
	t := &take{id: filepath.Base(dir), folder: filepath.Base(dir), dir: dir, meta: objectOf(metaRaw),
		models: map[string]bool{}}
	t.video = objectOf(field(t.meta, "video"))
	if v := field(t.meta, "version"); jsonx.Kind(v) == "string" {
		s := str(v)
		t.version = &s
	} else {
		t.version = verLabel(str(field(t.video, "name")))
	}
	if a := objectOf(field(t.meta, "author")); jsonx.Kind(field(a, "name")) == "string" {
		t.author = str(field(a, "name"))
	}
	read := func(name string) json.RawMessage {
		raw, ok, err := readJSON(filepath.Join(dir, name))
		if err != nil {
			t.warnings = append(t.warnings, fmt.Sprintf("%s: %s is not valid JSON, ignored", t.folder, name))
		}
		if !ok {
			return nil
		}
		return raw
	}
	t.events = readObjects(read("events.json"))
	t.strokes = readObjects(read("strokes.json"))
	t.images = readObjects(read("images.json"))
	segsRaw := read("segments.json")
	if segsRaw == nil || jsonx.Kind(segsRaw) == "null" {
		// format 2 (one long take): the whole narration is one segment starting at 0
		for _, x := range []string{"webm", "ogg", "m4a", "wav"} {
			if _, err := os.Stat(filepath.Join(dir, "narration."+x)); err == nil {
				o := jsonx.NewObject()
				o.Set("id", json.RawMessage(`"S1"`))
				_ = o.SetValue("file", "narration."+x)
				o.Set("t_start", json.RawMessage("0.0"))
				end := field(objectOf(field(t.meta, "rec")), "duration")
				if !truthy(end) {
					end = json.RawMessage("0.0")
				}
				o.Set("t_end", end)
				segsRaw, _ = jsonx.Marshal([]*jsonx.Object{o})
				break
			}
		}
	}
	var segList []json.RawMessage
	if jsonx.Kind(segsRaw) == "array" {
		_ = json.Unmarshal(segsRaw, &segList)
	}
	for _, raw := range segList {
		o := objectOf(raw)
		t.segments = append(t.segments, &segment{obj: o, key: pyKey(field(o, "id")), id: str(field(o, "id"))})
	}
	files := map[string]json.RawMessage{}
	if f := field(objectOf(read("transcript.json")), "files"); jsonx.Kind(f) == "object" {
		_ = json.Unmarshal(f, &files)
	}
	for _, sg := range t.segments {
		file := str(sg.get("file"))
		if !truthy(sg.get("file")) {
			continue
		}
		p := filepath.Join(dir, file)
		st, err := os.Stat(p)
		if err != nil || st.Size() == 0 {
			continue
		}
		hit := objectOf(files[filepath.Base(p)])
		size, okSize := num(field(hit, "size"))
		if !okSize || size != float64(st.Size()) {
			t.missing = append(t.missing, Untranscribed{Take: t.folder, Seg: sg.id, File: file})
			t.warnings = append(t.warnings, fmt.Sprintf("%s %s: %s is not transcribed yet", t.folder, sg.id, file))
			continue
		}
		t.models[str(field(hit, "model"))] = true
		dur := numOr(field(hit, "duration"), 0)
		t.recorded += dur
		page := numOr(sg.get("t_end"), 0) - numOr(sg.get("t_start"), 0)
		if page != 0 && math.Abs(page-dur) > 1.5 {
			t.warnings = append(t.warnings, fmt.Sprintf("%s %s: audio is %.1fs but the page clock says %.1fs",
				t.folder, sg.id, dur, page))
		}
		t0 := numOr(sg.get("t_start"), 0)
		for _, w := range readObjects(field(hit, "words")) {
			p := 1.0
			if v, ok := num(w["p"]); ok {
				p = v
			}
			t.words = append(t.words, word{w: str(w["w"]), s: r3(t0 + numOr(w["s"], 0)), e: r3(t0 + numOr(w["e"], 0)),
				p: p, seg: sg.key})
		}
	}
	sort.SliceStable(t.words, func(i, j int) bool { return t.words[i].s < t.words[j].s })
	return t, nil
}

// relabelSegments numbers segments S1..Sn across all takes, in recording order.
func relabelSegments(takes []*take) {
	n := 0
	for _, t := range takes {
		t.segMap, t.segIDs = map[string]string{}, map[string]string{}
		segs := append([]*segment(nil), t.segments...)
		sort.SliceStable(segs, func(i, j int) bool {
			return numOr(segs[i].get("t_start"), 0) < numOr(segs[j].get("t_start"), 0)
		})
		for _, sg := range segs {
			n++
			sg.label = fmt.Sprintf("S%d", n)
			t.segMap[sg.key] = sg.label
			t.segIDs[sg.label] = sg.id
		}
		for i := range t.words {
			if l, ok := t.segMap[t.words[i].seg]; ok {
				t.words[i].seg = l
			}
		}
		t.segSpan = t.segSpan[:0]
		for _, sg := range t.segments {
			a := numOr(sg.get("t_start"), 0)
			e := 1e9
			if truthy(sg.get("t_end")) {
				e = numOr(sg.get("t_end"), 1e9)
			}
			t.segSpan = append(t.segSpan, segSpan{a: a, e: e, label: sg.label})
		}
		sort.SliceStable(t.segSpan, func(i, j int) bool {
			x, y := t.segSpan[i], t.segSpan[j]
			if x.a != y.a {
				return x.a < y.a
			}
			if x.e != y.e {
				return x.e < y.e
			}
			return x.label < y.label
		})
	}
}

// segOf returns the label for a stroke's or image's seg field, or the segment recording at s when it has none.
func (t *take) segOf(raw json.RawMessage, s float64) string {
	if truthy(raw) {
		if l, ok := t.segMap[pyKey(raw)]; ok {
			return l
		}
		return str(raw)
	}
	for _, sp := range t.segSpan {
		if sp.a-0.5 <= s && s <= sp.e+0.5 {
			return sp.label
		}
	}
	return "no-audio"
}

// segID returns the take's own id for a segment label (the label itself when it isn't one of the take's).
func (t *take) segID(label string) string {
	if id, ok := t.segIDs[label]; ok {
		return id
	}
	return label
}

// voWord is one word of the edit's own voice-over.
type voWord struct {
	w    string
	s, e float64
}

// loadVideoWords reads the edit's own VO transcript: [{w,s,e}], [{word,start,end}], [{words:[...]}] or
// {"words": [...]} / {"segments": [...]}.
func loadVideoWords(path string) ([]voWord, error) {
	raw, ok, err := readJSON(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: no such file", path)
	}
	if jsonx.Kind(raw) == "object" {
		var o map[string]json.RawMessage
		_ = json.Unmarshal(raw, &o)
		raw = o["words"]
		if !truthy(raw) {
			raw = o["segments"]
		}
	}
	var flat []map[string]json.RawMessage
	for _, x := range readObjects(raw) {
		if ws := x["words"]; jsonx.Kind(ws) == "array" {
			flat = append(flat, readObjects(ws)...)
		} else {
			flat = append(flat, x)
		}
	}
	var out []voWord
	for _, x := range flat {
		w := x["w"]
		if !truthy(w) {
			w = x["word"]
		}
		if !truthy(w) {
			w = x["text"]
		}
		s0, e0 := x["s"], x["e"]
		if _, has := x["s"]; !has {
			s0 = x["start"]
		}
		if _, has := x["e"]; !has {
			e0 = x["end"]
		}
		s, okS := num(s0)
		e, okE := num(e0)
		if truthy(w) && okS && okE {
			out = append(out, voWord{w: strings.TrimSpace(str(w)), s: s, e: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].s < out[j].s })
	return out, nil
}
