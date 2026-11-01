// Package home is the home screen of spec/home.md: which folders of a folder are projects, and each project's card
// (its newest version, status, streaming state and thumbnail) for GET /api/home. retake open <folder> and the team
// server both answer /api/home with it.
package home

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
	"github.com/samahlstrom/retake/internal/workspace"
)

// Status is where a project's newest version is in the review loop.
type Status string

// The statuses of spec/home.md, "Status".
const (
	ToReview    Status = "to_review"    // no one has closed a take on it yet
	Reviewed    Status = "reviewed"     // closed takes, nothing open: the notes are being written up, or there are none
	BeingEdited Status = "being_edited" // closed takes and open notes: the agent's turn
)

// StatusOf applies the rules: closed is the number of closed takes on the newest version, open the number of its
// notes that are open or asked about.
func StatusOf(closed, open int) Status {
	switch {
	case closed == 0:
		return ToReview
	case open > 0:
		return BeingEdited
	}
	return Reviewed
}

// Stream is a version's streaming state, when the streaming side reports one in /api/info.
type Stream struct {
	State string `json:"state"` // pending | rendering | complete
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Card is one project on the home screen.
type Card struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	URL      string   `json:"url"` // relative to the home
	Version  string   `json:"version"`
	Versions int      `json:"versions"`
	Duration *float64 `json:"duration"`
	Pushed   string   `json:"pushed"` // RFC 3339 UTC; "" when unknown
	Status   Status   `json:"status"`
	Takes    int      `json:"takes"` // closed takes on the newest version
	Open     int      `json:"open"`  // its open or asked-about notes
	Stream   *Stream  `json:"stream"`
	Thumb    string   `json:"thumb"` // relative to the home
}

// User is the signed-in person on the team server's home.
type User struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Response is GET /api/home.
type Response struct {
	Title     string            `json:"title"`
	Folder    string            `json:"folder,omitempty"` // the owner's only
	Role      string            `json:"role"`
	User      *User             `json:"user,omitempty"` // the team server's
	Projects  []Card            `json:"projects"`
	Workspace *workspace.Layout `json:"workspace,omitempty"`
	// Machines are the computers that edit these projects, for the owner; [] for anyone else (spec/home.md).
	Machines []Machine `json:"machines"`
}

// Source is what a card is made from.
type Source struct {
	Name, Title string
	// Info is the project's GET /api/info answer; nil or empty when it has no version yet.
	Info []byte
	// Store is the project store; nil when the project has none yet (no pushes, no notes).
	Store *project.Store
}

// infoVersion is what a card reads of a version object in /api/info.
type infoVersion struct {
	Label    string          `json:"label"`
	Path     string          `json:"path"`
	Name     string          `json:"name"`
	Duration *float64        `json:"duration"`
	Stream   json.RawMessage `json:"stream"`
	Poster   *string         `json:"poster"`
}

type infoBody struct {
	infoVersion
	Versions []infoVersion `json:"versions"`
}

// Make builds a project's card from its /api/info and its store. A project without a version is "to review", with
// no version and no thumbnail.
func Make(src Source) Card {
	c := Card{Name: src.Name, Title: src.Title, URL: "p/" + src.Name + "/", Status: ToReview}
	if c.Title == "" {
		c.Title = src.Name
	}
	var info infoBody
	if len(src.Info) == 0 || json.Unmarshal(src.Info, &info) != nil || info.Label == "" {
		return c
	}
	cur := info.infoVersion
	for _, v := range info.Versions {
		if v.Label != cur.Label {
			continue
		}
		// The family's entry may carry what the top level doesn't (or the other way round).
		if len(cur.Stream) == 0 {
			cur.Stream = v.Stream
		}
		if cur.Poster == nil {
			cur.Poster = v.Poster
		}
	}
	c.Version, c.Versions, c.Duration = cur.Label, len(info.Versions), cur.Duration
	c.Stream = ParseStream(cur.Stream)
	c.Thumb = c.URL + "api/poster"
	if cur.Poster != nil && *cur.Poster != "" {
		c.Thumb = joinURL(c.URL, *cur.Poster)
	}
	c.Takes = ClosedTakes(cur.Path)
	if src.Store != nil {
		c.Open = openNotes(src.Store, cur.Label, cur.Path)
		if row, err := src.Store.Version(cur.Label); err == nil {
			if t, err := project.ParseTime(row.Created); err == nil {
				c.Pushed = t.UTC().Format(time.RFC3339)
			}
		}
	}
	if c.Pushed == "" && cur.Path != "" {
		if st, err := os.Stat(cur.Path); err == nil {
			c.Pushed = st.ModTime().UTC().Format(time.RFC3339)
		}
	}
	c.Status = StatusOf(c.Takes, c.Open)
	return c
}

// joinURL resolves a URL the page uses (relative to the project page) against the project's URL on the home.
func joinURL(base, u string) string {
	if u == "" || u[0] == '/' || hasScheme(u) {
		return u
	}
	for len(u) >= 2 && u[:2] == "./" {
		u = u[2:]
	}
	return base + u
}

func hasScheme(u string) bool {
	for i, r := range u {
		switch {
		case r == ':':
			return i > 0
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return false
}

// ParseStream reads a version's `stream` object from /api/info: {"state", "done", "total"}. It also takes "status"
// for the state and "ready" or "rendered" for done. nil when there's none.
func ParseStream(raw json.RawMessage) *Stream {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	num := func(keys ...string) int {
		for _, k := range keys {
			if f, ok := m[k].(float64); ok && f >= 0 {
				return int(f)
			}
		}
		return 0
	}
	s := &Stream{State: str("state", "status"), Done: num("done", "ready", "rendered"), Total: num("total")}
	if s.State == "" {
		return nil
	}
	return s
}

// ClosedTakes counts a version's own takes that someone closed: session.json says closed (take format v1.1), or the
// legacy notes.md is in the take folder. Takes carried over by inherit.json don't count.
func ClosedTakes(video string) int {
	if video == "" {
		return 0
	}
	entries, err := os.ReadDir(store.ReviewDir(video))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !store.ValidTakeID(e.Name()) || !e.IsDir() {
			continue
		}
		dir := filepath.Join(store.ReviewDir(video), e.Name())
		if closedTake(dir) {
			n++
		}
	}
	return n
}

func closedTake(dir string) bool {
	if b, err := os.ReadFile(filepath.Join(dir, "session.json")); err == nil { //nolint:gosec // a take folder
		var s struct {
			Closed bool `json:"closed"`
		}
		if json.Unmarshal(b, &s) == nil && s.Closed {
			return true
		}
	}
	st, err := os.Stat(filepath.Join(dir, "notes.md"))
	return err == nil && st.Mode().IsRegular()
}

// openNotes counts the notes on a version (recorded on it, or carried over inside their ranges) that are open or
// asked about.
func openNotes(st *project.Store, label, video string) int {
	sc := project.Scope{Version: label}
	for _, x := range store.InheritedTakes(video) {
		sc.Carried = append(sc.Carried, project.Carried{Take: filepath.Base(x.Dir), Ranges: x.Ranges})
	}
	notes, err := st.Notes(project.NoteFilter{Statuses: []project.Status{project.StatusOpen, project.StatusQuestion},
		Scope: &sc})
	if err != nil {
		return 0
	}
	return len(notes)
}

// Sort puts the cards in the home's order: to review first, then the newest push, then by name.
func Sort(cards []Card) {
	slices.SortStableFunc(cards, func(a, b Card) int {
		if (a.Status == ToReview) != (b.Status == ToReview) {
			if a.Status == ToReview {
				return -1
			}
			return 1
		}
		return cmp.Or(-cmp.Compare(pushedAt(a), pushedAt(b)), cmp.Compare(a.Name, b.Name))
	})
}

func pushedAt(c Card) int64 {
	t, err := time.Parse(time.RFC3339, c.Pushed)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}
