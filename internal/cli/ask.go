package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/worker"
)

// `retake ask` from inside a running editing task (spec/automation.md, "Questions from inside a task"): no video and
// no token; the worker of the task passes the question on.

// taskAsk is what `retake ask` was given in a task: the request for the worker, built from its flags.
type taskAsk struct {
	note, at, from, to, version string
	px, asJSON                  bool
	boxes, arrows, images, opts []string
	draw                        string
	// A place in a document (spec/extensions.md, "Questions with a text anchor"): --file, --line, --heading, --quote.
	file, line, heading, quote string
}

// parseClock reads a time: seconds (83, 83.5) or m:ss / h:mm:ss (1:23, 1:02:03.5).
func parseClock(s string) (float64, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) > 3 || parts[0] == "" {
		return 0, fmt.Errorf("%q is not a time (seconds, or m:ss)", s)
	}
	total := 0.0
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		whole := i == len(parts)-1 || v == math.Trunc(v)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || !whole || i > 0 && v >= 60 {
			return 0, fmt.Errorf("%q is not a time (seconds, or m:ss)", s)
		}
		total = total*60 + v
	}
	return total, nil
}

// numbers reads n comma-separated numbers.
func numbers(s string, n int, what string) ([]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("%s needs %d numbers separated by commas, not %q", what, n, s)
	}
	out := make([]float64, n)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s needs %d numbers separated by commas, not %q", what, n, s)
		}
		out[i] = v
	}
	return out, nil
}

// withLabel splits "1,2,3,4:label" into the numbers and the label.
func withLabel(s string) (nums, label string, err error) {
	nums, label, _ = strings.Cut(s, ":")
	if len([]rune(strings.TrimSpace(label))) > project.MaxAgentLabel {
		return "", "", fmt.Errorf("a label is at most %d characters", project.MaxAgentLabel)
	}
	return nums, strings.TrimSpace(label), nil
}

var imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}

// readImage reads an image file for a question, checking its size and type.
func readImage(path string, getenv func(string) string) (project.AgentImage, error) {
	name := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(name))
	ok := false
	for _, e := range imageExts {
		ok = ok || e == ext
	}
	if !ok {
		return project.AgentImage{}, fmt.Errorf("%s: an image is a .png, .jpg, .webp or .gif", path)
	}
	f, err := os.Open(absPath(path, getenv))
	if err != nil {
		return project.AgentImage{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, project.MaxAgentImage+1))
	if err != nil {
		return project.AgentImage{}, err
	}
	if len(data) > project.MaxAgentImage {
		return project.AgentImage{}, fmt.Errorf("%s is over %d MiB", path, project.MaxAgentImage>>20)
	}
	want := project.ImageTypes[strings.Replace(ext, ".jpeg", ".jpg", 1)]
	if http.DetectContentType(data) != want {
		return project.AgentImage{}, fmt.Errorf("%s isn't the %s its name says", path, want)
	}
	return project.AgentImage{Name: name, Data: data}, nil
}

// request builds the worker's request from the flags.
func (a taskAsk) request(text string, getenv func(string) string) (project.WorkAsk, error) {
	q := project.WorkAsk{Text: strings.TrimSpace(text), Version: a.version, Px: a.px}
	var err error
	if q.Text == "" {
		return q, errors.New("the question needs its words")
	}
	if a.note != "" {
		if q.Note, err = parseNoteID(a.note); err != nil {
			return q, err
		}
	}
	for _, t := range []struct {
		flag, value string
		out         **float64
	}{{"--at", a.at, &q.At}, {"--from", a.from, &q.From}, {"--to", a.to, &q.To}} {
		if t.value == "" {
			continue
		}
		v, err := parseClock(t.value)
		if err != nil {
			return q, fmt.Errorf("%s: %w", t.flag, err)
		}
		*t.out = &v
	}
	if (q.From == nil) != (q.To == nil) || q.From != nil && *q.From >= *q.To {
		return q, errors.New("give a range as --from and --to, from before to")
	}
	if a.file != "" || a.line != "" || a.heading != "" || a.quote != "" {
		if a.file == "" {
			return q, errors.New("--line, --heading and --quote go with --file, the document the question is about")
		}
		placed := slices.ContainsFunc(a.images, func(s string) bool {
			i := strings.LastIndex(s, "@")
			_, err := numbers(s[i+1:], 4, "--image")
			return i >= 0 && err == nil
		})
		if a.at != "" || a.from != "" || a.to != "" || len(a.boxes)+len(a.arrows) > 0 || a.draw != "" || a.px || placed {
			return q, errors.New("a question about a document (--file) has no moment or frame: --at, --from, --to, " +
				"--box, --arrow, --draw, --px and placed images are refused")
		}
		an := project.Anchor{File: strings.TrimPrefix(filepath.ToSlash(a.file), "./"), Line: 1,
			Heading: strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(a.heading), "#")), Quote: strings.TrimSpace(a.quote)}
		if a.line != "" {
			n, err := strconv.Atoi(strings.TrimSpace(a.line))
			if err != nil {
				return q, fmt.Errorf("--line %q is not a line number", a.line)
			}
			an.Line = n
		}
		if err := an.Check(); err != nil {
			return q, err
		}
		q.Anchor = &an
	}
	if len(a.boxes) > project.MaxAgentBoxes || len(a.arrows) > project.MaxAgentArrows ||
		len(a.images) > project.MaxAgentImages || len(a.opts) > project.MaxAgentOptions {
		return q, fmt.Errorf("at most %d boxes, %d arrows, %d images and %d options", project.MaxAgentBoxes,
			project.MaxAgentArrows, project.MaxAgentImages, project.MaxAgentOptions)
	}
	for _, b := range a.boxes {
		nums, label, err := withLabel(b)
		if err != nil {
			return q, err
		}
		v, err := numbers(nums, 4, "--box")
		if err != nil {
			return q, err
		}
		q.Boxes = append(q.Boxes, project.AgentBox{Box: [4]float64(v), Label: label})
	}
	for _, s := range a.arrows {
		nums, label, err := withLabel(s)
		if err != nil {
			return q, err
		}
		v, err := numbers(nums, 4, "--arrow")
		if err != nil {
			return q, err
		}
		q.Arrows = append(q.Arrows, project.AgentArrow{From: [2]float64{v[0], v[1]}, To: [2]float64{v[2], v[3]}, Label: label})
	}
	if a.draw != "" {
		f, err := os.Open(absPath(a.draw, getenv))
		if err != nil {
			return q, err
		}
		var lines []struct {
			Points [][]float64 `json:"points"`
		}
		err = json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&lines)
		_ = f.Close()
		if err != nil {
			return q, fmt.Errorf("--draw %s: a JSON list of lines, [{\"points\": [[x, y], ...]}]: %w", a.draw, err)
		}
		points := 0
		for _, l := range lines {
			var st project.AgentStroke
			for _, p := range l.Points {
				if len(p) < 2 {
					return q, fmt.Errorf("--draw %s: a point is [x, y]", a.draw)
				}
				st.Points = append(st.Points, [2]float64{p[0], p[1]})
			}
			points += len(st.Points)
			q.Strokes = append(q.Strokes, st)
		}
		if len(q.Strokes) > project.MaxAgentStrokes || points > project.MaxAgentPoints {
			return q, fmt.Errorf("--draw: at most %d lines and %d points", project.MaxAgentStrokes, project.MaxAgentPoints)
		}
	}
	for _, s := range a.images {
		path, place := s, ""
		if i := strings.LastIndex(s, "@"); i >= 0 {
			if _, err := numbers(s[i+1:], 4, "--image"); err == nil {
				path, place = s[:i], s[i+1:]
			}
		}
		im, err := readImage(path, getenv)
		if err != nil {
			return q, err
		}
		if place != "" {
			v, _ := numbers(place, 4, "--image")
			b := [4]float64(v)
			im.Box = &b
		}
		q.Images = append(q.Images, im)
	}
	for _, s := range a.opts {
		text, file := s, ""
		if i := strings.LastIndex(s, "@"); i >= 0 {
			ext := strings.ToLower(filepath.Ext(s[i+1:]))
			for _, e := range imageExts {
				if e == ext {
					text, file = s[:i], s[i+1:]
				}
			}
		}
		text = strings.TrimSpace(text)
		if text == "" || len([]rune(text)) > project.MaxOptionText {
			return q, fmt.Errorf("an option is 1 to %d characters", project.MaxOptionText)
		}
		o := project.AgentOption{Text: text}
		if file != "" {
			im, err := readImage(file, getenv)
			if err != nil {
				return q, err
			}
			o.Image = &im
		}
		q.Options = append(q.Options, o)
	}
	return q, nil
}

// askEndpoint finds the worker of the task this runs in: RETAKE_ASK_URL and RETAKE_ASK_TOKEN, else the nearest
// .retake/worker/ask.json from the current folder up.
func askEndpoint(getenv func(string) string) (worker.AskEndpoint, error) {
	if u, t := getenv("RETAKE_ASK_URL"), getenv("RETAKE_ASK_TOKEN"); u != "" && t != "" {
		return worker.AskEndpoint{URL: u, Token: t}, nil
	}
	return askFile()
}

// askFile is the nearest .retake/worker/ask.json from the current folder up.
func askFile() (worker.AskEndpoint, error) {
	dir, err := os.Getwd()
	if err != nil {
		return worker.AskEndpoint{}, err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, ".retake", "worker", worker.AskFile)) //nolint:gosec // the project's own worker file
		if err == nil {
			var e worker.AskEndpoint
			if json.Unmarshal(b, &e) == nil && e.URL != "" && e.Token != "" {
				return e, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return worker.AskEndpoint{}, errors.New(`not inside a Retake task: no RETAKE_ASK_URL, and no .retake/worker/ask.json here or above (outside a task, give the video: retake ask <video> <note> "question")`)
}

// askInTask is `retake ask "question"` inside a task.
func askInTask(ctx context.Context, a taskAsk, text string, env Env) int {
	q, err := a.request(text, env.Getenv)
	if err != nil {
		return usageError(env, "ask", err)
	}
	end, err := askEndpoint(env.Getenv)
	if err != nil {
		return usageError(env, "ask", err)
	}
	body, err := json.Marshal(q)
	if err != nil {
		return opFailed(env, "ask", err, a.asJSON)
	}
	if len(body) > worker.MaxAskBody {
		return usageError(env, "ask", fmt.Errorf("the question and its images are over %d MiB", worker.MaxAskBody>>20))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	post := func(end worker.AskEndpoint) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(end.URL, "/")+"/ask", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+end.Token)
		req.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(req)
	}
	resp, err := post(end)
	if err != nil {
		// The worker that started this agent may have restarted on another port: its ask.json says where it is now
		// (spec/automation.md, "Agents outlive Retake").
		if now, ferr := askFile(); ferr == nil && now.URL != end.URL {
			resp, err = post(now)
		}
	}
	if err != nil {
		return opFailed(env, "ask", fmt.Errorf("can't reach the task's worker (has the task ended?): %w", err), a.asJSON)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var reply struct{ Error string }
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &reply) == nil && reply.Error != "" {
			msg = reply.Error
		}
		return opFailed(env, "ask", errors.New(msg), a.asJSON)
	}
	var out project.TaskQuestion
	if err = json.Unmarshal(data, &out); err != nil {
		return opFailed(env, "ask", err, a.asJSON)
	}
	if a.asJSON {
		_ = printJSON(env.Stdout, out)
		return ExitOK
	}
	if out.Note == 0 {
		// Asked from a chat turn (spec/chat.md): it shows in the chat.
		_, _ = fmt.Fprintf(env.Stdout, "asked in the chat (question %d): %s\n", out.ID, out.Text)
		return ExitOK
	}
	where := "at " + project.Clock(out.At)
	if out.Anchor != nil {
		where = "in " + out.Anchor.File
	}
	_, _ = fmt.Fprintf(env.Stdout, "asked about #%d %s (question %d): %s\n", out.Note, where, out.ID, out.Text)
	return ExitOK
}
