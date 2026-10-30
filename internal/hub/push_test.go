package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/tus"
)

// videoFile writes size bytes of fake video and returns its path and sha256.
func videoFile(t *testing.T, name string, size int, seed byte) (string, string) {
	t.Helper()
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*7) ^ seed
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return p, hex.EncodeToString(sum[:])
}

// cutTransport cuts the body of the first PATCH after n bytes: a dropped connection mid-upload.
type cutTransport struct {
	n    int64
	done atomic.Bool
}

type cutReader struct {
	r    io.Reader
	left int64
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("connection reset")
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func (c *cutTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPatch && c.done.CompareAndSwap(false, true) {
		r = r.Clone(r.Context())
		r.Body = io.NopCloser(&cutReader{r: r.Body, left: c.n})
		r.ContentLength = -1
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (x *th) pushJSON(tok string, body any) res {
	x.t.Helper()
	b, _ := json.Marshal(body)
	return x.do(http.MethodPost, "/p/demo/api/push", string(b), bearerTok(tok))
}

func TestUploadAndPush(t *testing.T) {
	x := newTH(t, Config{}, nil)
	x.project("demo", true)
	x.project("other", true)
	tok := x.token("demo", team.Agent, "editor")
	endpoint := x.url + "/p/demo/api/uploads"

	// The first version of an empty project, sent with a connection that breaks halfway: the client resumes.
	v1, sum1 := videoFile(t, "intro.mp4", 3<<20, 1)
	ct := &cutTransport{n: 1 << 20}
	var created []string
	c := &tus.Client{HTTP: &http.Client{Transport: ct}, Token: tok, ChunkSize: 2 << 20,
		Backoff: func(int) time.Duration { return time.Millisecond }, Created: func(u string) { created = append(created, u) }}
	up, err := c.Upload(context.Background(), endpoint, v1, map[string]string{"filename": "intro.mp4", "sha256": sum1}, "")
	if err != nil || !ct.done.Load() || len(created) != 1 || created[0] != up ||
		!strings.HasPrefix(up, x.url+"/p/demo/api/uploads/up_") {
		t.Fatalf("upload: %v %q %v", err, up, created)
	}
	id := up[strings.LastIndex(up, "/")+1:]
	// Another project's token can't see it.
	other := x.token("other", team.Agent, "")
	if r := x.do(http.MethodHead, "/p/demo/api/uploads/"+id, "", bearerTok(other), header("Tus-Resumable",
		"1.0.0")); r.code != 401 {
		t.Fatalf("other project's token: %d", r.code)
	}
	if r := x.pushJSON(tok, map[string]any{"upload": id, "changes": []map[string]any{{"note": 1, "summary": "x"}}}); r.code != 400 {
		t.Fatalf("first version with changes: %d %s", r.code, r.body)
	}
	r := x.pushJSON(tok, map[string]any{"upload": id})
	if r.code != 200 {
		t.Fatalf("first push: %d %s", r.code, r.body)
	}
	var pr struct {
		Version project.VersionRow `json:"version"`
	}
	_ = json.Unmarshal([]byte(r.body), &pr)
	ws := x.st.Workspace("demo")
	if pr.Version.Label != "v1" || pr.Version.SHA256 != sum1 || pr.Version.By != "editor" ||
		pr.Version.Path != filepath.Join(ws, "media", "demo_v1.mp4") {
		t.Fatalf("v1: %+v", pr.Version)
	}
	if _, err := os.Stat(filepath.Join(x.st.UploadsDir(), id+".json")); !os.IsNotExist(err) {
		t.Fatal("a pushed upload is gone")
	}
	if r := x.pushJSON(tok, map[string]any{"upload": id}); r.code != 404 {
		t.Fatalf("pushing a used upload: %d %s", r.code, r.body)
	}

	// A note opens on v1. A push that doesn't handle it is refused, and keeps the upload for a second try.
	p, _ := x.h.project("demo")
	if _, err := p.store.Sync(project.Run{Version: "v1", Comments: []project.Comment{{Take: "2026-10-30_10-00-00",
		Seg: "S1", N: 1, T0: 1, T1: 2, Text: "tighten the intro"}}}); err != nil {
		t.Fatal(err)
	}
	v2, sum2 := videoFile(t, "intro_v2.mp4", 1<<20, 2)
	c2 := &tus.Client{Token: tok}
	up2, err := c2.Upload(context.Background(), endpoint, v2, map[string]string{"filename": "intro_v2.mp4",
		"sha256": sum2}, "")
	if err != nil {
		t.Fatal(err)
	}
	id2 := up2[strings.LastIndex(up2, "/")+1:]
	r = x.pushJSON(tok, map[string]any{"upload": id2})
	if r.code != 409 || r.json(t)["code"] != "push_rule" || !strings.Contains(r.body, `"notes":[1]`) {
		t.Fatalf("push rule: %d %s", r.code, r.body)
	}
	if _, err := x.h.uploads.Lookup("demo", id2); err != nil {
		t.Fatalf("the refused push's upload is gone: %v", err)
	}
	r = x.pushJSON(tok, map[string]any{"upload": id2, "changes": []map[string]any{{"note": 1, "summary": "cut 2 s"}}})
	if r.code != 200 || !strings.Contains(r.body, `"answered":[1]`) {
		t.Fatalf("push v2: %d %s", r.code, r.body)
	}
	// The page sees v2 as current.
	admin := x.user("boss@example.com", true)
	info := x.do(http.MethodGet, "/p/demo/api/info", "", cookie(SessionCookie, x.session(admin))).json(t)
	if info["label"] != "v2" || len(info["versions"].([]any)) != 2 {
		t.Fatalf("info after push: %v", info)
	}

	// A file whose content doesn't match its sha256 is refused and emptied.
	bad, _ := videoFile(t, "bad.mp4", 4096, 3)
	_, err = (&tus.Client{Token: tok, Retries: 1}).Upload(context.Background(), endpoint, bad,
		map[string]string{"filename": "bad.mp4", "sha256": strings.Repeat("0", 64)}, "")
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("sha mismatch: %v", err)
	}
	// An incomplete upload can't be pushed.
	part := x.do(http.MethodPost, "/p/demo/api/uploads", "", bearerTok(tok), header("Tus-Resumable", "1.0.0"),
		header("Upload-Length", "100"), header("Upload-Metadata", "filename "+b64("x.mp4")+",sha256 "+
			b64(strings.Repeat("a", 64))))
	pid := part.hdr.Get("Location")[strings.LastIndex(part.hdr.Get("Location"), "/")+1:]
	if r := x.pushJSON(tok, map[string]any{"upload": pid}); r.code != 409 || !strings.Contains(r.body, "isn't complete") {
		t.Fatalf("incomplete: %d %s", r.code, r.body)
	}
}

func mcpSession(t *testing.T, x *th, tok string) *sdk.ClientSession {
	t.Helper()
	hdr := http.Header{"Authorization": {"Bearer " + tok}}
	tr := &sdk.StreamableClientTransport{Endpoint: x.url + "/p/demo/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: headerRT{hdr}, Timeout: 20 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "remote-agent", Version: "1"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type headerRT struct{ h http.Header }

func (t headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.h {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestRemoteMCPWithUploads(t *testing.T) {
	x := newTH(t, Config{}, nil)
	x.project("demo", false)
	tok := x.token("demo", team.Agent, "")
	cs := mcpSession(t, x, tok)
	ctx := context.Background()
	call := func(name string, args map[string]any) (*sdk.CallToolResult, string) {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res.StructuredContent)
		text := string(b)
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				text += tc.Text
			}
		}
		return res, text
	}
	if res, text := call("versions_list", map[string]any{}); res.IsError || !strings.Contains(text, `"label":"v1"`) {
		t.Fatalf("versions_list: %s", text)
	}
	// A path on the server is refused: only uploads.
	if res, text := call("version_push", map[string]any{"file": "/etc/passwd"}); !res.IsError ||
		!strings.Contains(text, "upload:<id>") {
		t.Fatalf("a path: %s", text)
	}
	v2, sum := videoFile(t, "cut.mp4", 64<<10, 9)
	up, err := (&tus.Client{Token: tok}).Upload(ctx, x.url+"/p/demo/api/uploads", v2,
		map[string]string{"filename": "cut.mp4", "sha256": sum}, "")
	if err != nil {
		t.Fatal(err)
	}
	id := up[strings.LastIndex(up, "/")+1:]
	res, text := call("version_push", map[string]any{"file": "upload:" + id})
	if res.IsError || !strings.Contains(text, `"label":"v2"`) || !strings.Contains(text, sum) {
		t.Fatalf("version_push with an upload: %s", text)
	}
	if _, err := os.Stat(filepath.Join(x.st.Workspace("demo"), "media", "demo_v2.mp4")); err != nil {
		t.Fatal(err)
	}
	if res, text := call("version_push", map[string]any{"file": "upload:" + id}); !res.IsError ||
		!strings.Contains(text, "no upload") {
		t.Fatalf("the same upload twice: %s", text)
	}
	if n, err := x.h.uploads.Sweep(); err != nil || n != 1 {
		t.Fatalf("sweep after an MCP push: %d %v", n, err)
	}
	// notes_wait is capped for HTTP.
	start := time.Now()
	if res, text := call("notes_wait", map[string]any{"since": 1 << 40, "timeout": 0.2}); res.IsError ||
		!strings.Contains(text, `"timed_out":true`) || time.Since(start) > 5*time.Second {
		t.Fatalf("notes_wait: %s", text)
	}
}
