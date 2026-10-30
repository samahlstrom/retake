package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/tus"
)

type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d, w, h := 10.0, 64, 36
	return probe.Info{Duration: &d, FPS: 30, Width: &w, Height: &h}
}

// server is a hub on one driver's stores.
type server struct {
	t   *testing.T
	h   *hub.Hub
	ts  *httptest.Server
	url string
}

func startHub(t *testing.T, st *hub.Stores) *server {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	url := "http://" + ts.Listener.Addr().String()
	h, err := hub.New(hub.Options{Stores: st, PublicURL: url, Prober: fakeProber{}, Version: "test",
		UI: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}, SyncEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.Start()
	s := &server{t: t, h: h, ts: ts, url: url}
	t.Cleanup(s.stop)
	return s
}

func (s *server) stop() {
	s.ts.Close()
	_ = s.h.Close()
}

func (s *server) do(method, path, body string, hdr map[string]string) (int, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.url+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// The team server on each driver: a take recorded through the page's API, a version pushed by upload, media served
// with ranges, and with object storage, everything in the bucket, served from it, and back after the workspace is
// lost.
func TestHubOnEachDriver(t *testing.T) {
	each(t, func(t *testing.T, d driver) {
		ctx := context.Background()
		cfg := hub.Config{Data: t.TempDir()}
		var bk blob.Store
		if d.postgres {
			cfg.Database = database(t)
			var c blob.S3Config
			bk, c = bucket(t)
			cfg.S3 = &c
		}
		st, err := hub.OpenStores(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		p, err := st.CreateProject(ctx, "demo", "Demo")
		if err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(t.TempDir(), "first.mp4")
		video := []byte(strings.Repeat("0123456789", 1000))
		_ = os.WriteFile(src, video, 0o600)
		ps, err := st.ProjectStore(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hub.FirstVersion(ps, st.Workspace("demo"), "demo", src, "", "admin", "", false); err != nil {
			t.Fatal(err)
		}
		agentTok, err := hub.MakeToken(ps, "", "demo", team.Agent, "editor", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		_ = ps.Close()
		u, _ := st.Accounts.CreateUser("ana@example.com", "Ana", false)
		_ = st.Accounts.SetMember(p.ID, u.ID, team.Reviewer)
		sess, _, _ := st.Accounts.StartSession(u.ID, "", "")

		s := startHub(t, st)
		page := map[string]string{"Cookie": hub.SessionCookie + "=" + sess, "Origin": s.url}
		take := "2026-10-30_10-00-00"
		if code, body := s.do("POST", "/p/demo/api/session/"+take+"/data?v=v1", `{"session":{"id":"`+take+
			`"},"segments":[{"id":"S1","t_start":0,"t_end":1}]}`, page); code != 200 {
			t.Fatalf("data: %d %s", code, body)
		}
		audio := strings.Repeat("A", 5000)
		if code, body := s.do("POST", "/p/demo/api/session/"+take+"/audio?v=v1&name=seg_01.webm&offset=0", audio,
			page); code != 200 {
			t.Fatalf("audio: %d %s", code, body)
		}
		if code, body := s.do("POST", "/p/demo/api/session/"+take+"/done?v=v1", "", page); code != 200 ||
			!strings.Contains(body, `"transcribe"`) {
			t.Fatalf("done: %d %s", code, body)
		}
		get := map[string]string{"Cookie": hub.SessionCookie + "=" + sess, "Range": "bytes=10-19"}
		if code, body := s.do("GET", "/p/demo/video", "", get); code != 206 || body != "0123456789" {
			t.Fatalf("range: %d %q", code, body)
		}

		// A version pushed by upload.
		v2 := filepath.Join(t.TempDir(), "v2.mp4")
		v2data := []byte(strings.Repeat("v2", 40_000))
		_ = os.WriteFile(v2, v2data, 0o600)
		sum := sha256.Sum256(v2data)
		up, err := (&tus.Client{Token: agentTok.Value}).Upload(ctx, s.url+"/p/demo/api/uploads", v2,
			map[string]string{"filename": "v2.mp4", "sha256": hex.EncodeToString(sum[:])}, "")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"upload": up[strings.LastIndex(up, "/")+1:]})
		if code, body := s.do("POST", "/p/demo/api/push", string(b), map[string]string{"Authorization": "Bearer " +
			agentTok.Value}); code != 200 || !strings.Contains(body, `"label":"v2"`) {
			t.Fatalf("push: %d %s", code, body)
		}
		if !d.postgres {
			return // local media: the workspace is the store
		}

		// Object storage holds the versions and the take, audio included.
		if err := s.h.SyncProject(ctx, "demo"); err != nil {
			t.Fatal(err)
		}
		for key, size := range map[string]int64{
			"projects/demo/media/demo_v1.mp4": int64(len(video)), "projects/demo/media/demo_v2.mp4": int64(len(v2data)),
			"projects/demo/media/demo_v1.review/" + take + "/seg_01.webm":  int64(len(audio)),
			"projects/demo/media/demo_v1.review/" + take + "/session.json": -1,
		} {
			info, err := bk.Stat(ctx, key)
			if err != nil || size >= 0 && info.Size != size {
				t.Errorf("%s: %+v %v", key, info, err)
			}
		}
		if objs, _ := bk.List(ctx, "projects/demo/.retake"); len(objs) != 0 {
			t.Errorf("the store's folder went to the bucket: %v", objs)
		}
		// Versions are served from the bucket: the workspace copy can go.
		ws := st.Workspace("demo")
		_ = os.WriteFile(filepath.Join(ws, "media", "demo_v1.mp4"), []byte("not what the bucket has"), 0o600)
		if code, body := s.do("GET", "/p/demo/video/v1", "", get); code != 206 || body != "0123456789" {
			t.Fatalf("from the bucket: %d %q", code, body)
		}
		_ = os.WriteFile(filepath.Join(ws, "media", "demo_v1.mp4"), video, 0o600)

		// The workspace is a cache: lose it, and the next server downloads it again.
		s.stop()
		if err := os.RemoveAll(ws); err != nil {
			t.Fatal(err)
		}
		s2 := startHub(t, st)
		code, body := s2.do("GET", "/p/demo/api/info", "", map[string]string{"Cookie": hub.SessionCookie + "=" + sess})
		if code != 200 || !strings.Contains(body, `"label":"v2"`) || !strings.Contains(body, `"label":"v1"`) {
			t.Fatalf("after losing the workspace: %d %s", code, body)
		}
		got, err := os.ReadFile(filepath.Join(ws, "media", "demo_v1.review", take, "seg_01.webm"))
		if err != nil || string(got) != audio {
			t.Fatalf("take audio restored: %v", err)
		}
		if _, err := accounts.NormalizeEmail(u.Email); err != nil {
			t.Fatal(err)
		}
	})
}
