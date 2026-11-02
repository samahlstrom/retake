package ext

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/samahlstrom/retake/internal/config"
)

// Set is the extensions in effect, by id. A nil *Set has none, and all its methods work on it.
type Set struct {
	exts []*Ext // sorted by id
}

// Resolve picks the extensions that are on (spec/extensions.md, "Turning them on"): the ids in enabled and every one
// found with --ext. A project's extension also needs its pin to be its Hash. One that doesn't load (a fatal problem,
// an api other than API) stays off, and so does the second extension (by id) whose kind claims a folder another's
// already does. warnings are the startup lines for what's off and why; they never stop a server.
func Resolve(found []*Ext, enabled []string, pins map[string]string) (*Set, []string) {
	enabled = config.ExtensionIDs(enabled) // a renamed extension's old id is its new one
	var warnings []string
	var on []*Ext
	seen := map[string]bool{}
	for _, e := range found {
		seen[e.ID] = true
		if e.Where != WhereFlag && !slices.Contains(enabled, e.ID) {
			continue
		}
		if e.Where == WhereProject {
			switch pin := pins[e.ID]; {
			case pin == "":
				warnings = append(warnings, fmt.Sprintf("%s is the folder's own extension and isn't pinned: run retake ext "+
					"enable %s to trust it", e.ID, e.ID))
				continue
			case pin != e.Hash:
				warnings = append(warnings, fmt.Sprintf("%s changed since you enabled it: run retake ext enable %s again",
					e.ID, e.ID))
				continue
			}
		}
		if !e.OK() || e.API != API {
			warnings = append(warnings, notLoaded(e)...)
			continue
		}
		on = append(on, e)
	}
	for _, id := range enabled {
		if !seen[id] {
			warnings = append(warnings, fmt.Sprintf("%s is enabled but wasn't found (retake ext list shows what's found)", id))
			seen[id] = true
		}
	}
	slices.SortFunc(on, func(a, b *Ext) int { return strings.Compare(a.ID, b.ID) })
	s := &Set{}
	claimed := map[string]string{} // kind folder -> the full kind that claims it
	for _, e := range on {
		clash := ""
		for _, k := range e.Kinds {
			if other, ok := claimed[k.Folder]; ok {
				clash = fmt.Sprintf("%s isn't loaded: its kind %s claims %s/, which %s already claims", e.ID, e.FullKind(k),
					k.Folder, other)
				break
			}
		}
		if clash != "" {
			warnings = append(warnings, clash)
			continue
		}
		for _, k := range e.Kinds {
			claimed[k.Folder] = e.FullKind(k)
		}
		s.exts = append(s.exts, e)
	}
	return s, warnings
}

// notLoaded is the warnings of an extension that's on but can't load: its fatal problems.
func notLoaded(e *Ext) []string {
	var out []string
	for _, p := range e.Problems {
		switch {
		case p.Kind == "api" || p.Kind == "retake":
			out = append(out, p.Message)
		case p.Fatal():
			out = append(out, fmt.Sprintf("%s isn't loaded (%s): %s", e.ID, e.Display(), p.Message))
		}
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("%s %s needs extension API %d; this retake has %d", e.ID, e.Version, e.API, API))
	}
	return out
}

// List is the extensions in effect, by id.
func (s *Set) List() []*Ext {
	if s == nil {
		return nil
	}
	return slices.Clone(s.exts)
}

// Get is the extension id, or nil when it isn't in effect. A renamed extension's old id finds it under its new one
// (spec/extensions.md, "Renamed ids").
func (s *Set) Get(id string) *Ext {
	if s == nil {
		return nil
	}
	for _, want := range []string{id, config.ExtensionID(id)} {
		for _, e := range s.exts {
			if e.ID == want {
				return e
			}
		}
	}
	return nil
}

// Without is the set less the ids in off (a person's own choice on a hosted server).
func (s *Set) Without(off map[string]bool) *Set {
	if s == nil {
		return nil
	}
	off2 := map[string]bool{}
	for id, v := range off {
		off2[config.ExtensionID(id)] = off2[config.ExtensionID(id)] || v
	}
	out := &Set{}
	for _, e := range s.exts {
		if !off2[e.ID] {
			out.exts = append(out.exts, e)
		}
	}
	return out
}

// KindAt is the full kind of the version folder in videoDir that an extension's kind claims ("scripts.doc"): the
// kind's folder holds its marker as a regular file and no versioned video file (a v0/ with clip_v0.mp4 is a video
// version). "" when none does.
func (s *Set) KindAt(videoDir string) string {
	if s == nil {
		return ""
	}
	for _, e := range s.exts {
		for _, k := range e.Kinds {
			if isKindFolder(filepath.Join(videoDir, k.Folder), k.Marker) {
				return e.FullKind(k)
			}
		}
	}
	return ""
}

// isKindFolder reports whether dir is a folder that holds a regular file matching one of the markers, and no
// versioned video file.
func isKindFolder(dir string, markers Markers) bool {
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	found := slices.ContainsFunc(entries, func(en os.DirEntry) bool {
		return en.Type().IsRegular() && !strings.HasPrefix(en.Name(), ".") && markers.Matches(en.Name())
	})
	return found && !holdsVideo(dir)
}

// holdsVideo reports whether the folder dir holds a versioned video file (clip_v0.mp4).
func holdsVideo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(en os.DirEntry) bool { return !en.IsDir() && isFamilyVideo(en.Name()) })
}

// Kind finds a full kind id ("scripts.doc"): its extension and the kind. A renamed extension's old id works
// ("ignition.doc").
func (s *Set) Kind(full string) (*Ext, Kind, bool) {
	id, kid, ok := strings.Cut(full, ".")
	if !ok {
		return nil, Kind{}, false
	}
	e := s.Get(id)
	if e == nil {
		return nil, Kind{}, false
	}
	for _, k := range e.Kinds {
		if k.ID == kid {
			return e, k, true
		}
	}
	return nil, Kind{}, false
}

// CanonicalKind is a full kind id under its extension's name now: "ignition.doc" is "scripts.doc" (spec/extensions.md,
// "Renamed ids"). Anything else is returned as it is.
func CanonicalKind(full string) string {
	id, kid, ok := strings.Cut(full, ".")
	if !ok {
		return full
	}
	return config.ExtensionID(id) + "." + kid
}
