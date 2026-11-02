package project

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func f64(v float64) *float64 { return &v }

// running claims a task for notes ids, queued as one task (a task may hold several notes).
func running(t *testing.T, s *Store, w EditingWorker, ids ...int64) *EditingTask {
	t.Helper()
	if _, e := s.QueueFeedback(ids[:1], ""); e != nil {
		t.Fatal(e)
	}
	if len(ids) > 1 {
		e := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
			a.Tasks[len(a.Tasks)-1].Notes = ids
			return true, nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	j, e := s.PollWork(w, "", "")
	if e != nil || j == nil {
		t.Fatal("no task", j, e)
	}
	return j
}

func taskByID(t *testing.T, s *Store, id string) EditingTask {
	t.Helper()
	a, e := s.Automation()
	if e != nil {
		t.Fatal(e)
	}
	j := a.Task(id)
	if j == nil {
		t.Fatal("no task", id)
	}
	return *j
}

// A question from inside a running task: a thread on the note at its moment, with the agent's visuals kept beside it;
// the note is Question and the attempt keeps running. An attempt that ends with only that note waits in question
// without a render; the typed answer queues the task again, and its next attempt finishes it.
func TestAskInTaskThenAnswerResumesTheSameTask(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1)
	pic, still := pngBytes(t), jpegBytes(t)
	ask := WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, At: f64(1.5), From: f64(1), To: f64(3), Text: "Blue or teal?",
		Boxes:   []AgentBox{{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Label: "this title"}},
		Arrows:  []AgentArrow{{From: [2]float64{0.9, 0.1}, To: [2]float64{0.5, 0.3}}},
		Strokes: []AgentStroke{{Points: [][2]float64{{0.1, 0.1}, {0.2, 0.2}}}},
		Images:  []AgentImage{{Name: "mock.png", Data: pic, Box: &[4]float64{0.6, 0.6, 0.3, 0.3}}, {Name: "other.png", Data: pic}},
		Options: []AgentOption{{Text: "Blue"}, {Text: "Teal", Image: &AgentImage{Name: "teal.png", Data: pic}}},
		Frame:   still,
		Known:   func(l string) bool { return l == "v6" },
	}
	q, e := s.AskInTask(ask)
	if e != nil {
		t.Fatal(e)
	}
	if q.Note != 1 || q.Version != "v6" || q.At != 1.5 || *q.From != 1 || *q.To != 3 || q.Text != "Blue or teal?" {
		t.Fatalf("question %+v", q)
	}
	if n, _ := s.Note(1); n.Status != StatusQuestion {
		t.Fatalf("note is %s", n.Status)
	}
	if th, e := s.Thread(q.ID); e != nil || th.Kind != KindQuestion || th.By != "agent" || *th.T != 1.5 || *th.Note != 1 {
		t.Fatalf("thread %+v %v", th, e)
	}
	dir := s.QuestionDir(q.ID)
	for _, f := range []string{"visuals.json", "img_1.png", "img_2.png", "option_2.png", "frame.jpg"} {
		if _, e := os.Stat(filepath.Join(dir, f)); e != nil {
			t.Error(e)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "visuals.json")); bytes.Contains(b, []byte(`"data"`)) {
		t.Fatalf("image bytes in visuals.json: %s", b)
	}
	from, to, v := s.QuestionVisuals(q.ID, func(f string) string { return "/q/" + f })
	if *from != 1 || *to != 3 || v == nil || v.Frame != "/q/frame.jpg" || v.Boxes[0].Label != "this title" ||
		v.Images[0].URL != "/q/img_1.png" || v.Images[0].Box == nil || v.Images[1].Box != nil ||
		v.Options[1].Image.URL != "/q/option_2.png" || v.Options[0].Image != nil || len(v.Strokes[0].Points) != 2 {
		b, _ := json.Marshal(v)
		t.Fatalf("visuals %s", b)
	}
	// The note being Question doesn't withdraw it from its own running task.
	if e = s.ReconcileWork(); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); got.State != "running" || got.Stop || len(got.Asked) != 1 || got.Asked[0].ID != q.ID {
		t.Fatalf("task after asking: %+v", got)
	}
	// Completed with every note waiting on its question: no render needed; the task waits for the person.
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Asked which colour"}, 0); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); got.State != "question" || got.Lease != "" || got.Question != "" {
		t.Fatalf("task after its report: %+v", got)
	}
	if next, _ := s.PollWork(w, "", ""); next != nil {
		t.Fatal("a task waiting on its question ran again", next)
	}
	if e = s.AnswerTask(j.ID, q.ID+1, "Teal", "Ana"); e == nil {
		t.Fatal("answered a question the task didn't ask")
	}
	if e = s.AnswerTask(j.ID, q.ID, "Teal", "Ana"); e != nil {
		t.Fatal(e)
	}
	if e = s.AnswerTask(j.ID, q.ID, "Blue", "Ana"); e == nil {
		t.Fatal("answered twice")
	}
	if n, _ := s.Note(1); n.Status != StatusOpen {
		t.Fatalf("answered note is %s", n.Status)
	}
	again, e := s.PollWork(w, "", "")
	if e != nil || again == nil || again.ID != j.ID || again.Attempt != 2 {
		t.Fatal("the same task didn't resume", again, e)
	}
	qs, e := s.TaskQuestions(*again, func(id int64, f string) string { return f })
	if e != nil || len(qs) != 1 || qs[0].Answer == nil || qs[0].Answer.Text != "Teal" || qs[0].Answer.By != "Ana" ||
		qs[0].Visuals == nil || len(qs[0].Visuals.Options) != 2 {
		t.Fatalf("questions %+v %v", qs, e)
	}
	// The attempt's context had the answer: told, in the next poll.
	if _, e = s.PollWork(w, again.ID, again.Lease, qs[0].Answer.ID); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); !slices.Equal(got.Told, []int64{qs[0].Answer.ID}) {
		t.Fatalf("told %v", got.Told)
	}
	done := WorkReport{Worker: w.ID, Task: j.ID, Lease: again.Lease, State: "completed", Message: "Made it teal", Version: "v7", VersionChecked: true}
	if _, e = s.ReportWork(done, 0); e != nil {
		t.Fatal(e)
	}
	if n, _ := s.Note(1); n.Status != StatusAnswered || n.AnsweredIn != "v7" {
		t.Fatalf("note %+v", n)
	}
	if got := taskByID(t, s, j.ID); got.State != "completed" {
		t.Fatalf("task %+v", got)
	}
}

// A task with two notes ends an attempt with one Done and one still Question; the answer brings it back for the other.
func TestTaskEndsWithSomeNotesDoneAndSomeQuestion(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	if _, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which pause?"}); e == nil ||
		!strings.Contains(e.Error(), "--note") {
		t.Fatal("asked without saying which of two notes", e)
	}
	q, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 2, Text: "Which pause?"})
	if e != nil {
		t.Fatal(e)
	}
	if q.At != 0 || q.Version != "v6" {
		t.Fatalf("defaults: %+v", q) // the note's start, on its version
	}
	report := WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Title is blue", Version: "v7", VersionChecked: true}
	if _, e = s.ReportWork(report, 0); e != nil {
		t.Fatal(e)
	}
	one, _ := s.Note(1)
	two, _ := s.Note(2)
	got := taskByID(t, s, j.ID)
	if one.Status != StatusAnswered || two.Status != StatusQuestion || got.State != "question" || !slices.Equal(got.Done, []int64{1}) || got.Result != "v7" {
		t.Fatalf("after the report: %s %s %+v", one.Status, two.Status, got)
	}
	if e = s.ReconcileWork(); e != nil {
		t.Fatal(e)
	}
	if got = taskByID(t, s, j.ID); got.State != "question" {
		t.Fatalf("a Done note cancelled its own task: %+v", got)
	}
	// A recorded answer (a take segment pointing at the question) resumes it too.
	if _, e = s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Text: "make the title blue"},
		{Key: "second", Take: take1, Text: "trim the pause"}, {Key: "answer", Take: take1, Text: "the long one", AnswerTo: q.ID}}}); e != nil {
		t.Fatal(e)
	}
	if got = taskByID(t, s, j.ID); got.State != "queued" {
		t.Fatalf("a recorded answer didn't resume the task: %+v", got)
	}
	again, e := s.PollWork(w, "", "")
	if e != nil || again == nil || again.ID != j.ID {
		t.Fatal(again, e)
	}
	if !slices.Equal(again.Pending(), []int64{2}) {
		t.Fatalf("pending %v", again.Pending())
	}
	qs, _ := s.TaskQuestions(*again, func(int64, string) string { return "" })
	// Its comments are on v6, so their edits go in v7: the next run updates v7 in place.
	report = WorkReport{Worker: w.ID, Task: j.ID, Lease: again.Lease, State: "completed", Message: "Trimmed the long pause", Version: "v7", VersionChecked: true, Told: []int64{qs[0].Answer.ID}}
	if _, e = s.ReportWork(report, 0); e != nil {
		t.Fatal(e)
	}
	one, _ = s.Note(1)
	two, _ = s.Note(2)
	if one.AnsweredIn != "v7" || two.Status != StatusAnswered || two.AnsweredIn != "v7" || taskByID(t, s, j.ID).State != "completed" {
		t.Fatalf("finally: %+v %+v", one, two)
	}
}

// An answer that comes in while a background attempt runs isn't told to it: the attempt's report puts the task back
// in the queue, so the next attempt gets it.
func TestAnswerDuringABackgroundAttemptQueuesTheTask(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1)
	q, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which blue?"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AnswerTask(j.ID, q.ID, "Navy", "Ana"); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); got.State != "running" {
		t.Fatalf("an answer stopped the running attempt: %+v", got)
	}
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Waiting"}, 0); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); got.State != "queued" {
		t.Fatalf("task %+v", got)
	}
	// A render the attempt named stays a draft while its only note is held.
	j2, _ := s.PollWork(w, "", "")
	if _, e = s.AskInTask(WorkAsk{Worker: w.ID, Task: j2.ID, Lease: j2.Lease, Text: "And the shade?"}); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Renders = append(a.Renders, LiveRender{ID: "r1", Worker: w.ID, Task: j2.ID, Label: "v7-draft1", N: 7, Draft: true, Complete: true})
		return true, nil
	}); e != nil {
		t.Fatal(e)
	}
	v, e := s.ReportWork(WorkReport{Worker: w.ID, Task: j2.ID, Lease: j2.Lease, State: "completed", Message: "Half done", Result: "r1"}, 6)
	if e != nil || v.ID != "" {
		t.Fatal("promoted a held task's draft", v, e)
	}
	a, _ := s.Automation()
	if !a.Renders[0].Draft || a.Task(j2.ID).State != "question" {
		t.Fatalf("%+v %+v", a.Renders[0], a.Task(j2.ID))
	}
}

func TestAskInTaskRefusesBadQuestions(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1)
	pic := pngBytes(t)
	ok := WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which?"}
	for name, change := range map[string]func(*WorkAsk){
		"no text":          func(a *WorkAsk) { a.Text = "  " },
		"stale lease":      func(a *WorkAsk) { a.Lease = "old" },
		"another worker":   func(a *WorkAsk) { a.Worker = "other" },
		"not its note":     func(a *WorkAsk) { a.Note = 2 },
		"box outside":      func(a *WorkAsk) { a.Boxes = []AgentBox{{Box: [4]float64{0.8, 0, 0.5, 0.5}}} },
		"box without size": func(a *WorkAsk) { a.Boxes = []AgentBox{{Box: [4]float64{0.1, 0.1, 0, 0.5}}} },
		"long label":       func(a *WorkAsk) { a.Boxes = []AgentBox{{Box: [4]float64{0, 0, 1, 1}, Label: strings.Repeat("x", 41)}} },
		"arrow to itself":  func(a *WorkAsk) { a.Arrows = []AgentArrow{{From: [2]float64{0.5, 0.5}, To: [2]float64{0.5, 0.5}}} },
		"line outside":     func(a *WorkAsk) { a.Strokes = []AgentStroke{{Points: [][2]float64{{0.5, 1.5}}}} },
		"seven options":    func(a *WorkAsk) { a.Options = slices.Repeat([]AgentOption{{Text: "x"}}, 7) },
		"empty option":     func(a *WorkAsk) { a.Options = []AgentOption{{Text: " "}} },
		"not a png":        func(a *WorkAsk) { a.Images = []AgentImage{{Name: "x.png", Data: []byte("GIF89a......")}} },
		"no image type":    func(a *WorkAsk) { a.Images = []AgentImage{{Name: "x.bmp", Data: pic}} },
		"image too big": func(a *WorkAsk) {
			a.Images = []AgentImage{{Name: "x.png", Data: append(pic, make([]byte, MaxAgentImage)...)}}
		},
		"half a range":     func(a *WorkAsk) { a.From = f64(2) },
		"backwards range":  func(a *WorkAsk) { a.From, a.To = f64(3), f64(2) },
		"negative time":    func(a *WorkAsk) { a.At = f64(-1) },
		"unknown version":  func(a *WorkAsk) { a.Version, a.Known = "v99", func(string) bool { return false } },
		"pixels unsized":   func(a *WorkAsk) { a.Px = true },
		"frame not a jpeg": func(a *WorkAsk) { a.Frame = pic },
	} {
		a := ok
		change(&a)
		if _, e := s.AskInTask(a); e == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Pixels with a known frame size become 0–1.
	px := ok
	px.Px, px.Size = true, func(string) (int, int, bool) { return 1920, 1080, true }
	px.Boxes = []AgentBox{{Box: [4]float64{192, 108, 960, 540}}}
	q, e := s.AskInTask(px)
	if e != nil {
		t.Fatal(e)
	}
	_, _, v := s.QuestionVisuals(q.ID, func(f string) string { return f })
	if v == nil || v.Boxes[0].Box != [4]float64{0.1, 0.1, 0.5, 0.5} {
		t.Fatalf("pixels: %+v", v)
	}
}
