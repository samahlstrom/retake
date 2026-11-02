package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The chat (spec/chat.md): one thread per video, one turn at a time, never next to an editing task.

func chatFixture(t *testing.T) (*Store, *clock, EditingWorker) {
	t.Helper()
	s, c := newStore(t)
	w := EditingWorker{ID: "machine", Session: "session", Agents: []AgentProfile{{ID: "claude"}, {ID: "codex"}}}
	if _, err := s.PollWork(w, "", ""); err != nil {
		t.Fatal(err)
	}
	return s, c, w
}

func pollChat(t *testing.T, s *Store, w EditingWorker, turn *ChatTurn) (*EditingTask, *ChatTurn) {
	t.Helper()
	p := WorkPoll{Worker: w}
	if turn != nil {
		p.Chat, p.ChatLease = turn.ID, turn.Lease
	}
	task, _, chat, err := s.PollWorkerChat(p)
	if err != nil {
		t.Fatal(err)
	}
	return task, chat
}

func roles(ms []ChatMessage) string {
	var out []string
	for _, m := range ms {
		s := m.Role + ":" + m.Text
		if m.Waiting {
			s += "(waiting)"
		}
		if m.Streaming {
			s += "(streaming)"
		}
		out = append(out, s)
	}
	return strings.Join(out, " | ")
}

func TestChatTurns(t *testing.T) {
	s, _, w := chatFixture(t)
	if _, err := s.PostChat(ChatPost{Text: "  "}); err == nil {
		t.Fatal("an empty message")
	}
	for _, bad := range []string{"../x.png", "/abs.png", "v0/.hidden.png", ""} {
		if _, err := s.PostChat(ChatPost{Text: "hi", Images: []string{bad}}); err == nil {
			t.Fatalf("image %q accepted", bad)
		}
	}
	m1, err := s.PostChat(ChatPost{Text: "Pull in the link", By: "Sam", Images: []string{"v0/images/a.png"}})
	if err != nil || m1.Turn == "" || m1.Waiting || m1.By != "Sam" || m1.Images[0] != "v0/images/a.png" {
		t.Fatalf("%+v %v", m1, err)
	}
	// A second message while the turn waits joins it.
	m2, _ := s.PostChat(ChatPost{Text: "and an image", Agent: "codex"})
	if m2.Turn != m1.Turn {
		t.Fatalf("joined %+v", m2)
	}
	_, turn := pollChat(t, s, w, nil)
	if turn == nil || turn.State != ChatRunning || turn.Agent != "codex" || turn.Lease == "" {
		t.Fatalf("claim %+v", turn)
	}
	work, err := s.ChatWorkOf(*turn)
	if err != nil || len(work.Messages) != 2 || work.Session != "" {
		t.Fatalf("work %+v %v", work, err)
	}
	// While it runs, a message waits.
	m3, _ := s.PostChat(ChatPost{Text: "make it shorter"})
	if !m3.Waiting || m3.Turn != "" {
		t.Fatalf("waiting %+v", m3)
	}
	// The stream: words, one line per tool (the same twice in a row once), words again in a new message.
	stop, err := s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, Session: "sess-1", Events: []ChatEvent{
		{Type: "text", Text: "I'll add "}, {Type: "text", Text: "it."}, {Type: "tool", Text: "Reading youtube.com/…"},
		{Type: "tool", Text: "Editing the script"}, {Type: "tool", Text: "Editing the script"}, {Type: "text", Text: "Done."}}})
	if err != nil || stop {
		t.Fatal(stop, err)
	}
	if _, err = s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: "wrong"}); !errors.Is(err, ErrStaleChat) {
		t.Fatal("a stale lease", err)
	}
	ms, _ := s.ChatMessages(100)
	got := roles(ms)
	want := "person:Pull in the link | person:and an image | person:make it shorter(waiting) | agent:I'll add it. | " +
		"tool:Reading youtube.com/… | tool:Editing the script | agent:Done.(streaming)"
	if got != want {
		t.Fatalf("thread\n got %s\nwant %s", got, want)
	}
	// The end: streaming stops, and the waiting message gets the next turn.
	if _, err = s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, End: &ChatEnd{State: "done"}}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Automation()
	if a.Chat.Session != "sess-1" || a.Chat.SessionAgent != "codex" || a.Chat.Turn == nil || a.Chat.Turn.State != ChatQueued ||
		a.Chat.Turn.ID == turn.ID {
		t.Fatalf("after %+v", a.Chat)
	}
	ms, _ = s.ChatMessages(100)
	if ms[2].Waiting || ms[2].Turn != a.Chat.Turn.ID || ms[len(ms)-1].Streaming {
		t.Fatalf("next turn: %s", roles(ms))
	}
	// The next turn resumes the session only with the same agent.
	_, next := pollChat(t, s, w, nil)
	if work, _ = s.ChatWorkOf(*next); work.Session != "" || next.Agent != "claude" || len(work.History) != 4 {
		t.Fatalf("the configured agent, a new session: %+v %+v", next, work)
	}
	if err = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Chat.SessionAgent = "claude"; return true, nil }); err != nil {
		t.Fatal(err)
	}
	if work, _ = s.ChatWorkOf(*next); work.Session != "sess-1" {
		t.Fatalf("resumes %+v", work)
	}
}

// One agent per video: a turn waits for a running task ("busy"), and a task waits for a running turn.
func TestChatOneAgentPerVideo(t *testing.T) {
	s, _, w := chatFixture(t)
	n, err := s.AddTyped(TypedNote{Version: "v0", Text: "Build the first cut", Wants: WantsVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueFeedback([]int64{n.ID}, ""); err != nil {
		t.Fatal(err)
	}
	task, _ := pollChat(t, s, w, nil)
	if task == nil {
		t.Fatal("no task")
	}
	if _, err = s.PostChat(ChatPost{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.ChatView()
	if v.Busy != "The agent is busy with v1's edits" || v.Offline {
		t.Fatalf("busy %+v", v)
	}
	// The worker polls with its task: no turn for it.
	if _, _, chat, err := s.PollWorkerChat(WorkPoll{Worker: w, Task: task.ID, Lease: task.Lease}); err != nil || chat != nil {
		t.Fatal("a turn beside a task", chat, err)
	}
	if _, err = s.ReportWork(WorkReport{Worker: w.ID, Task: task.ID, Lease: task.Lease, State: "paused", Message: "x"}, 0); err != nil {
		t.Fatal(err)
	}
	// Now the turn goes, and a task sent meanwhile waits for it.
	_, turn := pollChat(t, s, w, nil)
	if turn == nil {
		t.Fatal("no turn")
	}
	if _, err = s.QueueFeedback([]int64{n.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if task, chat := pollChat(t, s, w, turn); task != nil || chat == nil {
		t.Fatal("a task beside a turn", task, chat)
	}
	if task, _ := pollChat(t, s, w, nil); task != nil {
		t.Fatal("a task while the turn runs")
	}
	if _, err = s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, End: &ChatEnd{State: "done"}}); err != nil {
		t.Fatal(err)
	}
	if task, _ := pollChat(t, s, w, nil); task == nil {
		t.Fatal("the task after the turn")
	}
}

func TestChatStopAndLostWorker(t *testing.T) {
	s, c, w := chatFixture(t)
	// A queued turn stops at once.
	if err := s.StopChat(); err == nil {
		t.Fatal("stop with no turn")
	}
	_, _ = s.PostChat(ChatPost{Text: "one"})
	if err := s.StopChat(); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.ChatMessages(10)
	if got := roles(ms); got != "person:one | info:Stopped" {
		t.Fatal(got)
	}
	// A running one is told to stop, through the poll and the progress answer.
	_, _ = s.PostChat(ChatPost{Text: "two"})
	_, turn := pollChat(t, s, w, nil)
	if err := s.StopChat(); err != nil {
		t.Fatal(err)
	}
	if stop, err := s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease}); err != nil || !stop {
		t.Fatal("stop", stop, err)
	}
	if _, chat := pollChat(t, s, w, turn); chat == nil || !chat.Stop || chat.State != ChatStopping {
		t.Fatalf("poll %+v", chat)
	}
	if _, err := s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, Events: []ChatEvent{{Type: "text", Text: "half"}},
		End: &ChatEnd{State: "stopped", Info: ChatStopped}}); err != nil {
		t.Fatal(err)
	}
	// A worker that goes away: its turn ends once its lease runs out; a message that waited gets the next turn.
	_, _ = s.PostChat(ChatPost{Text: "three"})
	_, turn = pollChat(t, s, w, nil)
	_, _ = s.PostChat(ChatPost{Text: "four"})
	c.add(WorkerLease + time.Second)
	v, err := s.ChatView()
	if err != nil {
		t.Fatal(err)
	}
	if v.Turn == nil || v.Turn.State != ChatQueued || !v.Offline {
		t.Fatalf("after the lease %+v", v)
	}
	if got := roles(v.Messages); !strings.HasSuffix(got, "person:three | person:four | info:"+ChatOffline) {
		t.Fatal(got)
	}
	if _, err = s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease}); !errors.Is(err, ErrStaleChat) {
		t.Fatal("the lost turn's late words", err)
	}
}

func TestChatQuestion(t *testing.T) {
	s, _, w := chatFixture(t)
	_, _ = s.PostChat(ChatPost{Text: "Which look?"})
	_, turn := pollChat(t, s, w, nil)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	at := 1.0
	if _, err := s.AskInChat(WorkAsk{Worker: w.ID, Task: turn.ID, Lease: turn.Lease, Text: "Which?", At: &at}); err == nil {
		t.Fatal("a moment in the chat")
	}
	if _, err := s.AskInChat(WorkAsk{Worker: w.ID, Task: turn.ID, Lease: "x", Text: "Which?"}); err == nil {
		t.Fatal("a stale lease")
	}
	_, _ = s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, Events: []ChatEvent{{Type: "text", Text: "Two looks:"}}})
	q, err := s.AskInChat(WorkAsk{Worker: w.ID, Task: turn.ID, Lease: turn.Lease, Text: " Which look? ",
		Options: []AgentOption{{Text: "Ink", Image: &AgentImage{Name: "ink.png", Data: b.Bytes()}}, {Text: "Neon"}},
		Anchor:  &Anchor{File: "v0/x_v0.md", Line: 1, Heading: "Cold open"}})
	if err != nil || q.ID == 0 || q.Note != 0 || q.Text != "Which look?" {
		t.Fatalf("%+v %v", q, err)
	}
	m, _ := s.ChatMessage(q.ID)
	if m.Role != ChatQuestion || len(m.Options) != 2 || m.Options[0].Image == nil ||
		m.Options[0].Image.URL != "api/chat/files/"+strconv.FormatInt(q.ID, 10)+"/option_1.png" || m.Anchor.Heading != "Cold open" {
		t.Fatalf("%+v", m)
	}
	if _, err = os.Stat(filepath.Join(s.ChatDir(q.ID), "option_1.png")); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.ChatMessages(10)
	if ms[1].Streaming {
		t.Fatal("the words before a question stop streaming")
	}
	// Tapping a choice: the question gets its answer, and the words wait for the next turn.
	if _, err = s.PostChat(ChatPost{Text: "Neon", Answer: ms[0].ID}); err == nil {
		t.Fatal("an answer to a person's message")
	}
	a, err := s.PostChat(ChatPost{Text: "Neon", Answer: q.ID})
	if err != nil || !a.Waiting || a.AnswerTo != q.ID {
		t.Fatalf("%+v %v", a, err)
	}
	if m, _ = s.ChatMessage(q.ID); m.Answer != "Neon" {
		t.Fatalf("%+v", m)
	}
	// Events carry it all.
	evs, _ := s.Events(0, 0)
	kinds := map[string]int{}
	for _, e := range evs {
		if e.Kind != EventChat {
			continue
		}
		var d ChatEventData
		_ = json.Unmarshal(e.Data, &d)
		kinds[d.Why]++
	}
	if kinds["message"] == 0 || kinds["turn"] == 0 {
		t.Fatalf("events %v", kinds)
	}
}

// A text event says where its words go, as the page counts (UTF-16).
func TestChatTextOffsets(t *testing.T) {
	s, _, w := chatFixture(t)
	_, _ = s.PostChat(ChatPost{Text: "go"})
	_, turn := pollChat(t, s, w, nil)
	seq, _ := s.LastSeq()
	for _, part := range []string{"🎬 ", "é", "x"} {
		if _, err := s.ChatProgress(w.ID, ChatProgress{Turn: turn.ID, Lease: turn.Lease, Events: []ChatEvent{{Type: "text", Text: part}}}); err != nil {
			t.Fatal(err)
		}
	}
	evs, _ := s.Events(seq, 0)
	var ats []int
	for _, e := range evs {
		var d ChatEventData
		if json.Unmarshal(e.Data, &d) == nil && d.Why == "text" {
			ats = append(ats, d.At)
		}
	}
	if len(ats) != 2 || ats[0] != 3 || ats[1] != 4 { // "🎬 " is 3 UTF-16 units
		t.Fatalf("offsets %v", ats)
	}
}

func TestChatSent(t *testing.T) {
	s, _, _ := chatFixture(t)
	n, _ := s.AddTyped(TypedNote{Version: "v0", Text: "Build the first cut", Wants: WantsVersion})
	if err := s.SetChatSent(ChatSent{File: "v0/x_v0.md", SHA256: "abc", Note: n.ID, Target: "v1"}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.ChatView()
	if v.Sent == nil || v.Sent.SHA256 != "abc" || !v.Sent.Open {
		t.Fatalf("not sent yet: %+v", v.Sent)
	}
	if _, err := s.QueueFeedback([]int64{n.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ = s.ChatView(); v.Sent.Open {
		t.Fatal("working: not open")
	}
}
