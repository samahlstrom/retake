package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/worker"
)

// progressAgent works two notes one at a time, as the task prompt asks: retake working, the edit (its video so far to
// $RETAKE_OUTPUT), retake done. It waits for the test's gates between them, so the test sees each step. Before that it
// tries a wrong secret and a done with no video yet; both are refused.
const progressAgent = `#!/bin/sh
cat >/dev/null
r() { RETAKE_HELPER=1 "$RETAKE_CLI" -test.run='^TestRetakeHelper$' -- "$@"; }
gate() { n=0; until [ -e "$GATES/$1" ]; do sleep 0.1; n=$((n+1)); [ $n -gt 900 ] && exit 9; done; }
RETAKE_ASK_TOKEN=wrong r working 1 "sneaky" > wrong.txt 2>&1; echo "exit $?" >> wrong.txt
r done 1 "too soon" > early.txt 2>&1; echo "exit $?" >> early.txt
r working 1 "slowing the mask fade" > w1.txt 2>&1 || { cat w1.txt >&2; exit 4; }
gate 1
cp clip_v1.mp4 "$RETAKE_OUTPUT"
r done 1 "Slowed the mask fade" > d1.txt 2>&1 || { cat d1.txt >&2; exit 5; }
r working 2 "moving the bell" > w2.txt 2>&1 || exit 6
gate 2
cp clip_v2.mp4 "$RETAKE_OUTPUT"
r done 2 "Moved the bell" > d2.txt 2>&1 || { cat d2.txt >&2; exit 7; }
gate 3
printf '{"state":"completed","message":"Both done","notes":{"2":"Moved the bell 6 frames earlier"}}' > "$RETAKE_REPORT"
`

// progressFixture is a folder with one plain project, promo (clip_v1, clip_v2), the gates' folder and progressAgent.
func progressFixture(t *testing.T) (dir, root, agentPath, gates string) {
	t.Helper()
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err = os.MkdirTemp(scratch, "progress-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root = filepath.Join(dir, "videos", "promo")
	testmedia.Video(t, filepath.Join(root, "clip_v1.mp4"), 2)
	testmedia.Video(t, filepath.Join(root, "clip_v2.mp4"), 3)
	agentPath = filepath.Join(dir, "fake-agent")
	if err = os.WriteFile(agentPath, []byte(progressAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	gates = filepath.Join(dir, "gates")
	if err = os.MkdirAll(gates, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, root, agentPath, gates
}

// revisions is the renders of task: its target's live revisions, oldest first.
func revisions(a project.AutomationState, task string) []project.LiveRender {
	var out []project.LiveRender
	for _, v := range a.Renders {
		if v.Task == task {
			out = append(out, v)
		}
	}
	return out
}

// progressStory is the story both modes tell: two comments on v2 are sent. v3 is live at once (its first revision is
// v2 itself); the agent marks #1 working, then done once its video so far is written: a new revision of v3 has it
// and #1 is ready while #2 is being worked on; then #2. The report finishes both, #1 with its mark's words and #2 with
// the report's own.
func progressStory(t *testing.T, c client, st *project.Store, root, gates string, log fmt.Stringer) {
	if _, err := st.Sync(project.Run{Version: "v2", Comments: []project.Comment{{Key: "fade", Text: "Fade the mask out slower", T0: 0.5, T1: 1},
		{Key: "bell", Text: "The bell is late", T0: 2, T1: 2.5}}}); err != nil {
		t.Fatal(err)
	}
	if code, b := c.do(http.MethodPost, "/p/"+c.project+"/api/automation/tasks", `{"notes":[1,2]}`); code != 200 {
		t.Fatalf("send: %d %s", code, b)
	}
	open := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(root, name))
		return string(b)
	}
	task := c.waitTask("#1 working, with v3 live", func(j project.EditingTask) bool {
		return j.State == "running" && j.Working != nil && j.Working.Note == 1 && len(revisions(c.automation(), j.ID)) == 1
	}, log)
	if task.Working.Text != "slowing the mask fade" || task.Target != "v3" || len(task.Marked) != 0 {
		t.Fatalf("working: %+v", task)
	}
	start := revisions(c.automation(), task.ID)[0]
	if start.Label != "v3-draft1" || start.Media != "video.mp4" || !start.Complete {
		t.Fatalf("v3's first revision: %+v", start)
	}
	if w := open("wrong.txt"); !strings.Contains(w, "no Retake task is running with this secret") || !strings.Contains(w, "exit 1") {
		t.Fatalf("a wrong secret: %s", w)
	}
	if e := open("early.txt"); !strings.Contains(e, "write the video so far to") || !strings.Contains(e, "exit 1") {
		t.Fatalf("done before any video: %s", e)
	}
	if w := open("w1.txt"); strings.TrimSpace(w) != "working on #1: slowing the mask fade" {
		t.Fatalf("w1: %q", w)
	}
	gate := func(n int) { writeFile(t, filepath.Join(gates, fmt.Sprint(n)), "") }
	gate(1)
	task = c.waitTask("#1 in v3", func(j project.EditingTask) bool {
		return len(j.Marked) == 1 && j.Marked[0].Ready && j.Working != nil && j.Working.Note == 2
	}, log)
	revs := revisions(c.automation(), task.ID)
	if m := task.Marked[0]; m.Note != 1 || m.Change != "Slowed the mask fade" || len(revs) != 2 || m.Render != revs[1].ID || revs[1].Label != "v3-draft2" {
		t.Fatalf("#1 done: %+v, revisions %+v", task.Marked, revs)
	}
	if d := open("d1.txt"); strings.TrimSpace(d) != "#1 done: it's in v3" {
		t.Fatalf("d1: %q", d)
	}
	if n, _ := st.Note(1); n.Status != project.StatusOpen {
		t.Fatalf("a mark finished #1: %s", n.Status)
	}
	// The revision plays through the media route (relayed when hosted).
	if code, b := c.do(http.MethodGet, "/p/"+c.project+"/video/"+revs[1].Label, ""); code != 200 || len(b) < 1000 {
		t.Fatalf("v3's revision: %d, %d bytes", code, len(b))
	}
	var info struct {
		Versions []struct{ Label, Name string } `json:"versions"`
	}
	_, b := c.do(http.MethodGet, "/p/"+c.project+"/api/info", "")
	if err := json.Unmarshal(b, &info); err != nil || info.Versions[len(info.Versions)-1].Label != "v3-draft2" ||
		info.Versions[len(info.Versions)-1].Name != "video.mp4" {
		t.Fatalf("the family: %s", b)
	}
	gate(2)
	task = c.waitTask("#2 in v3", func(j project.EditingTask) bool {
		return len(j.Marked) == 2 && j.Marked[1].Ready && j.Working == nil
	}, log)
	if len(revisions(c.automation(), task.ID)) != 3 {
		t.Fatalf("a revision per done: %+v", revisions(c.automation(), task.ID))
	}
	gate(3)
	done := c.waitTask("the version", func(j project.EditingTask) bool { return j.State == "completed" }, log)
	if done.Result != "v3" || done.Marked != nil || done.Working != nil {
		t.Fatalf("task %+v", done)
	}
	for id, change := range map[int64]string{1: "Slowed the mask fade", 2: "Moved the bell 6 frames earlier"} {
		if n, _ := st.Note(id); n.Status != project.StatusAnswered || n.AnsweredIn != "v3" || n.Change != change {
			t.Fatalf("#%d: %+v", id, n)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "clip_v3.mp4")); err != nil {
		t.Fatal("v3 isn't a version file:", err)
	}
}

// TestProgressFromInsideATaskLocally: retake open; the in-process worker's agent marks its notes as it goes.
func TestProgressFromInsideATaskLocally(t *testing.T) {
	dir, root, agentPath, gates := progressFixture(t)
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
			Env: map[string]string{"RETAKE_CLI": bin, "GATES": gates}}}
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
	progressStory(t, c, st, root, gates, w.stderr)
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

// TestProgressFromInsideATaskHosted: the same through retake connect: the marks and the revisions reach the server
// over the worker's connection, and the revisions play through the relay.
func TestProgressFromInsideATaskHosted(t *testing.T) {
	dir, root, agentPath, gates := progressFixture(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	data := filepath.Join(dir, "data")
	base, session, line, _ := hosted(t, w, data)
	profiles := filepath.Join(dir, "profiles.json")
	b, _ := json.Marshal([]map[string]any{{"id": "fake", "name": "Fake agent", "command": []string{agentPath},
		"env": map[string]string{"RETAKE_CLI": bin, "GATES": gates}}})
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
	progressStory(t, c, st, root, gates, w.stdout)
}

func TestProgressOutsideATask(t *testing.T) {
	w := newWorld(t)
	env := w.environment(nil)
	for _, args := range [][]string{{"working", "12", "x"}, {"done", "12", "Made it blue"}} {
		if code := Main(context.Background(), args, env); code != ExitUsage || !strings.Contains(w.stderr.String(), "not inside a Retake task") {
			t.Fatalf("%v: %d %s", args, code, w.stderr)
		}
	}
	for _, args := range [][]string{{"done", "12"}, {"done", "12", "  "}, {"working"}, {"working", "x"}, {"working", "0"}, {"working", "1", "a", "b"}} {
		w.stderr.Reset()
		if code := Main(context.Background(), args, env); code != ExitUsage {
			t.Fatalf("%v: %d %s", args, code, w.stderr)
		}
	}
}

// timelineProgressAgent makes the first shot blue in the timeline, then says done: the note shows Done once a render
// has it. It waits for the test's gates so the test sees v2 live before any edit.
const timelineProgressAgent = `#!/bin/sh
cat >/dev/null
r() { RETAKE_HELPER=1 "$RETAKE_CLI" -test.run='^TestRetakeHelper$' -- "$@"; }
gate() { n=0; until [ -e "$GATES/$1" ]; do sleep 0.1; n=$((n+1)); [ $n -gt 900 ] && exit 9; done; }
r working 1 "making the first shot blue" > w1.txt 2>&1 || { cat w1.txt >&2; exit 4; }
gate 1
sed "s/#aa3322/#2255dd/" edit/timeline.json > edit/timeline.json.tmp && mv edit/timeline.json.tmp edit/timeline.json
r done 1 "Made the first shot blue" > d1.txt 2>&1 || { cat d1.txt >&2; exit 5; }
gate 2
printf '{"state":"completed","message":"Edited the first shot."}' > "$RETAKE_REPORT"
`

// TestProgressInATimelineProject: v2 is live as soon as the task starts (a render of the unchanged source, every
// unit reused), and a done shows once a later render of the task has the edit.
func TestProgressInATimelineProject(t *testing.T) {
	editor, err := filepath.Abs("../../editor/target/release/retake-edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(editor); err != nil {
		t.Skip("build retake-edit to run the timeline progress test")
	}
	dir, _, _, gates := progressFixture(t)
	root := filepath.Join(dir, "demo")
	testmedia.Video(t, filepath.Join(root, "clip_v1.mp4"), 6)
	testmedia.Video(t, filepath.Join(root, "edit", "voice.mp4"), 6)
	timeline := `{"schema":"timeline/0.1","id":"progress-fixture","rev":1,"format":{"size":[320,180],"fps":12,"sample_rate":48000,"background":"#000000"},"assets":{"vo":{"path":"voice.mp4"}},"spine":[{"id":"a","asset":"vo","in":0,"out":2},{"id":"b","asset":"vo","in":2,"out":4},{"id":"c","asset":"vo","in":4,"out":6}],"scenes":[{"id":"scene","shots":[{"id":"first","at":"start:a","picture":{"background":"#aa3322","layers":[]}},{"id":"second","at":"start:b","picture":{"background":"#336633","layers":[]}},{"id":"third","at":"start:c","picture":{"background":"#333333","layers":[]}}]}]}`
	writeFile(t, filepath.Join(root, "edit", "timeline.json"), timeline)
	agentPath := filepath.Join(dir, "timeline-agent")
	if err = os.WriteFile(agentPath, []byte(timelineProgressAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser"}
	w.config(t, "idle_exit_minutes = 0\n[edit]\nretake_edit = \""+editor+"\"\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "fake", Name: "Fake agent"}, Command: []string{agentPath},
			Env: map[string]string{"RETAKE_CLI": bin, "GATES": gates}}}
	}
	url, stop, done := w.serve(t, env, "open", root, "--port", "0", "--no-open")
	c := client{t: t, base: url, project: "demo"}
	for deadline := time.Now().Add(60 * time.Second); len(c.automation().Workers) != 1 || c.automation().Config.Agent != "fake"; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no local worker\n%s", w.stderr)
		}
	}
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err = st.Sync(project.Run{Version: "v1", Comments: []project.Comment{{Key: "blue", Text: "Make the first shot blue", T0: 0, T1: 1}}}); err != nil {
		t.Fatal(err)
	}
	if code, b := c.do(http.MethodPost, "/p/demo/api/automation/tasks", `{"notes":[1]}`); code != 200 {
		t.Fatalf("send: %d %s", code, b)
	}
	// v2 is live before any edit: a complete render of the task, of the unchanged source.
	task := c.waitTask("v2 live", func(j project.EditingTask) bool {
		revs := revisions(c.automation(), j.ID)
		return j.Working != nil && len(revs) > 0 && revs[0].Complete
	}, w.stderr)
	start := revisions(c.automation(), task.ID)[0]
	if start.Label != "v2-draft1" || !start.Complete {
		t.Fatalf("v2's first revision: %+v", start)
	}
	writeFile(t, filepath.Join(gates, "1"), "")
	task = c.waitTask("#1 in v2", func(j project.EditingTask) bool { return len(j.Marked) == 1 && j.Marked[0].Ready }, w.stderr)
	var has project.LiveRender
	for _, v := range revisions(c.automation(), task.ID) {
		if v.ID == task.Marked[0].Render {
			has = v
		}
	}
	// Only the edited shot renders again: the others come from the cache.
	if !has.Complete || has.ID == start.ID || has.Rendered != 1 || has.Reused != 2 || task.Marked[0].Change != "Made the first shot blue" {
		t.Fatalf("the render with #1: %+v, start %+v, marks %+v", has, start, task.Marked)
	}
	if d, _ := os.ReadFile(filepath.Join(root, "d1.txt")); !strings.HasPrefix(string(d), "#1 done: it") {
		t.Fatalf("d1: %q", d)
	}
	writeFile(t, filepath.Join(gates, "2"), "")
	finished := c.waitTask("v2", func(j project.EditingTask) bool { return j.State == "completed" }, w.stderr)
	if finished.Result != "v2" {
		t.Fatalf("task %+v", finished)
	}
	if n, _ := st.Note(1); n.Status != project.StatusAnswered || n.AnsweredIn != "v2" || n.Change != "Made the first shot blue" {
		t.Fatalf("note %+v", n)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}
