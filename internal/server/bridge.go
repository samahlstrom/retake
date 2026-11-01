package server

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// The review bridge's routes (spec/bridge.md): the time map between two versions, and the parent's notes carried
// over onto the newest version's sidebar.

// manifestOf reads a version's render manifest: nil when it has none (or a broken one, which is logged).
func (s *Server) manifestOf(v family.Version) *bridge.Manifest {
	if remote, ok := s.remoteRender(v.Label); ok {
		return remote.Manifest
	}
	m, err := bridge.ForVideo(v.Path)
	if err != nil {
		if !errors.Is(err, bridge.ErrNoManifest) {
			_, _ = fmt.Fprintf(s.opts.Log, "retake: render manifest of %s: %v\n", v.Label, err)
		}
		return nil
	}
	return m
}

// timemap is GET /api/timemap?from=<label>&to=<label>: the map from one version's times to another's. Its segments
// are empty when either has no render manifest (the page then keeps the time).
func (s *Server) timemap(w http.ResponseWriter, r *http.Request) {
	fromLabel, toLabel := query(r, "from"), query(r, "to")
	if fromLabel == "" || toLabel == "" {
		s.fail(w, r, http.StatusBadRequest, "from and to are version labels")
		return
	}
	from, ok1 := s.version(fromLabel)
	to, ok2 := s.version(toLabel)
	if !ok1 || !ok2 {
		s.fail(w, r, http.StatusNotFound, "no such version")
		return
	}
	tm := bridge.TimeMap{From: from.Label, To: to.Label, Segments: []bridge.Segment{}}
	if ma, mb := s.manifestOf(from), s.manifestOf(to); ma != nil && mb != nil {
		tm = bridge.BuildTimeMap(from.Label, to.Label, ma, mb)
	}
	s.json(w, r, http.StatusOK, tm)
}

// parentOf returns the version v was pushed from, else the one before it in the family.
func parentOf(st *project.Store, v family.Version, fam []family.Version) (family.Version, bool) {
	if row, err := st.Version(v.Label); err == nil && row.Parent != "" {
		for _, x := range fam {
			if x.Label == row.Parent {
				return x, true
			}
		}
	}
	for i, x := range fam {
		if x.Label == v.Label && i > 0 {
			return fam[i-1], true
		}
	}
	return family.Version{}, false
}

// scopeOf is the notes on a version: recorded on it, plus the takes inherit.json carries over to it.
func scopeOf(v family.Version) project.Scope {
	sc := project.Scope{Version: v.Label}
	for _, x := range store.InheritedTakes(v.Path) {
		sc.Carried = append(sc.Carried, project.Carried{Take: filepath.Base(x.Dir), Ranges: x.Ranges})
	}
	return sc
}

// carry adds the parent's open and answered notes to the newest version's sidebar items at their mapped times, and
// moves the questions about the parent's notes to theirs, each marked with what became of its moment (spec/bridge.md,
// "Carrying notes over"). Without render manifests for both versions the items stay as they are.
func (s *Server) carry(st *project.Store, v family.Version, fam []family.Version, items []project.Item) ([]project.Item,
	error) {
	parent, ok := parentOf(st, v, fam)
	if !ok {
		return items, nil
	}
	ma, mb := s.manifestOf(parent), s.manifestOf(v)
	if ma == nil || mb == nil {
		return items, nil
	}
	tm := bridge.BuildTimeMap(parent.Label, v.Label, ma, mb)
	sc := scopeOf(parent)
	notes, err := st.Notes(project.NoteFilter{Scope: &sc})
	if err != nil {
		return nil, err
	}
	byID := map[int64]project.Note{}
	for _, n := range notes {
		byID[n.ID] = n
	}
	mark := func(it *project.Item, n project.Note) {
		as := bridge.Carry(ma, mb, n.T0, n.Detail.Audio, n.Status == project.StatusAnswered)
		it.Carried = &project.CarriedFrom{From: parent.Label, As: as, T0: n.T0, T1: n.T1}
		t0, t1 := tm.Map(n.T0), tm.Map(n.T1)
		it.T0, it.T1 = &t0, &t1
	}
	shown := map[int64]bool{}
	for i := range items {
		it := &items[i]
		switch {
		case it.Kind == "note":
			shown[it.ID] = true
		case it.Kind == "question" && it.Note != nil:
			if n, ok := byID[*it.Note]; ok {
				mark(it, n)
			}
		}
	}
	for _, n := range notes {
		if shown[n.ID] || (n.Status != project.StatusOpen && n.Status != project.StatusAnswered) {
			continue
		}
		it := project.ItemOf(n)
		mark(&it, n)
		items = append(items, it)
	}
	slices.SortStableFunc(items, func(a, b project.Item) int {
		return cmp.Or(cmp.Compare(deref(a.T0), deref(b.T0)), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	return items, nil
}

func deref(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}
