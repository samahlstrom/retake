package tus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client defaults.
const (
	DefaultChunkSize int64 = 8 << 20 // one PATCH
	DefaultRetries         = 5       // consecutive failures before giving up
	maxBackoff             = 30 * time.Second
)

// Client uploads files to a tus server, resuming after network errors and across runs.
type Client struct {
	HTTP      *http.Client                    // nil = a client with no overall timeout
	Token     string                          // sent as Authorization: Bearer
	ChunkSize int64                           // bytes per PATCH; 0 = DefaultChunkSize
	Retries   int                             // consecutive failures allowed; 0 = DefaultRetries
	Backoff   func(attempt int) time.Duration // the wait before retry attempt (1, 2, ...); nil = 1 s, 2 s, 4 s ... up to 30 s
	Progress  func(sent, total int64)         // called as the server confirms bytes; optional
	Created   func(uploadURL string)          // called once POST made the upload, so it can be resumed later; optional
}

// StatusError is a refusal from the server: its status and its error message.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the server answered %d %s", e.Code, http.StatusText(e.Code))
	}
	return fmt.Sprintf("the server answered %d: %s", e.Code, e.Message)
}

// ErrGone is returned when the upload being sent expired or was removed on the server.
var ErrGone = errors.New("the upload expired or was removed on the server; push again")

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

func (c *Client) chunk() int64 {
	if c.ChunkSize > 0 {
		return c.ChunkSize
	}
	return DefaultChunkSize
}

func (c *Client) retries() int {
	if c.Retries > 0 {
		return c.Retries
	}
	return DefaultRetries
}

func (c *Client) wait(ctx context.Context, attempt int) error {
	d := time.Second
	if c.Backoff != nil {
		d = c.Backoff(attempt)
	} else {
		for i := 1; i < attempt && d < maxBackoff; i++ {
			d *= 2
		}
		d = min(d, maxBackoff)
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) progress(sent, total int64) {
	if c.Progress != nil {
		c.Progress(sent, total)
	}
}

// Upload sends the file at path to the collection endpoint (an absolute URL), resuming resumeURL when it's given and
// still there with the same length and SHA-256 (else it starts a new upload). meta must include filename and sha256.
// It returns the upload's URL once the server has every byte and the SHA-256 matched.
func (c *Client) Upload(ctx context.Context, endpoint, path string, meta map[string]string, resumeURL string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil || !base.IsAbs() {
		return "", fmt.Errorf("the upload endpoint %q isn't an absolute URL", endpoint)
	}
	f, err := os.Open(path) //nolint:gosec // the file the caller pushes
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	upURL, offset := "", int64(0)
	if resumeURL != "" {
		h, err := c.headRetrying(ctx, resumeURL)
		switch {
		case errors.Is(err, ErrGone):
		case err != nil:
			return "", err
		case h.length == size && strings.EqualFold(h.meta["sha256"], meta["sha256"]):
			upURL, offset = resumeURL, h.offset
		}
	}
	if upURL == "" {
		if upURL, err = c.create(ctx, base, size, meta); err != nil {
			return "", err
		}
		if c.Created != nil {
			c.Created(upURL)
		}
	}
	c.progress(offset, size)
	if err := c.send(ctx, f, upURL, offset, size); err != nil {
		return upURL, err
	}
	return upURL, nil
}

// send PATCHes the file from offset to the end, going on from the server's offset after a failure.
func (c *Client) send(ctx context.Context, f *os.File, upURL string, offset, size int64) error {
	failures, restarted := 0, false
	for offset < size {
		n := min(c.chunk(), size-offset)
		res, err := c.patch(ctx, upURL, f, offset, n)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch {
		case err == nil && res.code == http.StatusNoContent:
			if res.offset <= offset || res.offset > size {
				return fmt.Errorf("the server answered with Upload-Offset %d after %d", res.offset, offset)
			}
			failures, offset = 0, res.offset
			c.progress(offset, size)
			continue
		case err == nil && res.code == StatusChecksumMismatch:
			if restarted {
				return fmt.Errorf("the file changed while it was uploading, or it arrived corrupted: %s", res.msg)
			}
			restarted, offset = true, 0 // the server emptied the upload: send it all once more
			c.progress(0, size)
			continue
		case err == nil && (res.code == http.StatusNotFound || res.code == http.StatusGone):
			return ErrGone
		case err == nil && res.code != http.StatusConflict && res.code < 500:
			return &StatusError{Code: res.code, Message: res.msg}
		}
		// A network error, a 5xx or a conflict: ask the server where it stands and go on from there.
		if failures, err = c.recover(ctx, failures, err, res); err != nil {
			return err
		}
		h, err := c.head(ctx, upURL)
		for retryable(ctx, err) {
			if failures, err = c.recover(ctx, failures, err, response{}); err != nil {
				return err
			}
			h, err = c.head(ctx, upURL)
		}
		if err != nil {
			return err
		}
		offset = h.offset
		c.progress(offset, size)
	}
	return nil
}

// recover counts one more failure and waits before the next try; it gives up after Retries in a row.
func (c *Client) recover(ctx context.Context, failures int, cause error, res response) (int, error) {
	failures++
	if failures > c.retries() {
		if cause == nil {
			cause = &StatusError{Code: res.code, Message: res.msg}
		}
		return failures, fmt.Errorf("upload failed %d times in a row: %w", failures, cause)
	}
	return failures, c.wait(ctx, failures)
}

type response struct {
	code   int
	offset int64
	msg    string
}

type headResult struct {
	offset, length int64
	meta           map[string]string
}

// reply is an answer, read and closed: its status, headers and error message.
type reply struct {
	code int
	hdr  http.Header
	msg  string
}

// do sends one request with the tus and auth headers and reads the answer.
func (c *Client) do(req *http.Request) (reply, error) {
	req.Header.Set("Tus-Resumable", Version)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var e struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	return reply{code: res.StatusCode, hdr: res.Header, msg: msg}, nil
}

// create POSTs a new upload, retrying network errors and 5xx answers.
func (c *Client) create(ctx context.Context, base *url.URL, size int64, meta map[string]string) (string, error) {
	failures := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Upload-Length", strconv.FormatInt(size, 10))
		req.Header.Set("Upload-Metadata", EncodeMetadata(meta))
		res, err := c.do(req)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil && res.code == http.StatusCreated {
			loc, err := base.Parse(res.hdr.Get("Location"))
			if err != nil || res.hdr.Get("Location") == "" {
				return "", errors.New("the server made the upload but sent no usable Location")
			}
			return loc.String(), nil
		}
		if err == nil && res.code < 500 {
			return "", &StatusError{Code: res.code, Message: res.msg}
		}
		if failures, err = c.recover(ctx, failures, err, response{code: res.code, msg: res.msg}); err != nil {
			return "", err
		}
	}
}

// head asks for an upload's offset once. 404 and 410 are ErrGone.
func (c *Client) head(ctx context.Context, upURL string) (headResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, upURL, nil)
	if err != nil {
		return headResult{}, err
	}
	res, err := c.do(req)
	if err != nil {
		return headResult{}, err
	}
	switch {
	case res.code == http.StatusNotFound || res.code == http.StatusGone:
		return headResult{}, ErrGone
	case res.code != http.StatusOK:
		return headResult{}, &StatusError{Code: res.code, Message: res.msg}
	}
	off, err1 := strconv.ParseInt(res.hdr.Get("Upload-Offset"), 10, 64)
	length, err2 := strconv.ParseInt(res.hdr.Get("Upload-Length"), 10, 64)
	if err1 != nil || err2 != nil {
		return headResult{}, errors.New("the server's HEAD answer has no Upload-Offset or Upload-Length")
	}
	meta, _ := ParseMetadata(res.hdr.Get("Upload-Metadata"))
	return headResult{offset: off, length: length, meta: meta}, nil
}

// retryable reports whether a failed request is worth another try: a network error or a 5xx answer, while ctx lives.
func retryable(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, ErrGone) || ctx.Err() != nil {
		return false
	}
	var se *StatusError
	return !errors.As(err, &se) || se.Code >= 500
}

// headRetrying is head with retries for network errors and 5xx answers.
func (c *Client) headRetrying(ctx context.Context, upURL string) (headResult, error) {
	failures := 0
	for {
		h, err := c.head(ctx, upURL)
		if !retryable(ctx, err) {
			if ctx.Err() != nil {
				return h, ctx.Err()
			}
			return h, err
		}
		if failures, err = c.recover(ctx, failures, err, response{}); err != nil {
			return h, err
		}
	}
}

// patch sends n bytes of f from offset.
func (c *Client) patch(ctx context.Context, upURL string, f *os.File, offset, n int64) (response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, upURL, io.NewSectionReader(f, offset, n))
	if err != nil {
		return response{}, err
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", OffsetType)
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	res, err := c.do(req)
	if err != nil {
		return response{}, err
	}
	r := response{code: res.code, msg: res.msg, offset: -1}
	if v, err := strconv.ParseInt(res.hdr.Get("Upload-Offset"), 10, 64); err == nil {
		r.offset = v
	}
	return r, nil
}
