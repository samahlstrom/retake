package assets

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func libraryComponent(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "library", "components", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComponentIsOneEntryWithItsPreviews(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.vars["RETAKE_EDIT"] = filepath.Join(t.TempDir(), "no-retake-edit") // the committed sheet is the fallback
	dir := libraryComponent(t, "pop-text")
	res, err := e.c.Import(ctx, ImportArgs{Paths: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	it := res.Items[0]
	if res.Imported != 1 || it.ID != "proj:components/pop-text@1.0.0" || it.Kind != KindComponent {
		t.Fatalf("import = %+v", res)
	}
	if !slices.ContainsFunc(it.Warnings, func(w string) bool { return strings.Contains(w, "previews from the package") }) {
		t.Fatalf("warnings = %v", it.Warnings)
	}
	hash, _ := PackageHash(dir)
	ent, err := e.c.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	comp, _ := ent.Meta["component"].(map[string]any)
	if ent.Kind != KindComponent || ent.License.Status != LicenseCleared || !ent.License.Publish ||
		comp["hash"] != hash || comp["ref"] != "pop-text@1.0.0" || *ent.Blob.Hash != hash || ent.Blob.InStore ||
		!ent.Blob.Exists || !slices.Contains(ent.Tags, "component") || !slices.Contains(ent.Tags, "pop") ||
		ent.Title != "Pop text" || !strings.Contains(ent.Caption, "word or two") {
		t.Fatalf("entry = %+v", ent)
	}
	if ent.Previews.Storybook == "" || ent.Previews.Paths["storybook"] == "" {
		t.Fatalf("previews = %+v", ent.Previews)
	}
	pv, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID})
	if err != nil || pv.Kind != PreviewStorybook || len(pv.Files) != 1 || pv.Files[0].Width < 1000 {
		t.Fatalf("preview = %+v %v", pv, err)
	}
	if _, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, Kind: PreviewAnimation}); err == nil ||
		!strings.Contains(err.Error(), "component preview") {
		t.Fatalf("no animation without retake-edit: %v", err)
	}
	if _, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, Kind: PreviewContactSheet}); err == nil {
		t.Fatal("a component has no contact sheet")
	}
	// Found by search, by kind and by words.
	sr, err := e.c.Search(ctx, SearchArgs{Query: "emphasis", Kind: []string{KindComponent}})
	if err != nil || len(sr.Results) != 1 || sr.Results[0].Asset != it.ID {
		t.Fatalf("search = %+v %v", sr, err)
	}
	// Again: the same package is a duplicate; another one under the same version is refused.
	if res, _ := e.c.Import(ctx, ImportArgs{Paths: []string{dir}}); res.Duplicates != 1 {
		t.Fatalf("again = %+v", res)
	}
	changed := filepath.Join(t.TempDir(), "pop-text")
	if err := copyTree(dir, changed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changed, "NOTES.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.c.Import(ctx, ImportArgs{Paths: []string{changed}}); res.Skipped != 1 ||
		!strings.Contains(res.Items[0].Reason, "never changes") {
		t.Fatalf("changed = %+v", res)
	}
	// Into the library: a copy where lib search and the editor look.
	res, err = e.c.Import(ctx, ImportArgs{Paths: []string{libraryComponent(t, "counter")}, Scope: "lib"})
	if err != nil || res.Items[0].ID != "lib:components/counter@1.0.0" {
		t.Fatalf("lib import = %+v %v", res, err)
	}
	if h, err := PackageHash(filepath.Join(e.lib, "components", "counter@1.0.0")); err != nil || h != res.Items[0].Hash {
		t.Fatalf("library copy: %s %v", h, err)
	}
}

func TestMigrationToTheComponentKindKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	// A catalog at schema version 1, with an asset and a span on it.
	sdb, err := sql.Open("sqlite", dsn(filepath.Join(dir, DBName), false))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(sdb, migrations[:1]); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO assets (id, scope, kind, locator, created, updated) VALUES ('proj:a', 'project', 'video', 'file:///a', 'x', 'x')`,
		`INSERT INTO spans (asset, t0, t1, type, created, updated) VALUES ('proj:a', 1, 2, 'tag', 'x', 'x')`,
	} {
		if _, err := sdb.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = sdb.Close()
	d, err := openDB(dir, ScopeProject, false, nil)
	if err != nil || d == nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.close() }()
	var v, spans int
	_ = d.rw.QueryRow("PRAGMA user_version").Scan(&v)
	_ = d.rw.QueryRow("SELECT COUNT(*) FROM spans WHERE asset = 'proj:a'").Scan(&spans)
	if v != SchemaVersion || spans != 1 {
		t.Fatalf("version %d, spans %d", v, spans)
	}
	if _, err := d.rw.Exec(`INSERT INTO assets (id, scope, kind, locator, created, updated)
		VALUES ('proj:c', 'project', 'component', 'file:///c', 'x', 'x')`); err != nil {
		t.Fatalf("the new kind: %v", err)
	}
	if _, err := d.rw.Exec(`INSERT INTO assets (id, scope, kind, locator, created, updated)
		VALUES ('proj:d', 'project', 'gif', 'file:///d', 'x', 'x')`); err == nil {
		t.Fatal("the check still holds")
	}
	// Foreign keys are back on: deleting the asset cascades to its span.
	if _, err := d.rw.Exec(`DELETE FROM assets WHERE id = 'proj:a'`); err != nil {
		t.Fatal(err)
	}
	_ = d.rw.QueryRow("SELECT COUNT(*) FROM spans").Scan(&spans)
	if spans != 0 {
		t.Fatalf("%d spans after the cascade", spans)
	}
}
