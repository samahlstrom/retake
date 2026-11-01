// Package still draws a review's pen lines over a video frame the way the notes engine's snapshots show them
// (spec/notes.md): flat lines as wide as the page draws them, each with a numbered badge beside its start. The notes
// engine's snapshots and the editing worker's own stills (spec/automation.md, "A note's stills and images") both use
// it.
package still

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Colors are the pen colors; any other color is drawn and named as red.
var Colors = map[string][3]uint8{
	"red":    {255, 59, 48},
	"yellow": {255, 214, 10},
	"green":  {48, 209, 88},
	"white":  {255, 255, 255},
}

// ColorOf is a pen color's RGB, red for an unknown name.
func ColorOf(name string) [3]uint8 {
	if c, ok := Colors[name]; ok {
		return c
	}
	return Colors["red"]
}

// Ink is the dark of the badges' digits and frames.
var Ink = color.RGBA{R: 17, G: 17, B: 19, A: 255}

// Line is one pen line: its points normalized 0-1 to the frame, its pen color's name and its number ("stroke N").
type Line struct {
	Points [][2]float64
	Color  string
	N      int
}

// Radius is a badge's radius on a frame whose shorter side is short pixels.
func Radius(short float64) float64 { return math.Max(11, math.RoundToEven(short*0.015)) }

// Draw draws the lines over img, then a numbered badge in each line's color beside its start.
func Draw(img *image.RGBA, lines []Line) error {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	fw, fh := float64(W), float64(H)
	short := math.Min(fw, fh)
	width := math.Max(3, short*0.0065) // flat strokes, as wide as the page draws them
	for _, l := range lines {
		if len(l.Points) == 0 {
			continue
		}
		z := vector.NewRasterizer(W, H)
		px := make([][2]float64, len(l.Points))
		for i, p := range l.Points {
			px[i] = [2]float64{p[0] * fw, p[1] * fh}
		}
		if len(px) == 1 {
			disc(z, px[0], width/2)
		}
		for i := 0; i+1 < len(px); i++ {
			capsule(z, px[i], px[i+1], width/2)
		}
		z.Draw(img, img.Bounds(), image.NewUniform(rgba(ColorOf(l.Color))), image.Point{})
	}
	r := Radius(short)
	f, err := Face(math.RoundToEven(r * 1.2))
	if err != nil {
		return err
	}
	for _, l := range lines { // a numbered badge beside each line's start, matching "stroke N" in notes.md
		if len(l.Points) == 0 {
			continue
		}
		x, y := l.Points[0][0]*fw, l.Points[0][1]*fh
		c := [2]float64{math.Min(math.Max(x-r*1.7, r), fw-r), math.Min(math.Max(y-r*1.7, r), fh-r)}
		z := vector.NewRasterizer(W, H)
		disc(z, c, r)
		z.Draw(img, img.Bounds(), image.NewUniform(rgba(ColorOf(l.Color))), image.Point{})
		ink := Ink
		if l.Color == "red" {
			ink = color.RGBA{R: 255, G: 255, B: 255, A: 255}
		}
		Label(img, f, strconv.Itoa(l.N), c, ink)
	}
	return nil
}

var (
	badgeFontOnce sync.Once
	badgeFont     *opentype.Font
)

// Face returns Go Bold (the Go fonts, BSD license, bundled with golang.org/x/image) at size pixels.
func Face(size float64) (font.Face, error) {
	badgeFontOnce.Do(func() { badgeFont, _ = opentype.Parse(gobold.TTF) })
	if badgeFont == nil {
		return nil, errors.New("bundled font unreadable")
	}
	return opentype.NewFace(badgeFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

// Label draws text centered on c (the middle of the font's ascent and descent, like Pillow's "mm" anchor).
func Label(dst draw.Image, f font.Face, text string, c [2]float64, ink color.Color) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: f}
	adv := d.MeasureString(text)
	m := f.Metrics()
	base := c[1] + (float64(m.Ascent)-float64(m.Descent))/64/2
	d.Dot = fixed.Point26_6{X: fixed.Int26_6((c[0] - float64(adv)/64/2) * 64), Y: fixed.Int26_6(base * 64)}
	d.DrawString(text)
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
