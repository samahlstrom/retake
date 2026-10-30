package notes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // decodes reviewers' dropped .gif images
	_ "image/jpeg" // decodes reviewers' dropped .jpg images
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
	_ "golang.org/x/image/webp" // decodes reviewers' dropped .webp images

	"github.com/samahlstrom/retake/internal/jsonx"
)

// snapshotTime is the frame a moment's snapshot shows: the middle one of its (unflipped) marks, else its first
// paused comment, else its first comment.
func snapshotTime(m *Moment) float64 {
	var ts []float64
	for _, c := range m.Comments {
		for _, s := range c.Strokes {
			if !s.Flip {
				ts = append(ts, s.videoT)
			}
		}
		for _, im := range c.Images {
			if !im.Flip {
				ts = append(ts, im.videoT)
			}
		}
	}
	if len(ts) > 0 {
		sort.Float64s(ts)
		return ts[(len(ts)-1)/2]
	}
	for _, c := range m.Comments {
		if c.Mode == "paused" {
			return float64(c.Anchor)
		}
	}
	return float64(m.Comments[0].Anchor)
}

// snapshots renders one PNG per moment into <out>/snaps/ (cleared first) and records them in the moments.
func snapshots(ctx context.Context, out string, video *jsonx.Object, moments []*Moment, opts Options) error {
	dir := filepath.Join(out, "snaps")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, m := range moments {
		m.Snapshot, m.SnapshotT = nil, nil
	}
	if opts.NoSnapshots || len(moments) == 0 {
		return nil
	}
	vpath := str(field(video, "path"))
	if vpath == "" || !isFile(vpath) {
		opts.logf("notes: video not found (%s); no snapshots\n", pyStr(field(video, "path")))
		return nil
	}
	ffmpeg := opts.FFmpeg
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpeg); err != nil {
		opts.logf("notes: %s not found; no snapshots\n", ffmpeg)
		return nil
	}
	fps := 30.0
	if v := field(video, "fps"); truthy(v) {
		fps = numOr(v, 30)
	}
	for i, m := range moments {
		t := snapshotTime(m)
		name := fmt.Sprintf("snaps/%02d_%s.png", i+1, strings.NewReplacer(":", "-", ".", "-").Replace(fmtT(t)))
		var strokes []*Stroke
		var images []*Image
		for _, c := range m.Comments {
			for _, s := range c.Strokes {
				if !s.Flip {
					strokes = append(strokes, s)
				}
			}
			for _, im := range c.Images {
				if !im.Flip {
					images = append(images, im)
				}
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a folder of PNGs other tools read
			return err
		}
		err := renderSnapshot(ctx, ffmpeg, vpath, t, fps, strokes, images, filepath.Join(out, filepath.FromSlash(name)))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			opts.logf("notes: snapshot at %s failed: %v\n", fmtT(t), err)
			continue
		}
		st := jsonFloat(r3(t))
		m.Snapshot, m.SnapshotT = &name, &st
	}
	return nil
}

// grabFrame returns the frame the browser shows at t (pts <= t).
func grabFrame(ctx context.Context, ffmpeg, video string, t, fps float64) (image.Image, error) {
	k := math.Floor(t*fps + 1e-3)
	seek := math.Max(0, (k-0.25)/fps)
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-ss", strconv.FormatFloat(seek, 'f', 4, 64), //nolint:gosec // argv only, no shell
		"-i", video, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return nil, errors.New("ffmpeg: " + msg)
	}
	return png.Decode(&stdout)
}

var (
	badgeFontOnce sync.Once
	badgeFont     *opentype.Font
)

// face returns Go Bold (the Go fonts, BSD license, bundled with golang.org/x/image) at size pixels.
func face(size float64) (font.Face, error) {
	badgeFontOnce.Do(func() { badgeFont, _ = opentype.Parse(gobold.TTF) })
	if badgeFont == nil {
		return nil, errors.New("bundled font unreadable")
	}
	return opentype.NewFace(badgeFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

func rgba(c [3]uint8) color.RGBA { return color.RGBA{R: c[0], G: c[1], B: c[2], A: 255} }

// capsule adds a segment p-q with round ends of radius r to z (always wound the same way, so overlaps stay solid).
func capsule(z *vector.Rasterizer, p, q [2]float64, r float64) {
	dx, dy := q[0]-p[0], q[1]-p[1]
	th := math.Atan2(dy, dx)
	if dx == 0 && dy == 0 {
		th = 0
	}
	const steps = 12
	arc := func(c [2]float64, from float64, first bool) {
		for i := 0; i <= steps; i++ {
			a := from - math.Pi*float64(i)/steps
			x, y := float32(c[0]+r*math.Cos(a)), float32(c[1]+r*math.Sin(a))
			if first && i == 0 {
				z.MoveTo(x, y)
			} else {
				z.LineTo(x, y)
			}
		}
	}
	arc(q, th+math.Pi/2, true)
	arc(p, th-math.Pi/2, false)
	z.ClosePath()
}

func disc(z *vector.Rasterizer, c [2]float64, r float64) { capsule(z, c, c, r) }

// renderSnapshot draws the numbered strokes and the framed images over the frame at t and saves it as a PNG.
func renderSnapshot(ctx context.Context, ffmpeg, video string, t, fps float64, strokes []*Stroke, images []*Image,
	out string) error {
	src, err := grabFrame(ctx, ffmpeg, video, t, fps)
	if err != nil {
		return err
	}
	b := src.Bounds()
	W, H := b.Dx(), b.Dy()
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), src, b.Min, draw.Src)
	fw, fh := float64(W), float64(H)
	short := math.Min(fw, fh)

	for _, im := range images {
		drawImage(img, im, fw, fh, short)
	}
	width := math.Max(3, short*0.0065) // flat strokes, as wide as the page draws them
	for _, st := range strokes {
		if len(st.pts) == 0 {
			continue
		}
		z := vector.NewRasterizer(W, H)
		px := make([][2]float64, len(st.pts))
		for i, p := range st.pts {
			px[i] = [2]float64{p[0] * fw, p[1] * fh}
		}
		if len(px) == 1 {
			disc(z, px[0], width/2)
		}
		for i := 0; i+1 < len(px); i++ {
			capsule(z, px[i], px[i+1], width/2)
		}
		z.Draw(img, img.Bounds(), image.NewUniform(rgba(colorOf(st.Color))), image.Point{})
	}
	r := math.Max(11, math.RoundToEven(short*0.015))
	f, err := face(math.RoundToEven(r * 1.2))
	if err != nil {
		return err
	}
	for _, st := range strokes { // numbered badge beside each stroke's start, matching "stroke N" in notes.md
		if len(st.pts) == 0 {
			continue
		}
		x, y := st.pts[0][0]*fw, st.pts[0][1]*fh
		c := [2]float64{math.Min(math.Max(x-r*1.7, r), fw-r), math.Min(math.Max(y-r*1.7, r), fh-r)}
		z := vector.NewRasterizer(W, H)
		disc(z, c, r)
		z.Draw(img, img.Bounds(), image.NewUniform(rgba(colorOf(st.Color))), image.Point{})
		ink := color.RGBA{R: 17, G: 17, B: 19, A: 255}
		if st.Color == "red" {
			ink = color.RGBA{R: 255, G: 255, B: 255, A: 255}
		}
		label(img, f, strconv.Itoa(st.N), c, ink)
	}
	for _, im := range images { // a square badge on each image's corner, matching "image N"
		x, y := float64(im.Box[0])*fw, float64(im.Box[1])*fh
		c := [2]float64{math.Min(math.Max(x, r), fw-r), math.Min(math.Max(y, r), fh-r)}
		sq := image.Rect(int(c[0]-r), int(c[1]-r), int(c[0]+r), int(c[1]+r))
		draw.Draw(img, sq, image.NewUniform(color.RGBA{R: 17, G: 17, B: 19, A: 255}), image.Point{}, draw.Src)
		label(img, f, strconv.Itoa(im.N), c, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	}
	return writePNG(out, img)
}

func colorOf(name string) [3]uint8 {
	if c, ok := colors[name]; ok {
		return c
	}
	return colors["red"]
}

// label draws text centered on c (the middle of the font's ascent and descent, like Pillow's "mm" anchor).
func label(dst draw.Image, f font.Face, text string, c [2]float64, ink color.Color) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: f}
	adv := d.MeasureString(text)
	m := f.Metrics()
	base := c[1] + (float64(m.Ascent)-float64(m.Descent))/64/2
	d.Dot = fixed.Point26_6{X: fixed.Int26_6((c[0] - float64(adv)/64/2) * 64), Y: fixed.Int26_6(base * 64)}
	d.DrawString(text)
}

// drawImage draws a dropped image scaled into its box, with a frame; an unreadable file leaves an empty frame.
func drawImage(dst *image.RGBA, im *Image, fw, fh, short float64) {
	x0, y0 := float64(im.Box[0])*fw, float64(im.Box[1])*fh
	x1, y1 := x0+float64(im.Box[2])*fw, y0+float64(im.Box[3])*fh
	box := image.Rect(int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x1)), int(math.Round(y1))).Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	if f, err := os.Open(im.Path); err == nil {
		if pic, _, err := image.Decode(f); err == nil {
			xdraw.ApproxBiLinear.Scale(dst, box, pic, pic.Bounds(), draw.Over, nil)
		}
		_ = f.Close()
	}
	frame := int(math.Max(2, math.Round(short*0.004)))
	white, dark := image.NewUniform(color.White), image.NewUniform(color.RGBA{R: 17, G: 17, B: 19, A: 255})
	for _, ring := range []struct {
		in  int
		src image.Image
	}{{-frame - 1, dark}, {-frame, white}} {
		outer := box.Inset(ring.in)
		inner := box
		for _, r := range []image.Rectangle{
			image.Rect(outer.Min.X, outer.Min.Y, outer.Max.X, inner.Min.Y),
			image.Rect(outer.Min.X, inner.Max.Y, outer.Max.X, outer.Max.Y),
			image.Rect(outer.Min.X, inner.Min.Y, inner.Min.X, inner.Max.Y),
			image.Rect(inner.Max.X, inner.Min.Y, outer.Max.X, inner.Max.Y),
		} {
			draw.Draw(dst, r.Intersect(dst.Bounds()), ring.src, image.Point{}, draw.Src)
		}
	}
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil { //nolint:gosec // a snapshot other tools read
		return err
	}
	return os.Rename(tmp, path)
}
