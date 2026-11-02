package worker

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// streamOf reads a captured stream (internal/worker/testdata: claude-ok and codex-ok are the real CLIs' output for
// "reply OK", the -resume ones the next turn in the same session; the -tools ones are built in the same shapes).
func streamOf(t *testing.T, name string) (*chatStream, []project.ChatEvent) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c := newChatStream("/work/video", "v0/jump_v0.md", "script")
	var all []project.ChatEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		c.line(append(sc.Bytes(), '\n'))
		all = append(all, c.take()...)
	}
	return c, all
}

// joined is the events as the server would keep them: words run together, one line per tool.
func joined(evs []project.ChatEvent) []string {
	var out []string
	for _, e := range evs {
		if n := len(out); n > 0 && e.Type == "text" && strings.HasPrefix(out[n-1], "text:") {
			out[n-1] += e.Text
			continue
		}
		out = append(out, e.Type+":"+e.Text)
	}
	return out
}

func TestChatStreamOfTheRealCLIs(t *testing.T) {
	for name, want := range map[string]string{"claude-ok.jsonl": "OK", "claude-resume.jsonl": "OK",
		"codex-ok.jsonl": "OK", "codex-resume.jsonl": "OK again"} {
		c, evs := streamOf(t, name)
		if got := joined(evs); !slices.Equal(got, []string{"text:" + want}) {
			t.Errorf("%s: %q", name, got)
		}
		if strings.HasPrefix(name, "claude") && c.session != "11111111-2222-4333-8444-555555555555" ||
			strings.HasPrefix(name, "codex") && c.session != "01a19b3e-9201-7752-a906-500bea402929" {
			t.Errorf("%s: session %q", name, c.session)
		}
	}
}

func TestChatStreamTools(t *testing.T) {
	_, evs := streamOf(t, "claude-tools.jsonl")
	want := []string{
		"text:I'll pull the link in.",
		"tool:Reading youtube.com/…",
		"tool:Reading the script",
		"tool:Editing the script",
		"tool:Editing the script", // the server keeps one of two in a row
		"tool:Downloading from images.example.org",
		"text:Added the video under Cold open.",
	}
	if got := joined(evs); !slices.Equal(got, want) {
		t.Errorf("claude:\n got %q\nwant %q", got, want)
	}
	_, evs = streamOf(t, "codex-tools.jsonl")
	want = []string{
		"text:Looking it up.",
		`tool:Searching for "neon sign photo cc0"`,
		"tool:Downloading from cdn.example.net",
		"tool:Editing the script",
		"text:Put the neon sign under Cold open.\n\nAnything else?",
	}
	if got := joined(evs); !slices.Equal(got, want) {
		t.Errorf("codex:\n got %q\nwant %q", got, want)
	}
	// A plain agent's lines are its words.
	c := newChatStream("/v", "", "script")
	c.line([]byte("Hello there\n"))
	c.line([]byte("{not json\n"))
	if got := joined(c.take()); !slices.Equal(got, []string{"text:Hello there\n{not json\n"}) {
		t.Errorf("plain %q", got)
	}
}

func TestChatToolLines(t *testing.T) {
	c := newChatStream("/work/video", "v0/jump_v0.md", "script")
	for in, want := range map[[2]string]string{
		{"v0/jump_v0.md", "w"}:               "Editing the script",
		{"./v0/jump_v0.md", "r"}:             "Reading the script",
		{"/work/video/v0/images/a.png", "w"}: "Saving an image",
		{"v0/images/a.png", "r"}:             "Looking at an image",
		{"/elsewhere/notes.txt", "w"}:        "Editing notes.txt",
	} {
		if got := c.fileLine(in[0], in[1] == "w"); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ls -la":                                 "Running a command",
		"yt-dlp -o v0/x.mp4 https://youtu.be/ab": "Downloading from youtu.be",
		"curl -s localhost":                      "Downloading a file",
		`retake ask --option A "Which?"`:         "",
		`'/opt/retake' ask "Which?"`:             "",
	} {
		if got := commandLine(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"https://www.x.com/a/status/1": "x.com/…", "https://example.org": "example.org",
		"nonsense": "a page"} {
		if got := shortURL(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if got := searchLine(strings.Repeat("word ", 30)); !strings.HasSuffix(got, `…"`) || len([]rune(got)) > 80 {
		t.Errorf("long search %q", got)
	}
}

func TestChatArgv(t *testing.T) {
	claude := []string{"/bin/claude", "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if got := chatArgv(claude, ""); !slices.Equal(got, append(slices.Clone(claude), "--include-partial-messages")) {
		t.Errorf("claude: %q", got)
	}
	if got := chatArgv(claude, "s1"); !slices.Equal(got[len(got)-3:], []string{"--include-partial-messages", "--resume", "s1"}) {
		t.Errorf("claude resume: %q", got)
	}
	codex := []string{"/bin/codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-"}
	if got := chatArgv(codex, ""); !slices.Equal(got, codex) {
		t.Errorf("codex: %q", got)
	}
	want := []string{"/bin/codex", "exec", "resume", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "t1", "-"}
	if got := chatArgv(codex, "t1"); !slices.Equal(got, want) {
		t.Errorf("codex resume: %q", got)
	}
	other := []string{"my-agent", "--flag"}
	if got := chatArgv(other, "x"); !slices.Equal(got, other) || agentStyle(other) != "" {
		t.Errorf("other: %q", got)
	}
	if agentStyle([]string{"claude", "--dangerously-skip-permissions"}) != "" {
		t.Error("an interactive claude has no headless session")
	}
}

func TestChatPrompt(t *testing.T) {
	r := &runner{opts: Options{Root: t.TempDir()}}
	cw := project.ChatWork{Messages: []project.ChatMessage{{Text: "Pull this in: https://x.com/a", Images: []string{"v0/images/a.png"}}},
		History: []project.ChatMessage{{Role: project.ChatPerson, Text: "Hi"}, {Role: project.ChatAgent, Text: "Hello"},
			{Role: project.ChatQuestion, Text: "Which?", Answer: "Neon"}}}
	p := r.chatPrompt(cw, nil, "v0/jump_v0.md", false)
	for _, want := range []string{"Retake's chat", "How this chat works (these win", "v0/jump_v0.md", "alone on its own line",
		"ask --option", "Don't render, push", "The person: Hi", "You: Hello", "(they picked: Neon)",
		"Pull this in: https://x.com/a\n(Attached: v0/images/a.png)"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if p = r.chatPrompt(cw, nil, "v0/jump_v0.md", true); strings.Contains(p, "The chat so far") || !strings.Contains(p, "Pull this in") {
		t.Errorf("resumed: %s", p)
	}
}
