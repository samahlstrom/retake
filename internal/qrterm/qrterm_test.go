package qrterm

import (
	"strings"
	"testing"

	"rsc.io/qr"
)

// The drawing is exactly the code's modules, with the quiet zone around them: read it back cell by cell.
func TestRenderMatchesTheCode(t *testing.T) {
	text := "https://fake-words-here.trycloudflare.com/s/rt_" + strings.Repeat("A", 43)
	out, err := Render(text)
	if err != nil {
		t.Fatal(err)
	}
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	n := c.Size + 2*quiet
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != (n+1)/2 {
		t.Fatalf("%d lines for %d modules", len(lines), n)
	}
	for row, line := range lines {
		if !strings.HasPrefix(line, colors) || !strings.HasSuffix(line, reset) {
			t.Fatalf("line %d isn't black on white: %q", row, line)
		}
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, colors), reset))
		if len(cells) != n {
			t.Fatalf("line %d has %d cells, want %d", row, len(cells), n)
		}
		for x, r := range cells {
			top := r == '█' || r == '▀'
			bottom := r == '█' || r == '▄'
			y := 2 * row
			if top != c.Black(x-quiet, y-quiet) || y+1 < n && bottom != c.Black(x-quiet, y+1-quiet) {
				t.Fatalf("cell %d,%d is %q", x, row, r)
			}
		}
	}
	// The quiet zone is light: the first line and the edges are blank.
	first := []rune(strings.TrimSuffix(strings.TrimPrefix(lines[0], colors), reset))
	if strings.TrimSpace(string(first)) != "" {
		t.Errorf("the top margin isn't blank: %q", string(first))
	}

	var b strings.Builder
	if err := Write(&b, "hello", "  "); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
		if !strings.HasPrefix(l, "  "+colors) {
			t.Fatalf("not indented: %q", l)
		}
	}
}
