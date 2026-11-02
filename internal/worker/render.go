package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
)

// fingerprint watches source files plus native asset references without reading gigabytes of footage each tick.
// Source bytes are hashed; media references include size and nanosecond mtime. retake-edit checks its own full
// dependency cache when planning, so only affected units are encoded.
func fingerprint(root, timeline string) (string, []byte, error) {
	doc, e := os.ReadFile(timeline) //nolint:gosec // the selected local project's native timeline
	if e != nil {
		return "", nil, e
	}
	var tree any
	if e = json.Unmarshal(doc, &tree); e != nil {
		return "", nil, e
	}
	h := sha256.New()
	_, _ = h.Write(doc)
	e = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "target" || name == "dist" || strings.HasSuffix(name, ".review")) {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(name) {
		case ".json", ".toml", ".ts", ".tsx", ".js", ".jsx", ".svg", ".css", ".glsl", ".wgsl", ".py":
		default:
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		_, _ = fmt.Fprintf(h, "%s:%d:%d\n", rel, info.Size(), info.ModTime().UnixNano())
		if info.Size() < 2<<20 {
			b, err := os.ReadFile(path) //nolint:gosec // source files enumerated within the selected project
			if err != nil {
				return err
			}
			_, _ = h.Write(b)
		}
		return nil
	})
	if e != nil {
		return "", nil, e
	}
	// Resolve known native asset path/word references, including files outside the project directory.
	var native struct {
		Assets map[string]struct {
			Path  string `json:"path"`
			Words string `json:"words"`
		} `json:"assets"`
	}
	_ = json.Unmarshal(doc, &native)
	paths := []string{}
	for _, a := range native.Assets {
		for _, p := range []string{a.Path, a.Words} {
			if p != "" {
				if !filepath.IsAbs(p) {
					p = filepath.Join(filepath.Dir(timeline), p)
				}
				paths = append(paths, p)
			}
		}
	}
	slices.Sort(paths)
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return "", nil, err
		}
		_, _ = fmt.Fprintf(h, "%s:%d:%d\n", p, st.Size(), st.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), doc, nil
}

func (r *runner) renderLoop(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var candidate, started string
	var stable time.Time
	var cancel context.CancelFunc
	var done chan struct{}
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
			done = nil
		}
	}
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t, online := r.state()
			if !online || t != nil && t.Stop {
				stop()
				started = ""
				continue
			}
			source, doc, e := fingerprint(r.opts.Root, r.timeline)
			if e != nil {
				continue
			} // an editor may be midway through saving
			task, lease := "", ""
			if t != nil {
				task, lease = t.ID, t.Lease
				r.mu.Lock()
				unchanged := r.render.Complete && r.render.Source == source && r.finalTask != task
				r.mu.Unlock()
				if unchanged {
					continue
				}
			} else {
				// Outside a task, only an empty project's first video is rendered: a plain save makes no version.
				r.mu.Lock()
				skip := r.versions > 0 || r.render.Complete && r.render.Source == source
				r.mu.Unlock()
				if skip {
					continue
				}
			}
			key := source + ":" + task + ":" + lease
			if candidate != key {
				candidate = key
				stable = time.Now()
				continue
			}
			if key == started || time.Since(stable) < 600*time.Millisecond {
				continue
			}
			stop()
			started = key
			child, c := context.WithCancel(ctx)
			cancel = c
			done = make(chan struct{})
			go func(ch chan struct{}, task, lease string) {
				defer close(ch)
				r.renderSource(child, source, doc, task, lease)
			}(done, task, lease)
		}
	}
}

func (r *runner) renderSource(ctx context.Context, source string, doc []byte, task, lease string) {
	v := project.LiveRender{ID: project.NewWorkID(), Worker: r.identity.ID, Task: task, Source: source}
	dir := filepath.Join(r.dir, "renders", v.ID)
	set := func(err string) {
		r.mu.Lock()
		changed := r.renderErr != err
		r.render = v
		r.renderErr = err
		r.mu.Unlock()
		if changed && err != "" {
			r.log("Render: %s", err)
		}
	}
	if e := os.MkdirAll(dir, 0o700); e != nil {
		set(e.Error())
		return
	}
	snapshot := filepath.Join(dir, "source.json")
	if e := os.WriteFile(snapshot, doc, 0o600); e != nil {
		set(e.Error())
		return
	}
	args := bridge.StreamArgs{Timeline: r.timeline, Doc: snapshot, Playlist: filepath.Join(dir, "index.m3u8")}
	plan, e := r.opts.Editor.Plan(ctx, r.editor, args)
	if e != nil {
		set(e.Error())
		return
	}
	if len(plan.Size) != 2 || len(plan.Range) != 2 {
		set("Renderer returned an invalid plan")
		return
	}
	v.Width, v.Height = plan.Size[0], plan.Size[1]
	v.FPS = plan.FPS
	v.Duration = plan.Range[1] - plan.Range[0]
	v.Total = plan.Hits + plan.Misses
	publish := func(publishCtx context.Context) error {
		if err := writeJSON(filepath.Join(dir, "state.json"), v); err != nil {
			return err
		}
		var registered project.LiveRender
		err := r.call(publishCtx, "/api/worker/render", struct {
			project.LiveRender
			Lease string `json:"lease"`
		}{v, lease}, &registered)
		if err == nil {
			v.Label, v.N = registered.Label, registered.N
			set("")
		}
		return err
	}
	if e = publish(ctx); e != nil {
		set(e.Error())
		return
	}
	report, e := r.opts.Editor.RenderStream(ctx, r.editor, args, func(p bridge.Progress) {
		if p.Progress == "publish" || p.Progress == "end" {
			v.Done = max(v.Done, p.Done)
			v.Total = max(v.Total, p.Total)
			v.Seconds = p.Seconds
			_ = publish(ctx)
		}
	})
	if e == nil {
		pl, err := family.ReadPlaylist(args.Playlist)
		if err != nil {
			e = err
		} else if !pl.Ended || pl.Segments == 0 {
			e = fmt.Errorf("render is not a complete playable playlist")
		}
		if e == nil {
			v.Reused, v.Rendered = report.Reused, report.Rendered
			v.Total = report.Units
			v.Complete = true
			v.Done = v.Total
			v.Seconds = pl.Seconds
			if report.ManifestPath != "" {
				v.Manifest, _ = bridge.Load(report.ManifestPath)
			}
		}
	}
	if e != nil {
		v.Error = e.Error()
	}
	if ctx.Err() != nil {
		v.Stopped = true
		v.Complete = false
		v.Error = ""
		finalCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = publish(finalCtx)
		cancel()
		set("Source changed or worker stopped")
		return
	}
	if e = publish(ctx); e != nil {
		set(e.Error())
		return
	}
	set(v.Error)
}

// lastRender is the newest complete render retained in dir (its state.json), so a restarted worker knows what it last
// rendered; zero when there is none.
func lastRender(dir string) project.LiveRender {
	var last project.LiveRender
	var newest time.Time
	entries, _ := os.ReadDir(filepath.Join(dir, "renders"))
	for _, e := range entries {
		if !e.IsDir() || !project.ValidWorkID(e.Name()) {
			continue
		}
		path := filepath.Join(dir, "renders", e.Name(), "state.json")
		info, err := os.Stat(path)
		if err != nil || info.Size() > 8<<20 || !info.ModTime().After(newest) {
			continue
		}
		b, err := os.ReadFile(path) //nolint:gosec // a render's own state, under the worker directory
		var v project.LiveRender
		if err != nil || json.Unmarshal(b, &v) != nil || v.ID != e.Name() || !v.Complete || len(v.Source) != 64 {
			continue
		}
		last, newest = v, info.ModTime()
	}
	return last
}

// checkout puts the source of render id (its source.json) in the timeline, for a task that updates an older version
// than the newest (spec/automation.md, "Comments on vN become edits in vN+1"), and returns what puts the timeline as
// it was back.
func (r *runner) checkout(id string) (restore func(), err error) {
	if !project.ValidWorkID(id) {
		return nil, fmt.Errorf("invalid render %q", id)
	}
	doc, err := os.ReadFile(filepath.Join(r.dir, "renders", id, "source.json")) //nolint:gosec // a render's own snapshot
	if err != nil {
		return nil, err
	}
	now, err := os.ReadFile(r.timeline)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(r.timeline, doc, 0o600); err != nil { //nolint:gosec // the project's own timeline
		return nil, err
	}
	r.log("%s: started from %s's source; the newest version's comes back after", filepath.Base(r.opts.Root), id)
	return func() {
		if e := os.WriteFile(r.timeline, now, 0o600); e != nil { //nolint:gosec // the project's own timeline
			r.log("%s: couldn't put the newest version's source back: %v", filepath.Base(r.opts.Root), e)
		}
	}, nil
}
