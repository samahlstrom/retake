package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
)

// LocalRetry is how long a blocked in-process worker waits before it checks again.
var LocalRetry = 15 * time.Second

// Local runs the editing worker of a project that this process serves (spec/automation.md, "Local mode"), until ctx
// is done. h serves the project's /api/worker/* routes. When the project can't be edited here, Local tells blocked
// why (Reason) and checks again every LocalRetry, asking profiles for the agents each time, so an agent installed
// meanwhile is found; it calls blocked("") once the worker runs. o.Handler is set to h.
func Local(ctx context.Context, o Options, profiles func() []Profile, h http.Handler, blocked func(string)) {
	o.Handler = h
	for ctx.Err() == nil {
		o.Profiles = profiles()
		err := Check(o)
		if err == nil {
			blocked("")
			err = Run(ctx, o)
		}
		if ctx.Err() != nil {
			return
		}
		blocked(Reason(err))
		select {
		case <-ctx.Done():
		case <-time.After(LocalRetry):
		}
	}
}

// Check reports what stops a worker from editing the project at o.Root, without starting one: no native timeline,
// no retake-edit, or no agent.
func Check(o Options) error {
	if _, err := bridge.FindTimeline(o.Root); err != nil {
		return err
	}
	if _, err := o.Editor.Find(); err != nil {
		return err
	}
	if len(o.Profiles) == 0 {
		return ErrNoAgents
	}
	return nil
}

// Reason is a short sentence for the page about why a project can't be edited here.
func Reason(err error) string {
	switch {
	case err == nil:
		return "The editing worker stopped. Restart retake to try again."
	case errors.Is(err, bridge.ErrNoTimeline):
		return "This project has no timeline (edit/timeline.json), so an agent can't edit it."
	case errors.Is(err, bridge.ErrNoEditor):
		return "retake-edit isn't installed, so edits can't be rendered."
	case errors.Is(err, ErrNoAgents):
		return "No agent found. Install Claude Code or Codex."
	case errors.Is(err, ErrBusy):
		return "Another retake worker is editing this project."
	}
	return "The editing worker can't run: " + err.Error()
}

// Profiles is the agents a worker offers: the configured ones, then Claude Code and Codex found on PATH unless a
// configured profile has the same id. find=false skips the PATH lookup.
func Profiles(configured []Profile, find bool) []Profile {
	out := append([]Profile(nil), configured...)
	if !find {
		return out
	}
	for _, p := range DefaultProfiles() {
		dup := false
		for _, c := range configured {
			dup = dup || c.ID == p.ID
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}

// handlerTransport answers a worker's requests with the project's handler in this process.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = http.NoBody
	}
	r := req.Clone(req.Context())
	r.RequestURI = req.URL.RequestURI()
	r.Header.Del("Authorization") // the handler grants the agent role itself
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return w.Result(), nil
}
