package feedback

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFeedbackConcurrentLimit(t *testing.T) {
	t.Setenv("RETAKE_FEEDBACK_TOKEN", "test-token")
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	var calls atomic.Int32
	http.DefaultTransport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(http.StatusCreated, `{"html_url":"https://github.com/samahlstrom/retake/issues/42","number":42}`), nil
	})
	for round := range 100 {
		h := New("test")
		h.limiter.Max = 1
		calls.Store(0)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				<-start
				r := httptest.NewRequest(http.MethodPost, "/api/feedback", strings.NewReader(`{"type":"bug","title":"Concurrent report","description":"Only one issue may be created."}`))
				h.ServeHTTP(httptest.NewRecorder(), r)
			})
		}
		close(start)
		wg.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("round %d created %d GitHub issues with a limit of 1", round, got)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func request(t *testing.T, h http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/api/feedback", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestFeedback(t *testing.T) {
	t.Run("title limits count characters rather than UTF-8 bytes", func(t *testing.T) {
		t.Setenv("RETAKE_FEEDBACK_TOKEN", "test-token")
		old := http.DefaultTransport
		t.Cleanup(func() { http.DefaultTransport = old })
		http.DefaultTransport = roundTrip(func(*http.Request) (*http.Response, error) {
			return response(http.StatusCreated, `{"html_url":"https://github.com/samahlstrom/retake/issues/42","number":42}`), nil
		})
		body, err := json.Marshal(map[string]string{"type": "feature", "title": strings.Repeat("é", 200), "description": "A valid title."})
		if err != nil {
			t.Fatal(err)
		}
		if got := request(t, New("0.1.0"), http.MethodPost, string(body)); got.Code != http.StatusCreated {
			t.Errorf("200-character title: %d %s; want 201", got.Code, got.Body.String())
		}
	})
	t.Run("reports enabled state and rejects unavailable submission", func(t *testing.T) {
		t.Setenv("RETAKE_FEEDBACK_TOKEN", "")
		h := New("0.1.0")

		get := request(t, h, http.MethodGet, "")
		if get.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", get.Code)
		}
		var state struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &state); err != nil || state.Enabled {
			t.Fatalf("GET body = %q, want enabled false: %v", get.Body.Bytes(), err)
		}

		post := request(t, h, http.MethodPost, `{"type":"bug","title":"Broken","description":"It broke."}`)
		if post.Code != http.StatusServiceUnavailable {
			t.Fatalf("POST without token = %d, want 503: %s", post.Code, post.Body.String())
		}
		if body := post.Body.String(); !strings.Contains(body, "GitHub") || strings.Contains(body, "RETAKE_FEEDBACK_TOKEN") {
			t.Errorf("unavailable error must be clear and safe: %q", body)
		}
	})

	t.Run("validates the bounded public input before creating an issue", func(t *testing.T) {
		t.Setenv("RETAKE_FEEDBACK_TOKEN", "test-token")
		calls := 0
		old := http.DefaultTransport
		http.DefaultTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			return response(http.StatusCreated, `{"html_url":"https://github.com/samahlstrom/retake/issues/42","number":42}`), nil
		})
		t.Cleanup(func() { http.DefaultTransport = old })
		h := New("0.1.0")

		valid := map[string]string{
			"type":        "feature",
			"title":       strings.Repeat("t", 200),
			"description": strings.Repeat("d", 8000),
		}
		body, err := json.Marshal(valid)
		if err != nil {
			t.Fatal(err)
		}
		if got := request(t, h, http.MethodPost, string(body)); got.Code != http.StatusCreated {
			t.Fatalf("boundary submission = %d, want 201: %s", got.Code, got.Body.String())
		}
		if calls != 1 {
			t.Fatalf("GitHub calls = %d, want 1", calls)
		}

		for name, in := range map[string]string{
			"blank title":       `{"type":"bug","title":" ","description":"details"}`,
			"long title":        `{"type":"bug","title":"` + strings.Repeat("t", 201) + `","description":"details"}`,
			"blank description": `{"type":"bug","title":"title","description":" "}`,
			"long description":  `{"type":"bug","title":"title","description":"` + strings.Repeat("d", 8001) + `"}`,
			"unknown type":      `{"type":"question","title":"title","description":"details"}`,
		} {
			t.Run(name, func(t *testing.T) {
				got := request(t, h, http.MethodPost, in)
				if got.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400: %s", got.Code, got.Body.String())
				}
			})
		}
		tooLarge := `{"type":"bug","title":"title","description":"` + strings.Repeat("d", 32<<10) + `"}`
		if got := request(t, h, http.MethodPost, tooLarge); got.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("32 KiB overflow = %d, want 413: %s", got.Code, got.Body.String())
		}
		if calls != 1 {
			t.Errorf("invalid input reached GitHub: %d calls", calls)
		}
	})

	t.Run("creates only the fixed repository issue and keeps GitHub failures safe", func(t *testing.T) {
		t.Setenv("RETAKE_FEEDBACK_TOKEN", "test-token")
		old := http.DefaultTransport
		defer func() { http.DefaultTransport = old }()
		h := New("0.1.0")
		http.DefaultTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
			if got, want := r.URL.String(), "https://api.github.com/repos/samahlstrom/retake/issues"; got != want {
				t.Errorf("GitHub URL = %q, want %q", got, want)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want server token", got)
			}
			var issue map[string]string
			if err := json.NewDecoder(r.Body).Decode(&issue); err != nil {
				t.Error(err)
			}
			if got, want := issue["title"], "Trim this"; got != want {
				t.Errorf("issue title = %q, want %q", got, want)
			}
			if body := issue["body"]; !strings.Contains(body, "Type: bug") || !strings.Contains(body, "The detail") || !strings.Contains(body, "0.1.0") ||
				strings.Contains(body, "private-path") {
				t.Errorf("issue body must contain only report details and build version: %q", body)
			}
			if len(issue) != 2 {
				t.Errorf("issue fields = %#v, want only title and body", issue)
			}
			return response(http.StatusCreated, `{"html_url":"https://github.com/samahlstrom/retake/issues/42","number":42}`), nil
		})

		got := request(t, h, http.MethodPost, `{"type":"bug","title":" Trim this ","description":"The detail"}`)
		if got.Code != http.StatusCreated {
			t.Fatalf("create = %d, want 201: %s", got.Code, got.Body.String())
		}
		var created struct {
			URL    string `json:"url"`
			Number int    `json:"number"`
		}
		if err := json.NewDecoder(bytes.NewReader(got.Body.Bytes())).Decode(&created); err != nil ||
			created.URL != "https://github.com/samahlstrom/retake/issues/42" || created.Number != 42 {
			t.Errorf("create response = %q: %v", got.Body.Bytes(), err)
		}

		http.DefaultTransport = roundTrip(func(*http.Request) (*http.Response, error) {
			return response(http.StatusCreated, `{"html_url":"https://example.test/issues/42","number":42}`), nil
		})
		invalidURL := request(t, h, http.MethodPost, `{"type":"bug","title":"Again","description":"The detail"}`)
		if invalidURL.Code != http.StatusBadGateway {
			t.Errorf("invalid GitHub issue URL = %d, want 502: %s", invalidURL.Code, invalidURL.Body.String())
		}

		http.DefaultTransport = roundTrip(func(*http.Request) (*http.Response, error) {
			return response(http.StatusBadGateway, `{"message":"upstream secret: test-token"}`), nil
		})
		failed := request(t, h, http.MethodPost, `{"type":"bug","title":"Again","description":"The detail"}`)
		if failed.Code != http.StatusBadGateway {
			t.Errorf("GitHub failure = %d, want 502", failed.Code)
		}
		if body := failed.Body.String(); strings.Contains(body, "secret") || strings.Contains(body, "test-token") {
			t.Errorf("GitHub failure leaked upstream data: %q", body)
		}
	})
}
