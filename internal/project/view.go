package project

import (
	"cmp"
	"slices"
	"time"
)

// Item is one entry of the review page's sidebar (GET /api/notes, spec/notes.md "What the sidebar shows").
type Item struct {
	ID         int64       `json:"id"`   // the note's id, or the question thread's id (answer_to of an answer)
	Kind       string      `json:"kind"` // note | question
	Status     Status      `json:"status"`
	T0         *float64    `json:"t0"`
	T1         *float64    `json:"t1"`
	Text       string      `json:"text"` // the note's words, or the question
	By         string      `json:"by"`
	Change     string      `json:"change,omitempty"`      // answered and accepted notes
	AnsweredIn string      `json:"answered_in,omitempty"` // answered and accepted notes
	Question   string      `json:"question,omitempty"`    // asked notes: the latest question about it
	Options    []string    `json:"options,omitempty"`     // questions: version labels to pick from
	Note       *ItemNote   `json:"note,omitempty"`        // questions about a note: that note
	Images     []ItemImage `json:"images,omitempty"`
}

// ItemNote is the note a question is about.
type ItemNote struct {
	ID   int64   `json:"id"`
	Text string  `json:"text"`
	T0   float64 `json:"t0"`
	T1   float64 `json:"t1"`
	By   string  `json:"by"`
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
	// Images returns the images of a note; nil = none.
	Images func(Note) []ItemImage
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
	images := q.Images
	if images == nil {
		images = func(Note) []ItemImage { return nil }
	}
	var items []Item
	var notes []Note
	var err error
	if q.Newest {
		qs, err := s.Unanswered()
		if err != nil {
			return nil, err
		}
		for _, th := range qs {
			it, err := s.questionItem(th, images)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
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
			Statuses: []Status{StatusOpen, StatusQuestion, StatusAnswered, StatusAccepted}, Scope: &Scope{Version: q.Version},
		})
		if err != nil {
			return nil, err
		}
		qs, err := s.QuestionsOn(q.Version)
		if err != nil {
			return nil, err
		}
		for _, th := range qs {
			it, err := s.questionItem(th, images)
			if err != nil {
				return nil, err
			}
			items = append(items, it)
		}
	}
	asked, err := s.latestQuestions(notes)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		t0, t1 := n.T0, n.T1
		it := Item{ID: n.ID, Kind: "note", Status: n.Status, T0: &t0, T1: &t1, Text: n.Text, By: n.Author,
			Images: images(n)}
		switch n.Status {
		case StatusAnswered, StatusAccepted:
			it.Change, it.AnsweredIn = n.Change, n.AnsweredIn
		case StatusQuestion:
			if th, ok := asked[n.ID]; ok {
				it.Question, it.Options = th.Text, th.Options
			}
		}
		items = append(items, it)
	}
	slices.SortStableFunc(items, func(a, b Item) int {
		return cmp.Or(cmp.Compare(deref(a.T0), deref(b.T0)), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

func deref(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}

func (s *Store) questionItem(th Thread, images func(Note) []ItemImage) (Item, error) {
	it := Item{ID: th.ID, Kind: "question", Status: StatusQuestion, T0: th.T, T1: th.T, Text: th.Text, By: th.By,
		Options: th.Options}
	if th.Note != nil {
		n, err := s.Note(*th.Note)
		if err != nil {
			return it, err
		}
		it.Note = &ItemNote{ID: n.ID, Text: n.Text, T0: n.T0, T1: n.T1, By: n.Author}
		it.Images = images(n)
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
