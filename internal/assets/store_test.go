package assets

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsAndReadsNeverCreate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// Reads on an empty project don't create a catalog.
	if res, err := e.c.Search(ctx, SearchArgs{Query: "anything"}); err != nil || len(res.Results) != 0 {
		t.Fatalf("search on nothing = %+v, %v", res, err)
	}
	if _, err := e.c.Get(ctx, "proj:nope"); !isCode(err, CodeNotFound) {
		t.Fatalf("get on nothing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".retake", DBName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read created the catalog: %v", err)
	}
	if _, err := os.Stat(e.lib); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read created the library: %v", err)
	}

	// A write creates it at the current schema version; opening again doesn't migrate twice.
	write(t, filepath.Join(e.root, "notes.txt"), "plain text")
	it := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(e.root, "notes.txt")}})
	if it.Status != StatusImported || it.ID != "proj:notes" || it.Kind != KindOther {
		t.Fatalf("import = %+v", it)
	}
	e.reopen(t)
	if _, err := e.c.Get(ctx, "proj:notes"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.root, ".retake", DBName)
	sdb, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sdb.Close() }()
	var v int
	if err := sdb.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if err := migrate(sdb, migrations); err != nil {
		t.Fatalf("migrating an up-to-date catalog: %v", err)
	}
	// A catalog from a newer retake is refused, not touched.
	if _, err := sdb.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(sdb, migrations); err == nil || !strings.Contains(err.Error(), "newer than this retake") {
		t.Fatalf("newer catalog: %v", err)
	}
}

func isCode(err error, code string) bool {
	var ae *Error
	return errors.As(err, &ae) && ae.Code == code
}

func TestIDsAndNames(t *testing.T) {
	for in, want := range map[string]string{
		"Memes/Thanos REM (final)!":            "memes/thanos-rem-final",
		"a-few-moments-later__time-skip__meme": "a-few-moments-later__time-skip__meme",
		"  ..odd..  ":                          "odd",
		"日本語":                                  "asset",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	for id, want := range map[string][2]string{
		"proj:a/b": {ScopeProject, "a/b"}, "lib:sfx/x": {ScopeLib, "sfx/x"}, "brand:k@2/logo": {ScopeBrand, "k@2/logo"},
	} {
		scope, name, ok := ParseID(id)
		if !ok || scope != want[0] || name != want[1] {
			t.Errorf("ParseID(%q) = %q %q %v", id, scope, name, ok)
		}
	}
	if _, _, ok := ParseID("logo"); ok {
		t.Error("an unprefixed id parsed as scoped")
	}
	for _, s := range []string{"sp:12", "sp:lib/7"} {
		scope, n, err := ParseSpanID(s)
		if err != nil || SpanID(scope, n) != s {
			t.Errorf("span id %s: %s %d %v", s, scope, n, err)
		}
	}
	for _, bad := range []string{"sp:", "sp:x", "12", "sp:-1"} {
		if _, _, err := ParseSpanID(bad); err == nil {
			t.Errorf("ParseSpanID(%q) accepted", bad)
		}
	}
	if compareVersions("10", "9") <= 0 || compareVersions("1.2", "1.10") >= 0 || compareVersions("2", "2") != 0 {
		t.Error("compareVersions")
	}
	for in, want := range map[string]float64{"75.5": 75.5, "1:02": 62, "1:02:03.5": 3723.5} {
		if got, err := ParseTime(in); err != nil || got != want {
			t.Errorf("ParseTime(%q) = %g, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "1:75", "-3", "1:2:3:4"} {
		if _, err := ParseTime(bad); err == nil {
			t.Errorf("ParseTime(%q) accepted", bad)
		}
	}
}

func TestScopeLookupOrder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	src := t.TempDir()
	picture(t, filepath.Join(src, "proj", "logo.png"), 64, 48, 0)
	picture(t, filepath.Join(src, "lib", "logo.png"), 64, 48, 1)
	picture(t, filepath.Join(src, "lib", "badge.png"), 40, 40, 0)

	if it := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "lib", "logo.png")}, Scope: "lib"}); it.ID != "lib:logo" {
		t.Fatalf("lib import = %+v", it)
	}
	if it := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "proj", "logo.png")}}); it.ID != "proj:logo" {
		t.Fatalf("proj import = %+v", it)
	}
	if got, err := e.c.Get(ctx, "logo"); err != nil || got.ID != "proj:logo" || got.Scope != ScopeProject {
		t.Fatalf("logo -> %s, %v (the project comes first)", got.ID, err)
	}
	if got, err := e.c.Get(ctx, "lib:logo"); err != nil || got.Scope != ScopeLib {
		t.Fatalf("lib:logo -> %+v, %v", got, err)
	}

	// Brand kits: newest version by default, a pinned one on request, and an unprefixed id between project and lib.
	for _, v := range []string{"1", "2"} {
		kit := filepath.Join(src, "kit-v"+v)
		picture(t, filepath.Join(kit, "badge.png"), 30+len(v)*7, 30, 0)
		write(t, filepath.Join(kit, "kit.json"), `{"schema": "retake-kit/0.1", "name": "studio", "version": "`+v+`",
			"license": {"status": "owned"}, "tokens": {"colors": {"accent": "#ffcc00"}},
			"assets": [{"key": "badge", "path": "badge.png", "title": "Badge v`+v+`"}]}`)
		res, err := e.c.InstallKit(ctx, kit)
		if err != nil || res.Imported != 1 || res.Kit != "brand:studio@"+v {
			t.Fatalf("kit v%s = %+v, %v", v, res, err)
		}
	}
	importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "lib", "badge.png")}, Scope: "lib"})
	if got, err := e.c.Get(ctx, "brand:studio/badge"); err != nil || got.ID != "brand:studio@2/badge" ||
		got.License.Status != LicenseOwned || !got.Blob.InStore {
		t.Fatalf("brand:studio/badge -> %+v, %v", got, err)
	}
	if got, err := e.c.Get(ctx, "brand:studio@1/badge"); err != nil || got.Title != "Badge v1" {
		t.Fatalf("pinned -> %+v, %v", got, err)
	}
	if got, err := e.c.Get(ctx, "badge"); err != nil || got.ID != "brand:studio@2/badge" {
		t.Fatalf("badge -> %s, %v (brand before lib)", got.ID, err)
	}
	// A second kit holding the same key makes the unprefixed id ambiguous.
	other := filepath.Join(src, "kit-other")
	picture(t, filepath.Join(other, "b.png"), 20, 20, 1)
	write(t, filepath.Join(other, "kit.json"), `{"name": "zine", "version": "1", "assets": [{"key": "badge", "path": "b.png"}]}`)
	if _, err := e.c.InstallKit(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.Get(ctx, "badge"); !isCode(err, CodeAmbiguous) || !strings.Contains(err.Error(), "brand:zine@1/badge") {
		t.Fatalf("ambiguous badge: %v", err)
	}
	if got, err := e.c.Get(ctx, "brand:zine/badge"); err != nil || got.License.Status != LicenseReviewOnly {
		t.Fatalf("a kit without a license is review-only: %+v, %v", got.License, err)
	}
	if _, err := e.c.Get(ctx, "brand:studio/nope"); !isCode(err, CodeNotFound) {
		t.Fatalf("missing brand asset: %v", err)
	}
	// Reinstalling a kit version skips what's there.
	res, err := e.c.InstallKit(ctx, filepath.Join(src, "kit-v2"))
	if err != nil || res.Skipped != 1 || res.Imported != 0 {
		t.Fatalf("reinstall = %+v, %v", res, err)
	}
	// Brand assets can't be imported directly.
	if _, err := e.c.Import(ctx, ImportArgs{Paths: []string{filepath.Join(src, "lib", "badge.png")}, Scope: "brand"}); !isCode(err, CodeBadRequest) {
		t.Fatalf("brand import: %v", err)
	}
}

func TestBlobStoreAndInPlace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "small.bin"), "small file")
	write(t, filepath.Join(src, "big.bin"), strings.Repeat("x", 4096))
	e.c.o.InPlaceOver = 1024

	small := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "small.bin")}})
	got, err := e.c.Get(ctx, small.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Blob.InStore || !strings.HasPrefix(got.Blob.Locator, "blob:b3:") || !got.Blob.Exists ||
		!strings.HasSuffix(got.Blob.Path, ".bin") || got.Blob.Hash == nil || *got.Blob.Hash != HashBytes([]byte("small file")) {
		t.Fatalf("stored blob = %+v", got.Blob)
	}
	data, err := os.ReadFile(got.Blob.Path)
	if err != nil || string(data) != "small file" {
		t.Fatalf("blob content %q, %v", data, err)
	}
	big := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "big.bin")}})
	got, err = e.c.Get(ctx, big.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Blob.InStore || got.Blob.Path != filepath.Join(src, "big.bin") || !strings.HasPrefix(got.Blob.Locator, "file:///") {
		t.Fatalf("big file should stay in place: %+v", got.Blob)
	}
	// --in-place keeps even a small file where it is.
	write(t, filepath.Join(src, "keep.bin"), "keep me here")
	kept := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(src, "keep.bin")}, InPlace: true})
	if got, _ := e.c.Get(ctx, kept.ID); got.Blob.InStore {
		t.Fatal("--in-place copied the file")
	}
	// Hashes are cached by path, size and mtime.
	d, _ := e.c.catalogFor(ScopeProject, false)
	var n int
	if err := d.rw.QueryRow(`SELECT COUNT(*) FROM hash_cache`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("hash cache rows = %d, %v", n, err)
	}
}
