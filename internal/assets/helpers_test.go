package assets

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/goregular"
)

// env is a test project and library in temporary folders.
type env struct {
	root, lib string
	c         *Catalog
	vars      map[string]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{root: filepath.Join(base, "project"), lib: filepath.Join(base, "library"), vars: map[string]string{}}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		t.Fatal(err)
	}
	e.c = e.open(t)
	return e
}

func (e *env) open(t *testing.T) *Catalog {
	t.Helper()
	c, err := Open(Options{Root: e.root, Library: e.lib, Getenv: func(k string) string { return e.vars[k] },
		Now: func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// reopen closes the catalog and opens it again (to pick up new plug-ins, say).
func (e *env) reopen(t *testing.T) {
	t.Helper()
	_ = e.c.Close()
	e.c = e.open(t)
}

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y", "-threads", "2"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// video writes a generated w×h, 10 fps H.264 + AAC clip.
func video(t *testing.T, path string, seconds float64, w, h int) {
	t.Helper()
	needFFmpeg(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	d := strconv.FormatFloat(seconds, 'f', -1, 64)
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s="+strconv.Itoa(w)+"x"+strconv.Itoa(h)+":r=10:d="+d,
		"-f", "lavfi", "-i", "sine=f=440:sample_rate=48000:d="+d, "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
}

// tone writes a generated sine WAV.
func tone(t *testing.T, path string, seconds float64, freq int) {
	t.Helper()
	needFFmpeg(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg(t, "-f", "lavfi", "-i", "sine=f="+strconv.Itoa(freq)+":sample_rate=48000:d="+
		strconv.FormatFloat(seconds, 'f', -1, 64), "-c:a", "pcm_s16le", path)
}

// picture writes a PNG: a diagonal gradient with a block, so it has structure for hashing. variant changes it.
func picture(t *testing.T, path string, w, h, variant int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*255/w + y*255/h) / 2)
			c := color.RGBA{v, uint8(255 - int(v)), uint8((x * 7) % 256), 255}
			if variant == 1 {
				c = color.RGBA{uint8(255 - int(v)), v, 40, 255}
			}
			if variant != 1 && x > w/3 && x < w/2 && y > h/4 && y < h*3/4 {
				c = color.RGBA{250, 250, 250, 255}
			}
			img.Set(x, y, c)
		}
	}
	writePNG(t, path, img)
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// goFont writes the Go font (BSD licensed, compiled into x/image) as a TTF file.
func goFont(t *testing.T, path string) {
	t.Helper()
	write(t, path, string(goregular.TTF))
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func imgSize(t *testing.T, path string) (int, int) {
	t.Helper()
	w, h := imageSize(path)
	if w == 0 {
		t.Fatalf("%s isn't a readable image", path)
	}
	return w, h
}

func importOne(t *testing.T, c *Catalog, a ImportArgs) ImportItem {
	t.Helper()
	res, err := c.Import(context.Background(), a)
	if err != nil {
		t.Fatalf("import %v: %v", a.Paths, err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("import %v: %d items: %+v", a.Paths, len(res.Items), res.Items)
	}
	return res.Items[0]
}
