package assets

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// licenseFixture catalogs a VO, music, a meme, an SFX and a clip, and writes a timeline that uses them.
func licenseFixture(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	dir := filepath.Join(e.root, "media")
	add := func(name, scope string, lic LicenseArgs) {
		write(t, filepath.Join(dir, name+".bin"), "content of "+name)
		importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, name+".bin")}, Scope: scope, License: lic})
	}
	add("vo", "proj", LicenseArgs{Status: LicenseOwned})
	add("music", "proj", LicenseArgs{Status: LicenseAttribution, Credit: `Music: "Example Tune" by A. Composer, CC BY 4.0`})
	add("meme", "proj", LicenseArgs{Status: LicenseReviewOnly, ContentIDRisk: "high"})
	add("whoosh", "lib", LicenseArgs{Status: LicenseCleared, Credit: "SFX: Example Pack (CC0)"})
	add("clip", "proj", LicenseArgs{Status: LicenseOwned})
	write(t, filepath.Join(dir, "unknown.ttf"), "not cataloged")
	tl := `{
  "schema": "timeline/0.1", "id": "demo", "rev": 3,
  "format": {"size": [1920, 1080], "fps": 30},
  "assets": {
    "proj:vo": {"path": "media/vo.bin"},
    "proj:meme": {"path": "media/meme.bin"},
    "proj:footage": {"path": "media/clip.bin"},
    "lib:whoosh": {"path": "${RETAKE_LIB}/whoosh.bin"},
    "proj:music": {"path": "media/music.bin"}
  },
  "fonts": {"custom": {"path": "media/unknown.ttf"}},
  "spine": [
    {"id": "open_a", "asset": "proj:vo", "in": 0.2, "out": 4.1},
    {"id": "meme", "kind": "insert", "asset": "proj:meme", "in": 8.85, "frames": 45},
    {"id": "open_b", "asset": "proj:vo", "in": 4.9, "out": 9.0}
  ],
  "scenes": [{"id": "s1",
    "shots": [{"id": "fight", "at": "start:open_a", "picture": {"layers": [
      {"id": "fight/v", "type": "video", "asset": "proj:footage", "in": 2.0},
      {"id": "fight/t", "type": "text", "text": "GO"}]}}],
    "overlays": [{"id": "pop", "from": "word:open_b/flea", "to": "word:open_b/No-0.05", "layers": [
      {"id": "pop/sfx", "type": "image", "asset": "lib:whoosh"},
      {"id": "pop/t", "type": "text", "text": "NOT A FLEA MARKET", "font": "custom"}]}]}],
  "audio": [{"id": "bed", "asset": "proj:music", "from": "start:open_a", "loop": true}]
}`
	path := filepath.Join(e.root, "timeline.json")
	write(t, path, tl)
	return e, path
}

func TestLicenseReportTimeline(t *testing.T) {
	e, path := licenseFixture(t)
	ctx := context.Background()
	// Without retake-edit, uses list their anchors as written.
	e.c.o.EditBin = filepath.Join(t.TempDir(), "no-such-retake-edit")
	rep, err := e.c.LicenseReport(ctx, LicenseReportArgs{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != "timeline" || rep.Publishable || rep.Resolved {
		t.Fatalf("report = %+v", rep)
	}
	byAsset := map[string]AssetUse{}
	for _, a := range rep.Assets {
		byAsset[firstNonEmpty(a.Asset, a.TimelineID)] = a
	}
	if len(byAsset) != 7 {
		t.Fatalf("assets = %+v", rep.Assets)
	}
	if vo := byAsset["proj:vo"]; len(vo.Uses) != 2 || vo.Uses[0].Anchors["in"] != "t:0.2" || vo.License.Status != LicenseOwned {
		t.Errorf("vo = %+v", vo)
	}
	// proj:footage isn't a catalog id: it's matched by its file.
	if clip := byAsset["proj:clip"]; clip.TimelineID != "proj:footage" || clip.MatchedBy != "path" &&
		clip.MatchedBy != "hash" || clip.Uses[0].Element != "fight" || clip.Uses[0].Anchors["at"] != "start:open_a" {
		t.Errorf("clip = %+v", clip)
	}
	if f := byAsset["font:display"]; f.License == nil || f.License.Status != LicenseCleared || f.MatchedBy != "builtin" {
		t.Errorf("built-in font = %+v", f)
	}
	blockers := map[string]string{}
	for _, b := range rep.Blockers {
		blockers[b.Asset] = b.Reason
	}
	if len(blockers) != 2 || !strings.Contains(blockers["proj:meme"], "review-only") ||
		!strings.Contains(blockers["font:custom"], "not in the catalog") {
		t.Errorf("blockers = %+v", rep.Blockers)
	}
	if w := strings.Join(rep.Warnings, "\n"); len(rep.Warnings) != 2 || !strings.Contains(w, "proj:meme: content_id_risk high") ||
		!strings.Contains(w, "anchors as written") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if !strings.Contains(rep.Credits, "Example Tune") || !strings.Contains(rep.Credits, "Example Pack (CC0)") {
		t.Errorf("credits = %q", rep.Credits)
	}

	// With retake-edit's resolved times, each use has output seconds and ranges merge.
	resolved := filepath.Join(t.TempDir(), "resolved.json")
	write(t, resolved, `{"ok": true, "fps": 30, "end": 12.0, "frames": 360,
	  "spine": [{"id": "open_a", "start": 0, "end": 3.9}, {"id": "meme", "start": 3.9, "end": 5.4},
	            {"id": "open_b", "start": 5.4, "end": 9.5}],
	  "shots": [{"id": "fight", "start": 0, "end": 12.0}],
	  "overlays": [{"id": "pop", "from": 6.1, "to": 7.0}],
	  "audio": [{"id": "bed", "from": 0, "to": 12.0}]}`)
	rep, err = e.c.LicenseReport(ctx, LicenseReportArgs{Path: path, Resolved: resolved})
	if err != nil || !rep.Resolved {
		t.Fatalf("resolved report = %+v, %v", rep, err)
	}
	for _, a := range rep.Assets {
		switch firstNonEmpty(a.Asset, a.TimelineID) {
		case "proj:vo":
			if len(a.Ranges) != 2 || a.Ranges[0] != [2]float64{0, 3.9} || a.Ranges[1] != [2]float64{5.4, 9.5} {
				t.Errorf("vo ranges = %v", a.Ranges)
			}
		case "proj:meme":
			if len(a.Ranges) != 1 || a.Ranges[0] != [2]float64{3.9, 5.4} {
				t.Errorf("meme ranges = %v", a.Ranges)
			}
		case "font:custom":
			if len(a.Ranges) != 1 || a.Ranges[0] != [2]float64{0, 12} {
				t.Errorf("font ranges = %v", a.Ranges)
			}
		}
	}
	for _, b := range rep.Blockers {
		if b.Asset == "proj:meme" && (len(b.Ranges) != 1 || b.Ranges[0] != [2]float64{3.9, 5.4}) {
			t.Errorf("meme blocker ranges = %v", b.Ranges)
		}
	}

	// retake-edit on PATH (or $RETAKE_EDIT) is run when no file is given.
	if runtime.GOOS != "windows" {
		bin := filepath.Join(t.TempDir(), "retake-edit")
		data, _ := os.ReadFile(resolved)
		write(t, bin, "#!/bin/sh\ncat <<'EOF'\n"+string(data)+"\nEOF\n")
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		e.c.o.EditBin = ""
		e.vars["RETAKE_EDIT"] = bin
		if rep, err := e.c.LicenseReport(ctx, LicenseReportArgs{Path: path}); err != nil || !rep.Resolved {
			t.Fatalf("retake-edit run = %+v, %v", rep.Warnings, err)
		}
	}

	// Clearing the meme and cataloging the font makes it publishable.
	if _, err := e.c.Set(ctx, SetArgs{ID: "proj:meme", License: &LicenseArgs{Status: LicenseCleared}}); err != nil {
		t.Fatal(err)
	}
	importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(e.root, "media", "unknown.ttf")},
		License: LicenseArgs{Status: LicenseCleared, Credit: "Font: Example Sans (OFL)"}})
	rep, err = e.c.LicenseReport(ctx, LicenseReportArgs{Path: path, Resolved: resolved})
	if err != nil || !rep.Publishable || len(rep.Blockers) != 0 || !strings.Contains(rep.Credits, "Example Sans") {
		t.Fatalf("clean report = %+v, %v", rep, err)
	}
}

func TestLicenseReportManifest(t *testing.T) {
	e, _ := licenseFixture(t)
	ctx := context.Background()
	manifest := filepath.Join(e.root, "demo-r3.manifest.json")
	write(t, manifest, `{"schema": "manifest/0.1", "timeline": {"id": "demo", "rev": 3, "hash": "b3:00"},
	  "video": "demo-r3.mp4", "size": [1920, 1080], "fps": 30, "duration": 12, "frames": 360, "range": [0, 12],
	  "units": [
	    {"unit": "u1", "t": [0, 3.9], "elements": [
	      {"id": "fight", "ptr": "/scenes/0/shots/0", "kind": "shot", "t": [0, 3.9],
	       "sources": [{"asset": "proj:clip", "in": 2, "out": 5.9}]},
	      {"id": "open_a", "ptr": "/spine/0", "kind": "spine", "t": [0, 3.9], "sources": [{"asset": "proj:vo", "in": 0.2, "out": 4.1}]},
	      {"id": "bed", "ptr": "/audio/0", "kind": "audio", "sources": [{"asset": "proj:music", "in": 0, "out": 3.9}]}]},
	    {"unit": "u2", "t": [3.9, 5.4], "elements": [
	      {"id": "meme", "ptr": "/spine/1", "kind": "insert", "t": [3.9, 5.4], "sources": [{"asset": "proj:meme", "in": 8.85, "out": 10.35}]},
	      {"id": "bed", "ptr": "/audio/0", "kind": "audio", "sources": [{"asset": "proj:music", "in": 3.9, "out": 5.4}]}]},
	    {"unit": "u3", "t": [5.4, 12], "elements": [
	      {"id": "bed", "ptr": "/audio/0", "kind": "audio", "t": [5.4, 12], "sources": [{"asset": "proj:music", "in": 5.4, "out": 12}]},
	      {"id": "x", "ptr": "/scenes/0/overlays/0", "kind": "overlay", "t": [6, 7], "sources": [{"asset": "proj:gone", "in": 0, "out": 1}]}]}
	  ]}`)
	rep, err := e.c.LicenseReport(ctx, LicenseReportArgs{Path: manifest})
	if err != nil || rep.Kind != "manifest" {
		t.Fatalf("manifest report = %+v, %v", rep, err)
	}
	for _, a := range rep.Assets {
		if a.Asset == "proj:music" && (len(a.Ranges) != 1 || a.Ranges[0] != [2]float64{0, 12} || len(a.Uses) != 3) {
			t.Errorf("music = %+v (unit ranges merge)", a)
		}
	}
	got := map[string]bool{}
	for _, b := range rep.Blockers {
		got[b.Asset] = true
	}
	if len(got) != 2 || !got["proj:meme"] || !got["proj:gone"] || rep.Publishable {
		t.Errorf("blockers = %+v", rep.Blockers)
	}
	if _, err := e.c.LicenseReport(ctx, LicenseReportArgs{Path: filepath.Join(e.root, "media", "vo.bin")}); !isCode(err, CodeBadRequest) {
		t.Errorf("not JSON: %v", err)
	}
	write(t, filepath.Join(e.root, "other.json"), `{"schema": "something/1"}`)
	if _, err := e.c.LicenseReport(ctx, LicenseReportArgs{Path: filepath.Join(e.root, "other.json")}); !isCode(err, CodeBadRequest) {
		t.Errorf("unknown schema: %v", err)
	}
}
