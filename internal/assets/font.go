package assets

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// parseFont reads a TrueType or OpenType font, or the first font of a collection. WOFF and WOFF2 aren't read.
func parseFont(path string) (*sfnt.Font, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".woff", ".woff2":
		return nil, fmt.Errorf("can't read %s fonts yet: no specimen", strings.TrimPrefix(filepath.Ext(path), "."))
	}
	data, err := os.ReadFile(path) //nolint:gosec // the file being imported
	if err != nil {
		return nil, err
	}
	if f, err := sfnt.Parse(data); err == nil {
		return f, nil
	}
	coll, err := sfnt.ParseCollection(data)
	if err != nil {
		return nil, fmt.Errorf("can't read the font: %w", err)
	}
	return coll.Font(0)
}

func fontName(f *sfnt.Font, id sfnt.NameID) string {
	var buf sfnt.Buffer
	s, err := f.Name(&buf, id)
	if err != nil {
		return ""
	}
	return s
}

// fontMedia returns a font's family, style and glyph count.
func fontMedia(path string) (Media, error) {
	f, err := parseFont(path)
	if err != nil {
		return Media{}, err
	}
	m := Media{Family: fontName(f, sfnt.NameIDFamily), Style: fontName(f, sfnt.NameIDSubfamily), Glyphs: f.NumGlyphs()}
	if tf := fontName(f, sfnt.NameIDTypographicFamily); tf != "" {
		m.Family = tf
	}
	if ts := fontName(f, sfnt.NameIDTypographicSubfamily); ts != "" {
		m.Style = ts
	}
	return m, nil
}

// SpecimenWidth is a font specimen's width in pixels.
const SpecimenWidth = 1200

// specimen draws a font's specimen sheet with the font itself: names, the alphabet, digits, and a sample line at
// five sizes. The header is in the Go font so it reads even when the font is a dingbat.
func specimen(path string) ([]byte, error) {
	f, err := parseFont(path)
	if err != nil {
		return nil, err
	}
	m, _ := fontMedia(path)
	type line struct {
		text string
		size float64
	}
	sample := "The quick brown fox jumps over the lazy dog."
	lines := []line{
		{"Aa Bb Cc Gg Qq 123", 84},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", 44},
		{"abcdefghijklmnopqrstuvwxyz", 44},
		{"0123456789 !?&@#$%()[]{}.,;:'\"", 36},
		{sample, 32},
		{sample, 24},
		{sample, 18},
		{sample, 14},
	}
	pad := 32
	height := pad + 36
	for _, l := range lines {
		height += int(l.size*1.35) + 8
	}
	height += pad
	img := image.NewRGBA(image.Rect(0, 0, SpecimenWidth, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	ink := color.RGBA{20, 20, 24, 255}
	grey := color.RGBA{110, 110, 120, 255}
	head := strings.TrimSpace(m.Family + " " + m.Style)
	if head == "" {
		head = filepath.Base(path)
	}
	header := fmt.Sprintf("%s  ·  %d glyphs", head, m.Glyphs)
	if face := uiFace(18); face != nil {
		drawText(img, face, pad, pad+16, header, grey)
	}
	y := pad + 36
	for _, l := range lines {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: l.size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return nil, fmt.Errorf("can't draw the font: %w", err)
		}
		y += int(l.size*1.05) + 8
		drawText(img, face, pad, y, l.text, ink)
		y += int(l.size * 0.3)
		_ = face.Close()
	}
	return encodePNG(img)
}
