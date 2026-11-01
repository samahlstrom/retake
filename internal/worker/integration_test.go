package worker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
