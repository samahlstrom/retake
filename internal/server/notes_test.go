package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/project"
)

// Image bodies that sniff as their type.
var (
	pngBody  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)
	jpgBody  = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{2}, 64)...)
	gifBody  = append([]byte("GIF89a"), bytes.Repeat([]byte{3}, 64)...)
	webpBody = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{4}, 64)...)
)

// startM2 starts a server whose Close (job runner, store) runs at cleanup.
func startM2(t *testing.T, opts Options) (*harness, *fixture) {
	t.Helper()
	f := newFixture(t)
	opts.Video = f.video
	if opts.EventPoll == 0 {
		opts.EventPoll = 5 * time.Millisecond
	}
	h := start(t, opts)
	t.Cleanup(func() { _ = h.srv.Close() })
	return h, f
}

func (h *harness) origin() map[string]string {
	return map[string]string{"Origin": strings.TrimSuffix(h.url, "/")}
}

func (h *harness) postBytes(path string, body []byte, hdr map[string]string) resp {
	return h.do(http.MethodPost, path, bytes.NewReader(body), hdr)
}

// saveTake writes a take through /data, as the page does.
func (h *harness) saveTake(id, version string) {
	h.t.Helper()
	body := `{"session": {"format": 3}, "segments": [{"id": "S1"}], "strokes": []}`
	if r := h.post("/api/session/"+id+"/data?v="+version, body, nil); r.code != 200 {
		h.t.Fatalf("saving the take: %d %s", r.code, r.body)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDoneQueuesJobsEvenWithoutATranscriber(t *testing.T) {
	built := make(chan string, 4)
	engines := &jobs.Engines{
		Transcriber: func() (jobs.TakeTranscriber, error) { return nil, errors.New("whisper.cpp isn't installed") },
		Notes:       notesFunc(func(v family.Version) { built <- v.Label }),
	}
	h, f := startM2(t, Options{Jobs: engines})
	db := filepath.Join(f.root, ".retake", "project.db")

	// Reads never create the store.
	if r := h.get("/api/notes", nil); r.code != 200 {
		t.Fatalf("notes: %d %s", r.code, r.body)
	}
	if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("GET /api/notes created the project store")
	}

	if r := h.post("/api/session/"+take+"/done", "", nil); r.code != 404 || !strings.Contains(string(r.body), "no such take") {
		t.Fatalf("done before any save: %d %s", r.code, r.body)
	}
	if r := h.post("/api/session/"+take+"/done", "", map[string]string{"Origin": "http://evil.example"}); r.code != 403 {
		t.Fatalf("done from another origin: %d", r.code)
	}
	h.saveTake(take, "v6")
	r := h.post("/api/session/"+take+"/done?v=v6", "", h.origin())
	if r.code != 200 {
		t.Fatalf("done: %d %s", r.code, r.body)
	}
	var got struct {
		OK   bool     `json:"ok"`
		Jobs []jobRef `json:"jobs"`
	}
	if err := json.Unmarshal(r.body, &got); err != nil || !got.OK || len(got.Jobs) != 2 ||
		got.Jobs[0].Kind != "transcribe" || got.Jobs[1].Kind != "notes" {
		t.Fatalf("done = %s", r.body)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("done didn't create the project store")
	}
	takeDir := filepath.Join(f.root, "v6", "clip_v6.review", take)
	if !takeClosed(takeDir) {
		t.Fatal("session.json isn't closed")
	}
	// A later save from the page keeps it closed.
	h.saveTake(take, "v6")
	if !takeClosed(takeDir) {
		t.Fatal("a save after Done reopened the take")
	}

	// The transcription fails with a clear error, its notes run fails too, and the sidebar shows both.
	var side sidebarResponse
	waitFor(t, "the jobs to fail", func() bool {
		side = sidebarResponse{}
		_ = json.Unmarshal(h.get("/api/notes?v=v6", nil).body, &side)
		return len(side.Jobs) == 2 && side.Jobs[0].State == "failed" && side.Jobs[1].State == "failed"
	})
	byKind := map[string]jobView{}
	for _, j := range side.Jobs {
		byKind[j.Kind] = j
	}
	if e := byKind["transcribe"].Error; e != "can't transcribe: whisper.cpp isn't installed" || byKind["transcribe"].Take != take ||
		byKind["transcribe"].Hint != jobs.TranscribeHint || byKind["notes"].Hint != jobs.TranscribeHint {
		t.Errorf("transcribe job = %+v", byKind["transcribe"])
	}
	if e := byKind["notes"].Error; !strings.Contains(e, "transcribing take "+take+" failed") {
		t.Errorf("notes job = %+v", byKind["notes"])
	}
	select {
	case v := <-built:
		t.Fatalf("the notes engine ran for %s after a failed transcription", v)
	default:
	}
	// Jobs of other versions don't show.
	if r := h.get("/api/notes?v=v5", nil); !strings.Contains(string(r.body), `"jobs":[]`) {
		t.Errorf("v5 sidebar = %s", r.body)
	}
}

type notesFunc func(v family.Version)

func (f notesFunc) Build(_ context.Context, v family.Version, _ []family.Version) (project.Run, error) {
	f(v)
	return project.Run{}, nil
}

func TestDoneIsIdempotent(t *testing.T) {
	h, f := startM2(t, Options{}) // no engines: jobs stay queued
	h.saveTake(take, "")
	first := h.post("/api/session/"+take+"/done", "", nil)
	second := h.post("/api/session/"+take+"/done", "", nil)
	if first.code != 200 || !bytes.Equal(first.body, second.body) {
		t.Fatalf("done twice: %s / %s", first.body, second.body)
	}
	st, err := project.OpenExisting(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	all, _ := st.Jobs(0)
	if len(all) != 2 {
		t.Fatalf("jobs = %+v", all)
	}
	if all[1].Args.Dir != filepath.Join(f.root, "v6", "clip_v6.review", take) || all[1].Args.Version != "v6" {
		t.Errorf("transcribe args = %+v", all[1].Args)
	}
}

func TestDoneRunsTheNotesJob(t *testing.T) {
	built := make(chan string, 4)
	engines := &jobs.Engines{
		Transcriber: func() (jobs.TakeTranscriber, error) { return takeFunc(func(string) error { return nil }), nil },
		Notes:       notesFunc(func(v family.Version) { built <- v.Label }),
	}
	h, _ := startM2(t, Options{Jobs: engines})
	h.saveTake(take, "v7")
	if r := h.post("/api/session/"+take+"/done?v=v7", "", nil); r.code != 200 {
		t.Fatalf("done: %d %s", r.code, r.body)
	}
	select {
	case v := <-built:
		if v != "v7" {
			t.Fatalf("notes ran for %s", v)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the notes job didn't run")
	}
}

type takeFunc func(dir string) error

func (f takeFunc) Take(_ context.Context, dir string) error { return f(dir) }

func TestImages(t *testing.T) {
	h, f := startM2(t, Options{})
	base := "/api/session/" + take + "/image?v=v6&name="
	for _, tc := range []struct {
		name, ctype string
		body        []byte
	}{
		{"img_01.png", "image/png", pngBody},
		{"img_02.jpg", "image/jpeg", jpgBody},
		{"img_03.gif", "image/gif", gifBody},
		{"img_04.webp", "image/webp; charset=binary", webpBody},
	} {
		r := h.postBytes(base+tc.name, tc.body, map[string]string{"Content-Type": tc.ctype, "Origin": strings.TrimSuffix(h.url, "/")})
		if r.code != 200 || !strings.Contains(string(r.body), fmt.Sprintf(`"size":%d`, len(tc.body))) {
			t.Fatalf("%s: %d %s", tc.name, r.code, r.body)
		}
		got, err := os.ReadFile(filepath.Join(f.root, "v6", "clip_v6.review", take, tc.name))
		if err != nil || !bytes.Equal(got, tc.body) {
			t.Fatalf("%s on disk: %v", tc.name, err)
		}
	}
	png := map[string]string{"Content-Type": "image/png"}
	for _, tc := range []struct {
		path  string
		body  []byte
		hdr   map[string]string
		code  int
		error string
	}{
		{base + "img_1.png", pngBody, png, 400, "bad name"},
		{base + "../x.png", pngBody, png, 400, "bad name"},
		{base + "img_05.png", pngBody, map[string]string{"Content-Type": "image/jpeg"}, 415, "bad type"},
		{base + "img_05.png", pngBody, nil, 415, "bad type"},
		{base + "img_05.png", jpgBody, png, 415, "bad type"}, // the bytes aren't a PNG
		{base + "img_05.png", []byte{}, png, 415, "bad type"},
		{base + "img_05.png", pngBody, map[string]string{"Content-Type": "image/png", "Origin": "http://evil.example"}, 403, "bad origin"},
		{"/api/session/not-a-take/image?name=img_05.png", pngBody, png, 404, "not found"},
		{"/api/session/" + take + "/image?v=v99&name=img_05.png", pngBody, png, 400, "bad version"},
		{base + "img_05.png", append(slices.Clone(pngBody), make([]byte, MaxImage)...), png, 413, "body too large"},
	} {
		r := h.postBytes(tc.path, tc.body, tc.hdr)
		if r.code != tc.code || !strings.Contains(string(r.body), tc.error) {
			t.Errorf("POST %s: %d %s, want %d %s", tc.path, r.code, r.body, tc.code, tc.error)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "v6", "clip_v6.review", take, "img_05.png")); err == nil {
		t.Error("a refused image was written")
	}

	// GET /api/takes/<take>/<file> serves them back.
	r := h.get("/api/takes/"+take+"/img_01.png?v=v6", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "image/png" || !bytes.Equal(r.body, pngBody) {
		t.Fatalf("GET image: %d %q", r.code, r.header.Get("Content-Type"))
	}
	r = h.get("/api/takes/"+take+"/img_04.webp", map[string]string{"Range": "bytes=0-3"})
	if r.code != 206 || string(r.body) != "RIFF" || r.header.Get("Content-Type") != "image/webp" {
		t.Errorf("ranged GET: %d %q", r.code, r.body)
	}
	for _, tc := range []struct {
		path string
		code int
		err  string
	}{
		{"/api/takes/" + take + "/img_09.png", 404, "missing"},
		{"/api/takes/" + take + "/session.json", 404, "not found"},
		{"/api/takes/" + take + "/..%2fx.png", 404, "not found"},
		{"/api/takes/nope/img_01.png", 404, "not found"},
		{"/api/takes/" + take + "/img_01.png?v=v99", 404, "no such version"},
		{"/api/takes/" + take + "/img_01.png?v=v5", 404, "missing"},
	} {
		if r := h.get(tc.path, nil); r.code != tc.code || !strings.Contains(string(r.body), tc.err) {
			t.Errorf("GET %s: %d %s", tc.path, r.code, r.body)
		}
	}
}

func TestHistoryImages(t *testing.T) {
	h, f := startM2(t, Options{})
	own := filepath.Join(f.root, "v6", "clip_v6.review", take)
	plain := filepath.Join(f.root, "v6", "clip_v6.review", "2026-10-29_21-00-00")
	carried := filepath.Join(f.root, "v5", "clip_v5.review", "2026-10-01_10-00-00")
	for dir, images := range map[string]string{
		own:     `[{"seg": "S1", "file": "img_01.png", "video_t": 2.5, "box": [0.1, 0.2, 0.3, 0.4]}]`,
		carried: `[{"seg": "S1", "file": "img_01.png", "video_t": 3, "box": [0, 0, 1, 1]}, {"seg": "S1", "file": "img_02.png", "video_t": 50, "box": [0, 0, 1, 1]}]`,
		plain:   ``,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if images != "" {
			if err := os.WriteFile(filepath.Join(dir, "images.json"), []byte(images), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	inherit := fmt.Sprintf(`{"takes": [{"dir": %q, "version": "v5", "ranges": [[0, 10]]}]}`, carried)
	if err := os.WriteFile(filepath.Join(f.root, "v6", "clip_v6.review", "inherit.json"), []byte(inherit), 0o600); err != nil {
		t.Fatal(err)
	}
	var hist historyResponse
	r := h.get("/api/history?v=v6", nil)
	if err := json.Unmarshal(r.body, &hist); err != nil || len(hist.History) != 3 {
		t.Fatalf("history = %s", r.body)
	}
	if imgs := hist.History[0].Images; len(imgs) != 1 || imgs[0].URL != "/api/takes/"+take+"/img_01.png?v=v6" ||
		imgs[0].VideoT != 2.5 || imgs[0].Box != [4]float64{0.1, 0.2, 0.3, 0.4} {
		t.Errorf("own take's images = %+v", imgs)
	}
	if strings.Contains(string(r.body), `"id":"2026-10-29_21-00-00","inherited_from":null,"voice":{},"strokes":[]}`) == false {
		t.Errorf("a take without images changed shape: %s", r.body)
	}
	if imgs := hist.History[2].Images; len(imgs) != 1 || imgs[0].URL != "/api/takes/2026-10-01_10-00-00/img_01.png?v=v5" {
		t.Errorf("carried take's images (only inside its ranges) = %+v", imgs)
	}
	if info := h.get("/api/info", nil); !strings.Contains(string(info.body), `"/api/takes/`+take+`/img_01.png?v=v6"`) {
		t.Errorf("/api/info history has no images: %s", info.body)
	}
}

// seed fills the project store: notes on v6 (one with an image) and on v7, the newest.
func seed(t *testing.T, f *fixture) *project.Store {
	t.Helper()
	st, err := project.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := filepath.Join(f.root, "v6", "clip_v6.review", take)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images.json"),
		[]byte(`[{"seg": "S1", "file": "img_01.png", "video_t": 1.5, "box": [0.1, 0.2, 0.3, 0.4]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img_01.png"), pngBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sync(project.Run{Version: "v6", Comments: []project.Comment{
		{Take: take, Seg: "S1", N: 0, T0: 1, T1: 2, Text: "bigger title", Author: "Ana"},
		{Take: take, Seg: "S1", N: 1, T0: 3, T1: 4, Text: "cut the pause", Author: "Ana"},
		{Take: take, Seg: "S2", N: 0, T0: 5, T1: 6, Text: "music", Author: "Ana"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordPush(project.Push{Label: "v7", Path: "/x", Parent: "v6", Scope: project.Scope{Version: "v6"},
		Changelog: []project.ChangeEntry{{Note: 1, Summary: "title at 120%"}, {Note: 2, Summary: "pause cut"}},
		Asks:      []project.PushAsk{{Note: 3, Text: "which track instead?"}}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSidebarRoute(t *testing.T) {
	h, f := startM2(t, Options{})
	// No store yet: empty.
	r := h.get("/api/notes?v=v6", nil)
	if r.code != 200 || string(r.body) != `{"version":"v6","newest":false,"items":[],"jobs":[]}` {
		t.Fatalf("empty sidebar: %d %s", r.code, r.body)
	}
	if r := h.get("/api/notes", nil); !strings.Contains(string(r.body), `"version":"v6","newest":false`) {
		t.Errorf("no v = the current version: %s", r.body)
	}
	if r := h.get("/api/notes?v=v99", nil); r.code != 404 || !strings.Contains(string(r.body), "no such version") {
		t.Errorf("unknown version: %d %s", r.code, r.body)
	}
	seed(t, f)

	var older sidebarResponse
	r = h.get("/api/notes?v=v6", nil)
	if err := json.Unmarshal(r.body, &older); err != nil || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("v6: %s", r.body)
	}
	if older.Newest || len(older.Items) != 3 {
		t.Fatalf("v6 sidebar = %s", r.body)
	}
	it := older.Items[0]
	if it.Take != take || it.Seg != "S1" || it.Kind != "note" || it.Status != project.StatusAnswered || it.Change != "title at 120%" || it.AnsweredIn != "v7" ||
		it.By != "Ana" || len(it.Images) != 1 || it.Images[0].Box != [4]float64{0.1, 0.2, 0.3, 0.4} {
		t.Fatalf("answered item = %+v", it)
	}
	if img := h.get(it.Images[0].URL, nil); img.code != 200 || !bytes.Equal(img.body, pngBody) {
		t.Errorf("the item's image URL %s: %d", it.Images[0].URL, img.code)
	}
	if q := older.Items[2]; q.Status != project.StatusQuestion || q.Question != "which track instead?" || q.Change != "" {
		t.Errorf("asked item = %+v", q)
	}

	var newest sidebarResponse
	r = h.get("/api/notes?v=v7", nil)
	if err := json.Unmarshal(r.body, &newest); err != nil || !newest.Newest {
		t.Fatalf("v7: %s", r.body)
	}
	if len(newest.Items) != 1 || newest.Items[0].Kind != "question" || newest.Items[0].Note == nil ||
		*newest.Items[0].Note != 3 || *newest.Items[0].T0 != 5 || newest.Items[0].By != "agent" {
		t.Fatalf("v7 sidebar (only open questions) = %s", r.body)
	}
}

func TestReopenAndApprove(t *testing.T) {
	h, f := startM2(t, Options{})
	if r := h.post("/api/notes/1/reopen", "", nil); r.code != 404 || !strings.Contains(string(r.body), "no such note") {
		t.Fatalf("reopen without a store: %d %s", r.code, r.body)
	}
	if r := h.post("/api/versions/v7/approve", "", nil); r.code != 200 || string(r.body) != `{"ok":true,"accepted":[]}` {
		t.Fatalf("approve without a store: %d %s", r.code, r.body)
	}
	st := seed(t, f)
	for _, tc := range []struct {
		path string
		hdr  map[string]string
		code int
		body string
	}{
		{"/api/notes/1/reopen", map[string]string{"Origin": "http://evil.example"}, 403, `{"error":"bad origin"}`},
		{"/api/notes/1/reopen", h.origin(), 200, `{"ok":true,"id":1,"status":"open"}`},
		{"/api/notes/1/reopen", nil, 409, `note #1 is open; it can't become open`},
		{"/api/notes/3/reopen", nil, 409, `note #3 is question`},
		{"/api/notes/99/reopen", nil, 404, `{"error":"no such note"}`},
		{"/api/notes/x/reopen", nil, 404, `{"error":"no such note"}`},
		{"/api/notes/01/reopen", nil, 404, `{"error":"no such note"}`},
		{"/api/notes/1/close", nil, 404, `{"error":"not found"}`},
		{"/api/versions/v7/approve", map[string]string{"Origin": "http://evil.example"}, 403, `{"error":"bad origin"}`},
		{"/api/versions/v7/approve", nil, 200, `{"ok":true,"accepted":[2]}`},
		{"/api/versions/v7/approve", nil, 200, `{"ok":true,"accepted":[]}`},
		{"/api/versions/v99/approve", nil, 404, `{"error":"no such version"}`},
	} {
		r := h.post(tc.path, "", tc.hdr)
		if r.code != tc.code || !strings.Contains(string(r.body), tc.body) {
			t.Errorf("POST %s: %d %s, want %d %s", tc.path, r.code, r.body, tc.code, tc.body)
		}
	}
	for id, want := range map[int64]project.Status{1: project.StatusOpen, 2: project.StatusAccepted} {
		if n, _ := st.Note(id); n.Status != want {
			t.Errorf("note %d is %s, want %s", id, n.Status, want)
		}
	}
}

func TestNewRoutesCheckTheHost(t *testing.T) {
	h, _ := startM2(t, Options{})
	try := func(method, path string) (int, string) {
		req, err := http.NewRequest(method, h.url+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "evil.example"
		res, err := h.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var b bytes.Buffer
		_, _ = b.ReadFrom(res.Body)
		return res.StatusCode, b.String()
	}
	for _, p := range []string{"/api/notes", "/api/events", "/api/takes/" + take + "/img_01.png"} {
		if code, body := try(http.MethodGet, p); code != 403 || body != `{"error":"bad host"}` {
			t.Errorf("GET %s with a foreign Host: %d %s", p, code, body)
		}
	}
	for _, p := range []string{"/api/notes/1/reopen", "/api/versions/v7/approve", "/api/session/" + take + "/done",
		"/api/session/" + take + "/image?name=img_01.png"} {
		if code, _ := try(http.MethodPost, p); code != 403 {
			t.Errorf("POST %s with a foreign Host: %d", p, code)
		}
	}
}

type sseEvent struct{ id, event, data string }

// stream opens an event stream and returns its events as they come.
func (h *harness) stream(path string, hdr map[string]string) (http.Header, <-chan sseEvent, context.CancelFunc) {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := h.ts.Client().Do(req) //nolint:bodyclose // readEvents closes it when the stream ends
	if err != nil {
		h.t.Fatal(err)
	}
	ch := make(chan sseEvent, 64)
	go readEvents(res, ch)
	h.t.Cleanup(cancel)
	return res.Header, ch, cancel
}

// readEvents parses an event stream into ch until it ends, then closes the body and ch.
func readEvents(res *http.Response, ch chan<- sseEvent) {
	defer close(ch)
	defer func() { _ = res.Body.Close() }()
	sc := bufio.NewScanner(res.Body)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.event != "" {
				ch <- ev
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, "id: "):
			ev.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			ev.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			ev.data = line[6:]
		}
	}
}

func next(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("the stream ended")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	return sseEvent{}
}

func TestEventStream(t *testing.T) {
	h, f := startM2(t, Options{KeepAlive: 10 * time.Millisecond})
	if r := h.get("/api/events?since=x", nil); r.code != 400 || string(r.body) != `{"error":"bad since"}` {
		t.Fatalf("bad since: %d %s", r.code, r.body)
	}
	if r := h.get("/api/events", map[string]string{"Last-Event-ID": "-1"}); r.code != 400 {
		t.Fatalf("bad Last-Event-ID: %d %s", r.code, r.body)
	}

	// A stream opened before the store exists picks it up once Done creates it.
	hdr, live, _ := h.stream("/api/events", nil)
	if hdr.Get("Content-Type") != "text/event-stream" || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", hdr)
	}
	h.saveTake(take, "v6")
	if r := h.post("/api/session/"+take+"/done", "", nil); r.code != 200 {
		t.Fatalf("done: %d", r.code)
	}
	e1, e2 := next(t, live), next(t, live)
	if e1.id != "1" || e1.event != "job" || e2.id != "2" || e2.event != "job" {
		t.Fatalf("events = %+v %+v", e1, e2)
	}
	var je project.JobEvent
	if err := json.Unmarshal([]byte(e1.data), &je); err != nil || je.Kind != "transcribe" || je.State != "queued" || je.Take != take {
		t.Fatalf("job event = %s", e1.data)
	}

	// Another process writes (the agent asking, say): the stream finds it by polling.
	other := seed(t, f)
	ev := next(t, live)
	for ev.event != "version" {
		ev = next(t, live)
	}
	if !strings.Contains(ev.data, `"label":"v7"`) {
		t.Errorf("version event = %+v", ev)
	}
	last, _ := other.LastSeq()

	// ?since resumes after a seq; Last-Event-ID (a reconnect) wins over it.
	_, resumed, _ := h.stream("/api/events?since=1", nil)
	if ev := next(t, resumed); ev.id != "2" {
		t.Errorf("since=1 resumed at %+v", ev)
	}
	_, reconnected, _ := h.stream("/api/events?since=0", map[string]string{"Last-Event-ID": fmt.Sprint(last - 1)})
	if ev := next(t, reconnected); ev.id != fmt.Sprint(last) {
		t.Errorf("Last-Event-ID resumed at %+v", ev)
	}
	// No resume point: only what comes next.
	_, fresh, _ := h.stream("/api/events", nil)
	if _, err := other.Reopen(1); err != nil {
		t.Fatal(err)
	}
	ev = next(t, fresh)
	if ev.event != "notes" || ev.id != fmt.Sprint(last+1) || !strings.Contains(ev.data, `"why":"reopen"`) {
		t.Errorf("fresh stream got %+v", ev)
	}

	// Closing the server ends the streams.
	done := make(chan struct{})
	go func() {
		for range fresh {
		}
		close(done)
	}()
	_ = h.srv.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the server")
	}
}

func TestEventStreamKeepAlive(t *testing.T) {
	h, _ := startM2(t, Options{KeepAlive: 5 * time.Millisecond})
	res, err := h.ts.Client().Get(h.url + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	r := bufio.NewReader(res.Body)
	var lines []string
	for len(lines) < 3 {
		l, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	if lines[0] != "retry: 2000" || lines[2] != ": keep-alive" {
		t.Fatalf("stream starts %q", lines)
	}
}

func TestContinuousDiscussionIncludesResolvedComments(t *testing.T) {
	h, f := startM2(t, Options{})
	seed(t, f)
	r := h.get("/api/notes?v=v7&scope=video", nil)
	var res sidebarResponse
	if err := json.Unmarshal(r.body, &res); err != nil || r.code != 200 {
		t.Fatalf("%s", r.body)
	}
	if res.Scope != "video" || len(res.Items) != 4 {
		t.Fatalf("missing discussion: %s", r.body)
	}
	seen := map[string]bool{}
	for _, it := range res.Items {
		key := fmt.Sprintf("%s:%d", it.Kind, it.ID)
		if seen[key] {
			t.Fatalf("duplicate %s", key)
		}
		seen[key] = true
	}
}

func TestTranscribeBeforeDoneIsIdempotentAndKeepsTakeOpen(t *testing.T) {
	h, _ := startM2(t, Options{})
	root := "/api/session/" + take
	data := `{"session":{"format":3},"segments":[{"id":"S1","file":"seg_01.webm","recording":false},{"id":"S2","file":"seg_02.webm","recording":true}],"strokes":[]}`
	if r := h.post(root+"/data?v=v6", data, nil); r.code != 200 {
		t.Fatal(string(r.body))
	}
	if r := h.post(root+"/audio?v=v6&name=seg_01.webm&offset=0", "audio", nil); r.code != 200 {
		t.Fatal(string(r.body))
	}
	for range 2 {
		if r := h.post(root+"/transcribe?v=v6", "", nil); r.code != 200 {
			t.Fatal(string(r.body))
		}
	}
	st, err := h.srv.store(false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := st.Jobs(0)
	if err != nil || len(all) != 2 {
		t.Fatalf("jobs: %v %v", all, err)
	}
	for _, j := range all {
		if j.Kind == project.JobTranscribe && takeClosed(j.Args.Dir) {
			t.Fatal("clip closed the take")
		}
	}
	if r := h.post(root+"/done?v=v6", "", nil); r.code != 200 {
		t.Fatal(string(r.body))
	}
	all, err = st.Jobs(0)
	if err != nil || len(all) != 4 {
		t.Fatalf("final jobs: %v %v", all, err)
	}
}
