package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
	"github.com/samahlstrom/retake/internal/transcribe"
)

type historyVoiceTrack struct {
	Seg         string          `json:"seg"`
	Transcribed bool            `json:"transcribed"`
	Voice       json.RawMessage `json:"voice"`
}

// Completion is about the saved audio bytes, never about whether an edit was resolved.
func completedVoice(dir string) (bool, []historyVoiceTrack) {
	raw, err := os.ReadFile(filepath.Join(dir, "segments.json")) //nolint:gosec // validated take directory
	if err != nil {
		return false, nil
	}
	var segments []struct {
		ID        string          `json:"id"`
		File      string          `json:"file"`
		Recording bool            `json:"recording"`
		Voice     json.RawMessage `json:"voice"`
	}
	if json.Unmarshal(raw, &segments) != nil || len(segments) == 0 {
		return false, nil
	}
	tr, err := transcribe.ReadTranscript(filepath.Join(dir, transcribe.TranscriptFile))
	if err != nil {
		return false, nil
	}
	all, tracked := true, true
	var tracks []historyVoiceTrack
	for _, seg := range segments {
		complete := false
		if !seg.Recording && store.ValidSegName(seg.File) {
			if info, err := os.Stat(filepath.Join(dir, seg.File)); err == nil {
				if f, ok := tr.File(seg.File); ok && info.Size() > 0 && f.Size == info.Size() {
					complete = true
				}
			}
		}
		all = all && complete
		tracked = tracked && len(seg.Voice) > 0 && string(seg.Voice) != "null"
		tracks = append(tracks, historyVoiceTrack{Seg: seg.ID, Transcribed: complete, Voice: seg.Voice})
	}
	if !tracked {
		tracks = nil
	}
	return all, tracks
}

// Carry the actual annotation geometry, restricted to relevant feedback ranges. Sources remain immutable here;
// the eraser on the source version still works normally.
func (s *Server) reviewHistory(v family.Version, out []historyEntry) []historyEntry {
	st, err := s.store(false)
	if err != nil {
		return out
	}
	notes, err := st.Notes(project.NoteFilter{})
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, h := range out {
		seen[h.ID] = true
	}
	target := s.manifestOf(v)
	for _, source := range s.Family() {
		if source.N >= v.N {
			continue
		}
		ranges := map[string][][2]float64{}
		for _, n := range notes {
			if n.Version == source.Label && (n.Status == project.StatusOpen || n.Status == project.StatusQuestion || n.AnsweredIn == v.Label) && n.Status != project.StatusRetracted {
				ranges[n.Take] = append(ranges[n.Take], [2]float64{n.T0 - 2.5, n.T1 + 2.5})
			}
		}
		if len(ranges) == 0 {
			continue
		}
		var tm *bridge.TimeMap
		if origin := s.manifestOf(source); origin != nil && target != nil {
			m := bridge.BuildTimeMap(source.Label, v.Label, origin, target)
			tm = &m
		}
		for _, h := range s.history(source) {
			keep := ranges[h.ID]
			if seen[h.ID] || len(keep) == 0 {
				continue
			}
			h.SourceVersion = source.Label
			h.Voice, h.VoiceTracks = json.RawMessage("{}"), nil
			h.Strokes = slices.DeleteFunc(h.Strokes, func(stroke store.HistoryStroke) bool {
				var t float64
				return json.Unmarshal(stroke.VideoT, &t) != nil || !inAny(t, keep)
			})
			for i := range h.Strokes {
				if tm != nil {
					var t float64
					_ = json.Unmarshal(h.Strokes[i].VideoT, &t)
					h.Strokes[i].VideoT, _ = json.Marshal(tm.Map(t))
				}
			}
			h.Images = slices.DeleteFunc(h.Images, func(im historyImage) bool { return !inAny(im.VideoT, keep) })
			for i := range h.Images {
				if tm != nil {
					h.Images[i].VideoT = tm.Map(h.Images[i].VideoT)
				}
			}
			seen[h.ID] = true
			out = append(out, h)
		}
	}
	return out
}
