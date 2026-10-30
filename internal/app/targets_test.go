package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/notes"
	"github.com/samahlstrom/retake/internal/project"
)

func f(x float64) jsonx.Float { return jsonx.Float(x) }

// The notes of a version that has a render manifest get the timeline elements under their strokes; notes of a
// version without one keep time and strokes only.
func TestAttachTargets(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1", "demo_v1.mp4")
	if err := os.MkdirAll(filepath.Dir(v1), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("..", "bridge", "testdata", "demo_v1.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v1", "demo_v1.manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	fam := []family.Version{{Label: "v1", Path: v1}, {Label: "v0", Path: filepath.Join(dir, "v0", "demo_v0.mp4")}}
	v0 := "v0"
	stroke := func(vt float64, x0, y0, x1, y1 float64) *notes.Stroke {
		raw, _ := json.Marshal(vt)
		return &notes.Stroke{VideoT: raw, BBox: [4]jsonx.Float{f(x0), f(y0), f(x1), f(y1)}, Shape: "circle"}
	}
	res := &notes.Result{Comments: []*notes.Comment{
		{VideoT: 8.5, VideoTEnd: 9.5, Text: "bigger pip", Strokes: []*notes.Stroke{stroke(9, 0.7, 0.65, 0.8, 0.75)}},
		{VideoT: 1, VideoTEnd: 2, Text: "the title"},
		{VideoT: 9, VideoTEnd: 9.5, Text: "the VO pauses here", Audio: true},
		{VideoT: 9, VideoTEnd: 9.5, Text: "an older version's take", Version: &v0},
	}}
	run := runFromResult("v1", res)
	attachTargets(&run, res, manifests(fam[0], fam))
	want := []struct {
		top, unit string
		word      bool
	}{{"pip/v", "u5", false}, {"open/title", "u1", false}, {"pip/v", "u5", true}, {"", "", false}}
	for i, w := range want {
		d := run.Comments[i].Detail
		top := ""
		if len(d.Targets) > 0 {
			top = d.Targets[0].ID
		}
		if top != w.top || d.Unit != w.unit || (d.Word != nil) != w.word {
			t.Errorf("comment %d: top %q unit %q word %+v", i, top, d.Unit, d.Word)
		}
	}
	if !run.Comments[2].Detail.Audio || run.Comments[0].Detail.Audio {
		t.Fatal("the (audio) flag")
	}
	if len(run.Comments[0].Detail.Strokes) != 1 || run.Comments[0].Detail.Targets[0].Score != 1 {
		t.Fatalf("detail = %+v", run.Comments[0].Detail)
	}
	// Stored as the note's detail: the JSON the store keeps and notes_list returns.
	js, _ := json.Marshal(run.Comments[0].Detail)
	var back project.Detail
	if json.Unmarshal(js, &back) != nil || back.Targets[0].ID != "pip/v" || back.Targets[0].Box == nil {
		t.Fatalf("detail json = %s", js)
	}
}
