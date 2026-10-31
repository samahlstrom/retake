package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// emptyPlaylist is a streamed version's playlist before its first segment: it takes the version's number. The
// renderer replaces it with its own (the same header) when it publishes.
const emptyPlaylist = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n" +
	"#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-START:TIME-OFFSET=0\n"

// streamPush pushes a version that renders on demand (spec/bridge.md, "Streaming"): it snapshots the timeline,
// plans it, renders only its first frame as the poster, writes the playlist header and the push record (pending),
// and records the push. The review server renders it while someone watches it.
func (p *Project) streamPush(ctx context.Context, a PushArgs, push project.Push, rec bridge.PushRecord,
	timeline, bin string) (PushResult, error) {
	playlist := push.Path
	dir := filepath.Dir(playlist)
	stem := family.Stem(filepath.Base(playlist))
	newDir := false
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		newDir = true
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a version folder others may read
		return PushResult{}, errorf(CodeFailed, "%v", err)
	}
	doc, poster := family.Sidecar(playlist, ".timeline.json"), family.Sidecar(playlist, ".poster.jpg")
	rpath := bridge.PushRecordPath(playlist)
	undo := func(why error) (PushResult, error) {
		for _, f := range []string{playlist, doc, poster, rpath} {
			_ = os.Remove(f)
		}
		if newDir {
			_ = os.Remove(dir)
		}
		return PushResult{}, why
	}

	// The snapshot: the revision this version is, whenever it renders.
	src, err := os.ReadFile(timeline) //nolint:gosec // the project's timeline
	if err != nil {
		return undo(errorf(CodeFailed, "%v", err))
	}
	if err := store.WriteFileAtomic(doc, src); err != nil {
		return undo(errorf(CodeFailed, "writing %s: %v", doc, err))
	}
	args := bridge.StreamArgs{Timeline: timeline, Doc: doc, Playlist: playlist, Draft: a.Draft}
	plan, err := p.Editor.Plan(ctx, bin, args)
	if err != nil {
		return undo(errorf(CodeFailed, "planning %s: %v", timeline, err))
	}
	if err := makePoster(ctx, p.Editor, bin, args, poster); err != nil {
		return undo(errorf(CodeFailed, "the poster: %v", err))
	}
	st := &family.StreamRecord{State: family.StatePending, Playlist: filepath.Base(playlist),
		Poster: filepath.Base(poster), Doc: filepath.Base(doc), FPS: plan.FPS, Size: plan.Size,
		Total: plan.Hits + plan.Misses}
	if len(plan.Range) == 2 {
		st.Duration = plan.Range[1]
	}
	if a.Export {
		st.Export = stem + ".export.mp4"
	}
	rec.Stream = st
	rec.Units = bridge.Units{Total: plan.Hits + plan.Misses, Reused: plan.Hits, Rendered: plan.Misses}
	if err := store.WriteFileAtomic(playlist, []byte(emptyPlaylist)); err != nil {
		return undo(errorf(CodeFailed, "writing %s: %v", playlist, err))
	}
	if err := bridge.WritePushRecord(playlist, rec); err != nil {
		return undo(errorf(CodeFailed, "writing %s: %v", rpath, err))
	}
	sum, err := hashFile(doc) // the snapshot is what the version is until it renders
	if err != nil {
		return undo(errorf(CodeFailed, "%v", err))
	}
	push.SHA256 = sum
	res, err := p.Store.RecordPush(push)
	if err != nil {
		return undo(wrap(err))
	}
	if a.Notify != nil {
		a.Notify(fmt.Sprintf("%s streams when someone opens it: %d units (%d cached), %.1f s", push.Label,
			rec.Units.Total, rec.Units.Reused, st.Duration))
	}
	return PushResult{Version: res.Version, Answered: nonNil(res.Answered), Questions: nonNil(res.Questions),
		Render: &rec}, nil
}

// makePoster renders the first frame and keeps it as a JPEG at out.
func makePoster(ctx context.Context, ed bridge.Editor, bin string, a bridge.StreamArgs, out string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(out), ".poster-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	png, err := ed.Frame(ctx, bin, a, 0, tmp)
	if err != nil {
		return err
	}
	jpg := filepath.Join(tmp, "poster.jpg")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", png, "-q:v", "3", jpg) //nolint:gosec // argv only
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, b)
	}
	return os.Rename(jpg, out)
}
