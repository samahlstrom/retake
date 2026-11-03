package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// Words changed while they wait (spec/automation.md): a person edits a note of the running batch that the agent hasn't
// marked. The agent gets the new words in the same task and attempt, before the version is made.

// contextWords reads a round's context file: each note's words.
func contextWords(t *testing.T, path string) map[int64]string {
	t.Helper()
	var c plainContext
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &c) != nil {
		t.Fatalf("context %s: %v", path, err)
	}
	out := map[int64]string{}
	for _, n := range c.Notes {
		out[n.ID] = n.Text
	}
	return out
}

// In the background the agent's run can't be told anything, so once it reports, the same agent runs a follow-up
// round in the same task with the new words (in its context and quoted in its prompt), and one version holds it all.
func TestEditedWordsReachABackgroundAgent(t *testing.T) {
	testmedia.Need(t)
	base := scratchDir(t, "edit-worker-test-")
	gate := filepath.Join(base, "gate")
	if err := os.MkdirAll(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	v := startBatchVideo(t, base, "shop", 3, batchProfile(t, gate, 1))
	launches := filepath.Join(gate, "launches")
	v.send(t, 1, 2, 3)
	v.wait(t, "the agent to start", func(project.AutomationState) bool { return len(lines(t, launches)) == 1 })
	a, _ := v.st.Automation()
	task := a.Tasks[0].ID
	dir := filepath.Join(v.local, ".retake", "worker", "tasks", task)
	if got := contextWords(t, filepath.Join(dir, "context-1.json")); got[2] != "Fix number 2" {
		t.Fatalf("the first context: %v", got)
	}
	if _, _, err := v.st.EditNote(2, project.NoteEdit{Text: "Fix number 2, but only the left half", By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a = v.wait(t, "the version", func(a project.AutomationState) bool { return len(a.Tasks) == 1 && a.Tasks[0].State == "completed" })
	if j := a.Tasks[0]; j.ID != task || j.Attempt != 1 || j.Result != "v2" ||
		!slices.ContainsFunc(j.Events, func(e project.TaskEvent) bool { return e.Text == "Edited #2" }) {
		t.Fatalf("the task: %+v", j)
	}
	if got := v.answeredIn(t, 1, 2, 3); got != "v2" {
		t.Fatalf("version: %s", got)
	}
	if got := v.versions(t); !slices.Equal(got, []string{"shop_v1.mp4", "shop_v2.mp4"}) {
		t.Fatalf("versions: %q", got)
	}
	// One agent, run again in the same task for the new words.
	if got := lines(t, launches); !slices.Equal(got, []string{"shop [1 2 3]", "shop [1 2 3]"}) {
		t.Fatalf("launches: %q", got)
	}
	if got := contextWords(t, filepath.Join(dir, "context-1-2.json")); got[2] != "Fix number 2, but only the left half" || got[1] != "Fix number 1" {
		t.Fatalf("the follow-up context: %v", got)
	}
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt-1-2.md"))
	if err != nil || !strings.Contains(string(prompt), "follow-up round of Retake task "+task) ||
		!strings.Contains(string(prompt), `changed the words of note 2 while it waited. It now says: "Fix number 2, but only the left half"`) ||
		!strings.Contains(string(prompt), "report-1-2.json") {
		t.Fatalf("follow-up prompt: %s %v", prompt, err)
	}
	if !strings.Contains(v.log.String(), "the agent got the new words of #2") {
		t.Fatalf("worker log:\n%s", v.log)
	}
	// The round's report says what changed for the edited note.
	if n, _ := v.st.Note(2); n.Change != "Round 2" || n.Text != "Fix number 2, but only the left half" {
		t.Fatalf("note 2: %+v", n)
	}
}

// In herdr or tmux the session gets one more message for the same task, with the new words in its prompt, and the
// agent's one report finishes the batch.
func TestEditedWordsReachTheSession(t *testing.T) {
	testmedia.Need(t)
	dir := fakeViews(t)
	gate := filepath.Join(dir, "barrier")
	if err := os.Mkdir(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENT_MODE", "batch")
	t.Setenv("FAKE_BARRIER", gate)
	agent, err := filepath.Abs("testdata/agent")
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"}, Command: []string{agent},
		Interactive: []string{agent}, OpenIn: OpenTmux}
	v := startBatchVideo(t, scratchDir(t, "edit-session-test-"), "shop", 3, p)
	v.send(t, 1, 2, 3)
	v.wait(t, "the agent to start", func(project.AutomationState) bool { return len(lines(t, filepath.Join(gate, "launches"))) == 1 })
	if _, _, err := v.st.EditNote(3, project.NoteEdit{Text: "Fix number 3 in red"}); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(dir, "seen")
	v.wait(t, "the new words to reach the session", func(project.AutomationState) bool {
		return slices.ContainsFunc(lines(t, seen), func(l string) bool { return strings.HasPrefix(l, "Changed comments for Retake task") })
	})
	a, _ := v.st.Automation()
	task := a.Tasks[0]
	more := filepath.Join(v.local, ".retake", "worker", "tasks", task.ID, "prompt-1-2.md")
	want := fmt.Sprintf("Changed comments for Retake task %s (attempt 1): follow the instructions in %s", task.ID, more)
	if got := lines(t, seen); !slices.Contains(got, want) {
		t.Fatalf("seen %q, want %q", got, want)
	}
	prompt, err := os.ReadFile(more)
	if err != nil || !strings.Contains(string(prompt), `note 3 while it waited. It now says: "Fix number 3 in red"`) ||
		strings.Contains(string(prompt), "More comments were sent") {
		t.Fatalf("prompt: %s %v", prompt, err)
	}
	if got := contextWords(t, filepath.Join(filepath.Dir(more), "context-1-2.json")); got[3] != "Fix number 3 in red" {
		t.Fatalf("context: %v", got)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a = v.wait(t, "the batch", func(a project.AutomationState) bool { return len(a.Tasks) == 1 && a.Tasks[0].State == "completed" })
	if a.Tasks[0].Result != "v2" || len(a.Tasks[0].Notes) != 3 {
		t.Fatalf("the task: %+v", a.Tasks[0])
	}
	if got := lines(t, filepath.Join(gate, "launches")); len(got) != 1 {
		t.Fatalf("launches: %q", got)
	}
	// Told once: no second message for the same words.
	n := 0
	for _, l := range lines(t, seen) {
		if strings.HasPrefix(l, "Changed comments") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d messages for one edit", n)
	}
}
