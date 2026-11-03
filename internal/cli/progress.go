package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/worker"
)

// `retake working <note> ["what I'm doing"]` and `retake done <note> "what changed"` from inside a running editing
// task (spec/automation.md, "Marking progress"): like `retake ask`, no video and no token; the task's worker passes
// them on.

func progressCmd(ctx context.Context, state string, args []string, env Env) int {
	var asJSON bool
	f := flags{bools: map[string]*bool{"--json": &asJSON}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	switch {
	case err != nil:
	case len(pos) == 0:
		err = errors.New(`give the note: retake ` + state + ` 12 "` + map[string]string{
			project.ProgressWorking: "what you're doing", project.ProgressDone: "what changed"}[state] + `"`)
	case len(pos) > 2:
		err = errors.New("give the note and one line of words, in quotes")
	case state == project.ProgressDone && (len(pos) < 2 || strings.TrimSpace(pos[1]) == ""):
		err = errors.New(`say what changed: retake done 12 "Slowed the mask fade to 1.2 s"`)
	}
	var note int64
	if err == nil {
		note, err = parseNoteID(pos[0])
	}
	if err == nil && note == 0 {
		err = fmt.Errorf("%q is not a note id", pos[0])
	}
	if err != nil {
		return usageError(env, state, err)
	}
	text := ""
	if len(pos) == 2 {
		text = pos[1]
	}
	end, err := askEndpoint(env.Getenv)
	if err != nil {
		return usageError(env, state, err)
	}
	body, err := json.Marshal(worker.ProgressRequest{Note: note, State: state, Text: text})
	if err != nil {
		return opFailed(env, state, err, asJSON)
	}
	// A plain project's done copies the video so far into a revision first: that can take a little while.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	post := func(end worker.AskEndpoint) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(end.URL, "/")+"/progress", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+end.Token)
		req.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(req)
	}
	resp, err := post(end)
	if err != nil {
		// The worker may have restarted on another port: its ask.json says where it is now.
		if now, ferr := askFile(); ferr == nil && now.URL != end.URL {
			resp, err = post(now)
		}
	}
	if err != nil {
		return opFailed(env, state, fmt.Errorf("can't reach the task's worker (has the task ended?): %w", err), asJSON)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var reply struct{ Error string }
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &reply) == nil && reply.Error != "" {
			msg = reply.Error
		}
		return opFailed(env, state, errors.New(msg), asJSON)
	}
	var out project.ProgressMark
	if err = json.Unmarshal(data, &out); err != nil {
		return opFailed(env, state, err, asJSON)
	}
	if asJSON {
		_ = printJSON(env.Stdout, out)
		return ExitOK
	}
	switch {
	case state == project.ProgressWorking && text != "":
		_, _ = fmt.Fprintf(env.Stdout, "working on #%d: %s\n", out.Note, strings.Join(strings.Fields(text), " "))
	case state == project.ProgressWorking:
		_, _ = fmt.Fprintf(env.Stdout, "working on #%d\n", out.Note)
	case out.Target == "":
		_, _ = fmt.Fprintf(env.Stdout, "#%d done\n", out.Note)
	case out.Ready:
		_, _ = fmt.Fprintf(env.Stdout, "#%d done: it's in %s\n", out.Note, out.Target)
	default:
		_, _ = fmt.Fprintf(env.Stdout, "#%d done: it shows in %s once it's rendered\n", out.Note, out.Target)
	}
	return ExitOK
}
