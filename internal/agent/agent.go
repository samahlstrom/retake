// Package agent is the agent API of spec/notes.md: the operations behind both the CLI (retake notes, ask, note,
// push, versions) and the MCP server (retake mcp). Each takes the project found from a video path.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// Error codes.
const (
	CodeBadRequest = "bad_request" // a missing or malformed argument
	CodeNotFound   = "not_found"   // no such note, version or file
	CodeIllegal    = "illegal"     // a note state change that isn't allowed
	CodePushRule   = "push_rule"   // a push that leaves open notes unhandled
	CodeFailed     = "failed"      // anything else (I/O, the store)
)

// Error is an operation's failure, phrased for the agent: what was wrong and what to do instead.
type Error struct {
	Code    string  `json:"code"`
	Message string  `json:"error"`
	Notes   []int64 `json:"notes,omitempty"` // push_rule: the open notes the push left unhandled
}

func (e *Error) Error() string { return e.Message }

func errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrap turns a store error into an *Error.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	var pr *project.PushRuleError
	if errors.As(err, &pr) {
		return &Error{Code: CodePushRule, Message: err.Error(), Notes: pr.Notes}
	}
	var te *project.TransitionError
	switch {
	case errors.As(err, &te):
		return &Error{Code: CodeIllegal, Message: err.Error()}
	case errors.Is(err, project.ErrNotFound):
		return &Error{Code: CodeNotFound, Message: err.Error()}
	}
	return &Error{Code: CodeFailed, Message: err.Error()}
}

// Project is an open project: the store found from a video, and its version family.
type Project struct {
	Video  string // the video the project was opened from (absolute, symlinks resolved)
	Root   string
	Store  *project.Store
	Prober family.Prober
	// Editor finds and runs retake-edit, for pushes with render (spec/bridge.md).
	Editor bridge.Editor
}

// Open opens the project of a video (absolute, symlinks resolved), creating its store if needed.
func Open(video string, prober family.Prober) (*Project, error) {
	root := project.Root(video)
	st, err := project.Open(root)
	if err != nil {
		return nil, err
	}
	return &Project{Video: video, Root: root, Store: st, Prober: prober}, nil
}

// Close closes the store.
func (p *Project) Close() error { return p.Store.Close() }

// Family detects the version family again (it changes when versions are pushed).
func (p *Project) Family() []family.Version { return family.Detect(p.Video, nil, p.Prober) }

func find(fam []family.Version, label string) (family.Version, bool) {
	for _, v := range fam {
		if strings.EqualFold(v.Label, label) {
			return v, true
		}
	}
	return family.Version{}, false
}

// known reports whether label names a version: one in the family or one that was pushed.
func (p *Project) known(fam []family.Version, label string) (string, bool) {
	if v, ok := find(fam, label); ok {
		return v.Label, true
	}
	if row, err := p.Store.Version(label); err == nil {
		return row.Label, true
	}
	return "", false
}

// scope returns the notes on a version: recorded on it, plus those inherit.json carries over to it.
func scope(v family.Version) project.Scope {
	sc := project.Scope{Version: v.Label}
	for _, x := range store.InheritedTakes(v.Path) {
		take := filepath.Base(x.Dir)
		sc.Carried = append(sc.Carried, project.Carried{Take: take, Ranges: x.Ranges})
	}
	return sc
}

// ImageView is an image pinned to a note, for the agent: the file and where it sits on the frame.
type ImageView struct {
	Path   string     `json:"path"`
	Box    [4]float64 `json:"box"`
	VideoT float64    `json:"video_t"`
}

// NoteView is a note with everything the agent needs to act on it.
type NoteView struct {
	project.Note
	Strokes  []project.Stroke `json:"strokes"`
	Images   []ImageView      `json:"images"`
	Snapshot string           `json:"snapshot,omitempty"`
	Threads  []project.Thread `json:"threads"` // the questions about it and the reviewer's answers, oldest first
	// Targets are the timeline elements under it, best first (spec/bridge.md); empty without a render manifest.
	Targets []project.Target `json:"targets"`
	Word    *project.Word    `json:"word,omitempty"` // (audio) notes: the edit's spine word at its time
}

// views adds strokes, images and threads to notes.
func (p *Project) views(notes []project.Note, fam []family.Version) ([]NoteView, error) {
	out := make([]NoteView, 0, len(notes))
	if len(notes) == 0 {
		return out, nil
	}
	paths := map[string]string{}
	for _, v := range fam {
		paths[v.Label] = v.Path
	}
	takeDir := func(n project.Note) string {
		if path, ok := paths[n.Version]; ok && store.ValidTakeID(n.Take) {
			return store.TakeDir(path, n.Take)
		}
		return ""
	}
	images, err := p.Store.ImagesOf(notes, takeDir)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(notes))
	for i, n := range notes {
		ids[i] = n.ID
	}
	threads, err := p.Store.ThreadsOf(ids)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		v := NoteView{Note: n, Strokes: n.Detail.Strokes, Snapshot: n.Detail.Snapshot, Images: []ImageView{},
			Threads: []project.Thread{}, Targets: n.Detail.Targets}
		if v.Strokes == nil {
			v.Strokes = []project.Stroke{}
		}
		if v.Targets == nil {
			v.Targets = []project.Target{}
		}
		if n.Detail.Audio {
			v.Word = n.Detail.Word
		}
		for _, img := range images[n.ID] {
			v.Images = append(v.Images, ImageView{Path: filepath.Join(takeDir(n), img.File), Box: img.Box,
				VideoT: img.VideoT})
		}
		for _, th := range threads {
			if th.Note != nil && *th.Note == n.ID {
				v.Threads = append(v.Threads, th)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ListArgs are notes_list's arguments.
type ListArgs struct {
	Status  []string // only these states; none = all
	Version string   // only the notes on this version (recorded on it or carried over to it); "" = all
}

// ListResult is notes_list's result.
type ListResult struct {
	Cursor int64      `json:"cursor"` // pass it to notes_wait to hear about what changes next
	Notes  []NoteView `json:"notes"`
}

// NotesList returns the notes, with their text, times, author, strokes, images, snapshot and threads.
func (p *Project) NotesList(_ context.Context, a ListArgs) (ListResult, error) {
	var f project.NoteFilter
	for _, s := range a.Status {
		for part := range strings.SplitSeq(s, ",") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			st, err := project.ParseStatus(part)
			if err != nil {
				return ListResult{}, errorf(CodeBadRequest, "%v", err)
			}
			f.Statuses = append(f.Statuses, st)
		}
	}
	fam := p.Family()
	if a.Version != "" {
		v, ok := find(fam, a.Version)
		if !ok {
			if label, pushed := p.known(fam, a.Version); pushed {
				v = family.Version{Label: label}
			} else {
				return ListResult{}, errorf(CodeNotFound, "no version %s (the family has %s)", a.Version, labels(fam))
			}
		}
		sc := scope(v)
		f.Scope = &sc
	}
	cursor, err := p.Store.LastSeq()
	if err != nil {
		return ListResult{}, wrap(err)
	}
	notes, err := p.Store.Notes(f)
	if err != nil {
		return ListResult{}, wrap(err)
	}
	views, err := p.views(notes, fam)
	if err != nil {
		return ListResult{}, wrap(err)
	}
	return ListResult{Cursor: cursor, Notes: views}, nil
}

func labels(fam []family.Version) string {
	out := make([]string, len(fam))
	for i, v := range fam {
		out[i] = v.Label
	}
	return strings.Join(out, ", ")
}

// WaitArgs are notes_wait's arguments.
type WaitArgs struct {
	Since   int64         // the cursor of an earlier result; 0 = from the start
	Timeout time.Duration // give up after this long; 0 = wait as long as it takes
}

// WaitResult is notes_wait's result.
type WaitResult struct {
	Cursor   int64            `json:"cursor"`    // pass it as since next time
	Notes    []NoteView       `json:"notes"`     // notes that became open after since and still are
	Answers  []project.Thread `json:"answers"`   // the reviewer's answers that arrived after since
	TimedOut bool             `json:"timed_out"` // nothing arrived in time
}

// NotesWait blocks until notes became open (new, reinstated, reopened by the reviewer, or their question
// answered) or answers arrived after since, and returns them with the new cursor. Notes that were closed again
// in the meantime (retracted, say) don't count.
func (p *Project) NotesWait(ctx context.Context, a WaitArgs) (WaitResult, error) {
	if a.Since < 0 {
		return WaitResult{}, errorf(CodeBadRequest, "since must be 0 or a cursor from an earlier result")
	}
	wctx := ctx
	if a.Timeout > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(ctx, a.Timeout)
		defer cancel()
	}
	cursor := a.Since
	var opened, answers []int64
	for {
		evs, err := p.Store.WaitEvents(wctx, cursor, 0)
		if err != nil {
			if ctx.Err() != nil {
				return WaitResult{}, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return WaitResult{Cursor: cursor, Notes: []NoteView{}, Answers: []project.Thread{}, TimedOut: true}, nil
			}
			return WaitResult{}, wrap(err)
		}
		for _, e := range evs {
			cursor = e.Seq
			if e.Kind != project.EventNotes {
				continue
			}
			var ne project.NotesEvent
			if json.Unmarshal(e.Data, &ne) != nil {
				continue
			}
			opened = append(opened, ne.Opened...)
			answers = append(answers, ne.Answers...)
		}
		res, err := p.collect(opened, answers, cursor)
		if err != nil {
			return WaitResult{}, err
		}
		if len(res.Notes)+len(res.Answers) > 0 {
			return res, nil
		}
	}
}

func (p *Project) collect(opened, answers []int64, cursor int64) (WaitResult, error) {
	res := WaitResult{Cursor: cursor, Notes: []NoteView{}, Answers: []project.Thread{}}
	slices.Sort(opened)
	opened = slices.Compact(opened)
	if len(opened) > 0 {
		notes, err := p.Store.Notes(project.NoteFilter{IDs: opened, Statuses: []project.Status{project.StatusOpen}})
		if err != nil {
			return res, wrap(err)
		}
		if res.Notes, err = p.views(notes, p.Family()); err != nil {
			return res, wrap(err)
		}
	}
	if len(answers) > 0 {
		ths, err := p.Store.Threads(answers)
		if err != nil {
			return res, wrap(err)
		}
		res.Answers = append(res.Answers, ths...)
	}
	return res, nil
}

// AskArgs are note_ask's arguments.
type AskArgs struct {
	Note    int64    // the note to ask about; 0 = a question about a version as a whole
	Text    string   // the question
	Options []string // version labels the reviewer can pick from (alternatives you pushed)
	Version string   // with no note: the version it's about ("" = the newest)
}

// NoteAsk asks the reviewer a question about a note, which moves it from open to question until they answer.
func (p *Project) NoteAsk(_ context.Context, a AskArgs) (project.Thread, error) {
	if strings.TrimSpace(a.Text) == "" {
		return project.Thread{}, errorf(CodeBadRequest, "the question is empty")
	}
	fam := p.Family()
	opts := []string{}
	for _, o := range a.Options {
		label, ok := p.known(fam, o)
		if !ok {
			return project.Thread{}, errorf(CodeNotFound, "option %s is not a version (the family has %s); push it first",
				o, labels(fam))
		}
		opts = append(opts, label)
	}
	version := ""
	if a.Note == 0 {
		version = a.Version
		if version == "" && len(fam) > 0 {
			version = fam[len(fam)-1].Label
		} else if label, ok := p.known(fam, version); ok {
			version = label
		} else {
			return project.Thread{}, errorf(CodeNotFound, "no version %s", a.Version)
		}
	}
	th, err := p.Store.Ask(project.Ask{Note: a.Note, Version: version, Text: a.Text, Options: opts})
	return th, wrap(err)
}

// SetArgs are note_set's arguments.
type SetArgs struct {
	ID     int64
	Status string
	In     string // status answered: the version that answers it
	Change string // status answered: what that version changed for it, in one line
}

// NoteSet changes one note: the agent may answer an open note (with the version and the change), or correct the
// version and change of an answered one.
func (p *Project) NoteSet(_ context.Context, a SetArgs) (NoteView, error) {
	st, err := project.ParseStatus(a.Status)
	if err != nil {
		return NoteView{}, errorf(CodeBadRequest, "%v", err)
	}
	fam := p.Family()
	in := a.In
	if st == project.StatusAnswered {
		label, ok := p.known(fam, a.In)
		if !ok {
			return NoteView{}, errorf(CodeNotFound, "answering a note needs --in, the version that answers it (the "+
				"family has %s)", labels(fam))
		}
		in = label
	}
	n, err := p.Store.SetNote(a.ID, project.NoteUpdate{Status: st, In: in, Change: a.Change})
	if err != nil {
		return NoteView{}, wrap(err)
	}
	views, err := p.views([]project.Note{n}, fam)
	if err != nil {
		return NoteView{}, wrap(err)
	}
	return views[0], nil
}
