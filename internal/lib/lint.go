package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
)

// Violation is one house-rule violation `retake-edit lint` reported (spec/components.md).
type Violation struct {
	Rule     string     `json:"rule"`
	ID       string     `json:"id"`
	Severity string     `json:"severity"`
	Code     string     `json:"code"`
	Message  string     `json:"message"`
	Elements []string   `json:"elements"`
	Ptrs     []string   `json:"ptrs"`
	T        [2]float64 `json:"t,omitzero"`
	Hint     string     `json:"hint,omitempty"`
}

// LintNote is a rule that couldn't run, or why boxes weren't measured.
type LintNote struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// LintResult is `retake-edit lint --json`'s report.
type LintResult struct {
	Timeline   string      `json:"timeline"`
	Clean      bool        `json:"clean"`
	Errors     int         `json:"errors"`
	Warnings   int         `json:"warnings"`
	Rules      int         `json:"rules"`
	Elements   int         `json:"elements"`
	Boxes      bool        `json:"boxes"`
	Violations []Violation `json:"violations"`
	Notes      []LintNote  `json:"notes"`
}

// Lint runs `retake-edit lint <timeline> --json` and returns its report. Violations aren't an error; a timeline that
// doesn't load is (with retake-edit's message).
func Lint(ctx context.Context, ed bridge.Editor, timeline string) (LintResult, error) {
	bin, err := ed.Find()
	if err != nil {
		return LintResult{}, errorf(CodeFailed, "%v", err)
	}
	tl, err := filepath.Abs(timeline)
	if err != nil {
		return LintResult{}, wrap(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "lint", tl, "--json") //nolint:gosec // the configured editor
	cmd.Env = append(cmd.Environ(), ed.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var res struct {
		LintResult
		OK     bool `json:"ok"`
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"errors"`
	}
	// "errors" is a count in a report and a list in a failure: read the failure shape first.
	var fail struct {
		OK     bool            `json:"ok"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &fail); err != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) || runErr != nil {
			return LintResult{}, errorf(CodeFailed, "retake-edit lint: %v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return LintResult{}, errorf(CodeFailed, "retake-edit lint printed no JSON")
	}
	if !fail.OK {
		if err := json.Unmarshal(stdout.Bytes(), &res); err == nil && len(res.Errors) > 0 {
			var msgs []string
			for _, e := range res.Errors {
				m := e.Message
				if e.Hint != "" {
					m += " (" + e.Hint + ")"
				}
				msgs = append(msgs, m)
			}
			return LintResult{}, &Error{Code: res.Errors[0].Code, Message: strings.Join(msgs, "; ")}
		}
		return LintResult{}, errorf(CodeFailed, "retake-edit lint failed: %s", strings.TrimSpace(stdout.String()))
	}
	var out LintResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return LintResult{}, errorf(CodeFailed, "retake-edit lint's report: %v", err)
	}
	out.Timeline = tl
	if out.Violations == nil {
		out.Violations = []Violation{}
	}
	if out.Notes == nil {
		out.Notes = []LintNote{}
	}
	return out, nil
}
