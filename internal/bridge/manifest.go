// Package bridge connects the review side to the editor (spec/bridge.md): it reads render manifests
// (spec/manifest.md) to resolve notes to timeline elements, to map one version's times onto another's and to carry
// notes over, finds a project's timeline, and runs retake-edit. It reads only files, so a manifest written by any
// renderer works.
package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Manifest is a render manifest, manifest/0.1.
type Manifest struct {
	Schema   string      `json:"schema"`
	Timeline TimelineRef `json:"timeline"`
	Video    string      `json:"video,omitempty"`
	Size     [2]float64  `json:"size"`
	FPS      float64     `json:"fps"`
	Duration float64     `json:"duration"`
	Frames   int64       `json:"frames"`
	Range    [2]float64  `json:"range"`
	Draft    bool        `json:"draft"`
	Units    []Unit      `json:"units"`
}

// TimelineRef names the timeline revision a manifest was rendered from.
type TimelineRef struct {
	ID   string `json:"id"`
	Rev  int64  `json:"rev"`
	Hash string `json:"hash"`
}

// Unit is a shot (cut at insert edges) or an insert: one encoded segment.
type Unit struct {
	Unit      string     `json:"unit"`
	Key       string     `json:"key"`
	Kind      string     `json:"kind"`
	Scene     string     `json:"scene,omitempty"`
	Shot      string     `json:"shot,omitempty"`
	T         [2]float64 `json:"t"`
	Frames    [2]int64   `json:"frames"`
	VideoHash string     `json:"video_hash,omitempty"`
	AudioHash string     `json:"audio_hash,omitempty"`
	Elements  []Element  `json:"elements"`
	Words     []Word     `json:"words"`
}

// Element is a timeline element live in a unit.
type Element struct {
	ID      string            `json:"id"`
	Ptr     string            `json:"ptr"`
	Kind    string            `json:"kind"`
	Z       int64             `json:"z"`
	T       [2]float64        `json:"t"`
	Box     [][5]float64      `json:"box,omitempty"`
	Sources []Source          `json:"sources,omitempty"`
	Anchors map[string]string `json:"anchors,omitempty"`
	Parent  string            `json:"parent,omitempty"`
}

// Source is the stretch of an asset an element plays in a unit, in source seconds.
type Source struct {
	Asset string  `json:"asset"`
	In    float64 `json:"in"`
	Out   float64 `json:"out"`
}

// Word is a spine word, [start, end, text] in output seconds.
type Word struct {
	T0, T1 float64
	Text   string
}

// UnmarshalJSON reads [start, end, text].
func (w *Word) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || len(raw) != 3 {
		return fmt.Errorf("a word is [start, end, text]: %s", b)
	}
	if err := json.Unmarshal(raw[0], &w.T0); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &w.T1); err != nil {
		return err
	}
	return json.Unmarshal(raw[2], &w.Text)
}

// MarshalJSON writes [start, end, text].
func (w Word) MarshalJSON() ([]byte, error) { return json.Marshal([]any{w.T0, w.T1, w.Text}) }

// BoxAt returns the element's box at output time t ([x, y, w, h] in manifest pixels), or false when it isn't on
// screen.
func (e Element) BoxAt(t float64) ([4]float64, bool) {
	for i := len(e.Box) - 1; i >= 0; i-- {
		b := e.Box[i]
		if b[0] <= t+1e-9 {
			if b[3] <= 0 || b[4] <= 0 {
				return [4]float64{}, false
			}
			return [4]float64{b[1], b[2], b[3], b[4]}, true
		}
	}
	return [4]float64{}, false
}

// UnitAt returns the index of the unit holding output time t (the last unit also holds its end), or -1.
func (m *Manifest) UnitAt(t float64) int {
	last := len(m.Units) - 1
	for i, u := range m.Units {
		if t >= u.T[0] && (t < u.T[1] || (i == last && t <= u.T[1])) {
			return i
		}
	}
	return -1
}

// End is the last output second the manifest covers.
func (m *Manifest) End() float64 {
	if len(m.Units) == 0 {
		return m.Duration
	}
	return m.Units[len(m.Units)-1].T[1]
}

// sidecar returns the path of a file beside a video: X.mp4 -> X<suffix>.
func sidecar(video, suffix string) string {
	return strings.TrimSuffix(video, filepath.Ext(video)) + suffix
}

// ManifestPath is where a version's render manifest is: X.mp4 -> X.manifest.json beside it.
func ManifestPath(video string) string { return sidecar(video, ".manifest.json") }

// PushRecordPath is where a rendered push's record is: X.mp4 -> X.push.json beside it.
func PushRecordPath(video string) string { return sidecar(video, ".push.json") }

// ErrNoManifest is returned for a version that has no render manifest.
var ErrNoManifest = errors.New("no render manifest")

// Load reads a render manifest.
func Load(path string) (*Manifest, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a manifest beside a version of the family
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoManifest
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !strings.HasPrefix(m.Schema, "manifest/") {
		return nil, fmt.Errorf("%s: not a render manifest (schema %q)", path, m.Schema)
	}
	return &m, nil
}

// ForVideo reads a version's render manifest: ErrNoManifest when it has none.
func ForVideo(video string) (*Manifest, error) { return Load(ManifestPath(video)) }
