package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// EditorName is the editor's binary.
const EditorName = "retake-edit"

// ErrNoEditor is returned when retake-edit can't be found.
var ErrNoEditor = errors.New("retake-edit not found")

// Editor locates retake-edit and says how to run it.
type Editor struct {
	// Configured is [edit] retake_edit from the user config; "" = look for it.
	Configured string
	// Getenv reads the environment (os.Getenv when nil).
	Getenv func(string) string
	// Exe is the running retake binary (os.Executable when ""), for <repo>/editor/target/release.
	Exe string
	// Dir is the current folder (os.Getwd when ""), for ./editor/target/release.
	Dir string
	// Env is extra environment for retake-edit: the asset catalog's seam (spec/bridge.md), e.g. RETAKE_LIB=...
	Env []string
}

func (e Editor) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// Find returns retake-edit's path: the configured one, else $RETAKE_EDIT, else retake-edit on $PATH, else
// editor/target/release/retake-edit in the repo the retake binary was built in, or under the current folder.
func (e Editor) Find() (string, error) {
	if p := strings.TrimSpace(e.Configured); p != "" {
		if !isExec(p) {
			return "", fmt.Errorf("%w: [edit] retake_edit is %s, which isn't an executable file", ErrNoEditor, p)
		}
		return p, nil
	}
	if p := strings.TrimSpace(e.getenv("RETAKE_EDIT")); p != "" {
		if !isExec(p) {
			return "", fmt.Errorf("%w: $RETAKE_EDIT is %s, which isn't an executable file", ErrNoEditor, p)
		}
		return p, nil
	}
	if p, err := exec.LookPath(EditorName); err == nil {
		return p, nil
	}
	rel := filepath.Join("editor", "target", "release", EditorName)
	var tried []string
	exe := e.Exe
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe != "" {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		tried = append(tried, filepath.Join(filepath.Dir(filepath.Dir(exe)), rel))
	}
	dir := e.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if dir != "" {
		tried = append(tried, filepath.Join(dir, rel))
	}
	for _, p := range tried {
		if isExec(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: set [edit] retake_edit in config.toml or $RETAKE_EDIT, put it on $PATH, or build it "+
		"(cd editor && cargo build --release)", ErrNoEditor)
}

// RenderReport is what `retake-edit render --json` reports.
type RenderReport struct {
	Output       string             `json:"output"`
	ManifestPath string             `json:"manifest_path"`
	Units        int                `json:"units"`
	Reused       int                `json:"reused"`
	Rendered     int                `json:"rendered"`
	Frames       int64              `json:"frames"`
	Duration     float64            `json:"duration"`
	Timings      map[string]float64 `json:"timings"`
	// Stream is a streamed render's playlist and timings (render --hls).
	Stream *struct {
		Playlist string  `json:"playlist"`
		Segments int     `json:"segments"`
		FirstS   float64 `json:"first_s"`
		DoneS    float64 `json:"done_s"`
	} `json:"stream,omitempty"`
}

// Progress is one progress line of a streamed render (`render --hls --progress`, spec/bridge.md, "Streaming").
type Progress struct {
	Progress string  `json:"progress"` // plan | publish | end
	Units    int     `json:"units"`    // plan: units in the render
	Reused   int     `json:"reused"`   // plan: units from the cache
	Segments int     `json:"segments"` // segments planned (plan) or published (publish, end)
	Duration float64 `json:"duration"` // plan: the expected duration
	FPS      float64 `json:"fps"`
	Size     []int   `json:"size"`
	Done     int     `json:"done"`    // publish: units published
	Total    int     `json:"total"`   // publish: units in all
	Seconds  float64 `json:"seconds"` // publish, end: playable seconds
	Elapsed  float64 `json:"elapsed"` // publish, end: seconds since the render started
}

// StreamArgs say what a streamed render renders and where to.
type StreamArgs struct {
	Timeline string // the timeline file
	Doc      string // a snapshot of it to render instead, as if it were the timeline ("" = the file itself)
	Playlist string
	Export   string // also package an MP4 here at the end ("" = none)
	Draft    bool
}

func (a StreamArgs) common(args []string) []string {
	if a.Doc != "" {
		args = append(args, "--doc", a.Doc)
	}
	if a.Draft {
		args = append(args, "--draft")
	}
	return args
}

// RenderStream runs `retake-edit render <timeline> [--doc <snapshot>] --hls <playlist> --progress --json
// [-o <export>] [--draft]`, calling progress for each progress line as it comes, and returns the final report.
// Cancelling ctx stops it with SIGTERM (SIGKILL after StopWait): what it published stays.
func (e Editor) RenderStream(ctx context.Context, bin string, a StreamArgs, progress func(Progress)) (RenderReport,
	error) {
	args := a.common([]string{"render", a.Timeline, "--hls", a.Playlist, "--progress", "--json"})
	if a.Export != "" {
		args = append(args, "-o", a.Export)
	}
	var rep RenderReport
	err := e.runWith(ctx, bin, args, &rep, func(line []byte) bool {
		var p Progress
		if json.Unmarshal(line, &p) != nil || p.Progress == "" {
			return false
		}
		if progress != nil {
			progress(p)
		}
		return true
	})
	return rep, err
}

// PlanReport is what `retake-edit plan --json` reports that a streamed push needs.
type PlanReport struct {
	Size   []int     `json:"size"`
	FPS    float64   `json:"fps"`
	Range  []float64 `json:"range"`
	Hits   int       `json:"hits"`
	Misses int       `json:"misses"`
}

// Plan runs `retake-edit plan <timeline> [--doc <snapshot>] --json [--draft]`: the units and what they'll give,
// without rendering.
func (e Editor) Plan(ctx context.Context, bin string, a StreamArgs) (PlanReport, error) {
	var rep PlanReport
	err := e.run(ctx, bin, a.common([]string{"plan", a.Timeline, "--json"}), &rep)
	return rep, err
}

// Frame runs `retake-edit frames <timeline> <t> -o <dir> [--doc <snapshot>] --json [--draft]` and returns the PNG's
// path.
func (e Editor) Frame(ctx context.Context, bin string, a StreamArgs, t float64, dir string) (string, error) {
	var rep struct {
		Frames []struct {
			Path string `json:"path"`
		} `json:"frames"`
	}
	args := a.common([]string{"frames", a.Timeline, strconv.FormatFloat(t, 'f', -1, 64), "-o", dir, "--json"})
	if err := e.run(ctx, bin, args, &rep); err != nil {
		return "", err
	}
	if len(rep.Frames) == 0 {
		return "", errors.New("retake-edit frames gave no frame")
	}
	return rep.Frames[0].Path, nil
}

// StopWait is how long a cancelled retake-edit has after SIGTERM before it's killed.
var StopWait = 5 * time.Second

// EditError is a failure retake-edit reported (its typed errors, joined).
type EditError struct {
	Code    string
	Message string
}

func (e *EditError) Error() string { return "retake-edit: " + e.Message }

// Render runs `retake-edit render <timeline> -o <out> --json [--draft]` and returns its report.
func (e Editor) Render(ctx context.Context, bin, timeline, out string, draft bool) (RenderReport, error) {
	args := []string{"render", timeline, "-o", out, "--json"}
	if draft {
		args = append(args, "--draft")
	}
	var rep RenderReport
	err := e.run(ctx, bin, args, &rep)
	return rep, err
}

// run runs retake-edit with --json and decodes its result into v.
func (e Editor) run(ctx context.Context, bin string, args []string, v any) error {
	return e.runWith(ctx, bin, args, v, nil)
}

// lineWriter hands each complete line written to it to take, as it comes; the lines take doesn't want go to rest.
type lineWriter struct {
	take    func([]byte) bool
	partial []byte
	rest    *bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := w.partial[:i]
		if !w.take(bytes.TrimSpace(line)) {
			w.rest.Write(line)
			w.rest.WriteByte('\n')
		}
		w.partial = w.partial[i+1:]
	}
}

// runWith is run, with each stderr line offered to lines first (nil: none); the lines it takes aren't error text.
func (e Editor) runWith(ctx context.Context, bin string, args []string, v any, lines func([]byte) bool) error {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the configured editor binary
	cmd.Env = append(os.Environ(), e.Env...)
	// Cancelled (a streamed render nobody watches any more): SIGTERM first, so it ends at once and cleanly, then a kill.
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = StopWait
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var lw *lineWriter
	if lines != nil {
		lw = &lineWriter{take: lines, rest: &stderr}
		cmd.Stderr = lw
	}
	runErr := cmd.Run()
	if lw != nil {
		stderr.Write(lw.partial) // a last line without its newline
	}
	var res struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		if runErr != nil {
			return fmt.Errorf("retake-edit %s: %w: %s", args[0], runErr, strings.TrimSpace(stderr.String()))
		}
		return fmt.Errorf("retake-edit %s printed no JSON: %w", args[0], err)
	}
	if !res.OK {
		var msgs []string
		code := "failed"
		for _, x := range res.Errors {
			m := x.Message
			if x.Hint != "" {
				m += " (" + x.Hint + ")"
			}
			msgs = append(msgs, m)
			code = x.Code
		}
		return &EditError{Code: code, Message: strings.Join(msgs, "; ")}
	}
	return json.Unmarshal(stdout.Bytes(), v)
}
