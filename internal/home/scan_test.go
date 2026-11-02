package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// places lists what a scan found as "name path project anchor-file", one per video.
func places(list []Found) []string {
	var out []string
	for _, f := range list {
		out = append(out, strings.Join([]string{f.Name, f.Path, f.Project, filepath.Base(f.Anchor)}, " "))
	}
	return out
}

// TestScanLeavesRecordingsOut: a streams folder of OBS recordings (named for their date and time) and Twitch downloads
// (an ID after the v, alone or with a clip's range, one in a folder named for it), beside a real project. Only the
// project is a video: a recording never shows as a family at a made-up version like v2903906005 (spec/api.md,
// "Detection").
func TestScanLeavesRecordingsOut(t *testing.T) {
	top := filepath.Join(t.TempDir(), "Videos")
	old := time.Now().Add(-time.Hour)
	for _, rel := range []string{
		"streams/2026-10-17 21-30-05.mp4",
		"streams/2026-10-17 23-02-11.mkv",
		"streams/2026-10-17/2026-10-17 21-30-05.mp4",
		"streams/2026-10-17/twitch_v2903906005_3480-4520.mp4",
		"streams/2026-10-17/twitch_v2903906005_5100-5400.mp4",
		"streams/2026-10-19/v2905618565.mp4",
		"streams/2026-10-19/v2905683033/v2905683033.mp4",
		"streams/2026-10-19/evidence/work/v2905618565_mix.mp4",
		"item-shop/v8/itemshop_v8.mp4",
	} {
		touch(t, filepath.Join(top, filepath.FromSlash(rel)), old)
	}
	scan := ScanDir(top)
	want := []string{"item-shop item-shop  itemshop_v8.mp4"}
	if got := places(scan.Videos); !reflect.DeepEqual(got, want) {
		t.Errorf("videos:\n got %q\nwant %q", got, want)
	}
	for _, v := range scan.Videos {
		if strings.Contains(v.Anchor, "2903906005") || strings.Contains(v.Anchor, "21-30-05") {
			t.Errorf("a recording is a video: %+v", v)
		}
	}
}

// TestScanTree: a folder like a video maker's, with dated folders, a project with versions in vN folders and more
// videos in its shorts folders, junk folders, a timeline project without a version, and a video too deep.
func TestScanTree(t *testing.T) {
	top := filepath.Join(t.TempDir(), "deadlock")
	old, now := time.Now().Add(-time.Hour), time.Now()
	f := func(rel string, mtime time.Time) { touch(t, filepath.Join(top, filepath.FromSlash(rel)), mtime) }

	f("2026-10-19/fullgame/fullgame_v2.mp4", old)
	f("2026-10-19/fullgame/fullgame_v3_endcard.mp4", old)
	f("2026-10-19/shorts/aura_v2.mp4", old) // two families in one folder: the newest shows
	f("2026-10-19/shorts/baby_v2.mp4", now)
	f("2026-10-19/longform/MACRO_A.mp4", old) // no version: no project
	f("2026-10-25/jump/v1/jump_v1.mp4", old)
	f("2026-10-25/jump/v2/jump_v2.mp4", old)
	for i := 1; i <= 7; i++ {
		f(fmt.Sprintf("item-shop/v%d/itemshop_v%d.mp4", i, i), old)
	}
	f("item-shop/v1/itemshop_v1_draft.mp4", now)
	f("item-shop/v1/endcard/end_v1.mp4", old)                  // inside a version folder: not looked at
	f("item-shop/v1/work/shots/0d893190edc987.mp4", old)       // junk
	f("item-shop/shorts/one-item.mp4", old)                    // unnumbered: not a video
	f("item-shop/shorts/honored/v1/honored_v1.mp4", old)       // 2 folders down: a video of item-shop
	f("item-shop/shorts/honored/v2/honored_v2.mp4", old)       //
	f("item-shop/shorts_v2/dead-cash_v2.mp4", old)             // 1 folder down: a video of item-shop
	f("item-shop/assets/memes/gojo/cache/v2/gojo_v1.mp4", old) // 4 folders down: never
	f("item-shop/node_modules/pkg/demo_v1.mp4", old)           // never
	f("item-shop/.retake/thumbs/x_v1.mp4", old)                // hidden: never
	f("item-shop/vo/take_v1.wav", old)                         // not a video file
	f("broll/clips/sunset.mp4", old)
	f("brand/__pycache__/x_v1.mp4", old)
	touch(t, filepath.Join(top, "drafts", "new-cut", "edit", "timeline.json"), old) // a timeline, no version yet
	f("a/b/c/d/e/f/deep_v1.mp4", old)                                               // 6 folders down: found
	f("a/b/c/d/e/f/g/deeper_v1.mp4", old)                                           // 7: too deep (and inside a project anyway)
	f("h/i/j/k/l/m/n/deepest_v1.mp4", old)                                          // 7 folders down, no project above it: too deep

	scan := ScanDir(top)
	// A folder that holds one video is that video, in the project above it; one that holds more (two families, or
	// videos in its own folders) is a project with its own video.
	want := []string{
		"item-shop item-shop item-shop itemshop_v7.mp4",
		"2026-10-19-fullgame 2026-10-19/fullgame 2026-10-19 fullgame_v3_endcard.mp4",
		"2026-10-19-shorts 2026-10-19/shorts 2026-10-19/shorts baby_v2.mp4",
		"2026-10-25-jump 2026-10-25/jump 2026-10-25 jump_v2.mp4",
		"item-shop-shorts-v2 item-shop/shorts_v2 item-shop dead-cash_v2.mp4",
		"item-shop-shorts-honored item-shop/shorts/honored item-shop/shorts honored_v2.mp4",
		"a-b-c-d-e-f a/b/c/d/e/f a/b/c/d/e deep_v1.mp4",
	}
	if got := places(scan.Videos); !reflect.DeepEqual(got, want) {
		t.Errorf("videos:\n got %q\nwant %q", got, want)
	}
	if got := places(scan.Empty); !reflect.DeepEqual(got, []string{"drafts-new-cut drafts/new-cut drafts ."}) {
		t.Errorf("empty projects: %q", got)
	}
	for _, v := range scan.Videos {
		if v.Title != filepath.Base(v.Root) || v.Root != filepath.Join(top, filepath.FromSlash(v.Path)) {
			t.Errorf("title or root of %+v", v)
		}
	}
	if scan.Dirs == 0 || scan.Capped || scan.Took <= 0 {
		t.Errorf("stats: %+v", scan)
	}

	// The tree the home sends: folders, projects, their videos.
	var cards []Card
	for _, v := range scan.Videos {
		cards = append(cards, Card{Name: v.Name, Path: v.Path, Project: v.Project})
	}
	tree := BuildTree("deadlock", cards, nil)
	if got := outline(tree, ""); got != strings.Join([]string{
		"deadlock/",
		"  2026-10-19* [2026-10-19-fullgame]",
		"    shorts* [2026-10-19-shorts]",
		"  2026-10-25* [2026-10-25-jump]",
		"  a/", "    b/", "      c/", "        d/",
		"          e* [a-b-c-d-e-f]",
		"  item-shop* [item-shop item-shop-shorts-v2]",
		"    shorts* [item-shop-shorts-honored]",
	}, "\n") {
		t.Errorf("tree:\n%s", got)
	}
}

// outline draws a tree: a folder ends in /, a project has * and its videos' names.
func outline(n *Node, indent string) string {
	line := indent + n.Title
	if n.Project {
		var names []string
		for _, v := range n.Videos {
			names = append(names, v.Name)
		}
		line += "* [" + strings.Join(names, " ") + "]"
	} else {
		line += "/"
	}
	out := []string{line}
	for _, c := range n.Children {
		out = append(out, outline(c, indent+"  "))
	}
	return strings.Join(out, "\n")
}

func TestScanLinks(t *testing.T) {
	base := t.TempDir()
	top := filepath.Join(base, "top")
	elsewhere := filepath.Join(base, "elsewhere")
	touch(t, filepath.Join(top, "real", "real_v1.mp4"), time.Now())
	touch(t, filepath.Join(elsewhere, "far_v1.mp4"), time.Now())
	links := map[string]string{
		filepath.Join(top, "links", "alias"): filepath.Join(top, "real"),          // a folder inside: found as itself, once
		filepath.Join(top, "loop"):           top,                                 // a cycle: never entered
		filepath.Join(top, "outside"):        elsewhere,                           // a folder outside: followed
		filepath.Join(elsewhere, "back"):     elsewhere,                           // a cycle out there: entered once
		filepath.Join(top, "broken"):         filepath.Join(base, "nothing-here"), // a broken link: left out
	}
	if err := os.MkdirAll(filepath.Join(top, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Skip("no symlinks here:", err)
		}
	}
	got := places(ScanDir(top).Videos)
	want := []string{"outside outside  far_v1.mp4", "real real  real_v1.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("links: %q", got)
	}
}

func TestScanCap(t *testing.T) {
	top := t.TempDir()
	for i := range 10 {
		touch(t, filepath.Join(top, fmt.Sprintf("p%02d", i), fmt.Sprintf("p%02d_v1.mp4", i)), time.Now())
	}
	defer func(n int) { dirLimit = n }(dirLimit)
	dirLimit = 4
	scan := ScanDir(top)
	if !scan.Capped || scan.Dirs != 4 || len(scan.Videos) == 0 || len(scan.Videos) >= 10 {
		t.Errorf("capped scan: %d videos, %d folders, capped %v", len(scan.Videos), scan.Dirs, scan.Capped)
	}
	if got := ScanDir(filepath.Join(top, "missing")); len(got.Videos) != 0 || got.Capped {
		t.Errorf("a missing folder: %+v", got)
	}
}

// TestScanBig times a scan of a big generated tree: 200 projects in 20 folders, each with versions, shorts, assets
// and work folders, about 5 000 folders and 14 000 files.
func TestScanBig(t *testing.T) {
	if testing.Short() {
		t.Skip("a big tree")
	}
	top := t.TempDir()
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for d := range 20 {
		for p := range 10 {
			proj := filepath.Join(top, fmt.Sprintf("2026-%02d-01", d+1), fmt.Sprintf("project-%d", p))
			for v := 1; v <= 3; v++ {
				write(filepath.Join(proj, fmt.Sprintf("v%d", v), fmt.Sprintf("cut_v%d.mp4", v)))
			}
			for _, s := range []string{"one", "two"} {
				for v := 1; v <= 2; v++ {
					write(filepath.Join(proj, "shorts", s, fmt.Sprintf("v%d", v), fmt.Sprintf("%s_v%d.mp4", s, v)))
				}
			}
			for _, a := range []string{"a", "b", "c"} {
				for _, b := range []string{"d", "e"} {
					write(filepath.Join(proj, "assets", a, b, "x.png"))
				}
			}
			for i := range 50 {
				write(filepath.Join(proj, "work", "shots", fmt.Sprintf("%014x.mp4", i)))
			}
		}
	}
	scan := ScanDir(top)
	t.Logf("big tree: %d videos, %d folders read in %s", len(scan.Videos), scan.Dirs, scan.Took)
	if len(scan.Videos) != 600 || scan.Capped {
		t.Fatalf("big tree: %d videos, capped %v", len(scan.Videos), scan.Capped)
	}
	if scan.Took > 5*time.Second {
		t.Errorf("a scan of %d folders took %s", scan.Dirs, scan.Took)
	}
}

// TestScanReal times a scan of a real folder, read-only: RETAKE_SCAN_DIR=~/Videos go test -run TestScanReal -v.
func TestScanReal(t *testing.T) {
	dir := os.Getenv("RETAKE_SCAN_DIR")
	if dir == "" {
		t.Skip("set RETAKE_SCAN_DIR")
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		scan := ScanDir(dir)
		t.Logf("%s: %d videos, %d timeline projects, %d folders read in %s (capped %v)", dir, len(scan.Videos),
			len(scan.Empty), scan.Dirs, scan.Took, scan.Capped)
	}
	scan := ScanDir(dir)
	var cards []Card
	for _, v := range scan.Videos {
		cards = append(cards, Card{Name: v.Name, Path: v.Path, Project: v.Project})
	}
	t.Logf("\n%s", outline(BuildTree(filepath.Base(dir), cards, nil), ""))
}

func TestBuildTree(t *testing.T) {
	cards := []Card{
		{Name: "top", Path: "", Project: ""},                     // the folder's own video
		{Name: "shorts", Path: "show/shorts", Project: "show"},   // a project's shorts
		{Name: "show", Path: "show", Project: "show"},            // the project's own video comes first
		{Name: "b", Path: "Zeta/b", Project: "Zeta/b"},           // titles sort with case ignored
		{Name: "a", Path: "alpha/a", Project: "alpha/a"},         //
		{Name: "odd", Path: "x/y", Project: "q"},                 // a project that isn't above it: its own folder
		{Name: "bad", Path: "../up", Project: "../up"},           // not a place: the top
		{Name: "hosted", Path: "", Project: "", Title: "hosted"}, // made on the server: the top
	}
	tree := BuildTree("Your projects", cards, []string{"show", "empty/one", "../no"})
	if got := outline(tree, ""); got != strings.Join([]string{
		"Your projects* [hosted top bad]", // videos directly in it: a project
		"  alpha/", "    a* [a]",
		"  empty/", "    one* []",
		"  show* [show shorts]",
		"  x/", "    y* [odd]",
		"  Zeta/", "    b* [b]",
	}, "\n") {
		t.Errorf("tree:\n%s", got)
	}
	var top []string
	for _, v := range tree.Videos {
		top = append(top, v.Name)
	}
	if !reflect.DeepEqual(top, []string{"hosted", "top", "bad"}) || !tree.Project { // its own first: path ""
		t.Errorf("the top: %v %v", top, tree.Project)
	}
	// Empty lists are [] in JSON, never null.
	b, _ := json.Marshal(BuildTree("x", nil, nil))
	if string(b) != `{"title":"x","path":"","project":false,"videos":[],"children":[]}` {
		t.Errorf("empty tree: %s", b)
	}
}

func TestPaths(t *testing.T) {
	for p, ok := range map[string]bool{
		"": true, "a": true, "a/b c/d": true, "2026-10-19/fullgame": true, "Café": true,
		"/a": false, "a/": false, "a//b": false, "./a": false, "a/../b": false, "..": false, `a\b`: false,
		"a\x00b": false, "a\nb": false, strings.Repeat("a", 1025): false,
	} {
		if ValidPath(p) != ok {
			t.Errorf("ValidPath(%q) = %v", p, !ok)
		}
	}
	for _, c := range []struct {
		proj, path string
		ok         bool
	}{{"", "a/b", true}, {"a", "a/b", true}, {"a/b", "a/b", true}, {"a/b", "a", false}, {"ab", "a/b", false},
		{"a/b", "a/bc", false}} {
		if Within(c.proj, c.path) != c.ok {
			t.Errorf("Within(%q, %q) = %v", c.proj, c.path, !c.ok)
		}
	}
	for file, want := range map[string]string{
		"itemshop_v7.mp4": "itemshop", "v2/clip_v2_draft.mov": "clip", "fullgame_victor_1_v3_endcard.mp4": "fullgame_victor_1",
		"sunset.mp4": "sunset", "": "",
	} {
		if got := VideoName(file); got != want {
			t.Errorf("VideoName(%q) = %q, want %q", file, got, want)
		}
	}
}
