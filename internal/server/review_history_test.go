package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/transcribe"
)

func TestReviewScopeKeepsOriginsAndExcludesFutureFeedback(t *testing.T) {
	h, f := startM2(t, Options{})
	st := seed(t, f)
	if _, err := st.Sync(project.Run{Version: "v7", Comments: []project.Comment{{Take: take, Seg: "S3", N: 0, Text: "new feedback", T0: 9, T1: 10}}}); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v6", "v7"} {
		r := h.get("/api/notes?v="+version+"&scope=review", nil)
		var res sidebarResponse
		if err := json.Unmarshal(r.body, &res); err != nil || r.code != 200 {
			t.Fatal(string(r.body))
		}
		if res.Scope != "review" {
			t.Fatal(res.Scope)
		}
		resolved, current := 0, 0
		for _, item := range res.Items {
			if item.Version == "" {
				t.Fatal("missing origin")
			}
			if version == "v6" && item.Version == "v7" {
				t.Fatal("future feedback leaked")
			}
			if item.Status == project.StatusAnswered && item.AnsweredIn == "v7" {
				resolved++
			}
			if item.Version == "v7" && item.Text == "new feedback" {
				current++
			}
		}
		if resolved != 2 || version == "v7" && current != 1 {
			t.Fatalf("missing progress: %s", r.body)
		}
	}
}

func TestReviewHistoryCarriesGeometryAndImagesOnlyNearRelevantNotes(t *testing.T) {
	h, f := startM2(t, Options{})
	seed(t, f)
	dir := filepath.Join(f.root, "v6", "clip_v6.review", take)
	raw := `[{"id":1,"video_t":1.5,"color":"yellow","points":[[0.1,0.2],[0.8,0.7]]},{"id":2,"video_t":100,"color":"red","points":[[0,0],[1,1]]}]`
	if err := os.WriteFile(filepath.Join(dir, "strokes.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	r := h.get("/api/history?v=v7&scope=review", nil)
	var res historyResponse
	if err := json.Unmarshal(r.body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.History) != 1 {
		t.Fatalf("history: %s", r.body)
	}
	entry := res.History[0]
	if entry.SourceVersion != "v6" || len(entry.Strokes) != 1 || string(entry.Strokes[0].Points) != "[[0.1,0.2],[0.8,0.7]]" || len(entry.Images) != 1 {
		t.Fatalf("missing annotation: %s", r.body)
	}
}

func TestVoiceCompletionChecksEveryClipAndCurrentBytes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("segments.json", `[{"id":"S1","file":"seg_01.webm","recording":false,"voice":{"bin":0.25,"bins":[[4,1]]}},{"id":"S2","file":"seg_02.webm","recording":true,"voice":{"bin":0.25,"bins":[[4,2]]}}]`)
	write("seg_01.webm", "audio")
	write("seg_02.webm", "audio")
	tr := transcribe.NewTranscript()
	if err := tr.Set("seg_01.webm", transcribe.FileTranscript{Size: 5, Model: "test", Duration: 1, Words: []transcribe.Word{}}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Write(filepath.Join(dir, transcribe.TranscriptFile)); err != nil {
		t.Fatal(err)
	}
	done, tracks := completedVoice(dir)
	if done || len(tracks) != 2 || !tracks[0].Transcribed || tracks[1].Transcribed {
		t.Fatalf("bad partial status: %v %+v", done, tracks)
	}
	write("segments.json", `[{"id":"S1","file":"seg_01.webm","recording":false}]`)
	if done, _ := completedVoice(dir); !done {
		t.Fatal("legacy completed audio remains pending")
	}
	write("seg_01.webm", "more audio")
	if done, _ := completedVoice(dir); done {
		t.Fatal("stale transcript hides new audio")
	}
}
