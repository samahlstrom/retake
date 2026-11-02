package project

import (
	"cmp"
	"slices"
	"time"
)

// Item is one entry of the review page's sidebar (GET /api/notes, spec/notes.md "What the sidebar shows").
type Item struct {
	Work       *EditingTask `json:"work,omitempty"`
	Version    string       `json:"version,omitempty"`
	Take       string       `json:"take,omitempty"`
	Seg        string       `json:"seg,omitempty"`
	Drawings   []string     `json:"drawings,omitempty"`
	ID         int64        `json:"id"`   // the note's id, or the question thread's id (answer_to of an answer)
	Kind       string       `json:"kind"` // note | question
	Status     Status       `json:"status"`
	T0         *float64     `json:"t0"`
	T1         *float64     `json:"t1"`
	Text       string       `json:"text"` // the note's words, or the question
	By         string       `json:"by"`
	Change     string       `json:"change,omitempty"`      // answered and accepted notes
	AnsweredIn string       `json:"answered_in,omitempty"` // answered and accepted notes
	Question   string       `json:"question,omitempty"`    // asked notes: the latest question about it
	Options    []string     `json:"options,omitempty"`     // questions: version labels to pick from
	Note       *int64       `json:"note,omitempty"`        // questions about a note: its id
	Images     []ItemImage  `json:"images,omitempty"`
	Snapshot   string       `json:"snapshot,omitempty"` // notes: the URL of the still of its moment with its strokes
	Targets    []Target     `json:"targets,omitempty"`  // notes: the timeline elements under it (spec/bridge.md)
	Carried    *CarriedFrom `json:"carried,omitempty"`  // a note of the parent version, shown here at its mapped time
	Ask        *ItemAsk     `json:"ask,omitempty"`      // notes: the open question its task's agent asked about it
	Key        string       `json:"key,omitempty"`      // notes: a typed comment's doc/<n>
	Anchor     *Anchor      `json:"anchor,omitempty"`   // typed comments: where in a document it points
	Wants      string       `json:"wants,omitempty"`    // typed comments: "version" when it asks for a cut
	Strokes    []Stroke     `json:"strokes,omitempty"`  // a typed comment on a video: its pen lines

	imagesOf *Note // the note whose images this item shows
}

// ItemAsk is a question an agent asked from inside its editing task (retake ask), shown under its comment: the thread
// id (what an answer points at), the task, its time and range (on the shown version when mapped, else on Version)
// and the agent's visuals.
type ItemAsk struct {
	ID      int64    `json:"id"`
	Task    string   `json:"task"`
	T       float64  `json:"t"`
	From    *float64 `json:"from,omitempty"`
	To      *float64 `json:"to,omitempty"`
	Version string   `json:"version"`
	Text    string   `json:"text"`
	Visuals *Visuals `json:"visuals,omitempty"`
	// Anchor is a question about a place in a document (retake ask --file), in place of a moment of the video.
	Anchor *Anchor `json:"anchor,omitempty"`
}

// CarriedFrom says where a carried-over item comes from and what became of its moment (spec/bridge.md, "Carrying
// notes over"): As is unchanged, check (answered, check it) or changed (context changed); T0 and T1 are its times
// there.
type CarriedFrom struct {
	From string  `json:"from"`
	As   string  `json:"as"`
	T0   float64 `json:"t0"`
	T1   float64 `json:"t1"`
}

// ItemOf makes the sidebar item of a note, as Sidebar does (without its images).
func ItemOf(n Note) Item {
	t0, t1 := n.T0, n.T1
	it := Item{ID: n.ID, Kind: "note", Status: n.Status, T0: &t0, T1: &t1, Text: n.Text, By: n.Author,
		Targets: n.Detail.Targets, Take: n.Take, Seg: n.Seg, Version: n.Version}
	for _, stroke := range n.Detail.Strokes {
		if !slices.Contains(it.Drawings, stroke.Color) {
			it.Drawings = append(it.Drawings, stroke.Color)
		}
	}
	if n.Status == StatusAnswered || n.Status == StatusAccepted {
		it.Change, it.AnsweredIn = n.Change, n.AnsweredIn
	}
	if n.IsDoc() {
		it.Key, it.Anchor, it.Wants = n.Key, n.Detail.Anchor, n.Detail.Wants
		if n.Detail.Anchor == nil {
			it.Strokes = n.Detail.Strokes // a typed comment on a video: its lines, for the video and its card
		}
	}
	return it
}

// ItemImage is an image pinned to a note, for the sidebar's thumbnails.
type ItemImage struct {
	URL string     `json:"url"`
	Box [4]float64 `json:"box"`
}

// SidebarQuery says which sidebar to compute.
type SidebarQuery struct {
	Version string
	Newest  bool
	// PushedAt is when the newest version was pushed; only notes of takes recorded after it show. Zero = all.
	PushedAt time.Time
	// TakeDir returns a note's take folder, for its images ("" or nil: no images).
	TakeDir func(Note) string
	// ImageURL returns the URL the page loads one of a note's images from.
	ImageURL func(n Note, img TakeImage) string
	// SnapshotURL returns the URL the page loads a note's snapshot from, given the file's modification time (nil: no
	// snapshots). Only a snapshot whose file is in the project gets one.
	SnapshotURL func(n Note, modified time.Time) string
}

// takeTime parses a take id (local time, YYYY-MM-DD_HH-MM-SS).
func takeTime(take string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02_15-04-05", take, time.Local)
	return t, err == nil
}

// Sidebar computes the sidebar of a version. On the newest version it shows only the questions that have no answer
// yet and the open notes recorded on it since its push: nothing that's answered. On an older version it shows that
// version's notes in their final state (retracted ones left out), and the questions asked about it as a whole.
func (s *Store) Sidebar(q SidebarQuery) ([]Item, error) {
	var questions []Thread
	var notes []Note
	var err error
	if q.Newest {
		if questions, err = s.Unanswered(); err != nil {
			return nil, err
		}
		all, err := s.Notes(NoteFilter{Statuses: []Status{StatusOpen}, Scope: &Scope{Version: q.Version}})
		if err != nil {
			return nil, err
		}
		for _, n := range all {
			if t, ok := takeTime(n.Take); ok && !q.PushedAt.IsZero() && t.Before(q.PushedAt.Truncate(time.Second)) {
				continue
			}
			notes = append(notes, n)
		}
	} else {
		notes, err = s.Notes(NoteFilter{
			Statuses: []Status{StatusOpen, StatusQuestion, StatusAnswered, StatusAccepted},
			Scope:    &Scope{Version: q.Version},
		})
		if err != nil {
			return nil, err
		}
		if questions, err = s.QuestionsOn(q.Version); err != nil {
			return nil, err
		}
	}
	items := make([]Item, 0, len(questions)+len(notes))
	for _, th := range questions {
		it, err := s.questionItem(th)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	asked, err := s.latestQuestions(notes)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		it := ItemOf(n)
		it.imagesOf = &n
		if n.Status == StatusQuestion {
			if th, ok := asked[n.ID]; ok {
				it.Question, it.Options = th.Text, th.Options
			}
		}
		items = append(items, it)
	}
	if err := s.addImages(items, q); err != nil {
		return nil, err
	}
	if q.SnapshotURL != nil {
		for i := range items {
			if n := items[i].imagesOf; n != nil && items[i].Kind == "note" {
				if _, st, ok := s.SnapshotFile(*n); ok {
					items[i].Snapshot = q.SnapshotURL(*n, st.ModTime())
				}
			}
		}
	}
	slices.SortStableFunc(items, func(a, b Item) int {
		return cmp.Or(cmp.Compare(deref(a.T0), deref(b.T0)), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	return items, nil
}

func (s *Store) addImages(items []Item, q SidebarQuery) error {
	if q.TakeDir == nil || q.ImageURL == nil {
		return nil
	}
	var notes []Note
	for _, it := range items {
		if it.imagesOf != nil {
			notes = append(notes, *it.imagesOf)
		}
	}
	byNote, err := s.ImagesOf(notes, q.TakeDir)
	if err != nil {
		return err
	}
	for i := range items {
		n := items[i].imagesOf
		if n == nil {
			continue
		}
		for _, img := range byNote[n.ID] {
			items[i].Images = append(items[i].Images, ItemImage{URL: q.ImageURL(*n, img), Box: img.Box})
		}
	}
	return nil
}

func deref(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}

// questionItem makes a question's sidebar item. A question about the version as a whole sits at 0 s.
func (s *Store) questionItem(th Thread) (Item, error) {
	t := 0.0
	if th.T != nil {
		t = *th.T
	}
	it := Item{ID: th.ID, Kind: "question", Status: StatusQuestion, T0: &t, T1: &t, Text: th.Text, By: th.By,
		Options: th.Options, Note: th.Note, Version: th.Version}
	if th.Note != nil {
		n, err := s.Note(*th.Note)
		if err != nil {
			return it, err
		}
		it.imagesOf = &n
	}
	return it, nil
}

// latestQuestions returns the newest question about each of the notes, by note id.
func (s *Store) latestQuestions(notes []Note) (map[int64]Thread, error) {
	ids := make([]int64, 0, len(notes))
	for _, n := range notes {
		if n.Status == StatusQuestion {
			ids = append(ids, n.ID)
		}
	}
	ths, err := s.ThreadsOf(ids)
	if err != nil {
		return nil, err
	}
	out := map[int64]Thread{}
	for _, th := range ths {
		if th.Kind == KindQuestion && th.Note != nil {
			out[*th.Note] = th // oldest first, so the last one wins
		}
	}
	return out, nil
}
