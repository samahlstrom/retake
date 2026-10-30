package server

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/project"
)

// bridgeFixture is the demo's v1 and v2 (bridge/testdata: 0.3 s of VO trimmed after 2 s, the picture-in-picture made
// bigger) as a family with their render manifests, and v1's notes after the push of v2: #1 on the pip answered by
// v2, #2 on the title answered, #3 on the music card asked about, and #4 recorded on v1 after the push (open).
func bridgeFixture(t *testing.T) (root string, st *project.Store) {
	t.Helper()
	root = t.TempDir()
	for _, v := range []string{"v1", "v2"} {
		dir := filepath.Join(root, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo_"+v+".mp4"), []byte("video "+v), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join("..", "bridge", "testdata", "demo_"+v+".manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo_"+v+".manifest.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := func(seg string, t0 float64, text string) project.Comment {
		return project.Comment{Take: "2026-10-30_12-00-00", Seg: seg, N: 1, T0: t0, T1: t0 + 0.5, Text: text, Author: "Ana"}
	}
	if _, err := st.Sync(project.Run{Version: "v1", Comments: []project.Comment{
		c("S1", 9, "bigger pip"), c("S2", 1, "keep the title"), c("S3", 13, "card too dark"),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordPush(project.Push{Label: "v2", Path: filepath.Join(root, "v2", "demo_v2.mp4"), Parent: "v1",
		By: "agent", Scope: project.Scope{Version: "v1"},
		Changelog: []project.ChangeEntry{{Note: 1, Summary: "pip 40%"}, {Note: 2, Summary: "kept"}},
		Asks:      []project.PushAsk{{Note: 3, Text: "lighter card?"}}}); err != nil {
		t.Fatal(err)
	}
	later := c("S1", 2.5, "this cut is late")
	later.Take = "2026-10-30_13-00-00"
	if _, err := st.Sync(project.Run{Version: "v1", Comments: []project.Comment{
		c("S1", 9, "bigger pip"), c("S2", 1, "keep the title"), c("S3", 13, "card too dark"), later,
	}}); err != nil {
		t.Fatal(err)
	}
	return root, st
}

func TestTimemapRoute(t *testing.T) {
	root, _ := bridgeFixture(t)
	h := start(t, Options{Video: filepath.Join(root, "v2", "demo_v2.mp4")})
	t.Cleanup(func() { _ = h.srv.Close() })
	r := h.get("/api/timemap?from=v1&to=v2", nil)
	var tm bridge.TimeMap
	if r.code != 200 || json.Unmarshal(r.body, &tm) != nil || tm.From != "v1" || tm.To != "v2" || len(tm.Segments) == 0 {
		t.Fatalf("timemap: %d %s", r.code, r.body)
	}
	if got := tm.Map(9); math.Abs(got-8.7) > 1e-3 {
		t.Fatalf("9 -> %v", got)
	}
	if r := h.get("/api/timemap?from=v1&to=v9", nil); r.code != 404 {
		t.Fatalf("unknown version: %d", r.code)
	}
	if r := h.get("/api/timemap?from=v1", nil); r.code != 400 {
		t.Fatalf("no to: %d", r.code)
	}
	// Without a manifest the map is empty: the page keeps the time.
	if err := os.Remove(filepath.Join(root, "v2", "demo_v2.manifest.json")); err != nil {
		t.Fatal(err)
	}
	r = h.get("/api/timemap?from=v1&to=v2", nil)
	if r.code != 200 || string(r.body) != `{"from":"v1","to":"v2","segments":[]}`+"\n" && string(r.body) != `{"from":"v1","to":"v2","segments":[]}` {
		t.Fatalf("no manifest: %d %s", r.code, r.body)
	}
}

type carriedItem struct {
	ID      int64                `json:"id"`
	Kind    string               `json:"kind"`
	T0      float64              `json:"t0"`
	Note    *int64               `json:"note"`
	Text    string               `json:"text"`
	Carried *project.CarriedFrom `json:"carried"`
}

func TestSidebarCarriesTheParentsNotes(t *testing.T) {
	root, _ := bridgeFixture(t)
	h := start(t, Options{Video: filepath.Join(root, "v2", "demo_v2.mp4")})
	t.Cleanup(func() { _ = h.srv.Close() })
	r := h.get("/api/notes?v=v2", nil)
	var res struct {
		Newest bool          `json:"newest"`
		Items  []carriedItem `json:"items"`
	}
	if r.code != 200 || json.Unmarshal(r.body, &res) != nil || !res.Newest {
		t.Fatalf("notes: %d %s", r.code, r.body)
	}
	type want struct {
		kind string
		t0   float64
		as   string
		was  float64
	}
	got := map[string]want{}
	for _, it := range res.Items {
		if it.Carried == nil {
			t.Fatalf("an item that isn't carried: %+v", it)
		}
		key := it.Text
		got[key] = want{it.Kind, math.Round(it.T0*1000) / 1000, it.Carried.As, it.Carried.T0}
		if it.Carried.From != "v1" {
			t.Fatalf("from = %s", it.Carried.From)
		}
	}
	exp := map[string]want{
		"keep the title":   {"note", 1, "unchanged", 1},   // the title unit is the same bytes in v2
		"this cut is late": {"note", 2.5, "changed", 2.5}, // open, in the footage shot the trim shortened
		"bigger pip":       {"note", 8.7, "check", 9},     // answered, in a changed unit: answered, check it
		"lighter card?":    {"question", 12.7, "changed", 13},
	}
	if len(got) != len(exp) {
		t.Fatalf("items = %+v", got)
	}
	for k, w := range exp {
		if got[k] != w {
			t.Errorf("%q = %+v, want %+v", k, got[k], w)
		}
	}
	// In time order on v2.
	for i := 1; i < len(res.Items); i++ {
		if res.Items[i].T0 < res.Items[i-1].T0 {
			t.Fatalf("not in time order: %+v", res.Items)
		}
	}
	// An older version shows its own notes as resolved, nothing carried.
	r = h.get("/api/notes?v=v1", nil)
	var old struct {
		Items []carriedItem `json:"items"`
	}
	if json.Unmarshal(r.body, &old) != nil || len(old.Items) == 0 {
		t.Fatalf("v1: %s", r.body)
	}
	for _, it := range old.Items {
		if it.Carried != nil {
			t.Fatalf("v1 item carried: %+v", it)
		}
	}
	// Without v1's manifest nothing carries: the question stays at v1's time, and v1's notes stay on v1.
	if err := os.Remove(filepath.Join(root, "v1", "demo_v1.manifest.json")); err != nil {
		t.Fatal(err)
	}
	r = h.get("/api/notes?v=v2", nil)
	var plain struct {
		Items []carriedItem `json:"items"`
	}
	if json.Unmarshal(r.body, &plain) != nil || len(plain.Items) != 1 || plain.Items[0].Kind != "question" ||
		plain.Items[0].T0 != 13 || plain.Items[0].Carried != nil {
		t.Fatalf("no manifest: %s", r.body)
	}
}
