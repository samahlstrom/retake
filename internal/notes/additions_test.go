package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
)

// Tests of what milestone 2 adds to the legacy engine (spec/notes.md): images, answers, comment keys, the
// retracted state, untranscribed segments, relative inherit dirs and the Build API.

func runCase(t *testing.T, name string, withImages bool) (*Result, string) {
	t.Helper()
	root, gc, opts := setupCase(t, name, withImages)
	res, err := Run(context.Background(), filepath.Join(root, gc.Target), opts)
	if err != nil {
		t.Fatal(err)
	}
	return res, root
}

func TestImages(t *testing.T) {
	res, root := runCase(t, "image", true)
	type img struct {
		order int
		files []string
		ns    []int
	}
	var got []img
	for _, c := range res.Comments {
		var files []string
		var ns []int
		for _, im := range c.Images {
			files = append(files, im.File)
			ns = append(ns, im.N)
			if im.Path != filepath.Join(root, "clip_v6.review", "2026-05-05_12-00-00", im.File) {
				t.Errorf("image path %s", im.Path)
			}
		}
		got = append(got, img{c.Order, files, ns})
	}
	want := []img{
		{1, []string{"img_01.png"}, []int{1}},  // paused: joins everything said and drawn on that frame
		{2, []string{"img_02.jpg"}, []int{2}},  // playing: joins the sentence 0.4 s before it
		{3, []string{"img_03.webp"}, []int{3}}, // 8 s after any sentence: a comment of its own
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("images by comment:\n got %+v\nwant %+v", got, want)
	}
	c3 := res.Comments[2]
	if c3.Mode != "playing" || c3.Text != "" || float64(c3.Anchor) != 24.5 || c3.Key != "2026-05-05_12-00-00/S2/2" {
		t.Errorf("image-only comment: %+v", c3)
	}
	if b := res.Comments[0].Images[0].Box; b != [4]jsonFloat{0.55, 0.1, 0.3, 0.3} {
		t.Errorf("box %v", b)
	}
	for _, s := range []string{
		"· 1 stroke · 3 images · 0 flags",
		"- `image N` = a picture Sam dropped onto the frame",
		"· stroke 1: yellow line (0.20,0.60)→(0.45,0.60) heading right · image 1: img_01.png at (0.55,0.10)–(0.85,0.40)",
		`"And this logo in the corner." · image 2 @00:16.9: img_02.jpg at (0.05,0.05)–(0.20,0.15)`,
		"- #3 S2 [v6] · playing 00:24.5 · (no words) · image 3: img_03.webp at (0.40,0.40)–(0.60,0.60)",
		"- #1 S1 [v6] · paused 00:14.0 · \"Use this meme here instead.\" · stroke 1 · image 1 · M1",
	} {
		if !strings.Contains(res.Markdown, s) {
			t.Errorf("notes.md lacks %q", s)
		}
	}
	raw, _ := os.ReadFile(res.JSONPath)
	var doc struct {
		Moments []struct {
			Comments []struct {
				Images []map[string]any `json:"images"`
			} `json:"comments"`
		} `json:"moments"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	im := doc.Moments[0].Comments[0].Images[0]
	for _, k := range []string{"id", "seg", "file", "path", "mime", "version", "flip", "video_t", "box", "n", "session_t"} {
		if _, ok := im[k]; !ok {
			t.Errorf("notes.json image lacks %q: %v", k, im)
		}
	}
}

func TestSnapshotsWithImages(t *testing.T) {
	res, _ := runCase(t, "snapshot", true)
	var snaps []string
	for _, m := range res.Doc.Moments {
		if m.Snapshot == nil {
			t.Fatalf("moment %s has no snapshot", m.ID)
		}
		snaps = append(snaps, *m.Snapshot)
		f, err := os.Open(filepath.Join(res.Dir, *m.Snapshot))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(f)
		_ = f.Close()
		if err != nil || cfg.Width != 320 || cfg.Height != 180 {
			t.Errorf("%s: %+v %v", *m.Snapshot, cfg, err)
		}
	}
	if !reflect.DeepEqual(snaps, []string{"snaps/01_00-01-0.png", "snaps/02_00-04-5.png"}) {
		t.Errorf("snapshots %v", snaps)
	}
	if !strings.Contains(res.Markdown, "image 1: img_01.png at (0.05,0.60)–(0.30,0.90)") {
		t.Errorf("notes.md lacks the image:\n%s", res.Markdown)
	}
}

func TestAnswers(t *testing.T) {
	res, _ := runCase(t, "answer", false)
	var got [][3]string
	for _, c := range res.Comments {
		got = append(got, [3]string{c.Key, c.Seg, string(c.AnswerTo)})
	}
	want := [][3]string{
		{"2026-06-06_18-00-00/S1/1", "S1", ""},
		{"2026-06-06_18-00-00/S2/1", "S2", "12"},
		{"2026-06-06_18-00-00/S3/1", "S3", "13"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("answers: %v, want %v", got, want)
	}
	for _, s := range []string{
		"- `answer to question N` = Sam's answer to your question N",
		`- #2 S2 [v7] · paused 00:22.0 · "Go with the shorter version. And keep the sound effect." (audio) · ` +
			"answer to question 12 · stroke 1: green line",
		`- #3 S3 [v7] · paused 00:22.0 · "Yes, cut it." · answer to question 13 · M2`,
	} {
		if !strings.Contains(res.Markdown, s) {
			t.Errorf("notes.md lacks %q\n%s", s, res.Markdown)
		}
	}
	if res.Dir != filepath.Dir(res.MDPath) || !strings.HasSuffix(res.Dir, "2026-06-06_18-00-00") {
		t.Errorf("a take folder's notes go into it: %s", res.Dir)
	}
	if res.Comments[1].Author != "Tester" {
		t.Errorf("author %q", res.Comments[1].Author)
	}
}

// The retracted state replays the flags: a retraction voids, a later reinstatement restores.
func TestRetractedState(t *testing.T) {
	res, _ := runCase(t, "retract", false)
	var retracted []int
	for _, c := range res.Comments {
		if c.Retracted {
			retracted = append(retracted, c.Order)
		}
	}
	// #3 voids #1 #2, #5 restores them; #6 voids #5; #8 and #9 void #7, #10 undoes #9 and restores #7.
	if !reflect.DeepEqual(retracted, []int{5}) {
		t.Errorf("retracted %v, want [5]", retracted)
	}
	keys := map[string]bool{}
	for _, c := range res.Comments {
		keys[c.Key] = true
	}
	for _, k := range []string{"2026-01-10_10-00-00/S1/1", "2026-01-10_10-00-00/S1/2", "2026-01-10_11-30-00/S2/3",
		"2026-01-10_11-30-00/S3/1"} {
		if !keys[k] {
			t.Errorf("no comment key %s in %v", k, keys)
		}
	}
}

// An inherited take's keys don't depend on which of its comments fall in the ranges, and are the same in the
// version it was recorded on.
func TestInheritedKeysAreStable(t *testing.T) {
	res, root := runCase(t, "inherit", false)
	got := map[string]string{}
	for _, c := range res.Comments {
		got[c.Key] = c.Text
	}
	own, err := Run(context.Background(), filepath.Join(root, "v3", "clip_v3.review"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range own.Comments {
		if text, ok := got[c.Key]; ok && text != c.Text {
			t.Errorf("key %s: %q on v6, %q on v3", c.Key, text, c.Text)
		}
	}
	if _, ok := got["2026-01-05_09-00-00/S3/1"]; !ok {
		t.Errorf("v3's S3 comment (inside the ranges) missing: %v", got)
	}
	if c := res.Comments[1]; c.Take != "2026-01-05_09-00-00 (v3)" || c.TakeFolder() != "2026-01-05_09-00-00" ||
		c.Seg != "S3" || c.SegID() != "S3" || pyOpt(c.Version) != "v3" {
		t.Errorf("inherited comment: take %q folder %q seg %q/%q version %s", c.Take, c.TakeFolder(), c.Seg, c.SegID(),
			pyOpt(c.Version))
	}
	if c := res.Comments[3]; c.Seg != "S6" || c.SegID() != "S1" {
		t.Errorf("own take's first segment: seg %q, id %q", c.Seg, c.SegID())
	}
	if len(own.Comments) != 4 || len(res.Comments) != 5 {
		t.Errorf("%d comments on v3, %d on v6", len(own.Comments), len(res.Comments))
	}
}

// inherit.json dirs relative to the .review folder work like absolute ones.
func TestInheritRelativeDirs(t *testing.T) {
	root, gc, opts := setupCase(t, "inherit", false)
	p := filepath.Join(root, "v6", "clip_v6.review", "inherit.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.ReplaceAll(b, []byte(root+"/"), []byte("../../"))
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), filepath.Join(root, gc.Target), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doc.Takes) != 3 || res.Doc.Takes[0].Take != "2026-01-05_09-00-00 (v3)" {
		t.Errorf("takes %+v", res.Doc.Takes)
	}
	if got := legacyMD(strings.ReplaceAll(res.Markdown, root, "@ROOT@")); got != readGolden(t, "inherit", "notes.md") {
		t.Errorf("relative inherit dirs changed the notes:\n%s", lineDiff(readGolden(t, "inherit", "notes.md"), got))
	}
}

// Segments without a transcript (or with one for an older, shorter file) are reported, never transcribed.
func TestUntranscribed(t *testing.T) {
	root, gc, opts := setupCase(t, "retract", false)
	take := filepath.Join(root, "clip_v6.review", "2026-01-10_10-00-00")
	tr := filepath.Join(take, "transcript.json")
	b, err := os.ReadFile(tr)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Files map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc.Files, "seg_02.webm")
	b, _ = json.Marshal(doc)
	if err := os.WriteFile(tr, b, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(take, "seg_04.webm"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("more audio arrived after the transcript")
	_ = f.Close()

	res, err := Run(context.Background(), filepath.Join(root, gc.Target), opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []Untranscribed{{Take: "2026-01-10_10-00-00", Seg: "S2", File: "seg_02.webm"},
		{Take: "2026-01-10_10-00-00", Seg: "S4", File: "seg_04.webm"}}
	if !reflect.DeepEqual(res.Untranscribed, want) {
		t.Errorf("untranscribed %+v", res.Untranscribed)
	}
	if !strings.Contains(res.Markdown, "> warning: 2026-01-10_10-00-00 S2: seg_02.webm is not transcribed yet") {
		t.Errorf("no warning in notes.md")
	}
	for _, c := range res.Comments {
		if strings.Contains(c.Text, "Throw out") || strings.Contains(c.Text, "add that back") {
			t.Errorf("words of an untranscribed segment: %q", c.Text)
		}
	}
	after, _ := os.ReadFile(tr)
	if !bytes.Equal(after, b) {
		t.Errorf("the engine rewrote transcript.json")
	}
}

func TestBuild(t *testing.T) {
	root, _, opts := setupCase(t, "format2", false)
	var log bytes.Buffer
	opts.Log = &log
	d, w, h := 30.0, 1280, 720
	v := family.Version{Label: "v2", N: 2, Path: filepath.Join(root, "clip_v2.mp4"), Name: "clip_v2.mp4", Current: true,
		Duration: jsonx.FloatPtr(&d), FPS: 29.97, Width: &w, Height: &h}
	fam := []family.Version{{Label: "v1", N: 1}, v}
	res, err := Build(context.Background(), v, fam, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dir != filepath.Join(root, "clip_v2.review") {
		t.Errorf("notes in %s", res.Dir)
	}
	video, _ := jsonx.Marshal(res.Doc.Video)
	if want := `{"path":"` + v.Path + `","name":"clip_v2.mp4","duration":30.0,"fps":29.97,"width":1280,"height":720}`; string(video) != want {
		t.Errorf("video %s, want %s", video, want)
	}
	if !reflect.DeepEqual(res.Doc.Family, []string{"v1", "v2"}) {
		t.Errorf("family %v (the takes recorded none: the given family's labels)", res.Doc.Family)
	}
	if !strings.Contains(log.String(), "video not found") {
		t.Errorf("log: %q", log.String())
	}
	var keys []string
	for _, c := range res.Comments {
		keys = append(keys, c.Key)
	}
	// Strokes outside the take's segments keep their seg (or "no-audio") in the key.
	want := []string{"2025-12-01_08-00-00/S1/1", "2025-12-01_08-00-00/S1/2", "2025-12-01_08-00-00/S9/1",
		"2025-12-01_08-00-00/no-audio/1"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys %v, want %v", keys, want)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "notes.json")); err != nil {
		t.Error(err)
	}

	empty := family.Version{Path: filepath.Join(t.TempDir(), "other_v1.mp4"), Name: "other_v1.mp4"}
	if _, err := Build(context.Background(), empty, nil, Options{}); !errors.Is(err, ErrNoTakes) {
		t.Errorf("no takes: %v", err)
	}
	if _, err := Run(context.Background(), t.TempDir(), Options{}); !errors.Is(err, ErrNoTakes) {
		t.Errorf("Run on an empty folder: %v", err)
	}
}

func TestVideoWords(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"flat.json":     `[{"w": " Souls", "s": 1, "e": 1.5}, {"word": "are", "start": 0.5, "end": 0.9}]`,
		"words.json":    `{"words": [{"text": "Souls", "start": 1, "end": 1.5}, {"word": "are", "start": 0.5, "end": 0.9}]}`,
		"segments.json": `{"segments": [{"words": [{"word": "are", "start": 0.5, "end": 0.9}]}, {"words": [{"w": "Souls", "s": 1, "e": 1.5}]}, {"text": "untimed"}]}`,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ws, err := loadVideoWords(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(ws) != 2 || ws[0].w != "are" || ws[1].w != "Souls" || ws[1].e != 1.5 {
			t.Errorf("%s: %+v", name, ws)
		}
	}
	if _, err := loadVideoWords(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing file is an error")
	}
}
