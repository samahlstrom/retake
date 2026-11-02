package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The chat with the agent (spec/chat.md): one thread per video, kept in the chat table, and one turn at a time,
// kept with the automation state, so a turn and an editing task never run at once (one agent per video).

// Chat turn states.
const (
	ChatQueued   = "queued"
	ChatRunning  = "running"
	ChatStopping = "stopping"
)

// Chat message roles.
const (
	ChatPerson   = "person"
	ChatAgent    = "agent"
	ChatTool     = "tool"
	ChatQuestion = "question"
	ChatInfo     = "info"
)

// EventChat is the SSE event of the chat: a message, words added to one, or the turn.
const EventChat = "chat"

// Limits of the chat.
const (
	MaxChatText    = 20000
	MaxChatImages  = 8
	MaxChatShown   = 500     // the newest messages GET api/chat gives
	maxChatReply   = 200000 // characters of one agent message; more is dropped
	maxChatHistory = 12 << 10
	chatHistory    = 20
)

// Infos Retake adds to the thread.
const (
	ChatStopped = "Stopped"
	ChatOffline = "The editing computer went offline"
	ChatFailed  = "The agent stopped without finishing"
	ChatLimited = "Usage limit reached. Pick another agent in Settings or try later."
)

// ErrStaleChat refuses a worker's request for a turn it no longer runs.
var ErrStaleChat = errors.New("this chat turn isn't running on this worker any more")

// ChatState is the chat's part of the automation state: the turn, and the agent's session to resume.
type ChatState struct {
	Turn *ChatTurn `json:"turn,omitempty"`
	// Session is the agent's own session id (Claude Code's session_id, Codex's thread_id), for SessionAgent only.
	Session      string    `json:"session,omitempty"`
	SessionAgent string    `json:"session_agent,omitempty"`
	Sent         *ChatSent `json:"sent,omitempty"`
}

// ChatTurn is one run of the agent for the person's messages.
type ChatTurn struct {
	ID     string `json:"id"`
	State  string `json:"state"` // queued | running | stopping
	Agent  string `json:"agent,omitempty"`
	Worker string `json:"worker,omitempty"`
	Lease  string `json:"lease,omitempty"`
	Until  int64  `json:"until,omitempty"`
	Queued int64  `json:"queued"`
	Stop   bool   `json:"stop,omitempty"`
}

// ChatSent is the last Send on the chat's version: the document and its sha256 then, and the comment it made.
type ChatSent struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Note   int64  `json:"note"`
	Target string `json:"target"`
	At     int64  `json:"at"`
}

// ChatMessage is one message of the thread.
type ChatMessage struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	By      string `json:"by,omitempty"`
	Text    string `json:"text"`
	Created string `json:"created"`
	Turn    string `json:"turn,omitempty"`
	// Images: a person's are paths in the video's folder (v0/images/x.png); a question's are URLs.
	Images    []string      `json:"images,omitempty"`
	Voice     string        `json:"voice,omitempty"`
	Options   []AgentOption `json:"options,omitempty"`
	Answer    string        `json:"answer,omitempty"`
	AnswerTo  int64         `json:"answer_to,omitempty"`
	Anchor    *Anchor       `json:"anchor,omitempty"`
	Streaming bool          `json:"streaming,omitempty"`
	Waiting   bool          `json:"waiting,omitempty"`

	agent string // a person's message: the agent it picked
}

// chatData is a message's data column.
type chatData struct {
	Images   []string      `json:"images,omitempty"`
	Voice    string        `json:"voice,omitempty"`
	Options  []AgentOption `json:"options,omitempty"`
	Answer   string        `json:"answer,omitempty"`
	AnswerTo int64         `json:"answer_to,omitempty"`
	Anchor   *Anchor       `json:"anchor,omitempty"`
	Agent    string        `json:"agent,omitempty"`
}

// ChatEventData is the data of a chat event.
type ChatEventData struct {
	Why     string        `json:"why"` // message | text | turn
	Message *ChatMessage  `json:"message,omitempty"`
	ID      int64         `json:"id,omitempty"`
	At      int           `json:"at,omitempty"`
	Text    string        `json:"text,omitempty"`
	Turn    *ChatTurnView `json:"turn,omitempty"`
}

// ChatTurnView is a turn as the page sees it.
type ChatTurnView struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Agent string `json:"agent,omitempty"`
}

func (c *ChatTurn) view() *ChatTurnView {
	if c == nil {
		return nil
	}
	return &ChatTurnView{ID: c.ID, State: c.State, Agent: c.Agent}
}

// ChatWork is a turn as the worker gets it: the person's messages it carries, the recent thread before them (for a
// new session), and the session to resume.
type ChatWork struct {
	ID       string        `json:"id"`
	Lease    string        `json:"lease"`
	Agent    string        `json:"agent"`
	Session  string        `json:"session,omitempty"`
	Messages []ChatMessage `json:"messages"`
	History  []ChatMessage `json:"history,omitempty"`
	Stop     bool          `json:"stop,omitempty"`
}

// ChatPost is a person's message to add.
type ChatPost struct {
	Text   string
	By     string
	Images []string
	Voice  string
	Answer int64  // the question it answers
	Agent  string // the agent it picked, already checked as offered
}

// ChatProgress is the worker's POST /api/worker/chat: the stream so far, and last its end.
type ChatProgress struct {
	Turn    string      `json:"turn"`
	Lease   string      `json:"lease"`
	Session string      `json:"session,omitempty"`
	Events  []ChatEvent `json:"events,omitempty"`
	End     *ChatEnd    `json:"end,omitempty"`
}

// ChatEvent is the agent's words (text) or one quiet line for a tool it used (tool).
type ChatEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ChatEnd is how a turn ended: done, stopped or failed, with an info line.
type ChatEnd struct {
	State string `json:"state"`
	Info  string `json:"info,omitempty"`
}

// ChatView is GET api/chat.
type ChatView struct {
	Messages []ChatMessage `json:"messages"`
	Turn     *ChatTurnView `json:"turn"`
	Busy     string        `json:"busy"`
	Offline  bool          `json:"offline"`
	Blocked  string        `json:"blocked,omitempty"`
	Sent     *ChatSentView `json:"sent"`
}

// ChatSentView is the last Send as the page sees it: Open when its comment is open again with no task running it.
type ChatSentView struct {
	ChatSent
	Open bool `json:"open"`
}

func (a *AutomationState) chatTurn() *ChatTurn {
	if a.Chat == nil {
		return nil
	}
	return a.Chat.Turn
}

func (a *AutomationState) chat() *ChatState {
	if a.Chat == nil {
		a.Chat = &ChatState{}
	}
	return a.Chat
}

// claim makes a queued turn w's: its agent (the turn's, else the configured one, else w's first), a lease, running.
func (c *ChatTurn) claim(w EditingWorker, configured string, now int64) bool {
	if c.Agent == "" || !w.HasAgent(c.Agent) {
		c.Agent = configured
	}
	if !w.HasAgent(c.Agent) {
		if len(w.Agents) == 0 {
			return false
		}
		c.Agent = w.Agents[0].ID
	}
	c.State, c.Worker, c.Lease, c.Stop = ChatRunning, w.ID, NewWorkID(), false
	c.Until = now + int64(WorkerLease.Seconds())
	return true
}

// ChatLength is the length of s as the page counts it (UTF-16 code units), for a text event's at.
func ChatLength(s string) int { return len(utf16.Encode([]rune(s))) }

// validChatPath reports whether p is a relative slash path in the video's folder, without "..", hidden or empty
// segments.
func validChatPath(p string) bool {
	return p != "" && (Anchor{File: p, Line: 1}).Check() == nil
}

const chatCols = `id, turn, role, by, text, data, state, created`

func scanChat(rows interface{ Scan(...any) error }) (ChatMessage, error) {
	var m ChatMessage
	var data, state string
	if err := rows.Scan(&m.ID, &m.Turn, &m.Role, &m.By, &m.Text, &data, &state, &m.Created); err != nil {
		return m, err
	}
	var d chatData
	_ = json.Unmarshal([]byte(data), &d)
	m.Images, m.Voice, m.Options, m.Answer, m.AnswerTo, m.Anchor = d.Images, d.Voice, d.Options, d.Answer, d.AnswerTo, d.Anchor
	m.agent = d.Agent
	m.Streaming, m.Waiting = state == "streaming", state == "waiting"
	if m.Role == ChatQuestion {
		// A question's images are kept with the store: the page gets their URLs.
		for i, f := range m.Images {
			m.Images[i] = chatFileURL(m.ID, f)
		}
		for i := range m.Options {
			if im := m.Options[i].Image; im != nil {
				im.URL, im.File = chatFileURL(m.ID, im.File), ""
			}
		}
	}
	return m, nil
}

// chatFileURL is the page's URL of a question's file (relative, so it works under /p/<name>/ too).
func chatFileURL(id int64, file string) string {
	return "api/chat/files/" + strconv.FormatInt(id, 10) + "/" + file
}

func queryChat(q querier, where string, args ...any) ([]ChatMessage, error) {
	rows, err := q.Query(`SELECT `+chatCols+` FROM chat `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ChatMessage{}
	for rows.Next() {
		m, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func chatByID(q querier, id int64) (ChatMessage, error) {
	ms, err := queryChat(q, `WHERE id = ?`, id)
	if err != nil {
		return ChatMessage{}, err
	}
	if len(ms) == 0 {
		return ChatMessage{}, ErrNotFound
	}
	return ms[0], nil
}

// ChatMessages is the newest limit messages, oldest first.
func (s *Store) ChatMessages(limit int) ([]ChatMessage, error) {
	ms, err := queryChat(s.db, `ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	slices.Reverse(ms)
	return ms, nil
}

// ChatMessage is message id.
func (s *Store) ChatMessage(id int64) (ChatMessage, error) { return chatByID(s.db, id) }

// addChat stores a message and says so.
func (t *tx) addChat(m ChatMessage, state string) (ChatMessage, error) {
	d := chatData{Images: m.Images, Voice: m.Voice, Options: m.Options, Answer: m.Answer, AnswerTo: m.AnswerTo,
		Anchor: m.Anchor, Agent: m.agent}
	b, err := json.Marshal(d)
	if err != nil {
		return m, err
	}
	var id int64
	if err = t.QueryRow(`INSERT INTO chat (turn, role, by, text, data, state, created) VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING id`, m.Turn, m.Role, m.By, m.Text, string(b), state, t.s.stamp()).Scan(&id); err != nil {
		return m, err
	}
	out, err := chatByID(t, id)
	if err != nil {
		return m, err
	}
	t.emit(EventChat, ChatEventData{Why: "message", Message: &out})
	return out, nil
}

// chatChanged says a message changed (an answer, the end of its streaming).
func (t *tx) chatChanged(id int64) error {
	m, err := chatByID(t, id)
	if err != nil {
		return err
	}
	t.emit(EventChat, ChatEventData{Why: "message", Message: &m})
	return nil
}

// closeStreaming ends the streaming of turn's agent messages.
func (t *tx) closeStreaming(turn string) error {
	rows, err := queryChat(t, `WHERE turn = ? AND state = 'streaming'`, turn)
	if err != nil {
		return err
	}
	for _, m := range rows {
		if _, err = t.Exec(`UPDATE chat SET state = '' WHERE id = ?`, m.ID); err != nil {
			return err
		}
		if err = t.chatChanged(m.ID); err != nil {
			return err
		}
	}
	return nil
}

// nextTurn makes the turn that takes every waiting message, when there is one.
func (t *tx) nextTurn(a *AutomationState, now int64) error {
	var n int
	if err := t.QueryRow(`SELECT COUNT(*) FROM chat WHERE state = 'waiting'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	c := &ChatTurn{ID: NewWorkID(), State: ChatQueued, Queued: now}
	// The newest waiting message that picked an agent decides it.
	waiting, err := queryChat(t, `WHERE state = 'waiting' ORDER BY id`)
	if err != nil {
		return err
	}
	for _, m := range waiting {
		if m.agent != "" {
			c.Agent = m.agent
		}
		if _, err = t.Exec(`UPDATE chat SET state = '', turn = ? WHERE id = ?`, c.ID, m.ID); err != nil {
			return err
		}
		if err = t.chatChanged(m.ID); err != nil {
			return err
		}
	}
	a.chat().Turn = c
	return nil
}

// endTurn ends the running turn: its streaming stops, info is added, and the waiting messages get the next turn.
func (t *tx) endTurn(a *AutomationState, info string, now int64) error {
	c := a.chatTurn()
	if c == nil {
		return nil
	}
	if err := t.closeStreaming(c.ID); err != nil {
		return err
	}
	if info != "" {
		if _, err := t.addChat(ChatMessage{Role: ChatInfo, Text: info, Turn: c.ID}, ""); err != nil {
			return err
		}
	}
	a.Chat.Turn = nil
	if err := t.nextTurn(a, now); err != nil {
		return err
	}
	t.emit(EventChat, ChatEventData{Why: "turn", Turn: a.chatTurn().view()})
	return nil
}

// reconcileChat ends a running turn whose worker's lease ran out.
func (t *tx) reconcileChat(a *AutomationState, now int64) (bool, error) {
	c := a.chatTurn()
	if c == nil || c.State == ChatQueued || c.Until > now {
		return false, nil
	}
	return true, t.endTurn(a, ChatOffline, now)
}

// PostChat adds a person's message: with no turn it makes a queued one, a queued turn takes it, and while one runs it
// waits for the next (spec/chat.md, "Turns"). An answer to a question gives the question its answer too.
func (s *Store) PostChat(in ChatPost) (ChatMessage, error) {
	in.Text = strings.TrimSpace(in.Text)
	switch {
	case in.Text == "" || utf8.RuneCountInString(in.Text) > MaxChatText:
		return ChatMessage{}, fmt.Errorf("a message is 1 to %d characters", MaxChatText)
	case len(in.Images) > MaxChatImages:
		return ChatMessage{}, fmt.Errorf("a message has at most %d images", MaxChatImages)
	case in.Voice != "" && !validChatPath(in.Voice):
		return ChatMessage{}, errors.New("voice is a path in the video's folder")
	}
	for _, p := range in.Images {
		if !validChatPath(p) {
			return ChatMessage{}, fmt.Errorf("%q isn't a path in the video's folder", p)
		}
	}
	var out ChatMessage
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		if in.Answer != 0 {
			q, err := chatByID(t, in.Answer)
			if errors.Is(err, ErrNotFound) || err == nil && q.Role != ChatQuestion {
				return errors.New("that isn't a question of the chat")
			}
			if err != nil {
				return err
			}
			if q.Answer == "" {
				if err = t.setChatData(q.ID, func(d *chatData) { d.Answer = in.Text }); err != nil {
					return err
				}
			}
		}
		m := ChatMessage{Role: ChatPerson, By: in.By, Text: in.Text, Images: in.Images, Voice: in.Voice,
			AnswerTo: in.Answer, agent: in.Agent}
		state := ""
		c := a.chatTurn()
		switch {
		case c == nil:
			c = &ChatTurn{ID: NewWorkID(), State: ChatQueued, Agent: in.Agent, Queued: now}
			a.chat().Turn = c
			m.Turn = c.ID
		case c.State == ChatQueued:
			m.Turn = c.ID
			if in.Agent != "" {
				c.Agent = in.Agent
			}
		default:
			state = "waiting"
		}
		if out, err = t.addChat(m, state); err != nil {
			return err
		}
		if err = saveAutomation(t, a); err != nil {
			return err
		}
		t.emit(EventChat, ChatEventData{Why: "turn", Turn: a.chatTurn().view()})
		return nil
	})
	return out, err
}

// setChatData changes a message's data and says so.
func (t *tx) setChatData(id int64, fn func(*chatData)) error {
	var data string
	if err := t.QueryRow(`SELECT data FROM chat WHERE id = ?`, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var d chatData
	_ = json.Unmarshal([]byte(data), &d)
	fn(&d)
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err = t.Exec(`UPDATE chat SET data = ? WHERE id = ?`, string(b), id); err != nil {
		return err
	}
	return t.chatChanged(id)
}

// StopChat stops the turn: a running one is told to stop (its worker kills its process), a queued one ends.
func (s *Store) StopChat() error {
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		c := a.chatTurn()
		if c == nil {
			return errors.New("the agent isn't replying")
		}
		now := s.Now().Unix()
		if c.State == ChatQueued {
			if err = t.endTurn(&a, ChatStopped, now); err != nil {
				return err
			}
		} else {
			c.State, c.Stop = ChatStopping, true
			t.emit(EventChat, ChatEventData{Why: "turn", Turn: c.view()})
		}
		return saveAutomation(t, a)
	})
}

// ChatWorkOf is the work of turn c for its worker: its messages, and the recent thread before them (for a new session).
func (s *Store) ChatWorkOf(c ChatTurn) (ChatWork, error) {
	a, err := s.Automation()
	if err != nil {
		return ChatWork{}, err
	}
	w := ChatWork{ID: c.ID, Lease: c.Lease, Agent: c.Agent, Stop: c.Stop}
	if st := a.Chat; st != nil && st.SessionAgent == c.Agent {
		w.Session = st.Session
	}
	if w.Messages, err = queryChat(s.db, `WHERE turn = ? AND role = 'person' ORDER BY id`, c.ID); err != nil {
		return w, err
	}
	if len(w.Messages) == 0 {
		return w, nil
	}
	// The recent thread, for a new session (a resumed one that fails starts a new one too).
	// The thread before this turn: every other turn's words, but not what waits for the next one.
	before, err := queryChat(s.db, `WHERE turn <> ? AND turn <> '' AND role IN ('person', 'agent', 'question')
		ORDER BY id DESC LIMIT ?`, c.ID, chatHistory)
	if err != nil {
		return w, err
	}
	size := 0
	for _, m := range before {
		if size += len(m.Text); size > maxChatHistory {
			break
		}
		w.History = append(w.History, m)
	}
	slices.Reverse(w.History)
	return w, nil
}

// chatAttempt checks that worker runs turn with lease, and returns it.
func (a *AutomationState) chatAttempt(turn, worker, lease string, now int64) (*ChatTurn, error) {
	c := a.chatTurn()
	if c == nil || c.ID != turn || c.Worker != worker || c.Lease == "" || c.Lease != lease || c.Until <= now ||
		c.State != ChatRunning && c.State != ChatStopping {
		return nil, ErrStaleChat
	}
	return c, nil
}

// ChatProgress takes a running turn's stream: its words go into its agent message (a new one after a tool line), each
// tool line is added once in a row, and its end closes the turn. stop says the person pressed Stop.
func (s *Store) ChatProgress(worker string, p ChatProgress) (stop bool, err error) {
	if len(p.Events) > 1000 || len(p.Session) > 200 {
		return false, errors.New("too many events")
	}
	err = s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		c, err := a.chatAttempt(p.Turn, worker, p.Lease, now)
		if err != nil {
			return err
		}
		c.Until = now + int64(WorkerLease.Seconds())
		stop = c.Stop
		if p.Session != "" {
			a.chat().Session, a.Chat.SessionAgent = p.Session, c.Agent
		}
		for _, e := range p.Events {
			switch e.Type {
			case "text":
				if err = t.chatText(c.ID, e.Text); err != nil {
					return err
				}
			case "tool":
				if err = t.chatTool(c.ID, e.Text); err != nil {
					return err
				}
			}
		}
		if p.End != nil {
			info := strings.TrimSpace(p.End.Info)
			if utf8.RuneCountInString(info) > 300 {
				info = string([]rune(info)[:300])
			}
			if err = t.endTurn(&a, info, now); err != nil {
				return err
			}
		}
		return saveAutomation(t, a)
	})
	return stop, err
}

// chatText adds words to turn's streaming agent message, or starts one.
func (t *tx) chatText(turn, text string) error {
	if text == "" {
		return nil
	}
	ms, err := queryChat(t, `WHERE turn = ? AND role = 'agent' AND state = 'streaming' ORDER BY id DESC LIMIT 1`, turn)
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		_, err = t.addChat(ChatMessage{Role: ChatAgent, Turn: turn, Text: capReply("", text)}, "streaming")
		return err
	}
	m := ms[0]
	text = capReply(m.Text, text)
	if text == "" {
		return nil
	}
	if _, err = t.Exec(`UPDATE chat SET text = ? WHERE id = ?`, m.Text+text, m.ID); err != nil {
		return err
	}
	t.emit(EventChat, ChatEventData{Why: "text", ID: m.ID, At: ChatLength(m.Text), Text: text})
	return nil
}

// capReply is what of add fits after have in one agent message.
func capReply(have, add string) string {
	room := maxChatReply - len(have)
	if room <= 0 {
		return ""
	}
	if len(add) > room {
		add = strings.ToValidUTF8(add[:room], "")
	}
	return add
}

// chatTool ends the turn's streaming message and adds a tool line, unless the turn's last message is the same line.
func (t *tx) chatTool(turn, text string) error {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) > 120 {
		text = string([]rune(text)[:119]) + "…"
	}
	last, err := queryChat(t, `WHERE turn = ? ORDER BY id DESC LIMIT 1`, turn)
	if err != nil {
		return err
	}
	if len(last) == 1 && last[0].Role == ChatTool && last[0].Text == text {
		return nil
	}
	if err = t.closeStreaming(turn); err != nil {
		return err
	}
	_, err = t.addChat(ChatMessage{Role: ChatTool, Turn: turn, Text: text}, "")
	return err
}

// ChatDir is where a chat question's images are kept: .retake/chat/<id>/ in the project.
func (s *Store) ChatDir(id int64) string {
	return filepath.Join(s.root, ".retake", "chat", strconv.FormatInt(id, 10))
}

// AskInChat stores `retake ask` from a running turn (r.Task is the turn, r.Lease its lease) as a question message:
// its words, choices, images and place in a document. Times and visuals on a frame are refused.
func (s *Store) AskInChat(r WorkAsk) (TaskQuestion, error) {
	text := strings.TrimSpace(r.Text)
	placed := slices.ContainsFunc(r.Images, func(im AgentImage) bool { return im.Box != nil })
	switch {
	case text == "" || utf8.RuneCountInString(text) > MaxQuestion:
		return TaskQuestion{}, fmt.Errorf("a question is 1 to %d characters", MaxQuestion)
	case r.At != nil || r.From != nil || r.To != nil || len(r.Boxes)+len(r.Arrows)+len(r.Strokes) > 0 || r.Px || placed ||
		len(r.Frame) > 0:
		return TaskQuestion{}, errors.New("a question in the chat has no moment or visuals on a frame: --at, --from, " +
			"--to, --box, --arrow, --draw, --px and placed images are refused")
	}
	if r.Anchor != nil {
		if err := r.Anchor.Check(); err != nil {
			return TaskQuestion{}, err
		}
	}
	v, err := r.visuals(0, 0)
	if err != nil {
		return TaskQuestion{}, err
	}
	var out TaskQuestion
	err = s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		c, err := a.chatAttempt(r.Task, r.Worker, r.Lease, s.Now().Unix())
		if err != nil {
			return err
		}
		if err = t.closeStreaming(c.ID); err != nil {
			return err
		}
		m := ChatMessage{Role: ChatQuestion, Turn: c.ID, Text: text, Anchor: r.Anchor}
		for _, im := range v.Images {
			m.Images = append(m.Images, im.File)
		}
		for _, o := range v.Options {
			opt := AgentOption{Text: o.Text}
			if o.Image != nil {
				opt.Image = &AgentImage{File: o.Image.File}
			}
			m.Options = append(m.Options, opt)
		}
		// The message first, for its id; its files before the event's readers can ask for them.
		d, err := json.Marshal(chatData{Images: m.Images, Options: m.Options, Anchor: m.Anchor})
		if err != nil {
			return err
		}
		var id int64
		if err = t.QueryRow(`INSERT INTO chat (turn, role, by, text, data, state, created) VALUES (?, ?, 'agent', ?, ?, '',
			?) RETURNING id`, c.ID, ChatQuestion, text, string(d), s.stamp()).Scan(&id); err != nil {
			return err
		}
		if err = saveChatFiles(s.ChatDir(id), v); err != nil {
			return fmt.Errorf("can't keep the question's images: %w", err)
		}
		if err = t.chatChanged(id); err != nil {
			return err
		}
		saved, err := chatByID(t, id)
		if err != nil {
			return err
		}
		out = TaskQuestion{ID: id, Text: text, Anchor: r.Anchor,
			Visuals: &Visuals{Options: saved.Options, Anchor: r.Anchor}}
		for _, u := range saved.Images {
			out.Visuals.Images = append(out.Visuals.Images, AgentImage{URL: u})
		}
		return saveAutomation(t, a)
	})
	return out, err
}

// saveChatFiles writes a chat question's images into dir.
func saveChatFiles(dir string, v Visuals) error {
	var files []AgentImage
	files = append(files, v.Images...)
	for _, o := range v.Options {
		if o.Image != nil {
			files = append(files, *o.Image)
		}
	}
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, im := range files {
		tmp := filepath.Join(dir, "."+im.File+".tmp")
		if err := os.WriteFile(tmp, im.Data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, im.File)); err != nil {
			return err
		}
	}
	return nil
}

// SetChatSent remembers the last Send on the chat's version.
func (s *Store) SetChatSent(sent ChatSent) error {
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		a.chat().Sent = &sent
		return saveAutomation(t, a)
	})
}

// ChatSentOpen reports whether the last Send's comment is open again with no task running it (a paused batch, say):
// then a Send with the same document sends it again.
func (a *AutomationState) ChatSentOpen(n Note) bool {
	if n.Status != StatusOpen {
		return false
	}
	o := a.owner(n.ID)
	return o == nil || o.State == "paused"
}

// ChatView is GET api/chat without the server's parts (blocked).
func (s *Store) ChatView() (ChatView, error) {
	if err := s.ReconcileWork(); err != nil {
		return ChatView{}, err
	}
	a, err := s.Automation()
	if err != nil {
		return ChatView{}, err
	}
	v := ChatView{}
	if v.Messages, err = s.ChatMessages(MaxChatShown); err != nil {
		return v, err
	}
	c := a.chatTurn()
	v.Turn = c.view()
	if c != nil && c.State == ChatQueued {
		for _, j := range a.Tasks {
			if j.State == "running" || j.State == "stopping" {
				v.Busy = fmt.Sprintf("The agent is busy with %s's edits", j.Target)
				if j.Target == "" {
					v.Busy = "The agent is busy with the video's edits"
				}
			}
		}
		w := a.Worker(a.Config.Worker)
		v.Offline = w == nil || w.Seen+10 <= s.Now().Unix()
	}
	if a.Chat != nil && a.Chat.Sent != nil {
		v.Sent = &ChatSentView{ChatSent: *a.Chat.Sent}
		if n, err := s.Note(a.Chat.Sent.Note); err == nil {
			v.Sent.Open = a.ChatSentOpen(n)
		} else {
			v.Sent.Open = true // its comment is gone: a Send makes a new one
		}
	}
	return v, nil
}
