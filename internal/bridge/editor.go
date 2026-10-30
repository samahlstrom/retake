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
	"strings"
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
}

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
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the configured editor binary
	cmd.Env = append(os.Environ(), e.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
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
