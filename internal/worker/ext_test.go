package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// testScripts is spec/scripts.md's extension with stand-in UI files, turned on.
func testScripts(t *testing.T) *ext.Set {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "scripts")
	files := map[string]string{
		"extension.toml": `api = 1
id = "scripts"
name = "Scripts"
version = "1.0.0"
[ui]
module = "ui/scripts.js"
[[kinds]]
id = "doc"
folder = "v0"
marker = ["*_v0.md", "idea.md"]
label = "Script"
status = "Idea"
record = true
[[slots]]
slot = "version"
kind = "doc"
[files]
owns = ["v0/**"]
read = "watch"
write = "record"
create = "owner"
[agent]
instructions = "agent/task.md"
context = "agent/context.md"
`,
		"ui/scripts.js":    "export function version(el) {}\n",
		"agent/task.md":    "SCRIPTS-TASK: shape the talk-outs into v0/outline.md and v0/script.md.\n",
		"agent/context.md": "SCRIPTS-CONTEXT: follow v0/script.md.\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, warnings := ext.Resolve([]*ext.Ext{ext.LoadDir(dir)}, []string{"scripts"}, nil)
	if len(warnings) > 0 || set.Get("scripts") == nil {
		t.Fatalf("not on: %v", warnings)
	}
	return set
}

// An idea on a connected computer: the worker reports v0, serves its documents through the relay, runs a task of
// typed comments with the extension's instructions (the agent edits script.md and asks about a heading), keeps a
// copy before the agent so its edit can be undone, and builds the first cut, v1, from a comment that wants one.
func TestIdeaThroughAConnectedComputer(t *testing.T) {
	testmedia.Need(t)
	scratch, e := filepath.Abs("../../.retake-work")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(scratch, 0o700); e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(scratch, "idea-worker-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	local, hosted := filepath.Join(base, "jump-souls"), filepath.Join(base, "hosted")
	if e = os.MkdirAll(filepath.Join(local, "v0"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(local, "v0", "jump-souls_v0.md"), []byte("# Jump souls\n\nThe jump works if you time it.\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	set := testScripts(t)
	st, e := project.Open(hosted)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	token, hash, e := team.NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.CreateToken(project.NewToken{Hash: hash, Role: "agent"}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	srv := server.New(server.Options{Root: hosted, Store: st, Port: port, FollowNewest: true, Exts: set,
		RemoteFiles: true, ExtWatchEvery: 50 * time.Millisecond})
	ts.Config.Handler = srv
	ts.Start()
	defer ts.Close()
	defer func() { _ = srv.Close() }()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	profiles := []Profile{
		{AgentProfile: project.AgentProfile{ID: "doc", Name: "Doc"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "doc-edit"}},
		{AgentProfile: project.AgentProfile{ID: "cut", Name: "Cut"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "first-cut"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Options{URL: ts.URL, Token: token, Root: local, Name: "Test machine", Profiles: profiles,
			Editor: bridge.Editor{Configured: filepath.Join(base, "no-retake-edit")}, Exts: set, NoStill: true})
	}()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(label string, test func() bool) {
		t.Helper()
		for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if test() {
				return
			}
			select {
			case err := <-finished:
				t.Fatalf("worker exited: %v", err)
			default:
			}
		}
		a, _ := st.Automation()
		b, _ := json.Marshal(a)
		t.Fatalf("waiting for %s: %s", label, b)
	}
	do := func(method, path, body string, hdr ...string) (int, http.Header, string) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}

	// v0 reaches the server as the family's only version, with its kind.
	wait("v0", func() bool {
		f := srv.Rescan()
		return len(f) == 1 && f[0].Label == "v0" && f[0].Kind == "scripts.doc"
	})
	// Its files through the relay: list, read with the sha, write with If-Match, a stale write.
	code, _, body := do(http.MethodGet, "/api/ext/scripts/files", "")
	if code != 200 || !strings.Contains(body, `"v0/jump-souls_v0.md"`) {
		t.Fatalf("list: %d %s", code, body)
	}
	code, hdr, body := do(http.MethodGet, "/api/ext/scripts/files/v0/jump-souls_v0.md", "")
	if code != 200 || !strings.HasPrefix(body, "# Jump souls") || hdr.Get("ETag") == "" {
		t.Fatalf("read: %d %q %v", code, body, hdr)
	}
	code, _, body = do(http.MethodPut, "/api/ext/scripts/files/v0/outline.md", "## Cold open\n", "If-Match", "*")
	if code != 200 || !strings.Contains(body, `"v0/outline.md"`) {
		t.Fatalf("write: %d %s", code, body)
	}
	if code, _, _ = do(http.MethodPut, "/api/ext/scripts/files/v0/outline.md", "again", "If-Match", "*"); code != 409 {
		t.Fatalf("a write over a file that exists with If-Match *: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "v0", "outline.md")); string(b) != "## Cold open\n" {
		t.Fatalf("the file on the computer: %q", b)
	}
	if _, e = os.Stat(filepath.Join(hosted, "v0")); !os.IsNotExist(e) {
		t.Fatal("the documents were copied to the server")
	}

	// A typed comment, sent: the agent edits script.md and asks about "Cold open"; the task waits for the answer.
	if code, _, body = do(http.MethodPost, "/api/notes", `{"version": "v0", "text": "Work this talk-out into the outline and script.", "anchor": {"file": "v0/jump-souls_v0.md", "line": 3}}`); code != 201 {
		t.Fatalf("typed comment: %d %s", code, body)
	}
	if e = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) { a.Config.Agent = "doc"; return true, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = st.QueueFeedback([]int64{1}, ""); e != nil {
		t.Fatal(e)
	}
	wait("the question", func() bool {
		n, _ := st.Note(1)
		return n.Status == project.StatusQuestion
	})
	_, _, body = do(http.MethodGet, "/api/notes?v=v0", "")
	if !strings.Contains(body, `"heading":"Cold open"`) {
		t.Fatalf("the question's anchor in api/notes: %s", body)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "v0", "script.md")); !strings.Contains(string(b), "## Cold open") {
		t.Fatalf("script.md: %q", b)
	}
	a, _ := st.Automation()
	j := a.Tasks[0]
	if len(j.Asked) != 1 {
		t.Fatalf("asked: %+v", j)
	}
	if e = st.AnswerTask(j.ID, j.Asked[0].ID, "Start on the jump", "Sam"); e != nil {
		t.Fatal(e)
	}
	wait("the comment done", func() bool {
		n, _ := st.Note(1)
		return n.Status == project.StatusAnswered
	})
	if n, _ := st.Note(1); n.AnsweredIn != "v0" {
		t.Fatalf("%+v", n)
	}
	prompt, _ := os.ReadFile(filepath.Join(local, ".retake", "worker", "tasks", j.ID, "prompt-1.md"))
	p := string(prompt)
	if !strings.Contains(p, "## From the Scripts extension") || !strings.Contains(p, "SCRIPTS-TASK") ||
		!strings.Contains(p, "SCRIPTS-CONTEXT") || strings.Index(p, "SCRIPTS-TASK") > strings.Index(p, "Retake's rules") ||
		strings.Index(p, "Retake's rules") > strings.Index(p, "Nobody is watching") {
		t.Fatalf("prompt order: %s", p)
	}
	// The agent's edit can be undone, through the relay.
	code, _, body = do(http.MethodGet, "/api/ext/scripts/history?path=v0/script.md", "")
	var hist struct {
		Edits []ext.Edit `json:"edits"`
	}
	if json.Unmarshal([]byte(body), &hist) != nil || code != 200 || len(hist.Edits) == 0 || hist.Edits[0].Before != "" {
		t.Fatalf("history: %d %s", code, body)
	}
	undo, _ := json.Marshal(map[string]string{"id": hist.Edits[0].ID, "path": "v0/script.md"})
	if code, _, body = do(http.MethodPost, "/api/ext/scripts/history/undo", string(undo)); code != 200 {
		t.Fatalf("undo: %d %s", code, body)
	}
	if _, e = os.Stat(filepath.Join(local, "v0", "script.md")); !os.IsNotExist(e) {
		t.Fatal("undoing the agent's new file removes it")
	}

	// Start v1: a comment that wants a version; the agent writes a video and it becomes v1/jump-souls_v1.mp4.
	if code, _, body = do(http.MethodPost, "/api/notes", `{"version": "v0", "text": "Build the first cut (v1) from script.md.", "wants": "version"}`); code != 201 {
		t.Fatalf("start v1: %d %s", code, body)
	}
	if e = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) { a.Config.Agent = "cut"; return true, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = st.QueueFeedback([]int64{2}, ""); e != nil {
		t.Fatal(e)
	}
	wait("v1", func() bool {
		n, _ := st.Note(2)
		return n.Status == project.StatusAnswered
	})
	if n, _ := st.Note(2); n.AnsweredIn != "v1" {
		t.Fatalf("%+v", n)
	}
	if _, e = os.Stat(filepath.Join(local, "v1", "jump-souls_v1.mp4")); e != nil {
		t.Fatal("v1 isn't v1/jump-souls_v1.mp4:", e)
	}
	f := srv.Rescan()
	if len(f) != 2 || f[0].Label != "v0" || f[1].Label != "v1" || !f[1].Current {
		t.Fatalf("family: %+v", f)
	}
}

// docAgent is the fake agent's ext modes, called from TestAgentProcess.
func docAgent(t *testing.T, mode string, a project.WorkAssignment) bool {
	switch mode {
	case "doc-edit":
		// It asks until its context has the answer: in a later attempt, or a follow-up round of this one when the
		// answer came while it worked (spec/automation.md, "Batches").
		answered := slices.ContainsFunc(a.Questions, func(q project.TaskQuestion) bool { return q.Answer != nil })
		if !answered {
			if e := os.WriteFile("v0/script.md", []byte("## Cold open\n\n[The jump.] So the idea is...\n"), 0o600); e != nil {
				t.Fatal(e)
			}
			q, _ := json.Marshal(project.WorkAsk{Note: a.Task.Notes[0], Text: "Which opening?",
				Anchor:  &project.Anchor{File: "v0/script.md", Line: 1, Heading: "Cold open"},
				Options: []project.AgentOption{{Text: "Start on the jump"}, {Text: "Start on the shop"}}})
			req, _ := http.NewRequest(http.MethodPost, os.Getenv("RETAKE_ASK_URL")+"/ask", bytes.NewReader(q))
			req.Header.Set("Authorization", "Bearer "+os.Getenv("RETAKE_ASK_TOKEN"))
			res, e := http.DefaultClient.Do(req)
			if e != nil || res.StatusCode != http.StatusOK {
				b := []byte{}
				if res != nil {
					b, _ = io.ReadAll(res.Body)
				}
				t.Fatalf("ask: %v %s", e, b)
			}
			_ = res.Body.Close()
		}
		if e := writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "Wrote the script's cold open"}); e != nil {
			t.Fatal(e)
		}
		return true
	case "first-cut":
		cut := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-threads", "2", "-f", "lavfi", "-i",
			"testsrc=size=160x90:rate=10:duration=2", "-f", "lavfi", "-i", "sine=duration=2", "-c:v", "libx264",
			"-preset", "ultrafast", "-c:a", "aac", "-shortest", "-movflags", "+faststart", os.Getenv("RETAKE_OUTPUT"))
		if out, e := cut.CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
		if e := writeJSON(os.Getenv("RETAKE_REPORT"), agentResult{State: "completed", Message: "First cut from the script"}); e != nil {
			t.Fatal(e)
		}
		return true
	}
	return false
}

// An answer to a question about a document names the document and its heading in herdr or tmux.
func TestAnswerLineOfADocumentQuestion(t *testing.T) {
	q := project.TaskQuestion{Note: 12, Anchor: &project.Anchor{File: "v0/script.md", Line: 3, Heading: "Cold open"},
		Answer: &project.TaskAnswer{Text: " Start  on\nthe jump "}}
	if got, want := answerLine(q), `Answer to your question on note 12 at v0/script.md "Cold open": Start on the jump`; got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	q.Anchor.Heading = ""
	if got := answerLine(q); !strings.Contains(got, "at v0/script.md line 3:") {
		t.Fatal(got)
	}
	q.Anchor, q.At = nil, 83
	if got := answerLine(q); !strings.Contains(got, "at 1:23:") {
		t.Fatal(got)
	}
}

// docOnly: a task of typed comments that don't want a version.
func TestDocOnly(t *testing.T) {
	note := func(id int64, key, wants string) project.WorkNote {
		return project.WorkNote{Note: project.Note{ID: id, Key: key}, Detail: project.Detail{Wants: wants}}
	}
	a := project.WorkAssignment{Task: &project.EditingTask{Notes: []int64{1, 2}},
		Notes: []project.WorkNote{note(1, "doc/1", ""), note(2, "doc/2", "")}}
	if !docOnly(a) {
		t.Fatal("two typed comments")
	}
	a.Notes[1] = note(2, "doc/2", "version")
	if docOnly(a) {
		t.Fatal("one wants a version")
	}
	a.Notes[1] = note(2, "2026-10-01_12-00-00/S1/1", "")
	if docOnly(a) {
		t.Fatal("a note from a take")
	}
	a.Task.Done = []int64{2}
	if !docOnly(a) {
		t.Fatal("the take's note is done")
	}
}
