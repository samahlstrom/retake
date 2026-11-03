// Package feedback sends public product feedback to Retake's GitHub tracker.
package feedback

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/team"
)

const (
	issuesURL = "https://api.github.com/repos/samahlstrom/retake/issues"
	maxBody   = 32 << 10
)

// Handler is the small server-only bridge from the review page to GitHub.
type Handler struct {
	version string
	token   string
	client  *http.Client
	limiter *team.Limiter
}

// New returns the feedback endpoint for one Retake server. The token is read only
// when the server starts, never sent to the browser.
func New(version string) *Handler {
	return &Handler{
		version: version,
		token:   strings.TrimSpace(os.Getenv("RETAKE_FEEDBACK_TOKEN")),
		client:  &http.Client{Timeout: 10 * time.Second},
		limiter: team.NewLimiter(),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.token != ""})
	case http.MethodPost:
		h.submit(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

type report struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	if h.token == "" {
		writeError(w, http.StatusServiceUnavailable, "GitHub feedback is not available on this server")
		return
	}
	var in report
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		if isTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "feedback is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad feedback")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad feedback")
		return
	}
	in.Type, in.Title, in.Description = strings.TrimSpace(in.Type), strings.TrimSpace(in.Title), strings.TrimSpace(in.Description)
	if (in.Type != "bug" && in.Type != "feature") || utf8.RuneCountInString(in.Title) == 0 ||
		utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Description) == 0 ||
		utf8.RuneCountInString(in.Description) > 8000 {
		writeError(w, http.StatusBadRequest, "invalid feedback")
		return
	}

	key, now := clientKey(r), time.Now()
	if blocked, wait := h.limiter.Blocked(key, now); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many feedback reports")
		return
	}
	// The limiter is deliberately charged before the upstream call: a retry after
	// a lost response must not create an unbounded stream of duplicate issues.
	h.limiter.Fail(key, now)

	payload, _ := json.Marshal(map[string]string{
		"title": in.Title,
		"body":  "Type: " + in.Type + "\n\n" + in.Description + "\n\nRetake build: " + h.version,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, issuesURL, bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, "GitHub feedback is unavailable")
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GitHub feedback is unavailable")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		writeError(w, http.StatusBadGateway, "GitHub feedback is unavailable")
		return
	}
	var created struct {
		URL    string `json:"html_url"`
		Number int    `json:"number"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&created); err != nil || !validIssueURL(created.URL, created.Number) {
		writeError(w, http.StatusBadGateway, "GitHub feedback is unavailable")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": created.URL, "number": created.Number})
}

func validIssueURL(raw string, number int) bool {
	u, err := url.Parse(raw)
	return err == nil && number > 0 && u.Scheme == "https" && u.Host == "github.com" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && u.Path == "/samahlstrom/retake/issues/"+strconv.Itoa(number)
}

func clientKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func isTooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
