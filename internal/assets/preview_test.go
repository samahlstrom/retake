package assets

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"
)

func TestVideoPreviews(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "clips", "Big Fight.mp4")
	video(t, src, 12, 320, 180)

	it := importOne(t, e.c, ImportArgs{Paths: []string{src}, License: LicenseArgs{Status: LicenseOwned},
		Tags: []string{"Fight", "fight", " ranked "}})
	if it.Status != StatusImported || it.ID != "proj:big-fight" || it.Kind != KindVideo || len(it.Warnings) > 0 {
		t.Fatalf("import = %+v", it)
	}
	got, err := e.c.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Media
	if m.Duration == nil || *m.Duration < 11.9 || m.Size == nil || *m.Size != [2]int{320, 180} || m.FPS == nil ||
		*m.FPS != 10 || m.Codec != "h264" || len(m.Audio) != 1 || m.Audio[0].SampleRate != 48000 {
		t.Fatalf("media = %+v", m)
	}
	if got.Title != "Big Fight" || strings.Join(got.Tags, ",") != "fight,ranked" || got.License.Status != LicenseOwned ||
		!got.License.Publish {
		t.Fatalf("entry = %+v", got)
	}
	// 12 s gets a tile every second: 12 tiles, 6 columns × 2 rows of 256×144, 2 px margins and padding.
	if got.Previews.EveryS != 1 || got.Previews.Paths["contact_sheet"] == "" {
		t.Fatalf("previews = %+v", got.Previews)
	}
	w, h := imgSize(t, got.Previews.Paths["contact_sheet"])
	if w != 2*2+6*256+5*2 || h != 2*2+2*144+2 {
		t.Fatalf("contact sheet %dx%d", w, h)
	}
	if got.Proxy != nil {
		t.Fatalf("a 320×180 clip needs no proxy: %+v", got.Proxy)
	}

	// Frames on demand, labeled, at the source size (it's under 960 wide); cached.
	res, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, At: []float64{0, 5.5, 11.9}})
	if err != nil || len(res.Files) != 3 || res.Kind != PreviewFrames {
		t.Fatalf("frames = %+v, %v", res, err)
	}
	for _, f := range res.Files {
		if fw, fh := imgSize(t, f.Path); fw != 320 || fh != 180 || f.T == nil {
			t.Fatalf("frame %+v is %dx%d", f, fw, fh)
		}
	}
	again, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, At: []float64{5.5}})
	if err != nil || again.Files[0].Ref != res.Files[1].Ref {
		t.Fatalf("frame not cached: %+v %v", again, err)
	}
	if _, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, At: []float64{30}}); !isCode(err, CodeBadRequest) {
		t.Fatalf("frame past the end: %v", err)
	}
	if _, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, Kind: PreviewThumbnail}); !isCode(err, CodeBadRequest) {
		t.Fatalf("thumbnail of a video: %v", err)
	}

	// A span added by hand gets its 6-frame strip at once.
	sp, err := e.c.SpanAdd(ctx, SpanAddArgs{Asset: it.ID, T0: 2, T1: 8, Label: "the big swing"})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Span != "sp:1" || sp.Type != SpanTag || sp.SourceOfLabel != "manual" || sp.Strip == "" {
		t.Fatalf("span = %+v", sp)
	}
	if sw, sh := imgSize(t, sp.Strip); sw != 2*2+6*320+5*2 || sh != 2*2+180 {
		t.Fatalf("strip %dx%d", sw, sh)
	}
	strip, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, Span: sp.Span})
	if err != nil || strip.Files[0].Path != sp.Strip {
		t.Fatalf("strip preview = %+v, %v", strip, err)
	}
	if _, err := e.c.SpanAdd(ctx, SpanAddArgs{Asset: it.ID, T0: 5, T1: 40}); !isCode(err, CodeBadRequest) {
		t.Fatalf("span past the end: %v", err)
	}
	if _, err := e.c.SpanAdd(ctx, SpanAddArgs{Asset: it.ID, T0: 5, T1: 6, Type: "made_up"}); !isCode(err, CodeBadRequest) {
		t.Fatalf("unknown span type: %v", err)
	}
}

func TestProxy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "wide.mp4")
	video(t, src, 2, 1280, 720)
	it := importOne(t, e.c, ImportArgs{Paths: []string{src}})
	got, err := e.c.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Proxy == nil || got.Proxy.Size != [2]int{960, 540} || got.Proxy.GOP != proxyGOP {
		t.Fatalf("proxy = %+v (warnings %v)", got.Proxy, it.Warnings)
	}
	// Frames come from the proxy: 960 wide.
	res, err := e.c.Preview(ctx, PreviewArgs{ID: it.ID, At: []float64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if w, h := imgSize(t, res.Files[0].Path); w != 960 || h != 540 {
		t.Fatalf("frame %dx%d", w, h)
	}
	none := importOne(t, e.c, ImportArgs{Paths: []string{src}, Name: "again", Proxy: ProxyNever})
	if none.Status != StatusDuplicate || none.DuplicateOf != it.ID {
		t.Fatalf("same file again = %+v", none)
	}
}

func TestAudioImageFontPreviews(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	tone(t, filepath.Join(dir, "sfx", "whoosh.wav"), 2.5, 660)
	picture(t, filepath.Join(dir, "img", "card.png"), 1600, 900, 0)
	goFont(t, filepath.Join(dir, "fonts", "GoRegular.ttf"))
	write(t, filepath.Join(dir, "readme.md"), "not an asset kind")

	res, err := e.c.Import(ctx, ImportArgs{Paths: []string{dir}, Scope: "lib", License: LicenseArgs{Status: LicenseCleared}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 || res.Skipped != 1 {
		t.Fatalf("folder import = %+v", res)
	}
	base := filepath.Base(dir)
	byKind := map[string]Entry{}
	for _, it := range res.Items {
		if it.Status != StatusImported {
			continue
		}
		if !strings.HasPrefix(it.ID, "lib:"+strings.ToLower(base)+"/") {
			t.Fatalf("folder import ids keep the folder name: %s", it.ID)
		}
		got, err := e.c.Get(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		byKind[got.Kind] = got
	}

	audio := byKind[KindAudio]
	if audio.Previews.Loudness == nil || audio.Previews.Loudness.Integrated > -10 || audio.Previews.Loudness.Integrated < -40 {
		t.Fatalf("loudness = %+v", audio.Previews.Loudness)
	}
	if w, h := imgSize(t, audio.Previews.Paths["waveform"]); w != WaveformWidth || h != waveformHeight {
		t.Fatalf("waveform %dx%d", w, h)
	}

	img := byKind[KindImage]
	if img.Media.Size == nil || *img.Media.Size != [2]int{1600, 900} {
		t.Fatalf("image media = %+v", img.Media)
	}
	if w, h := imgSize(t, img.Previews.Paths["thumbnail"]); w != ThumbSize || h != 288 {
		t.Fatalf("thumbnail %dx%d", w, h)
	}

	font := byKind[KindFont]
	if font.Media.Family != "Go" || font.Media.Style != "Regular" || font.Media.Glyphs < 100 {
		t.Fatalf("font media = %+v", font.Media)
	}
	if w, h := imgSize(t, font.Previews.Paths["specimen"]); w != SpecimenWidth || h < 300 {
		t.Fatalf("specimen %dx%d", w, h)
	}
	// Asking again returns the stored preview.
	p, err := e.c.Preview(ctx, PreviewArgs{ID: font.ID})
	if err != nil || p.Kind != PreviewSpecimen || p.Files[0].Path != font.Previews.Paths["specimen"] {
		t.Fatalf("specimen preview = %+v, %v", p, err)
	}
}

func TestDedupExactAndNear(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	picture(t, filepath.Join(dir, "thanos.png"), 400, 300, 0)
	picture(t, filepath.Join(dir, "other.png"), 400, 300, 1)

	first := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, "thanos.png")}, Scope: "lib"})
	if first.Status != StatusImported {
		t.Fatal(first)
	}
	// The same bytes under another name, into the project: a duplicate of the library's copy.
	data, _ := os.ReadFile(filepath.Join(dir, "thanos.png"))
	write(t, filepath.Join(dir, "copy.png"), string(data))
	dup := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, "copy.png")}})
	if dup.Status != StatusDuplicate || dup.DuplicateOf != "lib:thanos" {
		t.Fatalf("exact duplicate = %+v", dup)
	}

	// A resized, re-encoded copy is a near duplicate: reported, not merged.
	f, _ := os.Open(filepath.Join(dir, "thanos.png"))
	src, _, err := image.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	small := image.NewRGBA(image.Rect(0, 0, 200, 150))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	writePNG(t, filepath.Join(dir, "thanos-small.png"), small)
	near := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, "thanos-small.png")}})
	if near.Status != StatusImported || len(near.Near) != 1 || near.Near[0].ID != "lib:thanos" || near.Near[0].Distance > 4 {
		t.Fatalf("near duplicate = %+v", near)
	}
	if got, _ := e.c.Get(ctx, near.ID); len(got.Near) != 1 {
		t.Fatalf("near isn't recorded: %+v", got.Near)
	}
	// A different picture isn't near.
	other := importOne(t, e.c, ImportArgs{Paths: []string{filepath.Join(dir, "other.png")}})
	if len(other.Near) != 0 {
		t.Fatalf("a different picture is near: %+v", other.Near)
	}
}
