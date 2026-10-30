package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The labels on previews use the Go font (BSD), compiled in.
var (
	uiFontOnce sync.Once
	uiFont     *opentype.Font
	uiFaces    sync.Map // float64 size -> font.Face
)

func uiFace(size float64) font.Face {
	uiFontOnce.Do(func() { uiFont, _ = opentype.Parse(goregular.TTF) })
	if f, ok := uiFaces.Load(size); ok {
		return f.(font.Face)
	}
	f, err := opentype.NewFace(uiFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	uiFaces.Store(size, f)
	return f
}

// textWidth measures s in face.
func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}

// drawText draws s with its baseline at (x, y).
func drawText(dst draw.Image, face font.Face, x, y int, s string, c color.Color) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// tag draws a label on a dark box with its bottom-left corner at (x, y).
func tag(dst draw.Image, x, y int, s string, size float64) {
	face := uiFace(size)
	if face == nil {
		return
	}
	m := face.Metrics()
	h := (m.Ascent + m.Descent).Ceil() + 4
	w := textWidth(face, s) + 8
	box := image.Rect(x, y-h, x+w, y).Intersect(dst.Bounds())
	draw.Draw(dst, box, image.NewUniform(color.RGBA{0, 0, 0, 190}), image.Point{}, draw.Over)
	drawText(dst, face, x+4, y-2-m.Descent.Ceil(), s, color.White)
}

// timecode formats seconds as m:ss or h:mm:ss, with tenths when the step is under a second.
func timecode(t float64, tenths bool) string {
	if t < 0 {
		t = 0
	}
	if tenths {
		t = math.Round(t*10) / 10
	} else {
		t = math.Floor(t + 1e-6)
	}
	h := int(t) / 3600
	m := int(t) / 60 % 60
	s := t - float64(int(t)/60*60)
	var out string
	switch {
	case h > 0 && tenths:
		out = fmt.Sprintf("%d:%02d:%04.1f", h, m, s)
	case h > 0:
		out = fmt.Sprintf("%d:%02d:%02d", h, m, int(s))
	case tenths:
		out = fmt.Sprintf("%d:%04.1f", m, s)
	default:
		out = fmt.Sprintf("%d:%02d", m, int(s))
	}
	return out
}

// toRGBA copies img into an RGBA image.
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82})
	return buf.Bytes(), err
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	err := enc.Encode(&buf, img)
	return buf.Bytes(), err
}
