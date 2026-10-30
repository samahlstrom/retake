package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// pushRendered is version_push with render (spec/bridge.md, "Push with render"): it renders the project's timeline
// at its current revision, and pushes the result as the next version with its render manifest beside it, a
// changelog made from the patches since the parent's revision (each patch's answers and summary), and a push record.
// The push rule is checked before anything renders; a failed render leaves nothing in the family.
func (p *Project) pushRendered(ctx context.Context, a PushArgs) (PushResult, error) {
	timeline, err := bridge.FindTimeline(p.Root)
	if errors.Is(err, bridge.ErrNoTimeline) {
		return PushResult{}, errorf(CodeNotFound, "%v; render needs a timeline project (spec/bridge.md)", err)
	}
	if err != nil {
		return PushResult{}, errorf(CodeFailed, "%v", err)
	}
	bin, err := p.Editor.Find()
	if err != nil {
		return PushResult{}, errorf(CodeNotFound, "%v", err)
	}
	rev, err := bridge.TimelineRev(timeline)
	if err != nil {
		return PushResult{}, errorf(CodeFailed, "%v", err)
	}
	log, err := bridge.ReadPatchLog(timeline)
	if err != nil {
		return PushResult{}, errorf(CodeFailed, "%v", err)
	}
	tg, err := p.pushTarget(a, ".mp4")
	if err != nil {
		return PushResult{}, err
	}
	base := int64(-1)
	if m, err := bridge.ForVideo(tg.parent.Path); err == nil {
		base = m.Timeline.Rev
	} else if !errors.Is(err, bridge.ErrNoManifest) {
		return PushResult{}, errorf(CodeFailed, "the parent's render manifest: %v", err)
	}
	auto, rec := bridge.Changelog(log, base, rev, func(id int64) (bool, string) {
		n, err := p.Store.Note(id)
		if err != nil {
			return false, fmt.Sprintf("there is no note #%d", id)
		}
		if n.Status != project.StatusOpen {
			return false, fmt.Sprintf("note #%d is %s, not open", id, n.Status)
		}
		return true, ""
	})
	push := tg.push
	push.Changelog = mergeChangelog(auto, a.Changes, a.Asks)
	if err := p.Store.CheckPush(push); err != nil {
		return PushResult{}, wrap(err)
	}

	// Render into the project's store folder (beside the family, so the move in is a rename), then move it in.
	tmpDir := filepath.Join(p.Root, project.Dir, "renders")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil { //nolint:gosec // a project folder others may read
		return PushResult{}, errorf(CodeFailed, "%v", err)
	}
	out := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.mp4", push.Label, time.Now().UnixNano()))
	cleanup := func() {
		_ = os.Remove(out)
		_ = os.Remove(bridge.ManifestPath(out))
	}
	rep, err := p.Editor.Render(ctx, bin, timeline, out, a.Draft)
	if err != nil {
		cleanup()
		return PushResult{}, errorf(CodeFailed, "rendering %s: %v", timeline, err)
	}
	manifest, err := renamedManifest(bridge.ManifestPath(out), filepath.Base(push.Path))
	if err != nil {
		cleanup()
		return PushResult{}, errorf(CodeFailed, "the render's manifest: %v", err)
	}
	placed, err := place(out, push.Path, true)
	if err != nil {
		cleanup()
		return PushResult{}, errorf(CodeFailed, "moving the render to %s: %v", push.Path, err)
	}
	mpath, rpath := bridge.ManifestPath(push.Path), bridge.PushRecordPath(push.Path)
	undo := func() {
		placed.undo()
		_ = os.Remove(mpath)
		_ = os.Remove(rpath)
		cleanup()
	}
	rec.Version, rec.From, rec.Rev, rec.Draft = push.Label, tg.parent.Label, rev, a.Draft
	rec.Timeline = timeline
	if rel, err := filepath.Rel(p.Root, timeline); err == nil {
		rec.Timeline = filepath.ToSlash(rel)
	}
	rec.Units = bridge.Units{Total: rep.Units, Reused: rep.Reused, Rendered: rep.Rendered}
	recJSON, err := jsonx.Marshal(rec)
	if err == nil {
		err = store.WriteJSON(mpath, manifest)
	}
	if err == nil {
		err = store.WriteJSON(rpath, recJSON)
	}
	if err != nil {
		undo()
		return PushResult{}, errorf(CodeFailed, "writing beside %s: %v", push.Path, err)
	}
	push.SHA256 = placed.sum
	res, err := p.Store.RecordPush(push)
	if err != nil {
		undo()
		return PushResult{}, wrap(err)
	}
	cleanup()
	return PushResult{Version: res.Version, Answered: nonNil(res.Answered), Questions: nonNil(res.Questions),
		Render: &rec, Manifest: mpath}, nil
}

// mergeChangelog joins the changelog made from patches with the explicit one: an explicit change replaces a note's
// summary (its patches stay listed), an explicit ask takes the note out, and explicit changes for other notes are
// added after.
func mergeChangelog(auto []bridge.NoteChange, changes []project.ChangeEntry, asks []project.PushAsk) []project.ChangeEntry {
	asked := map[int64]bool{}
	for _, q := range asks {
		asked[q.Note] = true
	}
	explicit := map[int64]string{}
	for _, c := range changes {
		explicit[c.Note] = c.Summary
	}
	out := []project.ChangeEntry{}
	seen := map[int64]bool{}
	for _, c := range auto {
		if asked[c.Note] {
			continue
		}
		e := project.ChangeEntry{Note: c.Note, Summary: c.Summary, Patches: c.Patches}
		if s, ok := explicit[c.Note]; ok {
			e.Summary = s
		}
		out = append(out, e)
		seen[c.Note] = true
	}
	for _, c := range changes {
		if !seen[c.Note] {
			out = append(out, c)
			seen[c.Note] = true
		}
	}
	return out
}

// renamedManifest reads a render manifest and points its video at the version's file name.
func renamedManifest(path, video string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the manifest retake-edit just wrote
	if err != nil {
		return nil, err
	}
	o, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	name, err := json.Marshal(video)
	if err != nil {
		return nil, err
	}
	o.Set("video", name)
	return o.MarshalJSON()
}
