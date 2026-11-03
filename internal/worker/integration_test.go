package worker

import (
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
)

func TestWorkerStreamsEditsFromSeparateMachineAndSwitchesProvider(t *testing.T) {
	editor, e := filepath.Abs("../../editor/target/release/retake-edit")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(editor); e != nil {
		t.Skip("build retake-edit to run the live renderer integration test")
	}
	scratch, e := filepath.Abs("../../.retake-work")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(scratch, 0o700); e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(scratch, "worker-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	local, hosted := filepath.Join(base, "local"), filepath.Join(base, "hosted")
	testmedia.Video(t, filepath.Join(local, "voice.mp4"), 6)
	timeline := `{"schema":"timeline/0.1","id":"worker-fixture","rev":1,"format":{"size":[320,180],"fps":12,"sample_rate":48000,"background":"#000000"},"assets":{"vo":{"path":"voice.mp4"}},"spine":[{"id":"a","asset":"vo","in":0,"out":2},{"id":"b","asset":"vo","in":2,"out":4},{"id":"c","asset":"vo","in":4,"out":6}],"scenes":[{"id":"scene","shots":[{"id":"first","at":"start:a","picture":{"background":"#aa3322","layers":[]}},{"id":"second","at":"start:b","picture":{"background":"#336633","layers":[]}},{"id":"third","at":"start:c","picture":{"background":"#333333","layers":[]}}]}]}`
	if e = os.WriteFile(filepath.Join(local, "timeline.json"), []byte(timeline), 0o600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(local, "retake.toml"), []byte("[edit]\ntimeline = 'timeline.json'\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	st, e := project.Open(hosted)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	token, hash, e := team.NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.CreateToken(project.NewToken{Hash: hash, Role: "agent"}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv := server.New(server.Options{Root: hosted, Store: st, Port: ts.Listener.Addr().(*net.TCPAddr).Port, FollowNewest: true})
	ts.Config.Handler = srv
	ts.Start()
	defer ts.Close()
	defer func() { _ = srv.Close() }()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	profiles := []Profile{{AgentProfile: project.AgentProfile{ID: "limited", Name: "Limited provider"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "limited"}}, {AgentProfile: project.AgentProfile{ID: "editor", Name: "Fixture editor"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "edit"}}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Options{URL: ts.URL, Token: token, Root: local, Name: "Test machine", Profiles: profiles, Editor: bridge.Editor{Configured: editor}})
	}()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(label string, test func(project.AutomationState) bool) project.AutomationState {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		var a project.AutomationState
		for time.Now().Before(deadline) {
			a, e = st.Automation()
			if e != nil {
				t.Fatal(e)
			}
			if test(a) {
				return a
			}
			select {
			case err := <-finished:
				t.Fatalf("worker exited: %v", err)
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		b, _ := json.Marshal(a)
		t.Fatalf("waiting for %s: %s", label, b)
		return a
	}
	a := wait("initial live render", func(a project.AutomationState) bool { return len(a.Renders) > 0 && a.Renders[0].Complete })
	initial := a.Renders[0]
	get := func(path string) []byte {
		t.Helper()
		res, e := http.Get(ts.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = res.Body.Close() }()
		b, e := io.ReadAll(res.Body)
		if e != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("media %s: %d %s %v", path, res.StatusCode, b, e)
		}
		return b
	}
	playlist := get("/video/" + initial.Label + "/index.m3u8")
	if !strings.Contains(string(playlist), "#EXT-X-ENDLIST") {
		t.Fatal("not a complete HLS playlist")
	}
	before := get("/video/" + initial.Label + "/seg_00000.m4s")
	if _, e = os.Stat(filepath.Join(hosted, "voice.mp4")); !os.IsNotExist(e) {
		t.Fatal("source was copied to host")
	}
	_, e = st.Sync(project.Run{Version: initial.Label, Comments: []project.Comment{{Key: "blue", Text: "Make the first shot blue", T0: 0, T1: 1, Detail: project.Detail{Strokes: []project.Stroke{{Shape: "circle", Color: "red", Box: [4]float64{.1, .1, .3, .3}}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		a.Config.Enabled = true
		a.Config.Agent = "limited"
		a.Config.Fallback = "editor"
		return true, nil
	}); e != nil {
		t.Fatal(e)
	}
	queued, e := st.QueueFeedback([]int64{1}, "")
	if e != nil {
		t.Fatal(e)
	}
	a = wait("fallback edit completes", func(a project.AutomationState) bool { return len(a.Tasks) > 0 && a.Tasks[0].State == "completed" })
	task := a.Tasks[0]
	if task.ID != queued[0].ID || task.Agent != "editor" || task.Attempt != 2 {
		t.Fatalf("handoff lost task: %+v", task)
	}
	result := a.Render(task.Result)
	if result == nil || !result.Complete || result.Reused < 2 || result.Rendered != 1 {
		t.Fatalf("incremental reuse: %+v", result)
	}
	after := get("/video/" + result.Label + "/seg_00000.m4s")
	if string(before) == string(after) {
		t.Fatal("edited segment did not change")
	}
	if string(before) != string(get("/video/"+initial.Label+"/seg_00000.m4s")) {
		t.Fatal("historical revision mutated")
	}
	n, e := st.Note(1)
	if e != nil || n.Status != project.StatusAnswered || n.AnsweredIn != result.Label {
		t.Fatal(n, e)
	}
	probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", ts.URL+"/video/"+result.Label+"/index.m3u8")
	out, e := probe.CombinedOutput()
	if e != nil || !strings.Contains(string(out), "6.000000") {
		t.Fatalf("relayed media doesn't decode: %s %v", out, e)
	}
}

// A folder of finished videos on one computer, reviewed on a server: the worker relays its version files (metadata
// to the server, bytes on request) and, without a timeline, its agent writes a video that becomes the next version.
func TestPlainProjectRelaysItsVersionsAndPushesTheAgentsVideo(t *testing.T) {
	testmedia.Need(t)
	scratch, e := filepath.Abs("../../.retake-work")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(scratch, 0o700); e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(scratch, "plain-worker-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	local, hosted := filepath.Join(base, "local"), filepath.Join(base, "hosted")
	testmedia.Video(t, filepath.Join(local, "clip_v1.mp4"), 2)
	testmedia.Video(t, filepath.Join(local, "clip_v2.mp4"), 3)
	v2, e := os.ReadFile(filepath.Join(local, "clip_v2.mp4"))
	if e != nil {
		t.Fatal(e)
	}
	st, e := project.Open(hosted)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	token, hash, e := team.NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.CreateToken(project.NewToken{Hash: hash, Role: "agent"}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv := server.New(server.Options{Root: hosted, Store: st, Port: ts.Listener.Addr().(*net.TCPAddr).Port, FollowNewest: true})
	ts.Config.Handler = srv
	ts.Start()
	defer ts.Close()
	defer func() { _ = srv.Close() }()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	profiles := []Profile{
		{AgentProfile: project.AgentProfile{ID: "lazy", Name: "Lazy"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "plain-lazy"}},
		{AgentProfile: project.AgentProfile{ID: "editor", Name: "Editor"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "plain-edit"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		// No renderer: a plain project doesn't need one.
		finished <- Run(ctx, Options{URL: ts.URL, Token: token, Root: local, Name: "Test machine", Profiles: profiles,
			Editor: bridge.Editor{Configured: filepath.Join(base, "no-retake-edit"), Env: []string{"RETAKE_ENCODER=x264"}}})
	}()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(label string, test func(project.AutomationState) bool) project.AutomationState {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			a, err := st.Automation()
			if err != nil {
				t.Fatal(err)
			}
			if test(a) {
				return a
			}
			select {
			case err := <-finished:
				t.Fatalf("worker exited: %v", err)
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		a, _ := st.Automation()
		b, _ := json.Marshal(a)
		t.Fatalf("waiting for %s: %s", label, b)
		return project.AutomationState{}
	}
	a := wait("the version files", func(a project.AutomationState) bool { return len(a.ListedFiles()) == 2 })
	if f := a.FileVersion("v2"); f == nil || f.Name != "clip_v2.mp4" || f.Size != int64(len(v2)) || f.Duration < 2.9 {
		t.Fatalf("v2: %+v", f)
	}
	fetch := func(path, rng string) (int, string, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Range"), b
	}
	if code, cr, b := fetch("/video/v2", "bytes=100-"); code != 206 || cr != "bytes 100-"+strconv.Itoa(len(v2)-1)+"/"+strconv.Itoa(len(v2)) || string(b) != string(v2[100:]) {
		t.Fatalf("v2 through the relay: %d %s", code, cr)
	}
	if _, e = os.Stat(filepath.Join(hosted, "clip_v2.mp4")); !os.IsNotExist(e) {
		t.Fatal("a version file was copied to the server")
	}
	// Download v2 (spec/api.md, "Download"): the computer makes the MP4 (this clip isn't High profile), the server
	// relays it as an attachment, and nothing of it is kept on the server but its relay cache.
	if res, err := http.Post(ts.URL+"/api/download?v=v2", "", nil); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/download: %v", err)
	} else {
		_ = res.Body.Close()
	}
	var ready struct {
		State string `json:"state"`
		Size  int64  `json:"size"`
		Name  string `json:"name"`
	}
	for deadline := time.Now().Add(time.Minute); ready.State != "ready"; {
		if time.Now().After(deadline) {
			t.Fatalf("the download never got ready: %+v", ready)
		}
		_, _, b := fetch("/api/download?v=v2", "")
		if e = json.Unmarshal(b, &ready); e != nil || ready.State == "failed" {
			t.Fatalf("%s %v", b, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	res, e := http.Get(ts.URL + "/video/v2/download")
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || int64(len(got)) != ready.Size || ready.Name != "clip_v2.mp4" ||
		res.Header.Get("Content-Disposition") != `attachment; filename="clip_v2.mp4"; filename*=UTF-8''clip_v2.mp4` ||
		res.Header.Get("Content-Length") != strconv.Itoa(len(got)) {
		t.Fatalf("download through the relay: %d %v, %d bytes of %d", res.StatusCode, res.Header, len(got), ready.Size)
	}
	dl := filepath.Join(base, "clip_v2_download.mp4")
	if e = os.WriteFile(dl, got, 0o600); e != nil {
		t.Fatal(e)
	}
	if out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,profile,sample_rate",
		"-of", "csv=p=0", dl).Output(); err != nil || strings.TrimSpace(string(out)) != "h264,High\naac,LC,48000" {
		t.Fatalf("the download: %q %v", out, err)
	}
	if _, e = os.Stat(filepath.Join(local, ".retake", "downloads")); e != nil {
		t.Fatal("the download wasn't made on the computer:", e)
	}

	// A note on v2, sent to an agent that says it's done without a video: the task pauses, nothing is answered.
	if _, e = st.Sync(project.Run{Version: "v2", Comments: []project.Comment{{Key: "intro", Text: "Cut the first second", T0: 0, T1: 1}}}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) { a.Config.Agent = "lazy"; return true, nil }); e != nil {
		t.Fatal(e)
	}
	queued, e := st.QueueFeedback([]int64{1}, "")
	if e != nil {
		t.Fatal(e)
	}
	a = wait("the lazy agent's pause", func(a project.AutomationState) bool { return a.Tasks[0].State == "paused" })
	if a.Tasks[0].Error != "The agent finished without writing a new version." {
		t.Fatalf("pause: %q", a.Tasks[0].Error)
	}
	// Retried with an agent that writes $RETAKE_OUTPUT: it becomes v3, next to the others, and answers the note.
	if e = st.ControlTask(queued[0].ID, "retry", "editor", ""); e != nil {
		t.Fatal(e)
	}
	a = wait("the edit", func(a project.AutomationState) bool { return a.Tasks[0].State == "completed" })
	if a.Tasks[0].Result != "v3" {
		t.Fatalf("result %q", a.Tasks[0].Result)
	}
	n, e := st.Note(1)
	if e != nil || n.Status != project.StatusAnswered || n.AnsweredIn != "v3" || n.Change != "Cut the first second of v2" {
		t.Fatalf("%+v %v", n, e)
	}
	v3, e := os.ReadFile(filepath.Join(local, "clip_v3.mp4"))
	if e != nil {
		t.Fatal("the agent's video isn't the next version:", e)
	}
	if code, _, b := fetch("/video/v3", "bytes=0-"); code != 206 || string(b) != string(v3) {
		t.Fatalf("v3 through the relay: %d", code)
	}
	if fv := a.FileVersion("v3"); fv == nil || fv.Duration < 1.9 || fv.Duration > 2.1 {
		t.Fatalf("v3: %+v", fv)
	}
}

// A note whose saved still the server can't give (its project was moved, say) never pauses the task: the worker makes
// its own from the version on this computer, at the note's moment, with the note's pen lines drawn on it, and says so
// in the note's context.
func TestMissingStillNeverStopsTheAgent(t *testing.T) {
	testmedia.Need(t)
	scratch, e := filepath.Abs("../../.retake-work")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(scratch, 0o700); e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(scratch, "still-worker-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	local, hosted := filepath.Join(base, "local"), filepath.Join(base, "hosted")
	testmedia.Video(t, filepath.Join(local, "clip_v1.mp4"), 3)
	st, e := project.Open(hosted)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = st.Close() }()
	token, hash, e := team.NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.CreateToken(project.NewToken{Hash: hash, Role: "agent"}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv := server.New(server.Options{Root: hosted, Store: st, Port: ts.Listener.Addr().(*net.TCPAddr).Port, FollowNewest: true})
	ts.Config.Handler = srv
	ts.Start()
	defer ts.Close()
	defer func() { _ = srv.Close() }()
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	profiles := []Profile{{AgentProfile: project.AgentProfile{ID: "still", Name: "Still"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "plain-still"}}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Options{URL: ts.URL, Token: token, Root: local, Name: "Test machine", Profiles: profiles,
			Editor: bridge.Editor{Configured: filepath.Join(base, "no-retake-edit")}})
	}()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(label string, test func(project.AutomationState) bool) project.AutomationState {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			a, err := st.Automation()
			if err != nil {
				t.Fatal(err)
			}
			if test(a) {
				return a
			}
			time.Sleep(100 * time.Millisecond)
		}
		a, _ := st.Automation()
		b, _ := json.Marshal(a)
		t.Fatalf("waiting for %s: %s", label, b)
		return project.AutomationState{}
	}
	wait("the version file", func(a project.AutomationState) bool { return len(a.ListedFiles()) == 1 })

	// Its still was saved where the project used to be: the server won't read outside the project.
	gone := filepath.Join(base, "old-place", "v1", "clip_v1.review", "snaps", "01_00-01-0.png")
	line := project.Stroke{Shape: "line", Color: "green", Box: [4]float64{0.1, 0.5, 0.8, 0}, Points: [][2]float64{{0.1, 0.5}, {0.9, 0.5}}, T: 1.5}
	if _, e = st.Sync(project.Run{Version: "v1", Comments: []project.Comment{
		{Key: "drawn", Text: "Cut where I drew", T0: 1, T1: 2, Detail: project.Detail{Snapshot: gone, Strokes: []project.Stroke{line}}},
		{Key: "words", Text: "And tighten the end", T0: 2, T1: 3},
	}}); e != nil {
		t.Fatal(e)
	}
	if e = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) { a.Config.Agent = "still"; return true, nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = st.QueueFeedback([]int64{1}, ""); e != nil { // note 2 rides along as context
		t.Fatal(e)
	}
	a := wait("the task", func(a project.AutomationState) bool {
		return len(a.Tasks) == 1 && (a.Tasks[0].State == "completed" || a.Tasks[0].State == "paused")
	})
	if a.Tasks[0].State != "completed" || a.Tasks[0].Result != "v2" {
		t.Fatalf("task %+v", a.Tasks[0])
	}
	f, e := os.Open(filepath.Join(local, "seen-still.png"))
	if e != nil {
		t.Fatal("the agent had no still:", e)
	}
	defer func() { _ = f.Close() }()
	img, e := png.Decode(f)
	if e != nil {
		t.Fatal(e)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2).RGBA()
	if b.Dx() < 64 || r>>8 != 48 || g>>8 != 209 || bl>>8 != 88 {
		t.Fatalf("the still is %v and its middle %d,%d,%d, not the green line", b, r>>8, g>>8, bl>>8)
	}
}
