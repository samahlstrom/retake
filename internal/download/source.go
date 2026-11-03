package download

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// ForVersion is the download of a version of the family on this computer's disk. A streamed version downloads its
// export (spec/bridge.md, "Download"); a file is itself, or made from it. ed renders exports.
func ForVersion(root string, v family.Version, ed bridge.Editor) Source {
	s := Source{Root: root, Path: v.Path, Env: ed.Env}
	switch {
	case v.Kind != "":
		s.Unavailable = v.Label + " isn't a video"
		return s
	case v.Stream != nil && v.Stream.State != family.StateComplete:
		s.Unavailable = v.Label + " is still rendering"
		return s
	}
	if man := bridge.ManifestPath(v.Path); isFile(man) {
		s.Manifest = man
	}
	if v.Stream != nil {
		rec, err := bridge.ReadPushRecord(v.Path)
		if err != nil || rec == nil || rec.Stream == nil {
			s.Unavailable = v.Label + " has no render record"
			return s
		}
		dir := filepath.Dir(v.Path)
		timeline := rec.Timeline
		if timeline != "" && !filepath.IsAbs(timeline) {
			timeline = filepath.Join(root, filepath.FromSlash(timeline))
		}
		if timeline == "" {
			timeline, _ = bridge.FindTimeline(root)
		}
		export := family.Stem(filepath.Base(v.Path)) + ".export.mp4"
		if rec.Stream.Export != "" {
			export = rec.Stream.Export
		}
		s.Render = &Render{Editor: ed, Timeline: timeline, Doc: filepath.Join(dir, rec.Stream.Doc),
			Export: filepath.Join(dir, export), Draft: rec.Draft}
		s.Keep = masterLoudness(s.Render.Doc)
		return s
	}
	if tl, err := bridge.FindTimeline(root); err == nil {
		s.Keep = masterLoudness(tl) // a timeline project's own render keeps the master's level
	}
	return s
}

// ForRender is the download of an editing worker's render (spec/automation.md): its export, index.export.mp4
// beside its playlist, from its source.json. dir is the render's folder, timeline the project's timeline file and
// bin the worker's retake-edit.
func ForRender(root, dir, timeline, bin string, ed bridge.Editor) Source {
	s := Source{Root: root, Path: filepath.Join(dir, "index.m3u8"), Env: ed.Env}
	var v project.LiveRender
	b, err := os.ReadFile(filepath.Join(dir, "state.json")) //nolint:gosec // a render's own state, under the worker's folder
	if err != nil || json.Unmarshal(b, &v) != nil || v.ID != filepath.Base(dir) {
		s.Unavailable = "This render isn't on this computer"
		return s
	}
	label := v.Label
	if label == "" {
		label = "This version"
	}
	if !v.Complete {
		s.Unavailable = label + " is still rendering"
		return s
	}
	doc := filepath.Join(dir, "source.json")
	if !isFile(doc) {
		s.Unavailable = label + " has no source to package"
		return s
	}
	s.Render = &Render{Editor: ed, Bin: bin, Timeline: timeline, Doc: doc, Export: filepath.Join(dir, "index.export.mp4")}
	s.Keep = masterLoudness(doc)
	if man := filepath.Join(dir, "index.manifest.json"); isFile(man) {
		s.Manifest = man
	}
	return s
}

// masterLoudness reports whether a timeline document's master sets a loudness of its own.
func masterLoudness(doc string) bool {
	b, err := os.ReadFile(doc) //nolint:gosec // the project's timeline or a version's snapshot
	if err != nil {
		return false
	}
	var tl struct {
		Master *struct {
			Loudness *float64 `json:"loudness"`
		} `json:"master"`
	}
	return json.Unmarshal(b, &tl) == nil && tl.Master != nil && tl.Master.Loudness != nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
