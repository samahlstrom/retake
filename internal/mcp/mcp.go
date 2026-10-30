// Package mcp serves the agent API (internal/agent) over the Model Context Protocol, using the official Go SDK:
// `retake mcp <video>` speaks it on stdin and stdout.
package mcp

import (
	"context"
	"io"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/project"
)

// DefaultWait is how long notes_wait blocks when the call gives no timeout.
const DefaultWait = 120 * time.Second

const instructions = `Retake is a video review loop. A person watches a version of a video, presses R and talks and ` +
	`draws over it; their words become numbered notes (#1, #2, ...) with video times. You turn the notes into the ` +
	`next version and ask when you're unsure.

The loop: notes_wait (or notes_list) to get the open notes; make the edits and render a new file; version_push it ` +
	`with a one-line change for every open note, or a question (asks) for the ones you're unsure of; then notes_wait ` +
	`again with the cursor you got, for the reviewer's next notes and answers. A push that leaves an open note ` +
	`unhandled is rejected. Approving a version is the reviewer's action, never yours.`

const (
	waitDesc = `Wait for review work. Blocks until something needs you: a note became open (newly recorded, ` +
		`reopened by the reviewer, reinstated, or its question answered) or the reviewer answered one of your ` +
		`questions. Returns those notes (only the ones still open, each with id, version, t0/t1 in seconds, text, ` +
		`author, strokes, images, snapshot and threads), the answers (threads of kind "answer", with answer_to = your ` +
		`question's id), and a cursor. Pass the cursor as since next time to get only what is new. With since 0 every ` +
		`open note comes back at once. If nothing arrives within timeout seconds (default 120) it returns ` +
		`timed_out: true with the same cursor: just call it again.`
	listDesc = `List the review notes. Each note has its id (#N), the version label it was recorded on, its span ` +
		`t0-t1 in video seconds, the reviewer's words (text), author, and status: open (to do), question (you asked; ` +
		`waiting for the reviewer), answered (answered_in names the version that addressed it, change says how), ` +
		`accepted (the reviewer approved that version) or retracted (the reviewer took it back; ignore it). It also has ` +
		`the strokes drawn on the frame (shape, color, box [x, y, w, h] normalized 0-1), images the reviewer dropped ` +
		`(file path, box), a snapshot image path, and threads (your questions and their answers, oldest first). Filter ` +
		`by status and by version (notes on it, including ones carried over from older takes). Returns a cursor for ` +
		`notes_wait.`
	askDesc = `Ask the reviewer a question about one note when you can't tell what they want. The note goes from ` +
		`open to question, which satisfies the push rule for it. The reviewer answers by voice in the review page; ` +
		`notes_wait then returns the answer and the note, open again. When the answer is a choice between versions ` +
		`you pushed (say two alternatives), list their labels in options. note 0 asks about a version as a whole ` +
		`(version, default the newest). Returns the question thread; its id is what answers point to.`
	setDesc = `Mark one open note answered by a version that is already in the family: status "answered", in = that ` +
		`version's label, change = a one-line summary of what changed for this note. Also corrects in/change of an ` +
		`answered note. When you push a new version, list the notes in version_push's changes instead. Questions go ` +
		`through note_ask; approving, reopening and retracting are the reviewer's, and are refused. Returns the note.`
	pushDesc = `Push a new version of the video for review. Copies the rendered file (or moves it, with move) into ` +
		`the family as the next v<N>, or as label, in the same folder layout as its parent (a sibling v<N>/ folder ` +
		`when the family uses version folders), and records its sha256, parent (default: the newest version) and ` +
		`changelog. PUSH RULE: every note that is open on the parent version must be handled in this same push, ` +
		`either in changes (note id + a one-line summary of what you changed) or in asks (note id + a question for ` +
		`the reviewer). Otherwise the push is rejected, nothing is copied, and the error lists the ids of the ` +
		`unhandled notes. Only open notes can be listed, each once. Changed notes become answered in the new version; ` +
		`asked ones become question. Returns the version record, the answered note ids and the question ids.`
	versionsDesc = `List the versions of the video, oldest first: label, file path, duration in seconds, which is ` +
		`the newest, and for pushed ones the parent, sha256, who pushed, when, and the changelog ([{note, summary}]). ` +
		`open counts the notes still open on each version: a push from that version must handle them.`
)

type waitIn struct {
	Since   int64   `json:"since,omitempty" jsonschema:"the cursor of the previous notes_wait or notes_list result; 0 or absent: from the start"`
	Timeout float64 `json:"timeout,omitempty" jsonschema:"seconds to wait before returning timed_out: true (default 120)"`
}

type listIn struct {
	Status  []string `json:"status,omitempty" jsonschema:"only notes in these states: open, question, answered, accepted, retracted"`
	Version string   `json:"version,omitempty" jsonschema:"only the notes on this version label, e.g. v7"`
}

type askIn struct {
	Note     int64    `json:"note" jsonschema:"the note id; 0 for a question about a version as a whole"`
	Question string   `json:"question" jsonschema:"the question, short and concrete, as you would ask it out loud"`
	Options  []string `json:"options,omitempty" jsonschema:"version labels the reviewer can pick from, e.g. two alternatives you pushed"`
	Version  string   `json:"version,omitempty" jsonschema:"with note 0: the version the question is about (default: the newest)"`
}

type setIn struct {
	ID     int64  `json:"id" jsonschema:"the note id"`
	Status string `json:"status" jsonschema:"answered"`
	In     string `json:"in,omitempty" jsonschema:"the label of the version that answers it, e.g. v8"`
	Change string `json:"change,omitempty" jsonschema:"one line: what that version changed for this note"`
}

type changeIn struct {
	Note    int64  `json:"note" jsonschema:"the note id"`
	Summary string `json:"summary" jsonschema:"one line: what this version changed for the note"`
}

type askEntry struct {
	Note     int64  `json:"note" jsonschema:"the note id"`
	Question string `json:"question" jsonschema:"what you need the reviewer to decide"`
}

type pushIn struct {
	File    string     `json:"file" jsonschema:"path of the rendered video file to push"`
	Parent  string     `json:"parent,omitempty" jsonschema:"the version label it was made from (default: the newest version)"`
	Label   string     `json:"label,omitempty" jsonschema:"v<N> to use instead of the next free number"`
	Changes []changeIn `json:"changes,omitempty" jsonschema:"the notes this version addresses"`
	Asks    []askEntry `json:"asks,omitempty" jsonschema:"questions about the notes you could not resolve"`
	Move    bool       `json:"move,omitempty" jsonschema:"move the file instead of copying it"`
}

type versionsIn struct{}

// NewServer returns an MCP server with the agent tools of p.
func NewServer(p *agent.Project, version string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "retake", Version: version},
		&sdk.ServerOptions{Instructions: instructions})
	sdk.AddTool(s, &sdk.Tool{Name: "notes_wait", Description: waitDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in waitIn) (*sdk.CallToolResult, agent.WaitResult, error) {
			timeout := DefaultWait
			if in.Timeout > 0 {
				timeout = time.Duration(in.Timeout * float64(time.Second))
			}
			res, err := p.NotesWait(ctx, agent.WaitArgs{Since: in.Since, Timeout: timeout})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "notes_list", Description: listDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in listIn) (*sdk.CallToolResult, agent.ListResult, error) {
			res, err := p.NotesList(ctx, agent.ListArgs{Status: in.Status, Version: in.Version})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "note_ask", Description: askDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in askIn) (*sdk.CallToolResult, project.Thread, error) {
			th, err := p.NoteAsk(ctx, agent.AskArgs{Note: in.Note, Text: in.Question, Options: in.Options,
				Version: in.Version})
			return nil, th, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "note_set", Description: setDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in setIn) (*sdk.CallToolResult, agent.NoteView, error) {
			n, err := p.NoteSet(ctx, agent.SetArgs{ID: in.ID, Status: in.Status, In: in.In, Change: in.Change})
			return nil, n, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "version_push", Description: pushDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in pushIn) (*sdk.CallToolResult, agent.PushResult, error) {
			a := agent.PushArgs{File: in.File, Parent: in.Parent, Label: in.Label, Move: in.Move}
			for _, c := range in.Changes {
				a.Changes = append(a.Changes, project.ChangeEntry{Note: c.Note, Summary: c.Summary})
			}
			for _, q := range in.Asks {
				a.Asks = append(a.Asks, project.PushAsk{Note: q.Note, Text: q.Question})
			}
			res, err := p.VersionPush(ctx, a)
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "versions_list", Description: versionsDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ versionsIn) (*sdk.CallToolResult, agent.VersionsResult, error) {
			res, err := p.VersionsList(ctx)
			return nil, res, err
		})
	AddAssetTools(s, AssetsFor(p.Root))
	return s
}

// Serve serves the tools of p on in and out (newline-delimited JSON-RPC, as on stdio) until the client hangs up
// or ctx is done.
func Serve(ctx context.Context, p *agent.Project, version string, in io.Reader, out io.Writer) error {
	return NewServer(p, version).Run(ctx, &sdk.IOTransport{Reader: io.NopCloser(in), Writer: nopCloser{out}})
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
