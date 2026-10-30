package notes

import (
	"encoding/json"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// jsonFloat encodes like Python's json.dumps (40.0, not 40), so notes.json reads like the legacy file.
type jsonFloat = jsonx.Float

// Doc is notes.json. Its structure and keys are the legacy engine's; the fields marked new are additions that
// legacy readers ignore.
type Doc struct {
	Video         *jsonx.Object   `json:"video"` // {path, name, duration, fps, width, height}
	Version       string          `json:"version"`
	Family        []string        `json:"family"`
	Window        jsonFloat       `json:"window"`
	MaxSpan       jsonFloat       `json:"max_span"`
	Generated     string          `json:"generated"`
	Rules         []string        `json:"rules"`
	Clock         string          `json:"clock"`
	Takes         []TakeInfo      `json:"takes"`
	Segments      []*jsonx.Object `json:"segments"` // {label, take, ...every field of segments.json}
	Flags         []FlagEntry     `json:"flags"`
	Moments       []*Moment       `json:"moments"`
	Chronological []int           `json:"chronological"` // comment orders, in spoken order
}

var docRules = []string{
	"later statements override earlier ones (higher order = later)",
	"a retraction voids the comments it refers to: same clip/moment unless Sam names a wider scope",
	"a reinstatement restores what that retraction voided",
	"when scope is unclear, ask Sam rather than guess; flags are heuristics, nothing was deleted",
}

const docClock = "video_t = seconds in the video; session_t = seconds since the take started (segment audio starts " +
	"at t_start)"

// TakeInfo describes one take that went into the notes.
type TakeInfo struct {
	Take                string          `json:"take"` // the folder name, plus " (v3)" for an inherited take
	Version             *string         `json:"version"`
	Recorded            jsonFloat       `json:"recorded"` // seconds of transcribed audio
	Ranges              json.RawMessage `json:"ranges"`   // inherited takes: the video ranges that count here
	InheritNote         json.RawMessage `json:"inherit_note"`
	Models              []string        `json:"models"`
	Warnings            []string        `json:"warnings"`
	SmallStrokesDropped int             `json:"small_strokes_dropped"`
}

// Moment is a cluster of comments close together in video time, shown with one snapshot.
type Moment struct {
	ID        string     `json:"id"` // M1, M2, ... in video order
	Start     jsonFloat  `json:"start"`
	End       jsonFloat  `json:"end"`
	Comments  []*Comment `json:"comments"` // in spoken order
	Snapshot  *string    `json:"snapshot"` // "snaps/01_00-41-2.png", relative to the notes folder
	SnapshotT *jsonFloat `json:"snapshot_t"`
}

// Comment is one note: what was said (and drawn) at one paused frame, or one sentence over a playing stretch.
type Comment struct {
	Take        string    `json:"take"`
	Seg         string    `json:"seg"` // S1..Sn, numbered across all the takes in these notes
	Version     *string   `json:"version"`
	Flip        bool      `json:"flip"`
	Pass        int       `json:"pass"`
	Mode        string    `json:"mode"` // "paused" or "playing"
	Anchor      jsonFloat `json:"anchor"`
	VideoT      jsonFloat `json:"video_t"`
	VideoTEnd   jsonFloat `json:"video_t_end"`
	SessionT    jsonFloat `json:"session_t"`
	SessionTEnd jsonFloat `json:"session_t_end"`
	Text        string    `json:"text"`
	Unclear     bool      `json:"unclear"`
	Moved       *Path     `json:"moved"`
	Audio       bool      `json:"audio"`
	Speech      []Speech  `json:"speech"`
	Strokes     []*Stroke `json:"strokes"`
	Images      []*Image  `json:"images,omitempty"` // new
	Order       int       `json:"order"`
	Moment      string    `json:"moment"`
	Flag        *Flag     `json:"flag"`
	VO          *VO       `json:"vo,omitempty"`
	// Key is the comment's stable key, <take>/<seg>/<n>: the take's folder, the segment's own id in that take, and
	// the comment's 1-based index within that segment (new).
	Key string `json:"key"`
	// AnswerTo is the thread id from the segment's answer_to: the comment answers that question (new).
	AnswerTo json.RawMessage `json:"answer_to,omitempty"`
	// Retracted: a flagged retraction voids this comment and no later reinstatement restored it (new).
	Retracted bool   `json:"retracted,omitempty"`
	Author    string `json:"author,omitempty"` // the take's session.json author.name (new)

	takeFolder string // the take's folder name, for Key
	origSeg    string // the segment's own id in the take, for Key
}

// TakeFolder is the take's folder name: Take without an inherited take's " (v3)".
func (c *Comment) TakeFolder() string { return c.takeFolder }

// SegID is the segment's own id in its take ("S2"), as in Key; Seg is renumbered across the takes of the notes.
func (c *Comment) SegID() string { return c.origSeg }

// Speech is one sentence of a comment.
type Speech struct {
	Text        string    `json:"text"`
	Conf        jsonFloat `json:"conf"` // mean word probability
	SessionT    jsonFloat `json:"session_t"`
	SessionTEnd jsonFloat `json:"session_t_end"`
	VideoT      jsonFloat `json:"video_t"`
	VideoTEnd   jsonFloat `json:"video_t_end"`
	Path        *Path     `json:"path"`
}

// Stroke is one pen stroke of a comment, with its shape.
type Stroke struct {
	ID          json.RawMessage `json:"id"`
	Color       string          `json:"color"`
	Seg         string          `json:"seg"`
	VideoT      json.RawMessage `json:"video_t"`
	Version     json.RawMessage `json:"version"`
	Flip        bool            `json:"flip"`
	Desc        string          `json:"desc"`
	BBox        [4]jsonFloat    `json:"bbox"`
	From        [2]jsonFloat    `json:"from"`
	To          [2]jsonFloat    `json:"to"`
	Shape       string          `json:"shape"` // line, arrow, circle, curve or x
	Points      json.RawMessage `json:"points"`
	At          *[2]jsonFloat   `json:"at,omitempty"` // an X's crossing
	N           int             `json:"n"`            // "stroke N" in notes.md and on the snapshot
	SessionT    jsonFloat       `json:"session_t"`
	SessionTEnd jsonFloat       `json:"session_t_end"`

	pts    [][2]float64
	videoT float64
}

func (s *Stroke) from() [2]float64 { return [2]float64{float64(s.From[0]), float64(s.From[1])} }
func (s *Stroke) to() [2]float64   { return [2]float64{float64(s.To[0]), float64(s.To[1])} }

// Image is a picture the reviewer dropped on the frame while recording (images.json; new).
type Image struct {
	ID       json.RawMessage `json:"id"`
	Seg      string          `json:"seg"`
	File     string          `json:"file"` // in the take folder
	Path     string          `json:"path"` // absolute
	Mime     string          `json:"mime,omitempty"`
	Version  json.RawMessage `json:"version"`
	Flip     bool            `json:"flip"`
	VideoT   json.RawMessage `json:"video_t"`
	Box      [4]jsonFloat    `json:"box"` // x, y, w, h, normalized 0-1 to the frame
	N        int             `json:"n"`   // "image N" in notes.md and on the snapshot
	SessionT jsonFloat       `json:"session_t"`

	videoT float64
}

// VO is the edit's own voice-over around an (audio) comment, from --video-transcript.
type VO struct {
	Text  string    `json:"text"`
	Start jsonFloat `json:"start"`
	End   jsonFloat `json:"end"`
}
