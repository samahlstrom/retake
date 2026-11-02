package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Questions an agent asks from inside its editing task (spec/automation.md, "Questions from inside a task"): the
// question is a thread about one of the task's notes, at a moment of a version, recorded on the task so its answer
// comes back to it.

// TaskAsk is a question a task's agent asked: its thread and the note it's about. Its visuals are kept beside it on
// disk (QuestionDir), not here: the automation state is rewritten on every poll.
type TaskAsk struct {
	ID   int64 `json:"id"`
	Note int64 `json:"note"`
}

// The agent's visuals on a question (spec/automation.md, "Questions from inside a task"), normalized 0–1 to the frame.

// AgentBox is a rectangle with a short label.
type AgentBox struct {
	Box   [4]float64 `json:"box"`
	Label string     `json:"label,omitempty"`
}

// AgentArrow points from From to To.
type AgentArrow struct {
	From  [2]float64 `json:"from"`
	To    [2]float64 `json:"to"`
	Label string     `json:"label,omitempty"`
}

// AgentStroke is a freehand line, in the take's stroke format (only its points).
type AgentStroke struct {
	Points [][2]float64 `json:"points"`
}

// AgentImage is an image the agent made. Coming in it has Name and Data; stored, File; going out, URL. With Box it is
// placed on the frame, else it shows in the question's card.
type AgentImage struct {
	Name string      `json:"name,omitempty"`
	Data []byte      `json:"data,omitempty"`
	File string      `json:"file,omitempty"`
	URL  string      `json:"url,omitempty"`
	Box  *[4]float64 `json:"box,omitempty"`
}

// AgentOption is a choice the reviewer can answer with one tap.
type AgentOption struct {
	Text  string      `json:"text"`
	Image *AgentImage `json:"image,omitempty"`
}

// Visuals is everything an agent drew for one question. Frame is the still of its moment: frame.jpg stored, its URL
// going out.
type Visuals struct {
	Boxes   []AgentBox    `json:"boxes,omitempty"`
	Arrows  []AgentArrow  `json:"arrows,omitempty"`
	Strokes []AgentStroke `json:"strokes,omitempty"`
	Images  []AgentImage  `json:"images,omitempty"`
	Options []AgentOption `json:"options,omitempty"`
	Frame   string        `json:"frame,omitempty"`
	// Anchor is a question about a place in an extension's document (retake ask --file): no frame, no times.
	Anchor *Anchor `json:"anchor,omitempty"`
}

// Limits of a question's visuals.
const (
	MaxQuestion     = 2000
	MaxAgentLabel   = 40
	MaxOptionText   = 120
	MaxAgentBoxes   = 10
	MaxAgentArrows  = 10
	MaxAgentStrokes = 50
	MaxAgentPoints  = 5000
	MaxAgentImages  = 4
	MaxAgentOptions = 6
	MaxAgentImage   = 8 << 20
	MaxFrame        = 2 << 20
)

// WorkAsk is a worker's POST /api/worker/ask: a question from the task's running attempt. Worker comes from the
// request, not the body. Known says whether a label is a version or render the question may point at (nil: any);
// Size is a version's frame size, for Px (nil or !ok: unknown).
type WorkAsk struct {
	Worker  string        `json:"-"`
	Task    string        `json:"task"`
	Lease   string        `json:"lease"`
	Note    int64         `json:"note"`
	At      *float64      `json:"at"`
	From    *float64      `json:"from"`
	To      *float64      `json:"to"`
	Version string        `json:"version"`
	Px      bool          `json:"px"`
	Text    string        `json:"text"`
	Boxes   []AgentBox    `json:"boxes"`
	Arrows  []AgentArrow  `json:"arrows"`
	Strokes []AgentStroke `json:"strokes"`
	Images  []AgentImage  `json:"images"`
	Options []AgentOption `json:"options"`
	Frame   []byte        `json:"frame"`
	Anchor  *Anchor       `json:"anchor,omitempty"`

	Known func(string) bool                `json:"-"`
	Size  func(string) (w, h int, ok bool) `json:"-"`
}

// TaskQuestion is one of a task's questions, with its visuals and its answer once there is one (the newest, when
// there are more).
type TaskQuestion struct {
	ID      int64       `json:"id"`
	Note    int64       `json:"note"`
	Version string      `json:"version"`
	At      float64     `json:"at"`
	From    *float64    `json:"from,omitempty"`
	To      *float64    `json:"to,omitempty"`
	Text    string      `json:"text"`
	Visuals *Visuals    `json:"visuals,omitempty"`
	Answer  *TaskAnswer `json:"answer,omitempty"`
	Anchor  *Anchor     `json:"anchor,omitempty"` // a question about a place in a document
}

// TaskAnswer is the person's answer to a task's question.
type TaskAnswer struct {
	ID   int64  `json:"id"`
	By   string `json:"by"`
	Text string `json:"text"`
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// ValidBox reports whether b is a region of the frame: x, y, w, h normalized 0–1, inside it, with a size.
func ValidBox(b [4]float64) bool {
	const slack = 1e-6
	return finite(b[:]...) && b[0] >= 0 && b[1] >= 0 && b[2] > 0 && b[3] > 0 && b[0]+b[2] <= 1+slack &&
		b[1]+b[3] <= 1+slack
}

func validPoint(p [2]float64) bool {
	const slack = 1e-6
	return finite(p[:]...) && p[0] >= -slack && p[1] >= -slack && p[0] <= 1+slack && p[1] <= 1+slack
}

// imageType is an agent image's content type from its name, when its bytes say the same; "" otherwise.
func imageType(name string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	want := ImageTypes[ext]
	if want == "" || len(data) == 0 || http.DetectContentType(data) != want {
		return ""
	}
	return ext
}

// QuestionFile reports whether name is a file of a question's visuals: img_N, option_N (an image extension) or
// frame.jpg.
func QuestionFile(name string) bool { return questionFileRE.MatchString(name) }

var questionFileRE = regexp.MustCompile(`^((img|option)_\d{1,2}\.(png|jpg|webp|gif)|frame\.jpg)$`)

// QuestionDir is where question id's visuals are kept: .retake/questions/<id>/ in the project.
func (s *Store) QuestionDir(id int64) string {
	return filepath.Join(s.root, ".retake", "questions", strconv.FormatInt(id, 10))
}

// visuals checks a question's visuals and their limits, makes pixel coordinates (Px) normalized with the frame size
// w×h, and returns them with the images' files named, ready to store. Their bytes stay in Data.
func (r WorkAsk) visuals(w, h int) (Visuals, error) {
	v := Visuals{Boxes: r.Boxes, Arrows: r.Arrows, Strokes: r.Strokes}
	switch {
	case len(r.Boxes) > MaxAgentBoxes:
		return v, fmt.Errorf("at most %d boxes", MaxAgentBoxes)
	case len(r.Arrows) > MaxAgentArrows:
		return v, fmt.Errorf("at most %d arrows", MaxAgentArrows)
	case len(r.Strokes) > MaxAgentStrokes:
		return v, fmt.Errorf("at most %d drawn lines", MaxAgentStrokes)
	case len(r.Images) > MaxAgentImages:
		return v, fmt.Errorf("at most %d images", MaxAgentImages)
	case len(r.Options) > MaxAgentOptions:
		return v, fmt.Errorf("at most %d options", MaxAgentOptions)
	}
	sx, sy := 1.0, 1.0
	if r.Px {
		sx, sy = 1/float64(w), 1/float64(h)
	}
	box := func(b [4]float64) [4]float64 { return [4]float64{b[0] * sx, b[1] * sy, b[2] * sx, b[3] * sy} }
	point := func(p [2]float64) [2]float64 { return [2]float64{p[0] * sx, p[1] * sy} }
	label := func(l string) (string, error) {
		l = strings.Join(strings.Fields(l), " ")
		if utf8.RuneCountInString(l) > MaxAgentLabel {
			return "", fmt.Errorf("a label is at most %d characters", MaxAgentLabel)
		}
		return l, nil
	}
	var err error
	v.Boxes = make([]AgentBox, len(r.Boxes))
	for i, b := range r.Boxes {
		if v.Boxes[i].Label, err = label(b.Label); err != nil {
			return v, err
		}
		if v.Boxes[i].Box = box(b.Box); !ValidBox(v.Boxes[i].Box) {
			return v, errors.New("a box is x,y,w,h inside the frame, with a size")
		}
	}
	v.Arrows = make([]AgentArrow, len(r.Arrows))
	for i, a := range r.Arrows {
		if v.Arrows[i].Label, err = label(a.Label); err != nil {
			return v, err
		}
		v.Arrows[i].From, v.Arrows[i].To = point(a.From), point(a.To)
		if !validPoint(v.Arrows[i].From) || !validPoint(v.Arrows[i].To) || v.Arrows[i].From == v.Arrows[i].To {
			return v, errors.New("an arrow goes from one point of the frame to another")
		}
	}
	v.Strokes = make([]AgentStroke, len(r.Strokes))
	points := 0
	for i, st := range r.Strokes {
		if len(st.Points) == 0 {
			return v, errors.New("a drawn line needs points")
		}
		if points += len(st.Points); points > MaxAgentPoints {
			return v, fmt.Errorf("at most %d points in the drawn lines", MaxAgentPoints)
		}
		v.Strokes[i].Points = make([][2]float64, len(st.Points))
		for k, p := range st.Points {
			if v.Strokes[i].Points[k] = point(p); !validPoint(v.Strokes[i].Points[k]) {
				return v, errors.New("a drawn line stays inside the frame (0 to 1)")
			}
		}
	}
	image := func(im AgentImage, file string) (AgentImage, error) {
		if len(im.Data) > MaxAgentImage {
			return im, fmt.Errorf("%s is over %d MiB", im.Name, MaxAgentImage>>20)
		}
		ext := imageType(im.Name, im.Data)
		if ext == "" {
			return im, fmt.Errorf("%s isn't a PNG, JPEG, WebP or GIF (by its name and its bytes)", im.Name)
		}
		out := AgentImage{Name: im.Name, Data: im.Data, File: file + ext}
		if im.Box != nil {
			b := box(*im.Box)
			if !ValidBox(b) {
				return im, errors.New("an image's place is x,y,w,h inside the frame, with a size")
			}
			out.Box = &b
		}
		return out, nil
	}
	v.Images = make([]AgentImage, len(r.Images))
	for i, im := range r.Images {
		if v.Images[i], err = image(im, fmt.Sprintf("img_%d", i+1)); err != nil {
			return v, err
		}
	}
	v.Options = make([]AgentOption, len(r.Options))
	for i, o := range r.Options {
		text := strings.Join(strings.Fields(o.Text), " ")
		if text == "" || utf8.RuneCountInString(text) > MaxOptionText {
			return v, fmt.Errorf("an option is 1 to %d characters", MaxOptionText)
		}
		v.Options[i].Text = text
		if o.Image != nil {
			if o.Image.Box != nil {
				return v, errors.New("an option's image isn't placed on the frame")
			}
			im, err := image(*o.Image, fmt.Sprintf("option_%d", i+1))
			if err != nil {
				return v, err
			}
			v.Options[i].Image = &im
		}
	}
	return v, nil
}

// empty reports whether v has nothing to show.
func (v Visuals) empty() bool {
	return len(v.Boxes)+len(v.Arrows)+len(v.Strokes)+len(v.Images)+len(v.Options) == 0 && v.Frame == "" &&
		v.Anchor == nil
}

// storedQuestion is a question's visuals.json: its range and its visuals, the images by file name.
type storedQuestion struct {
	From *float64 `json:"from,omitempty"`
	To   *float64 `json:"to,omitempty"`
	Visuals
}

// saveVisuals writes a question's images and its visuals.json (without the images' bytes).
func saveVisuals(dir string, v Visuals, from, to *float64, frame []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(dir, name))
	}
	for i := range v.Images {
		if err := write(v.Images[i].File, v.Images[i].Data); err != nil {
			return err
		}
		v.Images[i].Data, v.Images[i].Name = nil, ""
	}
	for i := range v.Options {
		if im := v.Options[i].Image; im != nil {
			if err := write(im.File, im.Data); err != nil {
				return err
			}
			im.Data, im.Name = nil, ""
		}
	}
	if len(frame) > 0 {
		if err := write("frame.jpg", frame); err != nil {
			return err
		}
		v.Frame = "frame.jpg"
	}
	b, err := json.Marshal(storedQuestion{From: from, To: to, Visuals: v})
	if err != nil {
		return err
	}
	return write("visuals.json", b)
}

// QuestionVisuals reads question id's range and visuals, with URLs from url(file); v is nil when it has none.
func (s *Store) QuestionVisuals(id int64, url func(file string) string) (from, to *float64, v *Visuals) {
	b, err := os.ReadFile(filepath.Join(s.QuestionDir(id), "visuals.json"))
	var q storedQuestion
	if err != nil || len(b) > 1<<20 || json.Unmarshal(b, &q) != nil {
		return nil, nil, nil
	}
	if q.empty() {
		return q.From, q.To, nil
	}
	v = &q.Visuals
	link := func(file string) string {
		if !QuestionFile(file) {
			return ""
		}
		return url(file)
	}
	for i := range v.Images {
		v.Images[i].URL, v.Images[i].File = link(v.Images[i].File), ""
	}
	for i := range v.Options {
		if im := v.Options[i].Image; im != nil {
			im.URL, im.File = link(im.File), ""
		}
	}
	if v.Frame != "" {
		v.Frame = link(v.Frame)
	}
	return q.From, q.To, v
}

func validTime(t *float64) bool { return t == nil || finite(*t) && *t >= 0 && *t <= 1e7 }

// Pending is the task's notes it hasn't finished in an earlier attempt.
func (t *EditingTask) Pending() []int64 {
	var out []int64
	for _, id := range t.Notes {
		if !slices.Contains(t.Done, id) {
			out = append(out, id)
		}
	}
	return out
}

func noteList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(parts, ", ")
}

// Clock is t as m:ss (h:mm:ss past an hour), the way the page shows times.
func Clock(t float64) string {
	s := int(math.Max(0, t))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func saveAutomation(t *tx, a AutomationState) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = t.Exec(`INSERT INTO automation (id,data) VALUES (1,?) ON CONFLICT (id) DO UPDATE SET data = excluded.data`, string(b))
	return err
}

// AskInTask stores a question from a task's running attempt: a question thread about one of its notes, recorded on
// the task, and the note becomes question. The attempt keeps running.
func (s *Store) AskInTask(r WorkAsk) (TaskQuestion, error) {
	text := strings.TrimSpace(r.Text)
	switch {
	case text == "" || utf8.RuneCountInString(text) > MaxQuestion:
		return TaskQuestion{}, fmt.Errorf("a question is 1 to %d characters", MaxQuestion)
	case !validTime(r.At) || !validTime(r.From) || !validTime(r.To):
		return TaskQuestion{}, errors.New("a time is a number of seconds, 0 or more")
	case (r.From == nil) != (r.To == nil) || r.From != nil && *r.From >= *r.To:
		return TaskQuestion{}, errors.New("a range is --from and --to, from before to")
	case len(r.Frame) > MaxFrame || len(r.Frame) > 0 && http.DetectContentType(r.Frame) != "image/jpeg":
		return TaskQuestion{}, errors.New("the frame still is a JPEG of 2 MiB at most")
	}
	if r.Anchor != nil {
		if err := r.Anchor.Check(); err != nil {
			return TaskQuestion{}, err
		}
		placed := slices.ContainsFunc(r.Images, func(im AgentImage) bool { return im.Box != nil })
		if r.At != nil || r.From != nil || r.To != nil || len(r.Boxes)+len(r.Arrows)+len(r.Strokes) > 0 || r.Px ||
			placed || len(r.Frame) > 0 {
			return TaskQuestion{}, errors.New("a question about a place in a document (--file) has no moment or " +
				"visuals on the frame: --at, --from, --to, --box, --arrow, --draw, --px and placed images are refused")
		}
	}
	var out TaskQuestion
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		j, err := a.WorkAttempt(r.Task, r.Worker, r.Lease, now)
		if err != nil {
			return err
		}
		if j.Stop {
			return errors.New("task is stopping")
		}
		pending := j.Pending()
		note := r.Note
		if note == 0 {
			if len(pending) != 1 {
				return fmt.Errorf("say which note with --note: this task has %s", noteList(pending))
			}
			note = pending[0]
		}
		if !slices.Contains(pending, note) {
			return fmt.Errorf("note #%d isn't one of this task's notes (%s)", note, noteList(pending))
		}
		n, err := noteByID(t, note)
		if err != nil {
			return err
		}
		if n.Status != StatusOpen && n.Status != StatusQuestion {
			return fmt.Errorf("note #%d is %s", note, n.Status)
		}
		version := r.Version
		if version == "" {
			version = n.Version
		}
		if r.Known != nil && !r.Known(version) {
			return fmt.Errorf("there is no version or draft %q", version)
		}
		at := n.T0
		if r.From != nil {
			at = *r.From
		}
		if r.At != nil {
			at = *r.At
		}
		w, h, sized := 0, 0, false
		if r.Size != nil {
			w, h, sized = r.Size(version)
		}
		if r.Px && (!sized || w <= 0 || h <= 0) {
			return fmt.Errorf("--px needs the frame size of %s, which isn't known; give 0-1 coordinates", version)
		}
		v, err := r.visuals(w, h)
		if err != nil {
			return err
		}
		v.Anchor = r.Anchor
		var id int64
		if err = t.QueryRow(`INSERT INTO threads (note, version, t, kind, by, text, options, created)
			VALUES (?, ?, ?, ?, ?, ?, '[]', ?) RETURNING id`, note, version, at, KindQuestion, string(ByAgent), text,
			s.stamp()).Scan(&id); err != nil {
			return err
		}
		if n.Status == StatusOpen {
			if err = t.setStatus(note, StatusQuestion); err != nil {
				return err
			}
		}
		if !v.empty() || r.From != nil || len(r.Frame) > 0 {
			if err = saveVisuals(s.QuestionDir(id), v, r.From, r.To, r.Frame); err != nil {
				return fmt.Errorf("can't keep the question's visuals: %w", err)
			}
		}
		j.Asked = append(j.Asked, TaskAsk{ID: id, Note: note})
		where := "at " + Clock(at)
		if r.Anchor != nil {
			where = "in " + r.Anchor.File
		}
		j.AddEvent(now, fmt.Sprintf("Asked about #%d %s: %s", note, where, text))
		if err = saveAutomation(t, a); err != nil {
			return err
		}
		t.emit("automation", map[string]bool{"changed": true})
		t.emit(EventNotes, NotesEvent{Why: "ask", Version: n.Version, Notes: []int64{note}, Questions: []int64{id}})
		out = TaskQuestion{ID: id, Note: note, Version: version, At: at, From: r.From, To: r.To, Text: text,
			Anchor: r.Anchor}
		return nil
	})
	return out, err
}

// taskQuestions is j's questions, oldest first, each with its newest answer.
func taskQuestions(q querier, j *EditingTask) ([]TaskQuestion, error) {
	if len(j.Asked) == 0 {
		return []TaskQuestion{}, nil
	}
	ids := make([]any, len(j.Asked))
	for i, ask := range j.Asked {
		ids[i] = ask.ID
	}
	in := placeholders(len(ids))
	questions, err := queryThreads(q, `WHERE kind = 'question' AND id IN (`+in+`) ORDER BY id`, ids...)
	if err != nil {
		return nil, err
	}
	answers, err := queryThreads(q, `WHERE kind = 'answer' AND answer_to IN (`+in+`) ORDER BY id`, ids...)
	if err != nil {
		return nil, err
	}
	out := make([]TaskQuestion, 0, len(questions))
	for _, th := range questions {
		i := slices.IndexFunc(j.Asked, func(a TaskAsk) bool { return a.ID == th.ID })
		tq := TaskQuestion{ID: th.ID, Note: j.Asked[i].Note, Version: th.Version, Text: th.Text}
		if th.T != nil {
			tq.At = *th.T
		}
		for _, an := range answers {
			if an.AnswerTo != nil && *an.AnswerTo == th.ID {
				tq.Answer = &TaskAnswer{ID: an.ID, By: an.By, Text: an.Text} // oldest first: the newest wins
			}
		}
		out = append(out, tq)
	}
	return out, nil
}

// TaskQuestions is the task's questions, oldest first, with their range, visuals (url gives a file's URL) and
// answers.
func (s *Store) TaskQuestions(j EditingTask, url func(id int64, file string) string) ([]TaskQuestion, error) {
	qs, err := taskQuestions(s.db, &j)
	if err != nil {
		return nil, err
	}
	for i := range qs {
		id := qs[i].ID
		qs[i].From, qs[i].To, qs[i].Visuals = s.QuestionVisuals(id, func(file string) string { return url(id, file) })
		if qs[i].Visuals != nil {
			qs[i].Anchor = qs[i].Visuals.Anchor
		}
	}
	return qs, nil
}

// held reports which of j's unfinished notes are held by its questions: one has no answer yet (waiting, then, is
// true), or its answer hasn't been told to an agent.
func held(q querier, j *EditingTask) (notes []int64, waiting bool, err error) {
	qs, err := taskQuestions(q, j)
	if err != nil {
		return nil, false, err
	}
	for _, id := range j.Pending() {
		hold := false
		for _, tq := range qs {
			if tq.Note != id {
				continue
			}
			if tq.Answer == nil {
				hold, waiting = true, true
			} else if !slices.Contains(j.Told, tq.Answer.ID) {
				hold = true
			}
		}
		if hold {
			notes = append(notes, id)
		}
	}
	return notes, waiting, nil
}

// tell records answers that reached the task's agent.
func (t *EditingTask) tell(ids []int64) bool {
	changed := false
	for _, id := range ids {
		if id > 0 && !slices.Contains(t.Told, id) && len(t.Told) < 1000 {
			t.Told = append(t.Told, id)
			changed = true
		}
	}
	return changed
}

// wait puts a task whose attempt ended with held notes back to waiting: in question while one of its questions has
// no answer, else queued at once, so the next attempt gets the answers.
func (t *EditingTask) wait(waiting bool, now int64, why string) {
	t.Lease, t.Until, t.Stop, t.Error, t.Question = "", 0, false, "", ""
	t.State = "queued"
	if waiting {
		t.State = "question"
	}
	if why != "" {
		t.AddEvent(now, why)
	}
}

// resumeAnswered queues each task waiting in question (on its own questions, not a report's) once none of its
// questions waits for an answer. It reports whether a task changed.
func resumeAnswered(q querier, a *AutomationState, now int64) (bool, error) {
	changed := false
	for i := range a.Tasks {
		j := &a.Tasks[i]
		if j.State != "question" || j.Question != "" || len(j.Asked) == 0 {
			continue
		}
		_, waiting, err := held(q, j)
		if err != nil {
			return changed, err
		}
		if !waiting {
			j.State = "queued"
			j.AddEvent(now, "Reviewer answered")
			changed = true
		}
	}
	return changed, nil
}

// AnswerTask stores the person's typed answer to one of a task's questions: an answer thread, the note open again,
// and the task queued again once nothing it asked waits for an answer.
func (s *Store) AnswerTask(task string, question int64, text, by string) error {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 4000 {
		return errors.New("an answer is 1 to 4000 characters")
	}
	if by == "" {
		by = string(ByReviewer)
	}
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		j := a.Task(task)
		if j == nil {
			return ErrNotFound
		}
		if j.State == "completed" || j.State == "cancelled" {
			return errors.New("this task has ended")
		}
		if !slices.ContainsFunc(j.Asked, func(ask TaskAsk) bool { return ask.ID == question }) {
			return errors.New("that question isn't this task's")
		}
		qs, err := taskQuestions(t, j)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(qs, func(tq TaskQuestion) bool { return tq.ID == question })
		if i < 0 {
			return ErrNotFound
		}
		tq := qs[i]
		if tq.Answer != nil {
			return errors.New("that question has an answer already")
		}
		var id int64
		if err = t.QueryRow(`INSERT INTO threads (note, version, t, kind, by, text, options, answer_to, created)
			VALUES (?, ?, ?, ?, ?, ?, '[]', ?, ?) RETURNING id`, tq.Note, tq.Version, tq.At, KindAnswer, by, text,
			tq.ID, s.stamp()).Scan(&id); err != nil {
			return err
		}
		ev := NotesEvent{Why: "answer", Notes: []int64{tq.Note}, Answers: []int64{id}}
		n, err := noteByID(t, tq.Note)
		if err != nil {
			return err
		}
		ev.Version = n.Version
		var others int
		if err = t.QueryRow(`SELECT COUNT(*) FROM threads q WHERE q.kind = 'question' AND q.note = ?
			AND NOT EXISTS (SELECT 1 FROM threads x WHERE x.answer_to = q.id)`, tq.Note).Scan(&others); err != nil {
			return err
		}
		if n.Status == StatusQuestion && others == 0 {
			if err = t.setStatus(n.ID, StatusOpen); err != nil {
				return err
			}
			ev.Opened = []int64{n.ID}
		}
		now := s.Now().Unix()
		j.AddEvent(now, fmt.Sprintf("Reviewer answered #%d: %s", tq.Note, text))
		if _, err = resumeAnswered(t, &a, now); err != nil {
			return err
		}
		if err = saveAutomation(t, a); err != nil {
			return err
		}
		t.emit("automation", map[string]bool{"changed": true})
		t.emit(EventNotes, ev)
		return nil
	})
}

// resumeAfterSync is called inside a notes sync that stored recorded answers: a task waiting on them goes again.
func (t *tx) resumeAfterSync() error {
	a, err := readAutomation(t)
	if err != nil {
		return err
	}
	changed, err := resumeAnswered(t, &a, t.s.Now().Unix())
	if err != nil || !changed {
		return err
	}
	if err = saveAutomation(t, a); err != nil {
		return err
	}
	t.emit("automation", map[string]bool{"changed": true})
	return nil
}

// TaskOfQuestion is the task that asked question thread id, if one did.
func (a *AutomationState) TaskOfQuestion(id int64) (*EditingTask, *TaskAsk) {
	for i := range a.Tasks {
		for k := range a.Tasks[i].Asked {
			if a.Tasks[i].Asked[k].ID == id {
				return &a.Tasks[i], &a.Tasks[i].Asked[k]
			}
		}
	}
	return nil, nil
}
