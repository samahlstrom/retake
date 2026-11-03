package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
)

// The project's version files (spec/automation.md, "Version files" and "Plain projects"): a worker relays them like
// its renders, tells a server over HTTP what they are, and in a project without a timeline pushes the agent's video
// as the next one.

// onDisk is the project's versions as retake open lists them: the versions that probe with a duration, and streamed
// ones once complete. cached may answer from a look at most 2 s old.
func (r *runner) onDisk(cached bool) []family.Version {
	r.famMu.Lock()
	defer r.famMu.Unlock()
	if cached && !r.famAt.IsZero() && time.Since(r.famAt) < 2*time.Second {
		return r.fam
	}
	var out []family.Version
	if anchor := home.Anchor(r.opts.Root); anchor != "" {
		for _, v := range family.Detect(anchor, nil, r.opts.Prober) {
			if v.Stream != nil && v.Stream.State != family.StateComplete ||
				v.Stream == nil && (v.Duration == nil || *v.Duration <= 0) {
				continue
			}
			out = append(out, v)
		}
	}
	r.fam, r.famAt = out, time.Now()
	return out
}

// fileName reports whether a version's file name can be sent: what the server accepts (a plain name).
func fileName(name string) bool {
	return name != "" && len(name) <= 255 && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "/\\:\x00")
}

// fileVersions is what the server is told about the versions: metadata only.
func fileVersions(vs []family.Version) []project.FileVersion {
	out := []project.FileVersion{}
	for _, v := range vs {
		st, err := os.Stat(v.Path)
		name := filepath.Base(v.Path)
		if err != nil || !st.Mode().IsRegular() || !fileName(name) || !family.IsVideo(name) {
			continue
		}
		fv := project.FileVersion{Label: v.Label, N: v.N, Name: name, Size: st.Size(),
			Modified: st.ModTime().UnixMilli(), FPS: float64(v.FPS)}
		if v.Duration != nil {
			fv.Duration = float64(*v.Duration)
		}
		if v.Width != nil && v.Height != nil {
			fv.Width, fv.Height = *v.Width, *v.Height
		}
		out = append(out, fv)
	}
	return out
}

// sendFiles tells the server the project's version files as they are now.
func (r *runner) sendFiles(ctx context.Context) error {
	files := append(r.kindFiles(), fileVersions(r.onDisk(false))...)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.call(ctx, "/api/worker/files", map[string]any{"files": files}, nil)
	if err == nil {
		r.famMu.Lock()
		r.sent, r.sentAt = files, time.Now()
		r.famMu.Unlock()
	}
	return err
}

// filesLoop sends the version files when they change, and at least every minute. Only a worker that talks to a
// server over HTTP runs it: in Local mode the server reads the folder itself.
func (r *runner) filesLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		files := append(r.kindFiles(), fileVersions(r.onDisk(false))...)
		r.famMu.Lock()
		due := r.sentAt.IsZero() || !slices.Equal(files, r.sent) || time.Since(r.sentAt) > time.Minute
		r.famMu.Unlock()
		if due {
			_ = r.sendFiles(ctx) // before the first poll the server doesn't know us yet: the next tick tries again
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// plainVersion is a version in a plain task's context.
type plainVersion struct {
	Label    string  `json:"label"`
	Path     string  `json:"path"` // relative to the project
	Duration float64 `json:"duration"`
	FPS      float64 `json:"fps"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
}

// plainContext is a plain task's durable context: the assignment, the versions, and where the result goes.
type plainContext struct {
	project.WorkAssignment
	Family []plainVersion `json:"family"`
	Output string         `json:"output"`
}

func (r *runner) plainContext(a project.WorkAssignment, vs []family.Version, output string) plainContext {
	c := plainContext{WorkAssignment: a, Family: []plainVersion{}, Output: output}
	for _, v := range vs {
		rel, err := filepath.Rel(r.opts.Root, v.Path)
		if err != nil {
			rel = v.Path
		}
		pv := plainVersion{Label: v.Label, Path: filepath.ToSlash(rel), FPS: float64(v.FPS)}
		if v.Duration != nil {
			pv.Duration = float64(*v.Duration)
		}
		if v.Width != nil && v.Height != nil {
			pv.Width, pv.Height = *v.Width, *v.Height
		}
		c.Family = append(c.Family, pv)
	}
	return c
}

// plainPrompt is the task prompt of a project without a timeline. from is the video to start from ("" when the video
// has no cut yet, only an extension's version, v0), updating says it is the task's target itself, and into says
// where the result goes. extra is the extensions' section.
func plainPrompt(contextPath, from string, updating bool, output, into, reportPath, extra string) string {
	start := fmt.Sprintf("starting from %q, the version the notes were made on", from)
	if updating {
		start = fmt.Sprintf("starting from %q: the version these notes go in, which exists already with earlier changes to keep (comments on a version become edits in the next one)", from)
	}
	if from == "" {
		start = "building the video's first cut from what the notes and the video's folder give you (it has no cut yet)"
	}
	return fmt.Sprintf(`You are editing a Retake video project in the current working directory. It has no edit timeline: its versions are finished video files.
Read the project instructions, then the durable task context at %q: the notes, and under "family" every version with its path.
Only the note IDs in task.notes are assigned (minus task.done, finished in an earlier attempt). Other notes are context: check them and prior events for conflicting or already-applied feedback. Each note names the version it was made on and its times. Saved annotated stills, when available, are note-<id>.png in this task directory. Attached reference images are note-<id>-<file>; their boxes and original video times are in the context. A note's "missing" says which of its files couldn't be given to you and what you have instead.
Make the requested change with the tools you have (ffmpeg, for example), %[2]s. Write the result as one video file to %[3]q (H.264 and AAC in an MP4 play everywhere). Retake puts it in the project as %[4]s. Before each retake done (below), write the whole video so far, with that note's edit, to %[3]q: Retake shows it at once. Never edit, move or delete the existing versions or anyone else's files.
The next agent may differ from you; leave useful context on disk. Do not delete or edit the worker's identity, lock, logs, context files or ask.json.
`, contextPath, start, output, into) + extra + askRules(reportPath, "A completed report is only a request: Retake checks that the new video plays before resolving the comment. Do not run retake push or retake note yourself.")
}

// plainPlan is where a plain task's video goes (into, the target's label and file) and where its agent starts (from):
// comments on vN become edits in vN+1, the target made or updated in place. The agent starts from the target when it
// exists (it has the earlier changes), else from the version the notes were made on. vs is the versions now; output is
// where the agent writes its video.
func (r *runner) plainPlan(target string, vs []family.Version, output string) (*plainRun, error) {
	label, dest, exists, err := r.plainTarget(target, ".mp4")
	if err != nil {
		return nil, err
	}
	rel := func(p string) string {
		if rp, e := filepath.Rel(r.opts.Root, p); e == nil {
			return rp
		}
		return p
	}
	pl := &plainRun{Output: output, Updating: exists, Into: fmt.Sprintf("%s (%s)", label, rel(dest))}
	switch {
	case exists:
		pl.From = dest
		pl.Into = fmt.Sprintf("%s, in place of %s (Retake keeps the earlier file)", label, rel(dest))
	case len(vs) > 0:
		pl.From = newestPath(vs)
		for _, v := range vs {
			if project.LabelNumber(v.Label) == project.LabelNumber(label)-1 {
				pl.From = v.Path
			}
		}
	}
	for _, v := range vs {
		pl.Before = append(pl.Before, v.Label)
	}
	return pl, nil
}

// plainTarget is where a plain task's video goes: its target's file, existing or not, else (a task from before
// targets) the next version's (spec/automation.md, "Comments on vN become edits in vN+1").
func (r *runner) plainTarget(target, ext string) (label, dest string, exists bool, err error) {
	vs := r.onDisk(false)
	n := project.LabelNumber(target)
	switch {
	case n == 0 && len(vs) == 0, n == 1 && len(vs) == 0:
		label, dest = agent.FirstVersionFile(r.opts.Root, ext) // only v0 so far: v1
		return label, dest, false, nil
	case n == 0:
		label, dest, err = agent.NextVersionFile(vs, ext)
		return label, dest, false, err
	}
	return agent.VersionFile(vs, n, ext)
}

// pushPlain puts the agent's video in as the task's target: a new version, or in the place of the target when it
// exists, its earlier file kept in .retake/worker/history/. It returns the label, or why it couldn't.
func (r *runner) pushPlain(output, target string, before []string) (label, problem string) {
	if st, err := os.Stat(output); err == nil && st.Mode().IsRegular() {
		if !r.opts.Prober.Probe(output).HasDuration() {
			return "", "The agent's video doesn't play: it has no duration."
		}
		label, dest, exists, err := r.plainTarget(target, filepath.Ext(output))
		if err != nil {
			return "", "Couldn't name the version: " + err.Error()
		}
		if exists {
			// Updated in place: the same label, the earlier file kept as its history.
			if dest, err = r.keepHistory(label, dest, output); err != nil {
				return "", fmt.Sprintf("Couldn't keep the earlier %s: %v", label, err)
			}
		}
		if err = place(output, dest); err != nil {
			return "", fmt.Sprintf("Couldn't add the agent's video as %s: %v", label, err)
		}
		r.famMu.Lock()
		r.famAt = time.Time{} // look again: the file changed
		r.famMu.Unlock()
		for _, v := range r.onDisk(false) {
			if v.Label == label {
				return label, ""
			}
		}
		return "", fmt.Sprintf("The agent's video doesn't play as %s.", label)
	}
	var added *family.Version
	for _, v := range r.onDisk(false) {
		if !slices.Contains(before, v.Label) && (added == nil || v.N > added.N) && (target == "" || v.Label == target) {
			added = &v
		}
	}
	if added == nil {
		return "", "The agent finished without writing a new version."
	}
	return added.Label, ""
}

// keepHistory moves version label's current file (dest) to .retake/worker/history/<label>-<unix ms><ext>, so the
// agent's video can take its place, which it returns: the same path, with the new video's extension.
func (r *runner) keepHistory(label, dest, output string) (string, error) {
	dir := filepath.Join(r.dir, "history")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kept := filepath.Join(dir, fmt.Sprintf("%s-%d%s", label, time.Now().UnixMilli(), filepath.Ext(dest)))
	if err := os.Rename(dest, kept); err != nil {
		return "", err
	}
	return strings.TrimSuffix(dest, filepath.Ext(dest)) + strings.ToLower(filepath.Ext(output)), nil
}

// place moves src to dest, a new file (its folder made as needed): a rename, else a copy.
func place(src, dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dest))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // a version folder others may read
		return err
	}
	if os.Rename(src, dest) == nil {
		return nil
	}
	in, err := os.Open(src) //nolint:gosec // the task's own output file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".retake-tmp")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // a version file, as a push writes it
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(src)
	return nil
}
