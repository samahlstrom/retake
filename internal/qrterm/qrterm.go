// Package qrterm draws a QR code in a terminal, two modules per character cell (half blocks), in explicit black on
// white so it scans on dark and light terminal themes alike. The code itself comes from rsc.io/qr (pure Go).
package qrterm

import (
	"io"
	"strings"

	"rsc.io/qr"
)

// quiet is the margin of light modules around the code that scanners need.
const quiet = 2

const (
	reset = "\x1b[0m"
	// black on white: the foreground draws the dark modules.
	colors = "\x1b[30;107m"
)

// Render returns the code for text as terminal lines (ANSI colors, half blocks).
func Render(text string) (string, error) {
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	n := c.Size + 2*quiet
	dark := func(x, y int) bool { return c.Black(x-quiet, y-quiet) }
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString(colors)
		for x := range n {
			top, bottom := dark(x, y), y+1 < n && dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString(reset + "\n")
	}
	return b.String(), nil
}

// Write draws the code for text on w, each line indented.
func Write(w io.Writer, text, indent string) error {
	s, err := Render(text)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(s, "\n"), "\n") {
		if _, err := io.WriteString(w, indent+line+"\n"); err != nil {
			return err
		}
	}
	return nil
}
