package assets

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCSVIndexPlugin builds the example plug-in (plugins/csv-index) and imports a generated asset index with it.
func TestCSVIndexPlugin(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go isn't on PATH to build the plug-in")
	}
	e := newEnv(t)
	ctx := context.Background()
	pdir := filepath.Join(e.root, ".retake", "plugins", "csv-index")
	src, err := filepath.Abs(filepath.Join("..", "..", "plugins", "csv-index"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(pdir, "plugin.json"), string(manifest))
	exe := "csv-index"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command(gobin, "build", "-o", filepath.Join(pdir, exe), ".")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building csv-index: %v\n%s", err, out)
	}

	data := t.TempDir()
	picture(t, filepath.Join(data, "memes", "absolute-cinema__peak-moment.png"), 120, 90, 0)
	picture(t, filepath.Join(data, "kenney", "badge.png"), 64, 64, 1)
	write(t, filepath.Join(data, "INDEX.csv"), `filename,type,tags_when_to_use,source_url,license
memes/absolute-cinema__peak-moment.png,meme-image,peak moment / meme,https://example.com/x,"Unknown - user-uploaded meme (no license stated); copyrighted-meme risk, not CC"
kenney/badge.png,image,ui / badge,https://kenney.nl,CC0 1.0
missing/gone.png,image,gone,,owned
`)
	res, err := e.c.Import(ctx, ImportArgs{Via: "csv-index", Paths: []string{filepath.Join(data, "INDEX.csv")}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Skipped != 1 {
		t.Fatalf("csv import = %+v", res)
	}
	meme, err := e.c.Get(ctx, "proj:csv-index/memes/absolute-cinema__peak-moment.png")
	if err != nil {
		t.Fatal(err)
	}
	if meme.Title != "absolute cinema" || meme.License.Status != LicenseReviewOnly || meme.License.ContentIDRisk != "high" ||
		!meme.Blob.InStore || meme.Previews.Thumbnail == "" || meme.Provenance.Importer != "plugin:csv-index" {
		t.Fatalf("meme = %+v", meme)
	}
	badge, err := e.c.Get(ctx, "proj:csv-index/kenney/badge.png")
	if err != nil || badge.License.Status != LicenseCleared || badge.Plugin != "csv-index" {
		t.Fatalf("badge = %+v, %v", badge, err)
	}
	if hits, _ := e.c.Search(ctx, SearchArgs{Query: "peak", License: []string{LicenseReviewOnly}}); len(hits.Results) != 1 {
		t.Fatalf("search = %+v", hits)
	}
	q, err := e.c.PluginQuery(ctx, PluginQueryArgs{Plugin: "csv-index", Tool: "columns",
		Args: map[string]any{"path": filepath.Join(data, "INDEX.csv")}})
	if err != nil || q.Result == nil {
		t.Fatalf("columns = %+v, %v", q, err)
	}
}
