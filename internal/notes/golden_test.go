package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The golden tests run the engine on the fixture takes in testdata/<case>/ and compare its notes.md and notes.json
// with the LEGACY engine's output on the same folders (testdata/<case>/golden/, made by regen-goldens.sh). Text must
// be equal, numbers within 1e-3; "generated" is ignored. Snapshots must exist with the frame's size.
//
// Legacy doesn't know images.json, answer_to or the comment keys. The golden runs leave images.json out (legacy
// never reads it), and drop the additions (comment keys, answer_to, ...) from our output before comparing; the
// additions have their own tests below.

type goldenCase struct {
	Target string   `json:"target"`
	Args   []string `json:"args"`
	Video  *struct {
		File    string  `json:"file"`
		Seconds float64 `json:"seconds"`
		Size    string  `json:"size"`
		FPS     int     `json:"fps"`
	} `json:"video"`
}

func goldenCases(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("testdata", e.Name(), "case.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no golden cases in testdata")
	}
	return names
}

// setupCase copies testdata/<name> (without golden/) into a temp folder, fills in @ROOT@, makes the case's video,
// and returns the folder, the case and the build options its arguments give.
func setupCase(t *testing.T, name string, withImages bool) (string, goldenCase, Options) {
	t.Helper()
	src := filepath.Join("testdata", name)
	var gc goldenCase
	b, err := os.ReadFile(filepath.Join(src, "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &gc); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, name)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "golden" || rel == "case.json" || (!withImages && d.Name() == "images.json") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".json") {
			data = bytes.ReplaceAll(data, []byte("@ROOT@"), []byte(root))
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if gc.Video != nil {
		makeVideo(t, filepath.Join(root, gc.Video.File), gc.Video.Size, gc.Video.FPS, gc.Video.Seconds)
	}
	opts := Options{Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }}
	for i := 0; i < len(gc.Args); i++ {
		switch gc.Args[i] {
		case "--window":
			i++
			opts.Window, _ = strconv.ParseFloat(gc.Args[i], 64)
		case "--max-span":
			i++
			opts.MaxSpan, _ = strconv.ParseFloat(gc.Args[i], 64)
		case "--video-transcript":
			i++
			opts.VideoTranscript = filepath.Join(root, gc.Args[i])
		default:
			t.Fatalf("case %s: unknown argument %q", name, gc.Args[i])
		}
	}
	return root, gc, opts
}

// makeVideo writes the same video regen-goldens.sh makes: testsrc2, H.264, no audio (so the duration is exact).
func makeVideo(t *testing.T, path, size string, fps int, seconds float64) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	src := fmt.Sprintf("testsrc2=s=%s:r=%d:d=%s", size, fps, strconv.FormatFloat(seconds, 'f', -1, 64))
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-threads", "2", "-f", "lavfi", "-i", src,
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-an", "-movflags", "+faststart", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

// additions are the comment fields legacy doesn't write.
var additions = []string{"key", "images", "answer_to", "retracted", "author"}

var (
	answerPart = regexp.MustCompile(` · answer to question [^ ·\n]+`)
	addedRules = regexp.MustCompile("(?m)^- `(image N|answer to question N)` = .*\n")
)

// legacyMD drops our additions from notes.md.
func legacyMD(md string) string {
	return addedRules.ReplaceAllString(answerPart.ReplaceAllString(md, ""), "")
}

func TestGolden(t *testing.T) {
	for _, name := range goldenCases(t) {
		t.Run(name, func(t *testing.T) {
			root, gc, opts := setupCase(t, name, false)
			var log bytes.Buffer
			opts.Log = &log
			res, err := Run(context.Background(), filepath.Join(root, gc.Target), opts)
			if err != nil {
				t.Fatalf("Run: %v\nlog:\n%s", err, log.String())
			}
			md, err := os.ReadFile(res.MDPath)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(res.JSONPath)
			if err != nil {
				t.Fatal(err)
			}
			gotMD := legacyMD(strings.ReplaceAll(string(md), root, "@ROOT@"))
			wantMD := readGolden(t, name, "notes.md")
			if gotMD != wantMD {
				t.Errorf("notes.md differs from legacy:\n%s", lineDiff(wantMD, gotMD))
			}
			var got, want any
			if err := json.Unmarshal(bytes.ReplaceAll(raw, []byte(root), []byte("@ROOT@")), &got); err != nil {
				t.Fatalf("notes.json: %v", err)
			}
			if err := json.Unmarshal([]byte(readGolden(t, name, "notes.json")), &want); err != nil {
				t.Fatalf("golden notes.json: %v", err)
			}
			stripAdditions(got)
			delete(got.(map[string]any), "generated")
			delete(want.(map[string]any), "generated")
			var diffs []string
			compareJSON("$", got, want, &diffs)
			if len(diffs) > 0 {
				if len(diffs) > 30 {
					diffs = append(diffs[:30], fmt.Sprintf("... and %d more", len(diffs)-30))
				}
				t.Errorf("notes.json differs from legacy:\n%s", strings.Join(diffs, "\n"))
			}
			checkSnaps(t, name, res.Dir)
			checkKeys(t, res)
		})
	}
}

func readGolden(t *testing.T, name, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name, "golden", file))
	if err != nil {
		t.Fatalf("%v (run testdata/regen-goldens.sh)", err)
	}
	return string(b)
}

// checkSnaps compares the snapshots with golden/snaps.txt: the same files, each a PNG of the same size.
func checkSnaps(t *testing.T, name, dir string) {
	t.Helper()
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(readGolden(t, name, "snaps.txt")), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			want[f[0]] = f[1]
		}
	}
	got := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(dir, "snaps", "*.png"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(fh)
		_ = fh.Close()
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		got["snaps/"+filepath.Base(f)] = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("snapshots: got %v, want %v", got, want)
	}
}

// checkKeys: every comment has a unique key <take>/<seg>/<n>.
func checkKeys(t *testing.T, res *Result) {
	t.Helper()
	keyRE := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}/[^/]+/[1-9]\d*$`)
	seen := map[string]bool{}
	for _, c := range res.Comments {
		if !keyRE.MatchString(c.Key) {
			t.Errorf("comment #%d: bad key %q", c.Order, c.Key)
		}
		if seen[c.Key] {
			t.Errorf("comment #%d: duplicate key %q", c.Order, c.Key)
		}
		seen[c.Key] = true
	}
}

func stripAdditions(doc any) {
	ms, _ := doc.(map[string]any)["moments"].([]any)
	for _, m := range ms {
		cs, _ := m.(map[string]any)["comments"].([]any)
		for _, c := range cs {
			for _, k := range additions {
				delete(c.(map[string]any), k)
			}
		}
	}
}

// compareJSON records every difference between got and want: equal structure and text, numbers within 1e-3.
func compareJSON(path string, got, want any, diffs *[]string) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: got %s, want an object", path, short(got)))
			return
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			gv, gok := g[k]
			wv, wok := w[k]
			switch {
			case !gok:
				*diffs = append(*diffs, fmt.Sprintf("%s.%s: missing (want %s)", path, k, short(wv)))
			case !wok:
				*diffs = append(*diffs, fmt.Sprintf("%s.%s: unexpected %s", path, k, short(gv)))
			default:
				compareJSON(path+"."+k, gv, wv, diffs)
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			*diffs = append(*diffs, fmt.Sprintf("%s: got %s, want %s", path, short(got), short(want)))
			return
		}
		for i := range w {
			compareJSON(fmt.Sprintf("%s[%d]", path, i), g[i], w[i], diffs)
		}
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(g-w) > 1e-3 {
			*diffs = append(*diffs, fmt.Sprintf("%s: got %s, want %v", path, short(got), w))
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) || (got == nil) != (want == nil) {
			*diffs = append(*diffs, fmt.Sprintf("%s: got %s, want %s", path, short(got), short(want)))
		}
	}
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 120 {
		return string(b[:120]) + "..."
	}
	return string(b)
}

// lineDiff shows the first differing lines of two texts.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)) && len(out) < 40; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			out = append(out, fmt.Sprintf("line %d\n  want: %s\n  got:  %s", i+1, wl, gl))
		}
	}
	return strings.Join(out, "\n")
}
