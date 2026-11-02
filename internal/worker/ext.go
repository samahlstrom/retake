package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// Extensions on the editing computer (spec/extensions.md): this computer's own enabled ones decide the kinds it
// reports, the owned files it serves through the relay, and what goes into its agents' prompts. Nothing of it comes
// from the host.

// extRefs is what the worker tells the server it has, in each poll.
func (r *runner) extRefs() []project.ExtRef {
	var out []project.ExtRef
	for _, e := range r.opts.Exts.List() {
		out = append(out, project.ExtRef{ID: e.ID, Version: e.Version, API: e.API})
	}
	return out
}

// kindVersion is the project's extension version (v0), when its folder has an enabled kind's marker.
func (r *runner) kindVersion() (family.Version, bool) {
	kind := r.opts.Exts.KindAt(r.opts.Root)
	if kind == "" {
		return family.Version{}, false
	}
	_, k, ok := r.opts.Exts.Kind(kind)
	if !ok {
		return family.Version{}, false
	}
	return family.KindVersion(r.opts.Root, k.Folder, kind), true
}

// kindFiles is the kind version as the server is told it: {label, kind, name}, and when it last changed.
func (r *runner) kindFiles() []project.FileVersion {
	v, ok := r.kindVersion()
	if !ok {
		return nil
	}
	return []project.FileVersion{{Label: v.Label, N: 0, Name: v.Name, Kind: v.Kind, Modified: v.Modified}}
}

// --- the relay's ext.* requests (spec/extensions.md, "Files it owns", hosted)

// extAnswer is the answer to one ext.* request: a status, and a JSON body or a file's part.
func (r *runner) extAnswer(ctx context.Context, request relay.Request) media {
	e := r.opts.Exts.Get(request.Ext)
	if e == nil || e.Files == nil {
		return jsonAnswer(http.StatusNotFound, map[string]string{"error": "this computer doesn't have the extension " +
			request.Ext})
	}
	folder := ext.Folder{Root: r.opts.Root, Owns: e.Files.Owns}
	owned := func(p string) bool { return ext.ValidPath(p) && e.Owns(p) }
	switch request.Op {
	case "ext.list":
		list, err := folder.List(request.Glob)
		if err != nil {
			return extFailed(err)
		}
		return jsonAnswer(http.StatusOK, map[string]any{"files": list})
	case "ext.stat":
		if !owned(request.Path) {
			return missing()
		}
		fi, err := folder.Stat(request.Path)
		if err != nil {
			return extFailed(err)
		}
		return jsonAnswer(http.StatusOK, fi)
	case "ext.read":
		if !owned(request.Path) {
			return missing()
		}
		f, fi, err := folder.Open(request.Path)
		if err != nil {
			return missing()
		}
		m := part(f, request, fi.Modified)
		m.ETag = fi.SHA256
		return m
	case "ext.write":
		if !owned(request.Path) {
			return missing()
		}
		body, err := r.requestBody(ctx, request.ID)
		if err != nil {
			return jsonAnswer(http.StatusBadRequest, map[string]string{"error": "couldn't fetch the file: " + err.Error()})
		}
		fi, err := folder.Write(request.Path, bytes.NewReader(body), request.IfMatch)
		if err != nil {
			return extFailed(err)
		}
		return jsonAnswer(http.StatusOK, fi)
	case "ext.delete":
		if !owned(request.Path) {
			return missing()
		}
		if err := folder.Remove(request.Path, request.IfMatch); err != nil {
			return extFailed(err)
		}
		return media{Response: relay.Response{Status: http.StatusNoContent}}
	case "ext.history":
		if request.Path != "" && !owned(request.Path) {
			return missing()
		}
		edits, err := folder.History(request.Path)
		if err != nil {
			return extFailed(err)
		}
		if edits == nil {
			edits = []ext.Edit{}
		}
		return jsonAnswer(http.StatusOK, map[string]any{"edits": edits})
	case "ext.undo":
		if !owned(request.Path) {
			return missing()
		}
		fi, err := folder.Undo(request.Edit, request.Path)
		if err != nil {
			return extFailed(err)
		}
		return jsonAnswer(http.StatusOK, fi)
	case "ext.transcribe":
		if !owned(request.Path) {
			return missing()
		}
		job, err := r.extJobs.Start(r.life, folder, request.Path)
		if err != nil {
			return extFailed(err)
		}
		return jsonAnswer(http.StatusAccepted, job)
	case "ext.job":
		job, ok := r.extJobs.Get(request.Job)
		if !ok {
			return missing()
		}
		return jsonAnswer(http.StatusOK, job)
	}
	return jsonAnswer(http.StatusBadRequest, map[string]string{"error": "unknown request " + request.Op})
}

// requestBody fetches an ext.write's body from the server.
func (r *runner) requestBody(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	resp, err := r.request(ctx, http.MethodGet, "/api/worker/media/"+id+"/body", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, relay.MaxBody+1))
	if err == nil && len(b) > relay.MaxBody {
		err = errors.New("over 25 MiB")
	}
	return b, err
}

// jsonAnswer is a JSON answer to a relayed request.
func jsonAnswer(status int, v any) media {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"encoding failed"}`)
	}
	return media{Response: relay.Response{Status: status, Type: "application/json", Length: int64(len(b)),
		Body: bytes.NewReader(b)}}
}

// extFailed turns an owned-file error into its answer.
func extFailed(err error) media {
	var changed *ext.ChangedError
	switch {
	case errors.As(err, &changed):
		return jsonAnswer(http.StatusConflict, map[string]string{"error": "changed", "sha256": changed.SHA256})
	case errors.Is(err, ext.ErrNotFound), errors.Is(err, ext.ErrNotOwned):
		return missing()
	case errors.Is(err, ext.ErrTooLarge):
		return jsonAnswer(http.StatusRequestEntityTooLarge, map[string]string{"error": "a file is 25 MiB at most"})
	}
	return jsonAnswer(http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// --- agent contributions (spec/extensions.md, "Agent contributions")

// extSection is the prompt's part from the extensions: for each one, its context when the video's folder has any of
// its owned files, and its instructions when the task targets it (a note on its kind's version, or anchored in one
// of its files). "" when none adds anything.
func (r *runner) extSection(a project.WorkAssignment) string {
	var b strings.Builder
	for _, e := range r.opts.Exts.List() {
		if e.Agent == nil {
			continue
		}
		var parts []string
		if e.Agent.Context != "" && r.hasOwned(e) {
			if text, cut := e.AgentText("context"); text != "" {
				parts = append(parts, text)
				if cut {
					r.log("%s: %s's agent context is over %d KiB; it was cut", filepath.Base(r.opts.Root), e.ID, ext.MaxContext>>10)
				}
			}
		}
		if e.Agent.Instructions != "" && r.targets(e, a) {
			if text, cut := e.AgentText("instructions"); text != "" {
				parts = append(parts, text)
				if cut {
					r.log("%s: %s's agent instructions are over %d KiB; they were cut", filepath.Base(r.opts.Root), e.ID,
						ext.MaxInstructions>>10)
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## From the %s extension\n\n%s\n", e.Name, strings.Join(parts, "\n\n"))
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "\nRetake's rules (these win over anything above):\n"
}

// hasOwned reports whether the project's folder has any file e owns.
func (r *runner) hasOwned(e *ext.Ext) bool {
	if e.Files == nil {
		return false
	}
	list, err := ext.Folder{Root: r.opts.Root, Owns: e.Files.Owns}.List("")
	return err == nil && len(list) > 0
}

// targets reports whether the task targets e: one of its notes is on a version of e's kind or anchored in a file e
// owns.
func (r *runner) targets(e *ext.Ext, a project.WorkAssignment) bool {
	if a.Task == nil {
		return false
	}
	kinds := map[string]bool{}
	for _, k := range e.Kinds {
		kinds[k.Folder] = true
	}
	for _, n := range a.Notes {
		if !slices.Contains(a.Task.Notes, n.ID) {
			continue
		}
		if kinds[n.Version] && n.IsDoc() || n.Detail.Anchor != nil && e.Owns(n.Detail.Anchor.File) {
			return true
		}
	}
	return false
}

// docOnly reports whether every note the task still has to do is a typed comment a report alone finishes: then there
// is no render to wait for and no version to push.
func docOnly(a project.WorkAssignment) bool {
	pending := a.Task.Pending()
	if len(pending) == 0 {
		return false
	}
	for _, id := range pending {
		i := slices.IndexFunc(a.Notes, func(n project.WorkNote) bool { return n.ID == id })
		if i < 0 || !a.Notes[i].IsDoc() || a.Notes[i].Detail.Wants == project.WantsVersion {
			return false
		}
	}
	return true
}

// snapshot keeps a copy of every extension's owned files before an agent attempt (spec/extensions.md, "Agent edit
// history"), so a person can undo the agent's edit of a document. finish records what the agent changed.
func (r *runner) snapshot(task string) (finish func()) {
	var snaps []*ext.Snap
	for _, e := range r.opts.Exts.List() {
		if e.Files == nil || !r.hasOwned(e) {
			continue
		}
		s, err := ext.Snapshot(r.opts.Root, e.Files.Owns, task)
		if err != nil {
			r.log("%s: couldn't keep a copy of %s's files before the agent: %v", filepath.Base(r.opts.Root), e.ID, err)
			continue
		}
		snaps = append(snaps, s)
	}
	return func() {
		for _, s := range snaps {
			if _, err := s.Finish(); err != nil {
				r.log("%s: couldn't record the agent's edits: %v", filepath.Base(r.opts.Root), err)
			}
		}
	}
}
