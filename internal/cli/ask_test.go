package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/worker"
)

// TestRetakeHelper is retake itself for a fake agent: `<test binary> -test.run=^TestRetakeHelper$ -- ask ...` with
// RETAKE_HELPER=1 runs Main with those arguments and exits with its code.
func TestRetakeHelper(t *testing.T) {
	if os.Getenv("RETAKE_HELPER") != "1" {
		return
	}
	os.Exit(Main(context.Background(), flag.Args(), Env{}))
}

func pngFile(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, b.String())
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]float64{"83": 83, "83.5": 83.5, "1:23": 83, "1:23.5": 83.5, "1:02:03": 3723, "0:01.5": 1.5} {
		if got, err := parseClock(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-1", "1:60", "a", "1.5:20", "1:2:3:4", "NaN", "Inf"} {
		if _, err := parseClock(in); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestTaskAskFlags(t *testing.T) {
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "a.png"))
	writeFile(t, filepath.Join(dir, "fake.png"), "not a png")
	writeFile(t, filepath.Join(dir, "lines.json"), `[{"id":3,"color":"red","points":[[0.1,0.2],[0.3,0.4,9]]},{"points":[[0.5,0.5]]}]`)
	getenv := func(string) string { return "" }
	in := taskAsk{note: "#12", at: "1:23", from: "1:20", to: "86", version: "v4-draft2", px: true,
		boxes:  []string{"0.1,0.2,0.3,0.4:this logo", "0,0,1,1"},
		arrows: []string{"0.9,0.1,0.5,0.4:or this one?"},
		draw:   filepath.Join(dir, "lines.json"),
		images: []string{filepath.Join(dir, "a.png") + "@0.6,0.6,0.3,0.3", filepath.Join(dir, "a.png")},
		opts:   []string{"Centered@" + filepath.Join(dir, "a.png"), "Left @ the edge"}}
	q, err := in.request("  Which logo?  ", getenv)
	if err != nil {
		t.Fatal(err)
	}
	if q.Note != 12 || *q.At != 83 || *q.From != 80 || *q.To != 86 || q.Version != "v4-draft2" || !q.Px || q.Text != "Which logo?" {
		t.Fatalf("%+v", q)
	}
	if len(q.Boxes) != 2 || q.Boxes[0].Label != "this logo" || q.Boxes[0].Box != [4]float64{0.1, 0.2, 0.3, 0.4} || q.Boxes[1].Label != "" ||
		q.Arrows[0].From != [2]float64{0.9, 0.1} || q.Arrows[0].To != [2]float64{0.5, 0.4} || q.Arrows[0].Label != "or this one?" {
		t.Fatalf("boxes and arrows %+v %+v", q.Boxes, q.Arrows)
	}
	if len(q.Strokes) != 2 || q.Strokes[0].Points[1] != [2]float64{0.3, 0.4} {
		t.Fatalf("lines %+v", q.Strokes)
	}
	if len(q.Images) != 2 || q.Images[0].Box == nil || *q.Images[0].Box != [4]float64{0.6, 0.6, 0.3, 0.3} || q.Images[1].Box != nil ||
		q.Images[0].Name != "a.png" || len(q.Images[0].Data) == 0 {
		t.Fatalf("images %+v", q.Images)
	}
	if len(q.Options) != 2 || q.Options[0].Text != "Centered" || q.Options[0].Image == nil || q.Options[1].Text != "Left @ the edge" || q.Options[1].Image != nil {
		t.Fatalf("options %+v", q.Options)
	}
	for name, bad := range map[string]taskAsk{
		"box numbers":   {boxes: []string{"0.1,0.2,0.3"}},
		"arrow numbers": {arrows: []string{"1,2,x,4"}},
		"long label":    {boxes: []string{"0,0,1,1:" + strings.Repeat("x", 41)}},
		"half range":    {from: "1"},
		"bad time":      {at: "soon"},
		"not an image":  {images: []string{filepath.Join(dir, "fake.png")}},
		"no file":       {images: []string{filepath.Join(dir, "none.png")}},
		"draw missing":  {draw: filepath.Join(dir, "none.json")},
		"seven options": {opts: []string{"a", "b", "c", "d", "e", "f", "g"}},
		"bad note":      {note: "twelve"},
	} {
		if _, err := bad.request("q", getenv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := (taskAsk{}).request("  ", getenv); err == nil {
		t.Error("no words accepted")
	}
}

// Outside a task, `retake ask "question"` says so; the project-level form is unchanged.
func TestAskOutsideATask(t *testing.T) {
	w := newWorld(t)
	t.Chdir(t.TempDir())
	if code := w.run("ask", "--at", "1", "Which?"); code != ExitUsage || !strings.Contains(w.stderr.String(), "not inside a Retake task") {
		t.Fatalf("%d %s", code, w.stderr)
	}
	if code := w.run("ask", w.video, "1", "Which?", "--box", "0,0,1,1"); code != ExitUsage || !strings.Contains(w.stderr.String(), "inside a task") {
		t.Fatalf("%d %s", code, w.stderr)
	}
}

// TestAskFindsARestartedWorker: an agent that outlived its worker has the old worker's URL in its environment; when
// that doesn't answer, retake ask goes where the project's ask.json says the worker is now (spec/automation.md,
// "Agents outlive Retake").
func TestAskFindsARestartedWorker(t *testing.T) {
	w := newWorld(t)
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := "http://" + dead.Addr().String()
	_ = dead.Close()
	var got string
	now := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_ = json.NewEncoder(rw).Encode(project.TaskQuestion{ID: 7, Note: 1, Text: "Which?"})
	}))
	defer now.Close()
	folder := filepath.Join(t.TempDir(), "shop")
	if err = os.MkdirAll(filepath.Join(folder, ".retake", "worker"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(worker.AskEndpoint{URL: now.URL, Token: "the-secret"})
	if err = os.WriteFile(filepath.Join(folder, ".retake", "worker", worker.AskFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(folder)
	w.env["RETAKE_ASK_URL"], w.env["RETAKE_ASK_TOKEN"] = gone, "the-secret"
	if code := w.run("ask", "--at", "1", "Which?"); code != ExitOK || got != "Bearer the-secret" ||
		!strings.Contains(w.stdout.String(), "(question 7)") {
		t.Fatalf("%d %q %s %s", code, got, w.stdout, w.stderr)
	}
}

// askAgent is a background agent that asks with visuals from inside its task (through the test binary as retake),
// also with a wrong secret, and reports; once the context has the answer it makes the next version and reports.
const askAgent = `#!/bin/sh
cat >/dev/null
ask() { RETAKE_HELPER=1 "$RETAKE_CLI" -test.run='^TestRetakeHelper$' -- ask "$@"; }
if grep -q '"answer"' "$RETAKE_CONTEXT"; then
	grep -q '"text": "Teal"' "$RETAKE_CONTEXT" || { echo "the answer isn't in the context" >&2; exit 3; }
	cp clip_v2.mp4 "$RETAKE_OUTPUT"
	printf '{"state":"completed","message":"Made the title teal."}' > "$RETAKE_REPORT"
	exit 0
fi
RETAKE_ASK_TOKEN=wrong ask "Sneaky?" > wrong.txt 2>&1
echo "exit $?" >> wrong.txt
ask --at 0:01.5 --box 0.1,0.2,0.3,0.4:"this title" --arrow 0.9,0.1,0.5,0.3 --option Blue --option "Teal@$OPTION_PNG" \
	"Blue or teal?" > asked.txt 2>&1 || { cat asked.txt >&2; exit 4; }
printf '{"state":"completed","message":"Asked which colour."}' > "$RETAKE_REPORT"
`

// askFixture is a folder with one plain project, promo (clip_v1, clip_v2), a choice's image and askAgent.
func askFixture(t *testing.T) (dir, root, agentPath, option string) {
	t.Helper()
	testmedia.NeedShell(t)
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err = os.MkdirTemp(scratch, "ask-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root = filepath.Join(dir, "videos", "promo")
	testmedia.Video(t, filepath.Join(root, "clip_v1.mp4"), 2)
	testmedia.Video(t, filepath.Join(root, "clip_v2.mp4"), 3)
	agentPath = filepath.Join(dir, "fake-agent")
	if err = os.WriteFile(agentPath, []byte(askAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	option = filepath.Join(dir, "teal.png")
	pngFile(t, option)
	return dir, root, agentPath, option
}

// client talks to a review page as a person: the session cookie and the origin of a hosted one.
type client struct {
	t             *testing.T
	base, session string
	project       string
}

func (c client) do(method, path, body string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(method, strings.TrimSuffix(c.base, "/")+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", strings.TrimSuffix(c.base, "/"))
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: "retake_session", Value: c.session})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c client) automation() project.AutomationState {
	c.t.Helper()
	var a project.AutomationState
	if code, b := c.do(http.MethodGet, "/p/"+c.project+"/api/automation", ""); code == 200 {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

func (c client) waitTask(what string, ok func(project.EditingTask) bool, log fmt.Stringer) project.EditingTask {
	c.t.Helper()
	var a project.AutomationState
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if a = c.automation(); len(a.Tasks) == 1 && ok(a.Tasks[0]) {
			return a.Tasks[0]
		}
	}
	c.t.Fatalf("waiting for %s: %+v\n%s", what, a.Tasks, log)
	return project.EditingTask{}
}

// askStory is the story both modes tell: a comment is sent; the agent asks with visuals and reports; the comment is
// Question with the visuals under it; the person taps Teal; the same task resumes with the answer and makes v3.
func askStory(t *testing.T, c client, st *project.Store, root string, log fmt.Stringer) {
	if _, err := st.Sync(project.Run{Version: "v2", Comments: []project.Comment{{Key: "title", Text: "Change the title colour", T0: 1, T1: 2}}}); err != nil {
		t.Fatal(err)
	}
	if code, b := c.do(http.MethodPost, "/p/"+c.project+"/api/automation/tasks", `{"notes":[1]}`); code != 200 {
		t.Fatalf("send: %d %s", code, b)
	}
	task := c.waitTask("the question", func(j project.EditingTask) bool { return j.State == "question" }, log)
	asked, _ := os.ReadFile(filepath.Join(root, "asked.txt"))
	if !strings.Contains(string(asked), "asked about #1 at 0:01 (question ") {
		t.Fatalf("asked.txt: %s", asked)
	}
	wrong, _ := os.ReadFile(filepath.Join(root, "wrong.txt"))
	if !strings.Contains(string(wrong), "no Retake task is running with this secret") || !strings.Contains(string(wrong), "exit 1") {
		t.Fatalf("a wrong secret: %s", wrong)
	}
	if _, err := os.Stat(filepath.Join(root, ".retake", "worker", worker.AskFile)); !os.IsNotExist(err) {
		t.Fatal("ask.json outlived its attempt:", err)
	}
	if n, _ := st.Note(1); n.Status != project.StatusQuestion {
		t.Fatalf("note is %s", n.Status)
	}
	code, b := c.do(http.MethodGet, "/p/"+c.project+"/api/notes?v=v2&scope=review", "")
	var res struct {
		Items []project.Item `json:"items"`
	}
	if err := json.Unmarshal(b, &res); code != 200 || err != nil || len(res.Items) != 1 || res.Items[0].Ask == nil {
		t.Fatalf("notes: %d %s", code, b)
	}
	ask := res.Items[0].Ask
	v := ask.Visuals
	if ask.Task != task.ID || ask.T != 1.5 || ask.Text != "Blue or teal?" || v == nil || len(v.Boxes) != 1 || v.Boxes[0].Label != "this title" ||
		len(v.Arrows) != 1 || len(v.Options) != 2 || v.Options[1].Text != "Teal" || v.Options[1].Image == nil || v.Frame == "" {
		t.Fatalf("the question under its comment: %s", b)
	}
	for _, url := range []string{v.Options[1].Image.URL, v.Frame} {
		if code, img := c.do(http.MethodGet, strings.TrimPrefix(url, strings.TrimSuffix(c.base, "/")), ""); code != 200 || len(img) == 0 {
			t.Fatalf("%s: %d", url, code)
		}
	}
	// The tap: the choice's text is the answer, to the same task.
	if code, b := c.do(http.MethodPost, "/p/"+c.project+"/api/automation/tasks/"+task.ID+"/answer", fmt.Sprintf(`{"question":%d,"message":"Teal"}`, ask.ID)); code != 200 {
		t.Fatalf("answer: %d %s", code, b)
	}
	done := c.waitTask("the edit", func(j project.EditingTask) bool { return j.State == "completed" }, log)
	if done.ID != task.ID || done.Attempt != 2 || done.Result != "v3" {
		t.Fatalf("task %+v", done)
	}
	if n, _ := st.Note(1); n.Status != project.StatusAnswered || n.AnsweredIn != "v3" || n.Change != "Made the title teal." {
		t.Fatalf("note %+v", n)
	}
}

// TestAskFromInsideATaskLocally: retake open; the in-process worker's agent asks with `retake ask`.
func TestAskFromInsideATaskLocally(t *testing.T) {
	dir, root, agentPath, option := askFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser",
		"RETAKE_EDIT": filepath.Join(dir, "no-retake-edit")}
	w.config(t, "idle_exit_minutes = 0\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "fake", Name: "Fake agent"}, Command: []string{agentPath},
			Env: map[string]string{"RETAKE_CLI": bin, "OPTION_PNG": option}}}
	}
	url, stop, done := w.serve(t, env, "open", filepath.Dir(root), "--port", "0", "--no-open")
	c := client{t: t, base: url, project: "promo"}
	for deadline := time.Now().Add(30 * time.Second); len(c.automation().Workers) != 1; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no local worker\n%s", w.stderr)
		}
	}
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	askStory(t, c, st, root, w.stderr)
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

// TestAskFromInsideATaskHosted: the same through retake connect: the question and its images reach the server
// over the worker's connection.
func TestAskFromInsideATaskHosted(t *testing.T) {
	dir, root, agentPath, option := askFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	data := filepath.Join(dir, "data")
	base, session, line, _ := hosted(t, w, data)
	profiles := filepath.Join(dir, "profiles.json")
	b, _ := json.Marshal([]map[string]any{{"id": "fake", "name": "Fake agent", "command": []string{agentPath},
		"env": map[string]string{"RETAKE_CLI": bin, "OPTION_PNG": option}}})
	writeFile(t, profiles, string(b))
	env := w.environment(nil)
	env.ConnectEvery = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan int, 1)
	go func() {
		exited <- Main(ctx, []string{"connect", line[1], "--token", line[2], "--folder", filepath.Dir(root), "--name", "test-pc",
			"--profiles", profiles}, env)
	}()
	defer func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			t.Error("connect didn't stop")
		}
	}()
	c := client{t: t, base: base, session: session, project: "promo"}
	for deadline := time.Now().Add(30 * time.Second); len(c.automation().Workers) != 1; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no connected worker\n%s%s", w.stdout, w.stderr)
		}
	}
	st, err := project.Open(filepath.Join(data, "projects", "promo"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	askStory(t, c, st, root, w.stdout)
}

// The answer reaches an agent in herdr while it still works (a new message in its session), and an agent in tmux
// that had already reported (the task starts again in the same session, the answer first). Fake herdr and tmux on
// PATH; the fake agent asks with the test binary as retake.
func TestAnswersReachTheAgentsSession(t *testing.T) {
	for _, tc := range []struct{ mode, agent string }{{worker.OpenHerdr, "ask"}, {worker.OpenTmux, "ask-report"}} {
		t.Run(tc.mode, func(t *testing.T) {
			dir, root, _, _ := askFixture(t)
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			testdata, err := filepath.Abs("../worker/testdata")
			if err != nil {
				t.Fatal(err)
			}
			fake := filepath.Join(dir, "fake")
			for _, d := range []string{fake, filepath.Join(dir, "lifeline")} {
				if err = os.Mkdir(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			seen := filepath.Join(dir, "seen.log")
			t.Setenv("PATH", testdata+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "no-herdr.sock"))
			t.Setenv("FAKE_HERDR_DIR", fake)
			t.Setenv("FAKE_TMUX_DIR", fake)
			t.Setenv("FAKE_AGENT", filepath.Join(testdata, "agent"))
			t.Setenv("FAKE_AGENT_MODE", tc.agent)
			t.Setenv("FAKE_AGENT_SEEN", seen)
			t.Setenv("FAKE_LIFELINE", filepath.Join(dir, "lifeline"))
			t.Setenv("RETAKE_CLI", bin)
			t.Cleanup(func() {
				_ = os.Remove(filepath.Join(dir, "lifeline"))
				files, _ := filepath.Glob(filepath.Join(fake, "pid*"))
				typed, _ := filepath.Glob(filepath.Join(fake, "typed-*"))
				for _, f := range append(files, typed...) {
					b, _ := os.ReadFile(f)
					for _, pid := range strings.Fields(string(b)) {
						if n, e := strconv.Atoi(pid); e == nil {
							killPID(n)
						}
					}
				}
			})
			w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
			w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser",
				"RETAKE_EDIT": filepath.Join(dir, "no-retake-edit")}
			w.config(t, "idle_exit_minutes = 0\n")
			env := w.environment(nil)
			program := "claude" // herdr starts its agent by kind; the fake herdr runs $FAKE_AGENT
			if tc.mode == worker.OpenTmux {
				program = filepath.Join(testdata, "agent") // tmux runs the command itself
			}
			env.Agents = func() []worker.Profile {
				return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"},
					Command: []string{"claude", "-p"}, Interactive: []string{program, "--dangerously-skip-permissions"}, OpenIn: tc.mode}}
			}
			url, stop, done := w.serve(t, env, "open", filepath.Dir(root), "--port", "0", "--no-open")
			c := client{t: t, base: url, project: "promo"}
			for deadline := time.Now().Add(30 * time.Second); len(c.automation().Workers) != 1; time.Sleep(100 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("no local worker\n%s", w.stderr)
				}
			}
			st, err := project.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			if _, err = st.Sync(project.Run{Version: "v2", Comments: []project.Comment{{Key: "title", Text: "Change the title colour", T0: 1, T1: 2}}}); err != nil {
				t.Fatal(err)
			}
			if code, b := c.do(http.MethodPost, "/p/promo/api/automation/tasks", `{"notes":[1]}`); code != 200 {
				t.Fatalf("send: %d %s", code, b)
			}
			var task project.EditingTask
			var q int64
			for deadline := time.Now().Add(60 * time.Second); q == 0; time.Sleep(100 * time.Millisecond) {
				if time.Now().After(deadline) {
					s, _ := os.ReadFile(seen)
					t.Fatalf("the agent never asked: %+v\n%s\n%s", c.automation().Tasks, s, w.stderr)
				}
				a := c.automation()
				if len(a.Tasks) == 1 && len(a.Tasks[0].Asked) == 1 && (tc.agent == "ask" || a.Tasks[0].State == "question") {
					task, q = a.Tasks[0], a.Tasks[0].Asked[0].ID
				}
			}
			if tc.agent == "ask" && task.State != "running" {
				t.Fatalf("the herdr agent is still at it: %+v", task)
			}
			if code, b := c.do(http.MethodPost, "/p/promo/api/automation/tasks/"+task.ID+"/answer", fmt.Sprintf(`{"question":%d,"message":"Teal"}`, q)); code != 200 {
				t.Fatalf("answer: %d %s", code, b)
			}
			end := c.waitTask("the edit", func(j project.EditingTask) bool { return j.State == "completed" }, w.stderr)
			s, _ := os.ReadFile(seen)
			if !strings.Contains(string(s), "Answer to your question on note 1 at 0:01: Teal") || end.Result != "v3" {
				t.Fatalf("%+v\n%s", end, s)
			}
			attempts := 1
			if tc.agent == "ask-report" {
				attempts = 2 // it had reported: the task started again, in the same session, the answer first
				if !strings.Contains(string(s), "Answer to your question on note 1 at 0:01: Teal. Carry on with Retake task "+task.ID+" (attempt 2)") {
					t.Fatalf("seen:\n%s", s)
				}
			}
			if end.Attempt != attempts {
				t.Fatalf("attempt %d", end.Attempt)
			}
			argv, _ := os.ReadFile(filepath.Join(fake, "argv.log"))
			creates := strings.Count(string(argv), "workspace\x1fcreate") + strings.Count(string(argv), "new-session\x1f") + strings.Count(string(argv), "new-window\x1f")
			if creates != 1 {
				t.Fatalf("%d sessions opened:\n%s", creates, argv)
			}
			stop()
			if code := wait(t, done); code != ExitOK {
				t.Fatalf("exit %d: %s", code, w.stderr)
			}
		})
	}
}
