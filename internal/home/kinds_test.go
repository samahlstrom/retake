package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kindAt stands in for an enabled Scripts: v0/ with idea.md, and no versioned video in it.
func kindAt(dir string) string {
	if st, err := os.Stat(filepath.Join(dir, "v0", "idea.md")); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "v0"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".mp4") {
			return ""
		}
	}
	return "scripts.doc"
}

// Kinds in the scan (spec/extensions.md, "Version kinds"): an idea folder (only v0/) is a video, so is a video with
// v0 and cuts; a v0/ without its marker isn't, and a v0/clip_v0.mp4 is a video version as before.
func TestScanFindsKinds(t *testing.T) {
	top := filepath.Join(t.TempDir(), "deadlock")
	now := time.Now()
	f := func(rel string) { touch(t, filepath.Join(top, filepath.FromSlash(rel)), now) }
	f("jump-souls/v0/idea.md")
	f("jump-souls/v0/voice/20261101-190211.webm")
	f("item-shop/v0/idea.md")
	f("item-shop/v1/item-shop_v1.mp4")
	f("notes-only/v0/outline.md")
	f("clip/v0/clip_v0.mp4")
	f("clip/v0/idea.md")
	scan := ScanDir(top, kindAt)
	got := map[string]Found{}
	for _, v := range scan.Videos {
		got[v.Name] = v
	}
	if v, ok := got["jump-souls"]; !ok || v.Kind != "scripts.doc" || v.Anchor != "" || v.Project != "" {
		t.Fatalf("idea: %+v (%v)", v, places(scan.Videos))
	}
	if v, ok := got["item-shop"]; !ok || v.Kind != "scripts.doc" || filepath.Base(v.Anchor) != "item-shop_v1.mp4" {
		t.Fatalf("a video with v0 and a cut: %+v", v)
	}
	if _, ok := got["notes-only"]; ok {
		t.Fatal("a v0/ without its marker is a video")
	}
	if v, ok := got["clip"]; !ok || v.Kind != "" || filepath.Base(v.Anchor) != "clip_v0.mp4" {
		t.Fatalf("v0/clip_v0.mp4 is a video version: %+v", v)
	}
	// Without kinds (an extension off), the idea isn't found.
	for _, v := range ScanDir(top).Videos {
		if v.Name == "jump-souls" {
			t.Fatal("found with no kinds on")
		}
	}
}

// An idea's card: no cut, its kind, no thumbnail; a video with v0 lists the kind.
func TestCardsOfKinds(t *testing.T) {
	idea := Make(Source{Name: "jump-souls", Title: "jump-souls", Info: []byte(`{"label": "v0", "kind": "scripts.doc",
		"path": "/x/jump-souls/v0", "name": "v0", "duration": null, "versions": [{"label": "v0", "kind": "scripts.doc",
		"name": "v0", "duration": null, "modified": 1790000000000}]}`)})
	if idea.Status != NoCut || idea.Kind != "scripts.doc" || idea.Version != "v0" || idea.Thumb != "" ||
		idea.Changed != 1790000000000 || idea.Video != "" || len(idea.Kinds) != 1 {
		t.Fatalf("%+v", idea)
	}
	cut := Make(Source{Name: "item-shop", Title: "item-shop", Info: []byte(`{"label": "v1", "path": "/x/v1/item-shop_v1.mp4",
		"name": "item-shop_v1.mp4", "duration": 3, "versions": [{"label": "v0", "kind": "scripts.doc", "name": "v0",
		"modified": 5}, {"label": "v1", "name": "item-shop_v1.mp4", "duration": 3}]}`)})
	if cut.Status == NoCut || cut.Kind != "" || len(cut.Kinds) != 1 || cut.Version != "v1" {
		t.Fatalf("%+v", cut)
	}
}
