package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/worker"
)

// chatAgent stands in for Claude Code in the chat (spec/chat.md). It is named claude and takes -p, so the worker
// streams and resumes it as Claude Code. It logs its arguments, prints Claude's stream-json, edits the video's
// document and asks with retake ask (the test binary as retake). HANG-NOW in the message makes it hang (for Stop), and TWO-LOOKS makes it ask. Run
// as an editing task (RETAKE_REPORT is set), it waits for $FAKE_DIR/go and makes the next version from footage.mp4.
const chatAgent = `#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DIR/argv.log"
prompt=$(cat)
sid=11111111-2222-4333-8444-555555555555
ev() { printf '{"type":"stream_event","event":%s,"session_id":"%s","parent_tool_use_id":null}\n' "$1" "$sid"; }
say() {
	ev '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'
	ev "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"$1\"}}"
}
tool() { printf '{"type":"assistant","message":{"id":"m","role":"assistant","content":[{"type":"tool_use","id":"%s","name":"%s","input":%s}]},"session_id":"%s","parent_tool_use_id":null}\n' "$1" "$2" "$3" "$sid"; }
if [ -n "$RETAKE_REPORT" ]; then
	i=0
	while [ ! -f "$FAKE_DIR/go" ] && [ "$i" -lt 600 ]; do i=$((i + 1)); sleep 0.1; done
	cp footage.mp4 "$RETAKE_OUTPUT"
	printf '{"state":"completed","message":"Built it from the script."}' >"$RETAKE_REPORT"
	exit 0
fi
printf '{"type":"system","subtype":"init","cwd":"%s","session_id":"%s"}\n' "$PWD" "$sid"
case "$prompt" in
*HANG-NOW*)
	say "Working on it"
	echo $$ >"$FAKE_DIR/hang.pid"
	while :; do sleep 0.1; done
	;;
esac
doc=$(ls v0/*_v0.md | head -n 1)
say "I'll pull "
ev '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the link in."}}'
tool t1 Read "{\"file_path\":\"$PWD/$doc\"}"
tool t2 Edit "{\"file_path\":\"$PWD/$doc\",\"old_string\":\"x\",\"new_string\":\"y\"}"
printf '\nhttps://example.org/clip\n' >>"$doc"
tool t3 Edit "{\"file_path\":\"$PWD/$doc\",\"old_string\":\"y\",\"new_string\":\"z\"}"
case "$prompt" in
*TWO-LOOKS*)
	tool t4 Bash '{"command":"retake ask --option Ink --option Neon \"Which look?\""}'
	RETAKE_HELPER=1 "$RETAKE_CLI" -test.run='^TestRetakeHelper$' -- ask --option "Ink@v0/images/ink.png" --option Neon \
		"Which look?" >>"$FAKE_DIR/ask.log" 2>&1 || exit 7
	;;
esac
ev '{"type":"message_start","message":{"id":"m2","type":"message","role":"assistant","content":[]}}'
say "Done."
printf '{"type":"result","subtype":"success","is_error":false,"result":"Done.","session_id":"%s"}\n' "$sid"
`

// chatFixture is a folder with one idea, jump (its document, an image and footage), and the fake Claude Code.
func chatFixture(t *testing.T) (dir, root, agent string) {
	t.Helper()
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	if dir, err = os.MkdirTemp(scratch, "chat-"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root = filepath.Join(dir, "videos", "jump")
	writeFile(t, filepath.Join(root, "v0", "jump_v0.md"), "# Jump\n\nMy own words.\n")
	pngFile(t, filepath.Join(root, "v0", "images", "ink.png"))
	testmedia.Video(t, filepath.Join(root, "footage.mp4"), 2)
	agent = filepath.Join(dir, "bin", "claude")
	writeFile(t, agent, chatAgent)
	if err = os.Chmod(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killHung(filepath.Join(dir, "fake")) })
	return dir, root, agent
}

func killHung(fake string) {
	if b, err := os.ReadFile(filepath.Join(fake, "hang.pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (c client) chat() project.ChatView {
	c.t.Helper()
	var v project.ChatView
	code, b := c.do(http.MethodGet, "/p/"+c.project+"/api/chat", "")
	if code != 200 || json.Unmarshal(b, &v) != nil {
		c.t.Fatalf("api/chat: %d %s", code, b)
	}
	return v
}

func (c client) waitChat(what string, ok func(project.ChatView) bool, log fmt.Stringer) project.ChatView {
	c.t.Helper()
	var v project.ChatView
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if v = c.chat(); ok(v) {
			return v
		}
	}
	b, _ := json.MarshalIndent(v, "", " ")
	c.t.Fatalf("waiting for %s: %s\n%s", what, b, log)
	return v
}

func lastOf(v project.ChatView, role string) *project.ChatMessage {
	for i := len(v.Messages) - 1; i >= 0; i-- {
		if v.Messages[i].Role == role {
			return &v.Messages[i]
		}
	}
	return nil
}

// chatStory is the chat end to end, the same locally and hosted: a turn that streams words and quiet tool lines,
// edits the document and asks with choices; a tap that resumes the agent's session; Stop; Send making v1 while a
// message waits for the busy agent, then disabled with its reason.
func chatStory(t *testing.T, c client, root, fake string, log fmt.Stringer) {
	t.Helper()
	path := "/p/" + c.project + "/api/chat"
	if v := c.chat(); len(v.Messages) != 0 || v.Turn != nil {
		t.Fatalf("a new chat: %+v", v)
	}
	if code, b := c.do(http.MethodPost, path, `{"text":"Pull in the clip, TWO-LOOKS","images":["v0/images/ink.png"]}`); code != 201 {
		t.Fatalf("post: %d %s", code, b)
	}
	if code, _ := c.do(http.MethodPost, path, `{"text":"x","images":["../../etc/passwd"]}`); code != 400 {
		t.Fatal("an image outside the video's files", code)
	}
	v := c.waitChat("the first turn", func(v project.ChatView) bool {
		return v.Turn == nil && len(v.Messages) > 0 && v.Messages[len(v.Messages)-1].Text == "Done."
	}, log)
	var got []string
	for _, m := range v.Messages {
		got = append(got, m.Role+":"+m.Text)
		if strings.Contains(m.Text, `"type"`) {
			t.Fatalf("raw JSON in the chat: %q", m.Text)
		}
	}
	want := "person:Pull in the clip, TWO-LOOKS|agent:I'll pull the link in.|tool:Reading the doc|" +
		"tool:Editing the doc|question:Which look?|agent:Done."
	if strings.Join(got, "|") != want {
		t.Fatalf("thread:\n got %s\nwant %s\n%s", strings.Join(got, "|"), want, log)
	}
	q := lastOf(v, project.ChatQuestion)
	if len(q.Options) != 2 || q.Options[0].Image == nil || q.Options[1].Text != "Neon" {
		t.Fatalf("question %+v", q)
	}
	if code, img := c.do(http.MethodGet, "/p/"+c.project+"/"+q.Options[0].Image.URL, ""); code != 200 || len(img) == 0 {
		t.Fatalf("the choice's image: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "v0", "jump_v0.md")); !strings.Contains(string(b), "https://example.org/clip") ||
		!strings.Contains(string(b), "My own words.") {
		t.Fatalf("the document: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(fake, "ask.log")); !strings.Contains(string(b), "asked in the chat") {
		t.Fatalf("retake ask: %s", b)
	}
	// The turn's edit can be undone: it is in the agent edit history.
	code, b := c.do(http.MethodGet, "/p/"+c.project+"/api/ext/scripts/history?path=v0/jump_v0.md", "")
	if code != 200 || !strings.Contains(string(b), `"task":"chat-`) {
		t.Fatalf("history: %d %s", code, b)
	}

	// A tap answers it, and the next turn resumes the agent's session.
	if code, b := c.do(http.MethodPost, path, fmt.Sprintf(`{"text":"Neon","answer":%d}`, q.ID)); code != 201 {
		t.Fatalf("tap: %d %s", code, b)
	}
	v = c.waitChat("the second turn", func(v project.ChatView) bool {
		n := 0
		for _, m := range v.Messages {
			if m.Text == "Done." {
				n++
			}
		}
		return v.Turn == nil && n == 2
	}, log)
	if q = lastOf(v, project.ChatQuestion); q.Answer != "Neon" {
		t.Fatalf("the answer: %+v", q)
	}
	argv, _ := os.ReadFile(filepath.Join(fake, "argv.log"))
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "--resume") || !strings.Contains(lines[0], "--include-partial-messages") ||
		!strings.HasSuffix(lines[1], "--resume 11111111-2222-4333-8444-555555555555") {
		t.Fatalf("argv: %q", lines)
	}

	// Stop kills that turn's process and nothing else.
	if code, b := c.do(http.MethodPost, path, `{"text":"HANG-NOW"}`); code != 201 {
		t.Fatalf("post: %d %s", code, b)
	}
	c.waitChat("the words before the hang", func(v project.ChatView) bool {
		m := lastOf(v, project.ChatAgent)
		return m != nil && m.Text == "Working on it" && m.Streaming
	}, log)
	if code, b := c.do(http.MethodPost, path+"/stop", ""); code != 200 {
		t.Fatalf("stop: %d %s", code, b)
	}
	v = c.waitChat("stopped", func(v project.ChatView) bool {
		m := v.Messages[len(v.Messages)-1]
		return v.Turn == nil && m.Role == project.ChatInfo && m.Text == project.ChatStopped
	}, log)
	if m := lastOf(v, project.ChatAgent); m.Streaming {
		t.Fatal("still streaming after Stop")
	}
	b, _ = os.ReadFile(filepath.Join(fake, "hang.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for deadline := time.Now().Add(10 * time.Second); pid > 0 && syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the turn's process lives on after Stop")
		}
	}
	if code, _ := c.do(http.MethodPost, path+"/stop", ""); code != 409 {
		t.Fatal("stop with no turn", code)
	}

	// Send: "I like it, make the video". With no cut yet it builds v1; a message meanwhile waits for the busy agent.
	code, b = c.do(http.MethodPost, path+"/send", `{"file":"v0/jump_v0.md"}`)
	var sent struct {
		Note   int64  `json:"note"`
		Target string `json:"target"`
	}
	if code != 200 || json.Unmarshal(b, &sent) != nil || sent.Target != "v1" {
		t.Fatalf("send: %d %s", code, b)
	}
	c.waitTask("the build", func(j project.EditingTask) bool { return j.State == "running" }, log)
	if code, b := c.do(http.MethodPost, path, `{"text":"one more thing"}`); code != 201 {
		t.Fatalf("post: %d %s", code, b)
	}
	if v = c.chat(); v.Busy != "The agent is busy with v1's edits" {
		t.Fatalf("busy: %+v", v)
	}
	if code, b = c.do(http.MethodPost, path+"/send", `{"file":"v0/jump_v0.md"}`); code != 409 ||
		!strings.Contains(string(b), "Nothing changed since the last Send") {
		t.Fatalf("a second send: %d %s", code, b)
	}
	writeFile(t, filepath.Join(fake, "go"), "")
	c.waitTask("v1", func(j project.EditingTask) bool { return j.State == "completed" }, log)
	if _, err := os.Stat(filepath.Join(root, "v1", "jump_v1.mp4")); err != nil {
		t.Fatal("v1:", err)
	}
	// Then the waiting message gets its turn.
	c.waitChat("the turn after the build", func(v project.ChatView) bool {
		return v.Turn == nil && v.Messages[len(v.Messages)-1].Text == "Done." && v.Busy == ""
	}, log)
	if v = c.chat(); v.Sent == nil || v.Sent.Target != "v1" || v.Sent.Open {
		t.Fatalf("sent %+v", v.Sent)
	}
}

func TestChatLocally(t *testing.T) {
	dir, root, agent := chatFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake")
	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser",
		"RETAKE_EDIT": filepath.Join(dir, "no-retake-edit")}
	w.config(t, "idle_exit_minutes = 0\n[extensions]\nenabled = [\"scripts\"]\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"},
			Command: []string{agent, "-p", "--output-format", "stream-json", "--verbose"},
			Env:     map[string]string{"RETAKE_CLI": bin, "FAKE_DIR": fake}}}
	}
	url, stop, done := w.serve(t, env, "open", filepath.Dir(root), "--port", "0", "--no-open")
	c := client{t: t, base: url, project: "jump"}
	for deadline := time.Now().Add(30 * time.Second); len(c.automation().Workers) != 1; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no local worker\n%s", w.stderr)
		}
	}
	chatStory(t, c, root, fake, w.stderr)
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

// TestChatHosted: the same through retake serve and retake connect: the agent runs on the connected computer, its
// words reach the server over the worker's connection, and the document stays on the computer.
func TestChatHosted(t *testing.T) {
	dir, root, agent := chatFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake")
	w := newWorld(t)
	w.config(t, "[extensions]\nenabled = [\"scripts\"]\n")
	data := filepath.Join(dir, "data")
	base, session, line, _ := hosted(t, w, data, "--extensions", "scripts")
	profiles := filepath.Join(dir, "profiles.json")
	b, _ := json.Marshal([]map[string]any{{"id": "claude", "name": "Claude Code",
		"command": []string{agent, "-p", "--output-format", "stream-json", "--verbose"},
		"env":     map[string]string{"RETAKE_CLI": bin, "FAKE_DIR": fake}}})
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
	c := client{t: t, base: base, session: session, project: "jump"}
	for deadline := time.Now().Add(30 * time.Second); len(c.automation().Workers) != 1; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no connected worker\n%s%s", w.stdout, w.stderr)
		}
	}
	chatStory(t, c, root, fake, w.stdout)
	if _, err = os.Stat(filepath.Join(data, "projects", "jump", "v0")); !os.IsNotExist(err) {
		t.Fatal("the document was copied to the server")
	}
}
