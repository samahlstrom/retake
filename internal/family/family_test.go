package family

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/testmedia"
)

func TestSuffixAndStem(t *testing.T) {
	for _, tc := range []struct{ name, suffix, stem string }{
		{"clip_v6.mp4", ".mp4", "clip_v6"},
		{"a.tar.gz", ".gz", "a.tar"},
		{".hidden", "", ".hidden"},
		{"name.", "", "name."},
		{"noext", "", "noext"},
		{"clip_v7_0-010_preview.MP4", ".MP4", "clip_v7_0-010_preview"},
	} {
		if s := Suffix(tc.name); s != tc.suffix {
			t.Errorf("Suffix(%q) = %q, want %q", tc.name, s, tc.suffix)
		}
		if s := Stem(tc.name); s != tc.stem {
			t.Errorf("Stem(%q) = %q, want %q", tc.name, s, tc.stem)
		}
	}
}

func TestParseStem(t *testing.T) {
	for _, tc := range []struct {
		stem string
		ok   bool
		want Name
	}{
		{"clip_v6", true, Name{"clip", 6, ""}},
		{"clip_v5_draft", true, Name{"clip", 5, "_draft"}},
		{"clip_v7_0-010_preview", true, Name{"clip", 7, "_0-010_preview"}},
		{"tutorial-V007", true, Name{"tutorial", 7, ""}},
		{"my_video_v2", true, Name{"my_video", 2, ""}},
		{"clip_v6_v7", true, Name{"clip", 6, "_v7"}},
		{"clip_v6x", false, Name{}},
		{"clipv6", false, Name{}},
		{"_v6", false, Name{}},
		{"clip", false, Name{}},
	} {
		got, ok := ParseStem(tc.stem)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseStem(%q) = %+v, %v; want %+v, %v", tc.stem, got, ok, tc.want, tc.ok)
		}
	}
}

// layout builds files under root: a value of 0 means an unprobeable file, otherwise a video of that many seconds.
func layout(t *testing.T, root string, files map[string]float64) {
	t.Helper()
	for rel, secs := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if secs == 0 {
			testmedia.Garbage(t, p)
		} else {
			testmedia.Video(t, p, secs)
		}
	}
}

func setMtime(t *testing.T, p string, ago time.Duration) {
	t.Helper()
	ts := time.Now().Add(-ago)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
}

type got struct{ label, name string }

func summary(vs []Version) []got {
	out := make([]got, 0, len(vs))
	for _, v := range vs {
		name := v.Name
		if v.Current {
			name = "*" + name
		}
		out = append(out, got{v.Label, name})
	}
	return out
}

func eq(a, b []got) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDetect(t *testing.T) {
	testmedia.Need(t)
	for _, tc := range []struct {
		name     string
		files    map[string]float64
		open     string
		explicit []string
		setup    func(t *testing.T, root string)
		want     []got
	}{
		{
			name:  "same folder",
			files: map[string]float64{"clip_v1.mp4": 1, "clip_v2.mp4": 1, "clip_v3.mp4": 1, "other_v4.mp4": 1, "clip_v5.txt": 0},
			open:  "clip_v2.mp4",
			want:  []got{{"v1", "clip_v1.mp4"}, {"v2", "*clip_v2.mp4"}, {"v3", "clip_v3.mp4"}},
		},
		{
			name: "sibling folders, draft decoy, partial preview, .review skipped",
			files: map[string]float64{
				"v5/clip_v5.mp4": 2, "v5/clip_v5_draft.mp4": 1, "v6/clip_v6.mp4": 2,
				"v7/clip_v7_0-010_preview.mp4": 1, "clip_v6.review/clip_v8.mp4": 1, "v6/sub/clip_v9.mp4": 1,
			},
			open: "v6/clip_v6.mp4",
			setup: func(t *testing.T, root string) {
				setMtime(t, filepath.Join(root, "v5/clip_v5.mp4"), time.Hour) // the draft is newer and still loses
			},
			want: []got{{"v5", "clip_v5.mp4"}, {"v6", "*clip_v6.mp4"}, {"v7", "clip_v7_0-010_preview.mp4"}},
		},
		{
			name:  "newest of two non-plain candidates wins",
			files: map[string]float64{"clip_v1.mp4": 1, "clip_v2_a.mp4": 1, "clip_v2_b.mp4": 1},
			open:  "clip_v1.mp4",
			setup: func(t *testing.T, root string) { setMtime(t, filepath.Join(root, "clip_v2_a.mp4"), time.Hour) },
			want:  []got{{"v1", "*clip_v1.mp4"}, {"v2", "clip_v2_b.mp4"}},
		},
		{
			name:  "still rendering is skipped, and a probing draft stands in",
			files: map[string]float64{"clip_v1.mp4": 1, "clip_v2.mp4": 0, "clip_v3.mp4": 0, "clip_v3_draft.mp4": 1},
			open:  "clip_v1.mp4",
			want:  []got{{"v1", "*clip_v1.mp4"}, {"v3", "clip_v3_draft.mp4"}},
		},
		{
			name:  "the opened file stays even if it doesn't probe",
			files: map[string]float64{"clip_v1.mp4": 1, "clip_v2.mp4": 0},
			open:  "clip_v2.mp4",
			want:  []got{{"v1", "clip_v1.mp4"}, {"v2", "*clip_v2.mp4"}},
		},
		{
			name:  "prefix and extension are case-insensitive, other extensions too",
			files: map[string]float64{"Clip_V1.MP4": 1, "clip_v2.mov": 1, "CLIP-v3.mkv": 1, "clip_v4.webm": 1},
			open:  "clip_v2.mov",
			want:  []got{{"v1", "Clip_V1.MP4"}, {"v2", "*clip_v2.mov"}, {"v3", "CLIP-v3.mkv"}, {"v4", "clip_v4.webm"}},
		},
		{
			name:  "no version number: the current label",
			files: map[string]float64{"movie.mp4": 1, "movie_v2.mp4": 1},
			open:  "movie.mp4",
			want:  []got{{"current", "*movie.mp4"}},
		},
		{
			name:     "explicit versions with duplicate labels",
			files:    map[string]float64{"a.mp4": 1, "x/clip_v2.mp4": 1, "y/b_v2.mp4": 1, "c.mp4": 1, "d_v1.mp4": 1},
			open:     "a.mp4",
			explicit: []string{"c.mp4", "x/clip_v2.mp4", "a.mp4", "y/b_v2.mp4", "d_v1.mp4"},
			want: []got{
				{"v1", "d_v1.mp4"}, {"v2", "clip_v2.mp4"}, {"v2-3", "b_v2.mp4"}, {"a", "*a.mp4"}, {"c", "c.mp4"},
			},
		},
		{
			name:     "explicit versions skip one that doesn't probe",
			files:    map[string]float64{"clip_v1.mp4": 1, "clip_v2.mp4": 0},
			open:     "clip_v1.mp4",
			explicit: []string{"clip_v2.mp4"},
			want:     []got{{"v1", "*clip_v1.mp4"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			layout(t, root, tc.files)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			var explicit []string
			for _, e := range tc.explicit {
				explicit = append(explicit, filepath.Join(root, filepath.FromSlash(e)))
			}
			fam := Detect(filepath.Join(root, filepath.FromSlash(tc.open)), explicit, probe.New(nil))
			if g := summary(fam); !eq(g, tc.want) {
				t.Errorf("family = %v\nwant     %v", g, tc.want)
			}
		})
	}
}

func TestDetectFillsProbeFields(t *testing.T) {
	testmedia.Need(t)
	root := t.TempDir()
	layout(t, root, map[string]float64{"clip_v1.mp4": 1})
	testmedia.HLS(t, root, "clip_v2.m3u8", 2)
	fam := Detect(filepath.Join(root, "clip_v1.mp4"), nil, probe.New(nil))
	if len(fam) != 2 {
		t.Fatalf("family = %v", summary(fam))
	}
	v1, v2 := fam[0], fam[1]
	if v1.N != 1 || !v1.Current || v1.Duration == nil || *v1.Duration < 0.9 || v1.FPS != 10 ||
		*v1.Width != 64 || *v1.Height != 36 || v1.Path != filepath.Join(root, "clip_v1.mp4") {
		t.Errorf("v1 = %+v", v1)
	}
	if v2.Label != "v2" || v2.Current || !strings.HasSuffix(v2.Path, "clip_v2.m3u8") || v2.Duration == nil {
		t.Errorf("v2 = %+v", v2)
	}
	if !IsPlaylist(v2.Path) || IsPlaylist(v1.Path) {
		t.Error("IsPlaylist")
	}
}
