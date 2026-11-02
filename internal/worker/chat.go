package worker

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
)

// The chat with the agent (spec/chat.md): the worker runs one turn at a time, as a background process of the turn's
// agent in the video's folder, and sends the server its words and one quiet line per tool as they come.

// chatFlush is how often a turn's words go to the server; chatSettle is how long a question waits for the words the
// agent wrote before it to be read.
var (
	chatFlush  = 250 * time.Millisecond
	chatSettle = 150 * time.Millisecond
)

// chatRun is the turn this worker runs.
type chatRun struct {
	id, lease string
	cancel    context.CancelCauseFunc // errStop: a person's Stop, or the turn isn't this worker's; else the worker leaves
	done      chan struct{}
	secret    string // its retake ask secret

	mu   sync.Mutex
	send *chatSender // its words on their way to the server
}

// flush sends the turn's words so far: a question it asks comes after them.
func (run *chatRun) flush(ctx context.Context) {
	run.mu.Lock()
	send := run.send
	run.mu.Unlock()
	if send != nil {
		_ = send.flush(ctx, nil)
	}
}

// chatTurn is the running turn's id and lease, for the poll.
func (r *runner) chatTurn() (id, lease string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chat == nil {
		return "", ""
	}
	return r.chat.id, r.chat.lease
}

// takeChat acts on the poll's turn: starts a new one, stops the running one when Stop was pressed or the server no
// longer has it as this worker's.
func (r *runner) takeChat(ctx context.Context, cw *project.ChatWork) {
	r.mu.Lock()
	run := r.chat
	r.mu.Unlock()
	switch {
	case cw == nil && run != nil:
		run.cancel(errStop)
	case cw != nil && run != nil && (run.id != cw.ID || cw.Stop):
		run.cancel(errStop)
	case cw != nil && run == nil && !cw.Stop:
		r.startChat(ctx, *cw, nil)
	}
}

// resumeChat carries on with the turn an earlier worker left running (rec), when the server gave it back (cw); else
// its agent is stopped and its record goes (spec/automation.md, "Agents outlive Retake").
func (r *runner) resumeChat(ctx context.Context, cw *project.ChatWork, rec *turnRecord) {
	if cw == nil || cw.ID != rec.Turn {
		if rec.Proc.alive() {
			r.log("%s: the chat's turn isn't running any more; stopping its agent", filepath.Base(r.opts.Root))
			rec.Proc.stop()
		}
		_ = os.Remove(r.turnPath())
		r.takeChat(ctx, cw)
		return
	}
	run := r.startChat(ctx, *cw, rec)
	r.log("%s: took the chat's turn back: the agent works on", filepath.Base(r.opts.Root))
	if cw.Stop {
		run.cancel(errStop)
	}
}

// startChat runs turn cw (rec: taken back) until it ends, or the worker leaves.
func (r *runner) startChat(ctx context.Context, cw project.ChatWork, rec *turnRecord) *chatRun {
	child, cancel := context.WithCancelCause(ctx)
	run := &chatRun{id: cw.ID, lease: cw.Lease, cancel: cancel, done: make(chan struct{}), secret: newSecret()}
	if rec != nil && rec.Secret != "" {
		run.secret = rec.Secret
	}
	r.mu.Lock()
	r.chat = run
	r.mu.Unlock()
	go func() {
		defer close(run.done)
		defer cancel(nil)
		r.runChat(child, cw, run, rec)
		r.mu.Lock()
		if r.chat == run {
			r.chat = nil
		}
		r.mu.Unlock()
	}()
	return run
}

// leaveChat lets the running turn go as the worker leaves: its agent works on, for the next worker to take back.
func (r *runner) leaveChat() {
	r.mu.Lock()
	run := r.chat
	r.mu.Unlock()
	if run != nil {
		run.cancel(nil)
		<-run.done
	}
}

// chatDoc is the kind with a chat in the video's folder: its extension, the document (relative, slash) and what to
// call it ("doc", the kind's label in lower case).
func (r *runner) chatDoc() (e *ext.Ext, doc, word string) {
	set := r.opts.Exts
	kind := set.KindAt(r.opts.Root)
	if kind == "" {
		return nil, "", "document"
	}
	e, k, ok := set.Kind(kind)
	if !ok {
		return nil, "", "document"
	}
	word = strings.ToLower(k.Label)
	if word == "" {
		word = "document"
	}
	entries, _ := os.ReadDir(filepath.Join(r.opts.Root, k.Folder))
	own := filepath.Base(r.opts.Root) + "_" + k.Folder + ".md"
	for _, en := range entries {
		if !en.Type().IsRegular() {
			continue
		}
		for _, m := range k.Marker {
			if ok, _ := filepath.Match(m, en.Name()); ok && (doc == "" || en.Name() == own) {
				doc = k.Folder + "/" + en.Name()
			}
		}
	}
	return e, doc, word
}

// chatSection is the prompt's part from the extensions for a chat turn: each one's context when the folder has its
// files, and the instructions of the kind's extension.
func (r *runner) chatSection(kindExt *ext.Ext) string {
	var b strings.Builder
	for _, e := range r.opts.Exts.List() {
		if e.Agent == nil {
			continue
		}
		var parts []string
		if e.Agent.Context != "" && r.hasOwned(e) {
			if text, _ := e.AgentText("context"); text != "" {
				parts = append(parts, text)
			}
		}
		if e.Agent.Instructions != "" && kindExt != nil && e.ID == kindExt.ID {
			if text, _ := e.AgentText("instructions"); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "\n## From the %s extension\n\n%s\n", e.Name, strings.Join(parts, "\n\n"))
		}
	}
	return b.String()
}

// chatRules are the chat's own rules: they win over the extension's (spec/chat.md, "The prompt").
func chatRules(doc string) string {
	if doc == "" {
		doc = "the video's document in v0/"
	}
	return fmt.Sprintf(`
## How this chat works (these win over anything above)

- Do what the person asks, then answer in a few plain sentences: what you changed, or your answer. No headings or long lists.
- The document is theirs (%[1]s): edit it in place, keep their own words, keep headings stable. Don't make other documents.
- To show a page, a video or a post in the document, put its link alone on its own line: Retake shows it as an embed. Fetch a page when you need what's in it.
- Images go in v0/images/ (download or make them) and into the document as ![caption](images/<name>), relative to v0/. Images the person attached are there already; their paths are in the message.
- In this chat there are no comments or notes and no report file: ignore what the instructions above say about notes, --note and reports.
- When the person should choose (between looks, openings, titles), ask and carry on, don't wait: %[2]s ask --option "Ink@v0/images/ink.png" --option "Neon@v0/images/neon.png" "Which look?". It shows in the chat; the answer comes as their next message.
- Don't render, push or change any video version, and don't touch .retake/: making the video is the person's Send, not yours.
- Nobody is in this terminal: never wait for input and never use your own question tools.
`, doc, askCommand())
}

// chatMessages is the person's messages of a turn, as the prompt gives them.
func chatMessages(ms []project.ChatMessage) string {
	var b strings.Builder
	for i, m := range ms {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Text)
		if len(m.Images) > 0 {
			fmt.Fprintf(&b, "\n(Attached: %s)", strings.Join(m.Images, ", "))
		}
	}
	return b.String()
}

// chatHistory is the recent thread for a new session.
func chatHistory(ms []project.ChatMessage) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## The chat so far\n\n")
	for _, m := range ms {
		who := "You"
		switch m.Role {
		case project.ChatPerson:
			who = "The person"
		case project.ChatQuestion:
			who = "You asked"
		}
		text := m.Text
		if m.Answer != "" {
			text += " (they picked: " + m.Answer + ")"
		}
		fmt.Fprintf(&b, "%s: %s\n\n", who, text)
	}
	return b.String()
}

// chatPrompt is a turn's prompt: the whole of it for a new session, a reminder and the messages when resuming.
func (r *runner) chatPrompt(cw project.ChatWork, kindExt *ext.Ext, doc string, resume bool) string {
	msgs := chatMessages(cw.Messages)
	if resume {
		return fmt.Sprintf("(Retake chat: same rules as before. Edit %s in place, answer briefly, ask with %s ask --option when the person should choose.)\n\nThe person's message:\n\n%s\n",
			doc, askCommand(), msgs)
	}
	return "You are the agent in Retake's chat for this video. The person who makes the video talks to you here. " +
		"The working directory is the video's folder.\n" + r.chatSection(kindExt) + chatRules(doc) +
		chatHistory(cw.History) + "\n## The person's message\n\n" + msgs + "\n"
}

// agentStyle says how a profile's command takes a session: "claude" (claude -p), "codex" (codex exec) or "" (none).
func agentStyle(argv []string) string {
	if len(argv) < 2 {
		return ""
	}
	switch filepath.Base(argv[0]) {
	case "claude":
		if slices.Contains(argv, "-p") || slices.Contains(argv, "--print") {
			return "claude"
		}
	case "codex":
		if argv[1] == "exec" {
			return "codex"
		}
	}
	return ""
}

// chatArgv is a turn's command: the profile's, with Claude Code's text streamed as it's written, and the session
// resumed when there is one.
func chatArgv(argv []string, session string) []string {
	out := slices.Clone(argv)
	switch agentStyle(argv) {
	case "claude":
		if hasPair(out, "--output-format", "stream-json") && !slices.Contains(out, "--include-partial-messages") {
			out = append(out, "--include-partial-messages")
		}
		if session != "" {
			out = append(out, "--resume", session)
		}
	case "codex":
		if session != "" {
			rest := slices.Clone(out[2:])
			if n := len(rest); n > 0 && rest[n-1] == "-" {
				rest = rest[:n-1]
			}
			out = append(append([]string{out[0], "exec", "resume"}, rest...), session, "-")
		}
	}
	return out
}

func hasPair(argv []string, flag, value string) bool {
	for i, a := range argv {
		if a == flag+"="+value || a == flag && i+1 < len(argv) && argv[i+1] == value {
			return true
		}
	}
	return false
}

// chatSender posts a turn's events to the server, at most every chatFlush, and tells the turn to stop when the
// person pressed Stop or the turn isn't this worker's any more.
type chatSender struct {
	r       *runner
	run     *chatRun
	posting sync.Mutex // one post at a time, so they arrive in order
	mu      sync.Mutex
	events  []project.ChatEvent
	session string
	sent    string // the session the server has
	stop    func()
	offset  int64       // how far into the turn's output the events added so far go
	gen     int         // the turn's process the offset is in: a new one starts its output at 0
	saved   func(int64) // keeps how far the server has them
}

// restart starts reading a new process's output.
func (s *chatSender) restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset = 0
	s.gen++
}

// add queues the events read up to offset in the turn's output.
func (s *chatSender) add(evs []project.ChatEvent, session string, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset = max(s.offset, offset)
	for _, e := range evs {
		if n := len(s.events); n > 0 && e.Type == "text" && s.events[n-1].Type == "text" {
			s.events[n-1].Text += e.Text
			continue
		}
		s.events = append(s.events, e)
	}
	if session != "" {
		s.session = session
	}
}

// flush posts what's waiting, with end when it's the last post. It reports whether the server took it.
func (s *chatSender) flush(ctx context.Context, end *project.ChatEnd) error {
	s.posting.Lock()
	defer s.posting.Unlock()
	s.mu.Lock()
	evs, session, offset, gen := s.events, s.session, s.offset, s.gen
	s.events = nil
	s.mu.Unlock()
	if len(evs) == 0 && end == nil && session == s.sent {
		return nil
	}
	p := project.ChatProgress{Turn: s.run.id, Lease: s.run.lease, Events: evs, End: end}
	if session != s.sent {
		p.Session = session
	}
	var answer struct {
		Stop bool `json:"stop"`
	}
	err := s.r.call(ctx, "/api/worker/chat", p, &answer)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) && he.status == 409 && strings.Contains(he.message, "isn't running on this worker") {
			s.stop() // not this worker's turn any more
			return err
		}
		s.mu.Lock()
		s.events = append(evs, s.events...) // tried again with the next flush
		s.mu.Unlock()
		return err
	}
	s.sent = session
	s.mu.Lock()
	same := gen == s.gen
	s.mu.Unlock()
	if s.saved != nil && same {
		s.saved(offset)
	}
	if answer.Stop {
		s.stop()
	}
	return nil
}

// runChat runs one turn (or, rec, carries on with one an earlier worker started) and tells the server how it ended.
// When the worker leaves meanwhile, the turn's agent works on and nothing is told: the next worker takes it back.
func (r *runner) runChat(ctx context.Context, cw project.ChatWork, run *chatRun, rec *turnRecord) {
	name := filepath.Base(r.opts.Root)
	life := r.life
	if life == nil {
		life = context.Background()
	}
	procCtx, stopProc := context.WithCancelCause(ctx)
	defer stopProc(nil)
	if rec == nil {
		rec = &turnRecord{Turn: cw.ID, Lease: cw.Lease, Agent: cw.Agent, Session: cw.Session, Secret: run.secret}
	}
	rec.Lease = cw.Lease
	send := &chatSender{r: r, run: run, stop: func() { stopProc(errStop) }, offset: rec.Offset,
		saved: func(off int64) { r.saveTurn(rec, func(x *turnRecord) { x.Offset = max(x.Offset, off) }) }}
	run.mu.Lock()
	run.send = send
	run.mu.Unlock()
	flushing := make(chan struct{})
	tick, quit := time.NewTicker(chatFlush), make(chan struct{})
	go func() {
		defer close(flushing)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				_ = send.flush(life, nil)
			}
		}
	}()
	end, left := r.chatTurnRun(procCtx, cw, run, send, rec)
	close(quit)
	<-flushing
	if left {
		return // the worker is going away; the agent works on
	}
	if stopped(procCtx) && end.State != "stopped" {
		end = project.ChatEnd{State: "stopped", Info: project.ChatStopped}
	}
	// The end must reach the server; a hosted one may be briefly out of reach.
	for i := 0; i < 30; i++ {
		err := send.flush(life, &end)
		var he *httpError
		if err == nil || errors.As(err, &he) && he.status < 500 {
			_ = os.Remove(r.turnPath())
			break
		}
		if life.Err() != nil {
			break // the next worker sends it
		}
		select {
		case <-life.Done():
		case <-time.After(time.Second):
		}
	}
	r.log("%s: the chat's turn ended (%s)", name, end.State)
}

// chatTurnRun runs the agent for a turn (once more as a new session when a resumed one fails before saying anything)
// and says how it ended; left when the worker left while it ran.
func (r *runner) chatTurnRun(ctx context.Context, cw project.ChatWork, run *chatRun, send *chatSender, rec *turnRecord) (project.ChatEnd, bool) {
	index := slices.IndexFunc(r.opts.Profiles, func(p Profile) bool { return p.ID == cw.Agent })
	if index < 0 {
		return project.ChatEnd{State: "failed", Info: "The chat's agent isn't installed on the editing computer"}, false
	}
	p := r.opts.Profiles[index]
	kindExt, doc, word := r.chatDoc()
	dir := filepath.Join(r.dir, "chat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return project.ChatEnd{State: "failed", Info: project.ChatFailed}, false
	}
	// A copy of the extensions' files first, so ⋯ → Undo the agent's edit undoes this turn.
	var snaps []*ext.Snap
	if rec.Run == 0 {
		r.stopOrphans("", cw.ID) // one agent per video
		snaps = r.snapshot("chat-" + cw.ID)
		var states []ext.SnapState
		for _, s := range snaps {
			states = append(states, s.State())
		}
		url, _ := r.askURL()
		r.saveTurn(rec, func(x *turnRecord) { x.Snaps, x.AskURL = states, url })
	} else {
		for _, st := range rec.Snaps {
			if s := ext.ResumeSnap(r.opts.Root, st); s != nil {
				snaps = append(snaps, s)
			}
		}
	}
	left := false
	defer func() {
		if left {
			return
		}
		for _, s := range snaps {
			if _, err := s.Finish(); err != nil {
				r.log("%s: couldn't record the agent's edits: %v", filepath.Base(r.opts.Root), err)
			}
		}
	}()
	session := rec.Session
	if agentStyle(p.Command) == "" {
		session = ""
	}
	for attempt := max(rec.Run, 1); ; attempt++ {
		stream := newChatStream(r.opts.Root, doc, word)
		if attempt > rec.Run {
			// A new process for the turn.
			base := filepath.Join(dir, fmt.Sprintf("%s-%d", cw.ID, attempt))
			prompt := r.chatPrompt(cw, kindExt, doc, session != "")
			if err := os.WriteFile(base+".md", []byte(prompt), 0o600); err != nil {
				return project.ChatEnd{State: "failed", Info: project.ChatFailed}, false
			}
			env := chatEnv(os.Environ(), p.Env)
			if url, err := r.askURL(); err == nil {
				env = append(env, "RETAKE_ASK_URL="+url, "RETAKE_ASK_TOKEN="+run.secret)
			}
			proc, err := startDetached(chatArgv(p.Command, session), r.opts.Root, env, base+".md", base+".out", base+".log")
			if err != nil {
				return project.ChatEnd{State: "failed", Info: project.ChatFailed}, false
			}
			send.restart()
			r.saveTurn(rec, func(x *turnRecord) {
				x.Run, x.Session, x.Out, x.Log, x.Offset, x.Proc = attempt, session, base+".out", base+".log", 0, proc
			})
		}
		tail, gone, err := r.chatRead(ctx, rec, stream, send)
		if gone {
			left = true
			return project.ChatEnd{}, true
		}
		if stopped(ctx) {
			return project.ChatEnd{State: "stopped", Info: project.ChatStopped}, false
		}
		if _, limited := telemetry(tail); limited {
			return project.ChatEnd{State: "failed", Info: project.ChatLimited}, false
		}
		if err != nil && session != "" && !stream.said && attempt == 1 {
			r.log("%s: the agent couldn't resume the chat's session; starting a new one", filepath.Base(r.opts.Root))
			session = ""
			continue
		}
		if err != nil {
			return project.ChatEnd{State: "failed", Info: project.ChatFailed}, false
		}
		return project.ChatEnd{State: "done"}, false
	}
}

// chatRead reads the output of the turn's process as it writes it, from the start of its file: the words the server
// has already (up to the record's offset) only rebuild the stream's state, the rest goes to the server. It returns
// when the process has ended (gone: when the worker leaves while it runs), with the end of its output and how it
// exited (unknown, nil, for an earlier worker's).
func (r *runner) chatRead(ctx context.Context, rec *turnRecord, stream *chatStream, send *chatSender) (tail []byte, gone bool, err error) {
	var proc *agentProc
	switch {
	case rec.Proc != nil && rec.Proc.own:
		proc = rec.Proc
	case rec.Proc.alive():
		proc = r.watch(rec.Proc)
	}
	f, err := os.Open(rec.Out)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	skip := rec.Offset
	var pos int64
	var partial []byte
	buf := make([]byte, 64<<10)
	line := func(l []byte) {
		pos += int64(len(l))
		stream.line(l)
		evs := stream.take()
		if pos > skip {
			send.add(evs, stream.session, pos)
		}
	}
	ended := proc == nil
	for {
		n, _ := f.Read(buf)
		if n > 0 {
			data := append(partial, buf[:n]...)
			for i := bytes.IndexByte(data, '\n'); i >= 0; i = bytes.IndexByte(data, '\n') {
				line(data[:i+1])
				data = data[i+1:]
			}
			partial = slices.Clone(data)
			continue
		}
		if ended {
			if len(partial) > 0 {
				line(partial) // its last line, without a newline
			}
			break
		}
		select {
		case <-proc.done:
			ended = true // read what it wrote last, then stop
		case <-ctx.Done():
			if !stopped(ctx) {
				return nil, true, nil
			}
			proc.stop()
			ended = true
		case <-time.After(50 * time.Millisecond):
		}
	}
	var exit error
	if proc != nil && proc.own {
		exit = proc.err
		killGroup(proc.PID) // what it left running in its group
	}
	r.saveTurn(rec, func(x *turnRecord) { x.Proc = nil })
	return append(tailOf(rec.Out, 64<<10), tailOf(rec.Log, 64<<10)...), false, exit
}

// chatEnv is a turn's environment: the worker's without its own variables, and the profile's.
func chatEnv(env []string, extra map[string]string) []string {
	out := make([]string, 0, len(env)+len(extra)+2)
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if !reservedEnv(k) && !hasKey(extra, k) {
			out = append(out, v)
		}
	}
	return append(out, profileEnv(env, extra)...)
}

// chatAsk is the secret of the running turn, when token is it: then a retake ask is the chat's.
func (r *runner) chatAsk(token string) *chatRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chat == nil || r.chat.secret == "" || token == "" {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(r.chat.secret)) != 1 {
		return nil
	}
	return r.chat
}
