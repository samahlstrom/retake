package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/store"
)

// emptyPlaylist is the playlist before its first segment: it takes the version's number, and the family doesn't
// list it yet (it has no segment).
const emptyPlaylist = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n"

// progressEvery is the least time between two progress events of a streamed render.
const progressEvery = time.Second

// streamPush renders a push as a growing HLS playlist in its version folder (spec/bridge.md, "Streaming"). The
// version is in the family from the first published unit on; the push is recorded when the playlist ends. A failed
// or interrupted render takes everything back out and emits a "failed" event.
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
	unlock, err := family.LockRender(playlist)
	if err != nil {
		if newDir {
			_ = os.Remove(dir)
		}
		return PushResult{}, errorf(CodeFailed, "locking %s: %v", playlist, err)
	}
	rpath, mpath := bridge.PushRecordPath(playlist), bridge.ManifestPath(playlist)
	export := ""
	if a.Export {
		export = filepath.Join(dir, stem+".export.mp4")
	}
	notify := func(format string, args ...any) {
		if a.Notify != nil {
			a.Notify(fmt.Sprintf(format, args...))
		}
	}
	event := func(why string, pg *family.Progress) {
		ev := project.VersionEvent{Why: why, Label: push.Label, Parent: push.Parent, Path: playlist, Progress: pg}
		if err := p.Store.EmitVersion(ev); err != nil {
			notify("retake: the %s event of %s: %v", why, push.Label, err)
		}
	}
	announced := false
	fail := func(why error) (PushResult, error) {
		for _, f := range []string{playlist, rpath, mpath, export, bridge.ManifestPath(export)} {
			if f != "" {
				_ = os.Remove(f)
			}
		}
		for _, pattern := range []string{"seg_[0-9]*.ts", "." + stem + "-*", "." + stem + ".*.tmp"} {
			if matches, err := filepath.Glob(filepath.Join(dir, pattern)); err == nil {
				for _, m := range matches {
					_ = os.Remove(m)
				}
			}
		}
		unlock()
		if newDir {
			_ = os.Remove(dir) // only if empty: a take someone recorded on it meanwhile stays
		}
		if announced {
			event(project.WhyFailed, nil)
		}
		return PushResult{}, why
	}

	rec.Rendering = &family.RenderingState{Playlist: filepath.Base(playlist),
		Started: time.Now().UTC().Format(time.RFC3339)}
	var mu sync.Mutex // the progress lines come in on the editor's output goroutine
	writeRec := func() error {
		b, err := jsonx.Marshal(rec)
		if err != nil {
			return err
		}
		return store.WriteJSON(rpath, b)
	}
	if err := writeRec(); err != nil {
		return fail(errorf(CodeFailed, "writing %s: %v", rpath, err))
	}
	if err := store.WriteFileAtomic(playlist, []byte(emptyPlaylist)); err != nil {
		return fail(errorf(CodeFailed, "writing %s: %v", playlist, err))
	}

	var last time.Time
	reused := 0 // units from the cache, from the plan
	progress := func(pr bridge.Progress) {
		mu.Lock()
		defer mu.Unlock()
		st := rec.Rendering
		switch pr.Progress {
		case "plan":
			st.Duration, st.FPS, st.Size, st.Total = pr.Duration, pr.FPS, pr.Size, pr.Units
			reused = pr.Reused
			if err := writeRec(); err != nil {
				notify("retake: writing %s: %v", rpath, err)
			}
		case "publish":
			st.Done, st.Total = pr.Done, pr.Total
			if err := writeRec(); err != nil {
				notify("retake: writing %s: %v", rpath, err)
			}
			pg := &family.Progress{Done: pr.Done, Total: pr.Total, Seconds: jsonx.Float(pr.Seconds)}
			switch {
			case !announced:
				announced, last = true, time.Now()
				event(project.WhyRendering, pg)
				notify("%s is playable %.1f s into the render: %.1f of %.1f s, %d of %d units (%d from the cache)",
					push.Label, pr.Elapsed, pr.Seconds, st.Duration, pr.Done, pr.Total, reused)
			case time.Since(last) >= progressEvery || pr.Done == pr.Total:
				last = time.Now()
				event(project.WhyProgress, pg)
			}
		case "end":
			notify("%s rendered in %.1f s: %d segments, %.1f s", push.Label, pr.Elapsed, pr.Segments, pr.Seconds)
		}
	}
	rep, err := p.Editor.RenderStream(ctx, bin, timeline, playlist, export, a.Draft, progress)
	if err != nil {
		return fail(errorf(CodeFailed, "rendering %s: %v", timeline, err))
	}
	mu.Lock()
	defer mu.Unlock()
	rec.Rendering = nil
	rec.Units = bridge.Units{Total: rep.Units, Reused: rep.Reused, Rendered: rep.Rendered}
	if s := rep.Stream; s != nil {
		rec.Stream = &bridge.StreamStats{Playlist: filepath.Base(playlist), Segments: s.Segments, FirstS: s.FirstS,
			DoneS: s.DoneS}
	}
	if export != "" {
		rec.Export = filepath.Base(export)
	}
	if err := writeRec(); err != nil {
		return fail(errorf(CodeFailed, "writing %s: %v", rpath, err))
	}
	sum, err := hashFile(playlist)
	if err != nil {
		return fail(errorf(CodeFailed, "%v", err))
	}
	push.SHA256 = sum
	res, err := p.Store.RecordPush(push)
	if err != nil {
		return fail(wrap(err))
	}
	unlock()
	return PushResult{Version: res.Version, Answered: nonNil(res.Answered), Questions: nonNil(res.Questions),
		Render: &rec, Manifest: mpath}, nil
}
