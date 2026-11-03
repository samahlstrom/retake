package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/desktop"
	"github.com/samahlstrom/retake/internal/project"
)

// Questions from inside a task (spec/automation.md, "Questions from inside a task"): the worker listens on 127.0.0.1
// for its whole life, gives each attempt a fresh secret, and passes the agent's `retake ask` on to the server with
// its own credentials. The agent never sees the worker's host token.

// AskFile is where a task's agent finds the worker, under the project's .retake/worker/: {"url", "token"}. Agents in
// herdr and tmux read it, since their session's environment is fixed when it opens.
const AskFile = "ask.json"

// AskEndpoint is what `retake ask` needs to reach the worker of the task it runs in.
type AskEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// MaxAskBody is the most a question may send, its images included (base64).
const MaxAskBody = 48 << 20

// askState is the runner's side of the questions: the endpoint, the running attempt's secret, the task's questions
// as the server last said them (plus what was asked since), and answers told to the agent that the server hasn't
// heard of yet.
type askState struct {
	was       string // host:port the endpoint had in an earlier worker, to listen on again
	url       string
	secret    string
	notes     map[int64]project.WorkNote // the attempt's notes, for a question's default version and time
	questions []project.TaskQuestion
	told      []int64 // the server's (the task's Told) and this worker's
	unsent    []int64 // told here, not yet acknowledged by a poll or report
}

func newSecret() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// askURL starts the worker's question endpoint once, for the worker's whole life (r.life), and returns its URL.
func (r *runner) askURL() (string, error) {
	ctx := r.life
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ask.url != "" {
		return r.ask.url, nil
	}
	// The port it had, when it's free: an agent that outlived the last worker has that URL in its environment
	// (spec/automation.md, "Agents outlive Retake").
	l, err := net.Listen("tcp", r.ask.was)
	if r.ask.was == "" || err != nil {
		l, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: http.HandlerFunc(r.serveAsk), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	r.ask.url = "http://" + l.Addr().String()
	return r.ask.url, nil
}

// beginAsk gives an attempt its secret (secret when it has one: an attempt taken back keeps it) and writes the ask
// file; end takes both away, or with keep leaves the file for the agent that works on after its worker. a is the
// attempt's assignment: for a new attempt every answer in it is in the agent's context, so it is told; fresh is those
// the agent hadn't been told before. An attempt taken back (resumed) hasn't seen the answers that came meanwhile: they
// stay untold, for its session or its next round.
func (r *runner) beginAsk(a project.WorkAssignment, secret string, resumed bool) (env []string, fresh []project.TaskQuestion, key, url string, end func(keep bool)) {
	for _, q := range a.Questions {
		if q.Answer != nil && !slices.Contains(a.Task.Told, q.Answer.ID) && !resumed {
			fresh = append(fresh, q)
		}
	}
	r.mu.Lock()
	r.ask.questions = slices.Clone(a.Questions)
	r.ask.told = slices.Clone(a.Task.Told)
	r.ask.notes = map[int64]project.WorkNote{}
	for _, n := range a.Notes {
		r.ask.notes[n.ID] = n
	}
	r.mu.Unlock()
	for _, q := range fresh {
		r.tell(q.Answer.ID)
	}
	url, err := r.askURL()
	if err != nil {
		r.log("Can't listen for the agent's questions: %v", err)
		return nil, fresh, secret, "", func(bool) {}
	}
	if secret == "" {
		secret = newSecret()
	}
	r.mu.Lock()
	r.ask.secret = secret
	r.mu.Unlock()
	path := filepath.Join(r.dir, AskFile)
	if err = writeJSON(path, AskEndpoint{URL: url, Token: secret}); err != nil {
		r.log("Can't write %s: %v", path, err)
	}
	return []string{"RETAKE_ASK_URL=" + url, "RETAKE_ASK_TOKEN=" + secret}, fresh, secret, url, func(keep bool) {
		r.mu.Lock()
		if r.ask.secret == secret {
			r.ask.secret = ""
		}
		r.mu.Unlock()
		if !keep {
			_ = os.Remove(path)
		}
	}
}

// askWas remembers the endpoint an earlier worker had (its ask.json, or a record's), to listen on that port again.
func (r *runner) askWas(url string) {
	host := strings.TrimPrefix(url, "http://")
	if url == "" || host == url || !strings.HasPrefix(host, "127.0.0.1:") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ask.url == "" && r.ask.was == "" {
		r.ask.was = host
	}
}

// serveAsk is POST /ask from the running attempt's agent.
func (r *runner) serveAsk(w http.ResponseWriter, req *http.Request) {
	fail := func(status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if req.URL.Path == "/progress" && req.Method == http.MethodPost {
		r.serveProgress(w, req, fail) // retake working, retake done (progress.go)
		return
	}
	if req.URL.Path != "/ask" || req.Method != http.MethodPost {
		fail(http.StatusNotFound, "not found")
		return
	}
	given := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if run := r.chatAsk(given); run != nil {
		r.serveChatAsk(w, req, run, fail) // asked from a chat turn: it lands in the chat (spec/chat.md)
		return
	}
	r.mu.Lock()
	secret, active := r.ask.secret, r.active
	r.mu.Unlock()
	if secret == "" || active == nil || subtle.ConstantTimeCompare([]byte(given), []byte(secret)) != 1 {
		fail(http.StatusForbidden, "no Retake task is running with this secret")
		return
	}
	var body project.WorkAsk
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, MaxAskBody)).Decode(&body) != nil {
		fail(http.StatusBadRequest, "invalid question (or over 48 MiB)")
		return
	}
	body.Task, body.Lease, body.Frame = active.ID, active.Lease, nil
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Minute)
	defer cancel()
	if body.Anchor == nil {
		body.Frame = r.frameOf(ctx, active, body) // a question about a document has no frame
	}
	var q project.TaskQuestion
	if err := r.askServer(ctx, "/api/worker/ask", body, &q); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			msg := he.message
			var reply struct{ Error string }
			if json.Unmarshal([]byte(msg), &reply) == nil && reply.Error != "" {
				msg = reply.Error
			}
			fail(he.status, msg)
			return
		}
		fail(http.StatusBadGateway, "can't reach the review: "+err.Error())
		return
	}
	r.mu.Lock()
	if !slices.ContainsFunc(r.ask.questions, func(x project.TaskQuestion) bool { return x.ID == q.ID }) {
		r.ask.questions = append(r.ask.questions, q)
	}
	r.mu.Unlock()
	r.log("%s: the agent asked about #%d %s: %s", filepath.Base(r.opts.Root), q.Note, where(q), q.Text)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(q)
}

// serveChatAsk passes a chat turn's question on to the server: the chat shows it, and the answer comes as the
// person's next message.
func (r *runner) serveChatAsk(w http.ResponseWriter, req *http.Request, run *chatRun, fail func(int, string)) {
	var body project.WorkAsk
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, MaxAskBody)).Decode(&body) != nil {
		fail(http.StatusBadRequest, "invalid question (or over 48 MiB)")
		return
	}
	body.Task, body.Lease, body.Frame, body.Note = run.id, run.lease, nil, 0
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Minute)
	defer cancel()
	// What the agent wrote before it asked goes first: its output lines may still be on their way to the stream.
	time.Sleep(chatSettle)
	run.flush(ctx)
	var q project.TaskQuestion
	if err := r.askServer(ctx, "/api/worker/chat/ask", body, &q); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			msg := he.message
			var reply struct{ Error string }
			if json.Unmarshal([]byte(msg), &reply) == nil && reply.Error != "" {
				msg = reply.Error
			}
			fail(he.status, msg)
			return
		}
		fail(http.StatusBadGateway, "can't reach the review: "+err.Error())
		return
	}
	r.log("%s: the agent asked in the chat: %s", filepath.Base(r.opts.Root), q.Text)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(q)
}

// sawQuestions takes the task's questions and told answers from a poll.
func (r *runner) sawQuestions(t *project.EditingTask, qs []project.TaskQuestion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ask.secret == "" {
		return
	}
	for _, q := range r.ask.questions {
		if !slices.ContainsFunc(qs, func(x project.TaskQuestion) bool { return x.ID == q.ID }) {
			qs = append(qs, q) // asked just now: the poll didn't have it yet
		}
	}
	r.ask.questions = qs
	for _, id := range t.Told {
		if !slices.Contains(r.ask.told, id) {
			r.ask.told = append(r.ask.told, id)
		}
	}
}

// tell marks answers as told to the agent; the next poll or report says so.
func (r *runner) tell(ids ...int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if !slices.Contains(r.ask.told, id) {
			r.ask.told = append(r.ask.told, id)
		}
		if !slices.Contains(r.ask.unsent, id) {
			r.ask.unsent = append(r.ask.unsent, id)
		}
	}
}

// unsentTold is the told answers the server hasn't acknowledged; acked drops them once it has.
func (r *runner) unsentTold() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ask.unsent)
}

func (r *runner) acked(ids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ask.unsent = slices.DeleteFunc(r.ask.unsent, func(id int64) bool { return slices.Contains(ids, id) })
}

// untold is the answered questions whose answer the agent hasn't been told.
func (r *runner) untold() []project.TaskQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []project.TaskQuestion
	for _, q := range r.ask.questions {
		if q.Answer != nil && !slices.Contains(r.ask.told, q.Answer.ID) {
			out = append(out, q)
		}
	}
	return out
}

// allHeld reports whether every note t hasn't finished waits on one of its questions: unanswered, or answered but
// not told. Then a completed report needs no render or version.
func (r *runner) allHeld(t *project.EditingTask) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := t.Pending()
	if len(pending) == 0 {
		return false
	}
	for _, id := range pending {
		hold := false
		for _, q := range r.ask.questions {
			if q.Note == id && (q.Answer == nil || !slices.Contains(r.ask.told, q.Answer.ID)) {
				hold = true
			}
		}
		if !hold {
			return false
		}
	}
	return true
}

// answerLine is how an answer reaches an agent in herdr or tmux: one line, whitespace folded.
func answerLine(q project.TaskQuestion) string {
	text := strings.Join(strings.Fields(q.Answer.Text), " ")
	return fmt.Sprintf("Answer to your question on note %d %s: %s", q.Note, where(q), text)
}

// where is a question's place: "at 1:23" in the video, or "at v0/script.md "Cold open"" in a document.
func where(q project.TaskQuestion) string {
	if a := q.Anchor; a != nil {
		if a.Heading != "" {
			return fmt.Sprintf("at %s %q", a.File, a.Heading)
		}
		return fmt.Sprintf("at %s line %d", a.File, a.Line)
	}
	return "at " + project.Clock(q.At)
}

// askCommand is how the prompt names retake: "retake" when that is this program on PATH, else its full path.
var askCommand = func() string { return commandFor(desktop.Exe(), os.Getenv("PATH")) }

// commandFor names the retake binary self (links resolved) for an agent whose PATH is path: "retake" when the first
// retake on path is self, else self's full path, quoted for a shell when it needs it. A release bundle (npm, Homebrew,
// install.sh, the Mac app) put its own folder first on this process's PATH (spec/setup.md), but an agent in herdr or
// tmux, or the person's shell, doesn't have it, so that folder doesn't count. An npm install's `retake` is a node
// script, so its agents get the Go binary's full path: no node start-up per call, whatever node manager is active.
func commandFor(self, path string) string {
	if self == "" {
		return "retake"
	}
	bundle, _ := desktop.BundleDir(self)
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) || dir == bundle {
			continue
		}
		found := filepath.Join(dir, "retake")
		st, err := os.Stat(found) //nolint:gosec // a folder on PATH: only whether a retake there runs
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
			continue
		}
		if r, _ := filepath.EvalSymlinks(found); r == self {
			return "retake"
		}
		break // the shell would run that one, which isn't this program
	}
	if strings.ContainsAny(self, " '\"$`\\") {
		return "'" + strings.ReplaceAll(self, "'", `'\''`) + "'"
	}
	return self
}

// askRules is the part of every task prompt about asking and reporting (spec/automation.md, "Permissions").
func askRules(reportPath, done string) string {
	return fmt.Sprintf(`Nobody is watching this session. Never wait for input, and never ask the person here or with your own question tools (they would wait forever). When a note is unclear, its target is ambiguous, feedback conflicts, or needed material is missing, ask the reviewer about that moment of the video instead of guessing, then carry on with everything else you can do:
  %[1]s ask --note <note id> --at <seconds or m:ss> "your question"
Times are on the version the note was made on (the times in the context; add --version <label> for a draft of yours); --from <t> --to <t> gives the shot. Show what you mean, so one look answers it: when the question is about where something is, add --box x,y,w,h:"label" (0 to 1 from the frame's top left) or --arrow x1,y1,x2,y2:"label"; for freehand marks, --draw lines.json ([{"points": [[x, y], ...]}]). When choosing between looks, make a still or mockup of each and offer them as choices the reviewer taps: --option "Centered@centered.png" --option "Left@left.png". --image file.png shows another image (add @x,y,w,h to place it on the frame). For example:
  %[1]s ask --note 3 --at 1:23 --box 0.62,0.08,0.3,0.2:"this logo?" --option "Keep it" --option "Remove it" "Is this the logo you want gone?"
It returns at once; the answer comes back to you later, as a message in this session or in "questions" of the context when the task resumes. The context's "questions" holds what was asked in this task before, with the answers.
The assigned notes are one batch, sent together: do all of them in this one run and write one report, from which Retake makes one new version.
Work them one note at a time, and say so as you go: the reviewer watches each one land in the version being made.
  %[1]s working <note id> "what you're doing, in a few words"
  (make that note's edit and save it)
  %[1]s done <note id> "what changed"
Then the next note. Both return at once.
When you have done everything you can, write a JSON object to %[2]q: {"state":"completed","message":"concrete description of the change and checks","notes":{"<note id>":"what you changed for that note"}} ("notes" is optional: each comment shows its own entry, else the message). Write it also when some notes wait on your questions: Retake keeps those open for their answers and finishes the rest. Only if you can't carry on at all, write {"state":"question","message":"your question"} instead.
%[3]s
`, askCommand(), reportPath, done)
}

// askServer sends a question to the server: like call, but without the client's short timeout, since a question
// may carry images.
func (r *runner) askServer(ctx context.Context, path string, body project.WorkAsk, out *project.TaskQuestion) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.opts.URL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.opts.Token)
	req.Header.Set("X-Retake-Worker", r.identity.ID)
	req.Header.Set("X-Retake-Session", r.identity.Session)
	req.Header.Set("Content-Type", "application/json")
	client := *r.opts.HTTP
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{resp.StatusCode, string(data)}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Still makes a JPEG still of the video at path at t seconds, at most 480 px wide (spec/automation.md, "The frame").
type Still func(ctx context.Context, path string, t float64) ([]byte, error)

// FFmpegStill is the default Still: one frame with ffmpeg, within 15 s.
func FFmpegStill(ctx context.Context, path string, t float64) ([]byte, error) {
	return ffmpegFrame(ctx, path, t, 480, project.MaxFrame)
}

// ffmpegFrame is the frame of the video at path at t seconds as a JPEG, at most width pixels wide and limit bytes.
func ffmpegFrame(ctx context.Context, path string, t float64, width, limit int) ([]byte, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-nostdin", "-v", "error", "-threads", "2", "-ss", strconv.FormatFloat(t, 'f', 3, 64), //nolint:gosec // ffmpeg with a path of this project
		"-i", path, "-frames:v", "1", "-vf", "scale='min("+strconv.Itoa(width)+",iw)':-2", "-q:v", "4", "-f", "image2", "-c:v", "mjpeg", "pipe:1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	if out.Len() == 0 || out.Len() > limit {
		return nil, errors.New("no frame")
	}
	return out.Bytes(), nil
}

// frameOf is the still of a question's moment: the version it names (else its note's), from that version's file or a
// render of this machine. nil when there's no such file here or no still could be made.
func (r *runner) frameOf(ctx context.Context, t *project.EditingTask, q project.WorkAsk) []byte {
	if r.opts.Still == nil {
		return nil
	}
	r.mu.Lock()
	note := q.Note
	if note == 0 {
		if p := t.Pending(); len(p) == 1 {
			note = p[0]
		}
	}
	n, known := r.ask.notes[note]
	r.mu.Unlock()
	version, at := q.Version, 0.0
	if known {
		if version == "" {
			version = n.Version
		}
		at = n.T0
	}
	if q.From != nil {
		at = *q.From
	}
	if q.At != nil {
		at = *q.At
	}
	path := r.videoOf(version)
	if path == "" {
		return nil
	}
	b, err := r.opts.Still(ctx, path, at)
	if err != nil || len(b) > project.MaxFrame || http.DetectContentType(b) != "image/jpeg" {
		return nil
	}
	return b
}

// videoOf is the file of version label on this machine: a version file of the project, or a render's playlist.
func (r *runner) videoOf(label string) string {
	if label == "" {
		return ""
	}
	for _, v := range r.onDisk(true) {
		if v.Label == label {
			return v.Path
		}
	}
	entries, _ := os.ReadDir(filepath.Join(r.dir, "renders"))
	for _, e := range entries {
		if !e.IsDir() || !project.ValidWorkID(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.dir, "renders", e.Name(), "state.json"))
		var v project.LiveRender
		if err == nil && len(b) < 8<<20 && json.Unmarshal(b, &v) == nil && (v.Label == label || v.Alias == label) {
			return filepath.Join(r.dir, "renders", e.Name(), "index.m3u8")
		}
	}
	return ""
}
