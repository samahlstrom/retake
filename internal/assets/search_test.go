package assets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/transcribe"
)

func words(text string, start float64) []transcribe.Word {
	var out []transcribe.Word
	t := start
	for _, w := range strings.Fields(text) {
		out = append(out, transcribe.Word{Text: " " + w, Start: t, End: t + 0.4, Prob: 0.9})
		t += 0.5
	}
	return out
}

func TestSearchRanking(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	add := func(name, title string, tags []string, lic string) string {
		write(t, filepath.Join(dir, name+".txt"), name+" content")
		return importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, name+".txt")}, Title: title, Tags: tags,
			License: LicenseArgs{Status: lic}}).ID
	}
	a := add("a", "Dragon fight", nil, LicenseOwned)
	b := add("b", "Forest walk", nil, LicenseOwned)
	c := add("c", "Castle", nil, LicenseCleared)
	dd := add("d", "Misc", []string{"dragon", "b-roll"}, LicenseReviewOnly)
	add("e", "Nothing here", nil, LicenseOwned)
	d, _ := e.c.catalogFor(ScopeProject, false)
	if err := d.putTranscript(ctx, b, "fake", 0, words("we walk on and then the dragon appears over the hill", 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.SpanAdd(ctx, SpanAddArgs{Asset: c, T0: 1, T1: 2, Label: "dragon breathes fire"}); err != nil {
		t.Fatal(err)
	}

	res, err := e.c.Search(ctx, SearchArgs{Query: "Dragon"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	byID := map[string]Hit{}
	for _, h := range res.Results {
		order = append(order, h.Asset)
		byID[h.Asset] = h
	}
	if len(order) != 4 || order[0] != a {
		t.Fatalf("order = %v (the title match first)", order)
	}
	pos := func(id string) int {
		for i, x := range order {
			if x == id {
				return i
			}
		}
		return -1
	}
	if pos(dd) > pos(b) {
		t.Errorf("a tag match should rank above a transcript match: %v", order)
	}
	if h := byID[a]; strings.Join(h.Matched, ",") != "title" {
		t.Errorf("a matched %v", h.Matched)
	}
	if h := byID[dd]; strings.Join(h.Matched, ",") != "tags" {
		t.Errorf("d matched %v", h.Matched)
	}
	// Transcript hits carry the stored times of the words, never estimates.
	if h := byID[b]; strings.Join(h.Matched, ",") != "transcript" || len(h.Transcript) != 1 || h.Transcript[0].T0 != 13 ||
		!strings.Contains(h.Transcript[0].Text, "dragon appears") {
		t.Errorf("b = %+v", h)
	}
	if h := byID[c]; len(h.Spans) != 1 || h.Spans[0].Span != "sp:1" || h.Spans[0].T0 != 1 || strings.Join(h.Matched, ",") != "span" {
		t.Errorf("c = %+v", h)
	}

	// Words are prefixes, and all of them must match.
	if res, _ := e.c.Search(ctx, SearchArgs{Query: "drag fig"}); len(res.Results) != 1 || res.Results[0].Asset != a {
		t.Errorf("drag fig = %+v", res.Results)
	}
	// FTS syntax in a query is just words.
	if _, err := e.c.Search(ctx, SearchArgs{Query: `"unbalanced AND (OR NEAR`}); err != nil {
		t.Errorf("odd query: %v", err)
	}
	// Structured filters.
	if res, _ := e.c.Search(ctx, SearchArgs{Query: "dragon", License: []string{LicenseOwned}}); len(res.Results) != 2 {
		t.Errorf("license filter = %+v", res.Results)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{Tags: []string{"b-roll"}}); len(res.Results) != 1 || res.Results[0].Asset != dd {
		t.Errorf("tag filter = %+v", res.Results)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{Kind: []string{"video"}}); len(res.Results) != 0 {
		t.Errorf("kind filter = %+v", res.Results)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{Scope: []string{"lib"}, Query: "dragon"}); len(res.Results) != 0 {
		t.Errorf("scope filter = %+v", res.Results)
	}
	if _, err := e.c.Search(ctx, SearchArgs{Scope: []string{"nowhere"}}); !isCode(err, CodeBadRequest) {
		t.Errorf("bad scope: %v", err)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{}); len(res.Results) != 5 {
		t.Errorf("no query lists everything: %d", len(res.Results))
	}
	// Span filters return spans grouped by asset.
	if res, _ := e.c.Search(ctx, SearchArgs{Type: []string{"tag"}, Query: "fire"}); len(res.Results) != 1 || res.Results[0].Asset != c {
		t.Errorf("type + query = %+v", res.Results)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{Where: "t0 >= 1 AND label LIKE '%breathes%'"}); len(res.Results) != 1 {
		t.Errorf("where = %+v", res.Results)
	}
}

// fakeTranscriber returns the same words for any file.
type fakeTranscriber struct{ calls int }

func (f *fakeTranscriber) Transcribe(_ context.Context, path string, _ transcribe.Options) ([]transcribe.Word, error) {
	f.calls++
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return words("say hello to the catalog", 0.5), nil
}

func (f *fakeTranscriber) Name() string { return "fake whisper" }

func TestImportTranscribes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ft := &fakeTranscriber{}
	e.c.o.Transcriber = func() (Transcriber, error) { return ft, nil }
	src := filepath.Join(t.TempDir(), "vo.wav")
	tone(t, src, 1.5, 300)
	it := importOne(t, e.c, ImportArgs{Paths: []string{src}, Transcribe: true})
	if len(it.Warnings) > 0 || ft.calls != 1 {
		t.Fatalf("import = %+v (calls %d)", it, ft.calls)
	}
	got, _ := e.c.Get(ctx, it.ID)
	if got.Transcript == nil || got.Transcript.Words != 5 || got.Transcript.Model != "fake whisper" {
		t.Fatalf("transcript = %+v", got.Transcript)
	}
	res, err := e.c.Search(ctx, SearchArgs{Query: "catalog"})
	if err != nil || len(res.Results) != 1 || len(res.Results[0].Transcript) != 1 || res.Results[0].Transcript[0].T0 != 2.5 {
		t.Fatalf("transcript search = %+v, %v", res, err)
	}
	// Without a transcriber it's a warning, not a failure.
	e.c.o.Transcriber = nil
	src2 := filepath.Join(t.TempDir(), "vo2.wav")
	tone(t, src2, 1, 500)
	it = importOne(t, e.c, ImportArgs{Paths: []string{src2}, Transcribe: true})
	if it.Status != StatusImported || len(it.Warnings) != 1 || !strings.Contains(it.Warnings[0], "isn't configured") {
		t.Fatalf("no transcriber = %+v", it)
	}
}

func TestResolveAndSet(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "take.bin"), "first take")
	it := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, "take.bin")}, InPlace: true})
	r, err := e.c.Resolve(ctx, it.ID)
	if err != nil || r.Path != filepath.Join(dir, "take.bin") || r.Hash != HashBytes([]byte("first take")) || r.Stale {
		t.Fatalf("resolve = %+v, %v", r, err)
	}
	// The file changes in place: resolve says so, with both hashes.
	write(t, filepath.Join(dir, "take.bin"), "second take!")
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, "take.bin"), later, later)
	r, err = e.c.Resolve(ctx, it.ID)
	if err != nil || !r.Stale || r.CatalogedHash != HashBytes([]byte("first take")) || r.Hash != HashBytes([]byte("second take!")) {
		t.Fatalf("stale resolve = %+v, %v", r, err)
	}
	// Rehash accepts the new content.
	if r, err := e.c.Rehash(ctx, it.ID); err != nil || r.Hash != HashBytes([]byte("second take!")) {
		t.Fatalf("rehash = %+v, %v", r, err)
	}
	if r, err := e.c.Resolve(ctx, it.ID); err != nil || r.Stale {
		t.Fatalf("after rehash = %+v, %v", r, err)
	}
	if _, err := e.c.Resolve(ctx, "proj:nope"); !isCode(err, CodeNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "take.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.Resolve(ctx, it.ID); !isCode(err, CodeNotFound) {
		t.Fatalf("missing file: %v", err)
	}

	// Set: the license gate's only door.
	if _, err := e.c.Set(ctx, SetArgs{ID: it.ID, License: &LicenseArgs{Status: LicenseAttribution}}); !isCode(err, CodeBadRequest) {
		t.Fatalf("attribution without a credit: %v", err)
	}
	title := "Take one"
	got, err := e.c.Set(ctx, SetArgs{ID: it.ID, Title: &title, AddTags: []string{"VO", "draft"}, RmTags: []string{"draft"},
		License: &LicenseArgs{Status: LicenseAttribution, Credit: "Voice: A. Person", ContentIDRisk: "low"}})
	if err != nil || got.Title != title || strings.Join(got.Tags, ",") != "vo" || got.License.Credit != "Voice: A. Person" ||
		!got.License.Publish || got.License.Note != "" {
		t.Fatalf("set = %+v, %v", got, err)
	}
	if _, err := e.c.Set(ctx, SetArgs{ID: it.ID, License: &LicenseArgs{Status: "free"}}); !isCode(err, CodeBadRequest) {
		t.Fatalf("bad status: %v", err)
	}
	if res, _ := e.c.Search(ctx, SearchArgs{Query: "take one"}); len(res.Results) != 1 {
		t.Fatalf("set reindexes: %+v", res)
	}
}
