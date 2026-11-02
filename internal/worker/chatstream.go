package worker

import (
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/project"
)

// A chat turn's stream (spec/chat.md, "What the page sees of a turn"): the agent's JSON lines become its words and
// one quiet line per tool it uses. Nothing else of the stream reaches the page.

// chatStream reads one agent run's output, line by line.
type chatStream struct {
	root, doc, word string // the video's folder, the document (relative, slash) and what to call it ("script")

	session  string // the agent's own session id, once the stream said it
	partial  bool   // Claude Code sends its text as deltas: its whole messages' text is then a repeat
	lastText bool   // the last event was words, so the next words of a new block start a paragraph
	said     bool   // anything reached the page
	seen     map[string]bool
	out      []project.ChatEvent
}

func newChatStream(root, doc, word string) *chatStream {
	return &chatStream{root: root, doc: doc, word: word, seen: map[string]bool{}}
}

// take returns the events since the last take.
func (c *chatStream) take() []project.ChatEvent {
	out := c.out
	c.out = nil
	return out
}

func (c *chatStream) text(s string) {
	if s == "" {
		return
	}
	c.out = append(c.out, project.ChatEvent{Type: "text", Text: s})
	c.lastText, c.said = true, true
}

// block starts a new block of words: a paragraph after the words before it.
func (c *chatStream) block() {
	if c.lastText {
		c.text("\n\n")
	}
}

func (c *chatStream) tool(line string) {
	if line == "" {
		return
	}
	c.out = append(c.out, project.ChatEvent{Type: "tool", Text: line})
	c.lastText, c.said = false, true
}

// line reads one line of the agent's output: a JSON event of Claude Code or Codex, or plain words.
func (c *chatStream) line(b []byte) {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return
	}
	var ev map[string]json.RawMessage
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &ev) != nil {
		c.text(strings.TrimRight(string(b), "\r\n") + "\n") // a plain agent prints its answer
		return
	}
	var kind string
	_ = json.Unmarshal(ev["type"], &kind)
	if sid := str(ev["session_id"]); sid != "" && c.session == "" {
		c.session = sid
	}
	// A sub-agent's own messages and tools aren't the agent's words.
	if p := ev["parent_tool_use_id"]; len(p) > 0 && string(p) != "null" {
		return
	}
	switch kind {
	case "stream_event":
		c.claudeEvent(ev["event"])
	case "assistant":
		c.claudeMessage(ev["message"])
	case "thread.started":
		c.session = str(ev["thread_id"])
	case "item.started", "item.completed":
		c.codexItem(kind, ev["item"])
	}
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// claudeEvent is one of Claude Code's partial-message events: the text as it's written.
func (c *chatStream) claudeEvent(raw json.RawMessage) {
	var e struct {
		Type         string `json:"type"`
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	switch {
	case e.Type == "content_block_start" && e.ContentBlock.Type == "text":
		c.partial = true
		c.block()
	case e.Type == "content_block_delta" && e.Delta.Type == "text_delta":
		c.partial = true
		c.text(e.Delta.Text)
	}
}

// claudeMessage is one of Claude Code's whole messages: its tool uses, and its text when it sent no deltas.
func (c *chatStream) claudeMessage(raw json.RawMessage) {
	var m struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			if !c.partial {
				c.block()
				c.text(b.Text)
			}
		case "tool_use":
			if b.ID != "" && c.seen[b.ID] {
				continue
			}
			c.seen[b.ID] = true
			c.tool(c.claudeTool(b.Name, b.Input))
		}
	}
}

// claudeTool is the quiet line for one of Claude Code's tools ("" for none).
func (c *chatStream) claudeTool(name string, raw json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		URL          string `json:"url"`
		Query        string `json:"query"`
		Command      string `json:"command"`
	}
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "Edit", "MultiEdit", "Write":
		return c.fileLine(in.FilePath, true)
	case "NotebookEdit":
		return c.fileLine(in.NotebookPath, true)
	case "Read":
		return c.fileLine(in.FilePath, false)
	case "WebFetch":
		return "Reading " + shortURL(in.URL)
	case "WebSearch":
		return searchLine(in.Query)
	case "Bash":
		return commandLine(in.Command)
	case "Glob", "Grep", "LS":
		return "Looking through the files"
	case "TodoWrite", "Task", "Agent", "ExitPlanMode", "ToolSearch", "Skill", "AskUserQuestion":
		return ""
	}
	return "Working"
}

// codexItem is one of Codex's items: its words when an agent message completes, a tool line when a tool starts.
func (c *chatStream) codexItem(kind string, raw json.RawMessage) {
	var it struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
		Query   string `json:"query"`
		Tool    string `json:"tool"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if json.Unmarshal(raw, &it) != nil {
		return
	}
	key := it.ID + "/" + it.Type
	once := func() bool {
		if it.ID == "" {
			return true
		}
		if c.seen[key] {
			return false
		}
		c.seen[key] = true
		return true
	}
	switch it.Type {
	case "agent_message":
		if kind == "item.completed" {
			c.block()
			c.text(it.Text)
		}
	case "command_execution":
		if once() {
			c.tool(commandLine(it.Command))
		}
	case "web_search":
		if kind == "item.completed" && once() { // the query comes with the completed item
			c.tool(searchLine(it.Query))
		}
	case "mcp_tool_call":
		if once() {
			c.tool("Working")
		}
	case "file_change":
		if kind == "item.completed" && once() {
			for _, ch := range it.Changes {
				c.tool(c.fileLine(ch.Path, true))
			}
		}
	}
}

// fileLine is the line for reading or writing a file: the document by its name ("the script"), an image, or the
// file's own name.
func (c *chatStream) fileLine(p string, write bool) string {
	if p == "" {
		return ""
	}
	rel := filepath.ToSlash(p)
	if filepath.IsAbs(p) {
		if r, err := filepath.Rel(c.root, p); err == nil && filepath.IsLocal(r) {
			rel = filepath.ToSlash(r)
		}
	}
	rel = strings.TrimPrefix(rel, "./")
	switch {
	case rel == c.doc && c.doc != "":
		if write {
			return "Editing the " + c.word
		}
		return "Reading the " + c.word
	case strings.HasPrefix(rel, "v0/images/") && write:
		return "Saving an image"
	case strings.HasPrefix(rel, "v0/images/"):
		return "Looking at an image"
	case write:
		return "Editing " + path.Base(rel)
	}
	return "Reading " + path.Base(rel)
}

var (
	downloadRE = regexp.MustCompile(`\b(curl|wget|yt-dlp|gallery-dl)\b`)
	urlRE      = regexp.MustCompile(`https?://[^\s'"<>|;)]+`)
	askRE      = regexp.MustCompile(`\bretake'?\s+ask\b`)
)

// commandLine is the line for a shell command: a download says where from, retake ask says nothing (the question
// shows itself), anything else is "Running a command".
func commandLine(cmd string) string {
	switch {
	case askRE.MatchString(cmd):
		return ""
	case downloadRE.MatchString(cmd):
		if u := urlRE.FindString(cmd); u != "" {
			if p, err := url.Parse(u); err == nil && p.Host != "" {
				return "Downloading from " + strings.TrimPrefix(p.Hostname(), "www.")
			}
		}
		return "Downloading a file"
	}
	return "Running a command"
}

func searchLine(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if q == "" {
		return "Searching the web"
	}
	if utf8.RuneCountInString(q) > 60 {
		q = string([]rune(q)[:59]) + "…"
	}
	return `Searching for "` + q + `"`
}

// shortURL is a link as a quiet line shows it: its host, and "/…" when it has a path.
func shortURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "a page"
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return host + "/…"
	}
	return host
}
