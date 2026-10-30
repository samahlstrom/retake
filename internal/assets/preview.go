package assets

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Preview sizes.
const (
	SheetTileWidth = 256  // contact sheet tiles
	StripTileWidth = 320  // span strips
	StripFrames    = 6    // frames in a span strip
	FrameWidth     = 960  // on-demand frames, at most
	WaveformWidth  = 1200 // waveform PNG
	waveformHeight = 300
	maxSheetTiles  = 120
	frameWorkers   = 3 // ffmpeg seeks at once
)

// sheetSteps are the contact sheet intervals, in seconds.
var sheetSteps = []float64{0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1200}

// sheetEvery picks a contact sheet's interval for a duration: about 2√duration tiles, at least 12 and at most
// maxSheetTiles (a 12 s clip gets one a second, 10 minutes one every 15 s, two hours one every 2 minutes).
func sheetEvery(dur float64) float64 {
	target := min(max(2*math.Sqrt(max(dur, 0)), 12), maxSheetTiles)
	for _, s := range sheetSteps {
		if s >= dur/target-1e-9 {
			return s
		}
	}
	return sheetSteps[len(sheetSteps)-1]
}

// sheetCols is a sheet's column count for n tiles.
func sheetCols(n int) int {
	switch {
	case n <= 6:
		return n
	case n <= 24:
		return 6
	case n <= 64:
		return 8
	}
	return 10
}

// makePreviews makes an imported asset's previews by kind. Failures become warnings.
func (c *Catalog) makePreviews(ctx context.Context, d *db, id, file, kind string, m Media, img image.Image,
	src string, a ImportArgs) (Previews, []string) {
	var pv Previews
	var warns []string
	switch kind {
	case KindVideo:
		if !m.hasVideo() || m.duration() <= 0 {
			break
		}
		every := a.EveryS
		if every <= 0 {
			every = sheetEvery(m.duration())
		}
		r, err := c.contactSheet(ctx, d, id, file, m, every, src)
		if err != nil {
			warns = append(warns, "contact sheet: "+err.Error())
			break
		}
		pv.ContactSheet, pv.EveryS = r, every
	case KindAudio:
		r, loud, err := c.waveform(ctx, d, id, file, m, src)
		if err != nil {
			warns = append(warns, "waveform: "+err.Error())
			break
		}
		pv.Waveform, pv.Loudness = r, loud
	case KindImage:
		if img == nil {
			break
		}
		data, ext, err := thumbnail(img)
		if err == nil {
			pv.Thumbnail, err = d.blobs().putBytes(data, ext)
		}
		if err == nil {
			err = d.putDerived(ctx, id, "thumbnail", src, pv.Thumbnail, nil)
		}
		if err != nil {
			warns = append(warns, "thumbnail: "+err.Error())
		}
	case KindFont:
		data, err := specimen(file)
		if err == nil {
			pv.Specimen, err = d.blobs().putBytes(data, ".png")
		}
		if err == nil {
			err = d.putDerived(ctx, id, "specimen", src, pv.Specimen, nil)
		}
		if err != nil {
			warns = append(warns, "specimen: "+err.Error())
		}
	}
	return pv, warns
}

// scratch makes a temporary folder inside the catalog (not /tmp, which may be a small RAM disk).
func (d *db) scratch(pattern string) (string, func(), error) {
	base := filepath.Join(d.dir, "tmp")
	if err := os.MkdirAll(base, 0o755); err != nil { //nolint:gosec // a user folder
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// tileSize returns the tile size for a source of this media at width w (even numbers; 16:9 when unknown).
func tileSize(m Media, w int) (int, int) {
	sw, sh := 16, 9
	if m.Size != nil && m.Size[0] > 0 && m.Size[1] > 0 {
		sw, sh = m.Size[0], m.Size[1]
	}
	h := int(math.Round(float64(w)*float64(sh)/float64(sw)/2)) * 2
	return w, max(2, h)
}

// grabFrames writes one PNG per time into dir as f_00000.png, f_00001.png, … (seeking for each, a few at a
// time). A time ffmpeg can't decode gets a black frame.
func (c *Catalog) grabFrames(ctx context.Context, file, dir string, times []float64, w, h int) error {
	if !c.tools.available() {
		return fmt.Errorf("ffmpeg and ffprobe are needed for video previews")
	}
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1",
		w, h, w, h)
	sem := make(chan struct{}, frameWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	failed := 0
	for i, t := range times {
		out := filepath.Join(dir, fmt.Sprintf("f_%05d.png", i))
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, err := c.tools.ffmpegRun(ctx, "-threads", "2", "-ss", strconv.FormatFloat(t, 'f', 3, 64), "-i", file,
				"-frames:v", "1", "-vf", vf, out)
			if err == nil {
				if st, serr := os.Stat(out); serr != nil || st.Size() == 0 {
					err = fmt.Errorf("no frame at %.3f", t)
				}
			}
			if err != nil {
				mu.Lock()
				failed++
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				black := image.NewRGBA(image.Rect(0, 0, w, h))
				draw.Draw(black, black.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
				if data, err := encodePNG(black); err == nil {
					_ = os.WriteFile(out, data, 0o600)
				}
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if failed == len(times) {
		return firstErr
	}
	return nil
}

// tileSheet tiles dir's frames cols×rows with ffmpeg's tile filter, then burns in the labels, and stores the
// sheet as a JPEG blob.
func (c *Catalog) tileSheet(ctx context.Context, d *db, dir string, n, cols, w, h int, labels []string) (string, error) {
	const pad, margin = 2, 2
	rows := (n + cols - 1) / cols
	sheet := filepath.Join(dir, "sheet.png")
	_, err := c.tools.ffmpegRun(ctx, "-framerate", "1", "-start_number", "0", "-i", filepath.Join(dir, "f_%05d.png"),
		"-vf", fmt.Sprintf("tile=%dx%d:padding=%d:margin=%d:color=0x101014", cols, rows, pad, margin),
		"-frames:v", "1", sheet)
	if err != nil {
		return "", err
	}
	f, err := os.Open(sheet) //nolint:gosec // our own scratch file
	if err != nil {
		return "", err
	}
	img, _, err := image.Decode(f)
	_ = f.Close()
	if err != nil {
		return "", fmt.Errorf("reading the tiled sheet: %w", err)
	}
	rgba := toRGBA(img)
	size := 13.0
	if w >= 300 {
		size = 15
	}
	for i := 0; i < n && i < len(labels); i++ {
		x := margin + (i%cols)*(w+pad)
		y := margin + (i/cols)*(h+pad) + h
		tag(rgba, x+3, y-3, labels[i], size)
	}
	data, err := encodeJPEG(rgba)
	if err != nil {
		return "", err
	}
	return d.blobs().putBytes(data, ".jpg")
}

// contactSheet makes a video's contact sheet: one tile every `every` seconds, timecodes burned in.
func (c *Catalog) contactSheet(ctx context.Context, d *db, id, file string, m Media, every float64, src string) (string, error) {
	recipe := fmt.Sprintf("contact_sheet every=%g", every)
	if r := d.derivedBlob(ctx, id, recipe, src); r != "" {
		return r, nil
	}
	dur := m.duration()
	n := int(math.Ceil(dur/every - 1e-9))
	n = max(1, min(n, maxSheetTiles))
	times := make([]float64, n)
	labels := make([]string, n)
	last := lastFrameTime(m)
	for i := range times {
		times[i] = min(float64(i)*every, last)
		labels[i] = timecode(float64(i)*every, every < 1)
	}
	dir, done, err := d.scratch("sheet-*")
	if err != nil {
		return "", err
	}
	defer done()
	w, h := tileSize(m, SheetTileWidth)
	if err := c.grabFrames(ctx, file, dir, times, w, h); err != nil {
		return "", err
	}
	r, err := c.tileSheet(ctx, d, dir, n, sheetCols(n), w, h, labels)
	if err != nil {
		return "", err
	}
	return r, d.putDerived(ctx, id, recipe, src, r, map[string]any{"every_s": every, "tiles": n})
}

// lastFrameTime is the latest time worth seeking to: just before the end.
func lastFrameTime(m Media) float64 {
	fps := 30.0
	if m.FPS != nil && *m.FPS > 0 {
		fps = *m.FPS
	}
	return max(0, m.duration()-1.5/fps)
}

// stripTimes are a span's strip frames: StripFrames times evenly over [t0, t1], each in the middle of its slice.
func stripTimes(t0, t1 float64) []float64 {
	out := make([]float64, StripFrames)
	step := (t1 - t0) / StripFrames
	for i := range out {
		out[i] = t0 + (float64(i)+0.5)*step
	}
	return out
}

// strip makes a span's 6-frame strip.
func (c *Catalog) strip(ctx context.Context, d *db, id, file string, m Media, span int64, t0, t1 float64, src string) (string, error) {
	recipe := fmt.Sprintf("strip span=%d t0=%.3f t1=%.3f", span, t0, t1)
	if r := d.derivedBlob(ctx, id, recipe, src); r != "" {
		return r, nil
	}
	times := stripTimes(t0, t1)
	last := lastFrameTime(m)
	labels := make([]string, len(times))
	for i, t := range times {
		labels[i] = timecode(t, true)
		if last > 0 {
			times[i] = min(t, last)
		}
	}
	dir, done, err := d.scratch("strip-*")
	if err != nil {
		return "", err
	}
	defer done()
	w, h := tileSize(m, StripTileWidth)
	if err := c.grabFrames(ctx, file, dir, times, w, h); err != nil {
		return "", err
	}
	r, err := c.tileSheet(ctx, d, dir, len(times), len(times), w, h, labels)
	if err != nil {
		return "", err
	}
	return r, d.putDerived(ctx, id, recipe, src, r, map[string]any{"span": span})
}

// frame renders one frame at t (from the proxy when there is one), labeled with its timecode.
func (c *Catalog) frame(ctx context.Context, d *db, id, file string, m Media, t float64, src string) (string, error) {
	recipe := fmt.Sprintf("frame t=%.3f w=%d", t, FrameWidth)
	if r := d.derivedBlob(ctx, id, recipe, src); r != "" {
		return r, nil
	}
	dir, done, err := d.scratch("frame-*")
	if err != nil {
		return "", err
	}
	defer done()
	w := FrameWidth
	if m.Size != nil && m.Size[0] < w {
		w = m.Size[0] / 2 * 2
	}
	w, h := tileSize(m, w)
	seek := t
	if last := lastFrameTime(m); last > 0 {
		seek = min(t, last)
	}
	if err := c.grabFrames(ctx, file, dir, []float64{seek}, w, h); err != nil {
		return "", err
	}
	f, err := os.Open(filepath.Join(dir, "f_00000.png")) //nolint:gosec // our own scratch file
	if err != nil {
		return "", err
	}
	img, _, err := image.Decode(f)
	_ = f.Close()
	if err != nil {
		return "", err
	}
	rgba := toRGBA(img)
	tag(rgba, 6, rgba.Bounds().Dy()-6, timecode(t, true), 18)
	data, err := encodeJPEG(rgba)
	if err != nil {
		return "", err
	}
	r, err := d.blobs().putBytes(data, ".jpg")
	if err != nil {
		return "", err
	}
	return r, d.putDerived(ctx, id, recipe, src, r, map[string]any{"t": t})
}

var loudRE = map[string]*regexp.Regexp{
	"I":    regexp.MustCompile(`I:\s+(-?[\d.]+|-inf)\s+LUFS`),
	"LRA":  regexp.MustCompile(`LRA:\s+(-?[\d.]+)\s+LU`),
	"Peak": regexp.MustCompile(`Peak:\s+(-?[\d.]+|-inf)\s+dB`),
}

// parseLoudness reads ebur128's summary from ffmpeg's log.
func parseLoudness(log string) (*Loudness, bool) {
	i := strings.LastIndex(log, "Summary:")
	if i < 0 {
		return nil, false
	}
	s := log[i:]
	get := func(k string) float64 {
		m := loudRE[k].FindStringSubmatch(s)
		if m == nil {
			return math.NaN()
		}
		if m[1] == "-inf" {
			return -120
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return math.NaN()
		}
		return v
	}
	l := &Loudness{Integrated: get("I"), Range: get("LRA"), Peak: get("Peak")}
	if math.IsNaN(l.Integrated) || math.IsNaN(l.Range) || math.IsNaN(l.Peak) {
		return nil, false
	}
	if l.Integrated < -70 {
		l.Integrated = -70 // silence: ebur128's floor
	}
	return l, true
}

// waveform draws an audio file's waveform (peak and RMS per column) with its loudness.
func (c *Catalog) waveform(ctx context.Context, d *db, id, file string, m Media, src string) (string, *Loudness, error) {
	if !c.tools.available() {
		return "", nil, fmt.Errorf("ffmpeg and ffprobe are needed for waveforms")
	}
	const recipe = "waveform"
	dur := m.duration()
	if dur <= 0 {
		return "", nil, fmt.Errorf("no duration")
	}
	const rate = 8000
	cols := WaveformWidth
	perCol := max(1, int(math.Ceil(dur*rate/float64(cols))))
	stream := "0:a:0"
	cmd := exec.CommandContext(ctx, c.tools.ffmpeg, "-nostdin", "-hide_banner", "-nostats", "-v", "info", "-i", file, //nolint:gosec // argv only
		"-filter_complex", fmt.Sprintf("[%s]asplit=2[a][b];[a]ebur128=peak=sample[l];"+
			"[b]aresample=%d,aformat=sample_fmts=flt:channel_layouts=mono[w]", stream, rate),
		"-map", "[l]", "-f", "null", "-", "-map", "[w]", "-f", "f32le", "pipe:1")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	mins := make([]float32, cols)
	maxs := make([]float32, cols)
	sums := make([]float64, cols)
	counts := make([]int, cols)
	br := bufio.NewReaderSize(out, 1<<16)
	var buf [4]byte
	n := 0
	for {
		if _, err := io.ReadFull(br, buf[:]); err != nil {
			break
		}
		v := math.Float32frombits(binary.LittleEndian.Uint32(buf[:]))
		col := min(n/perCol, cols-1)
		if counts[col] == 0 || v < mins[col] {
			mins[col] = v
		}
		if counts[col] == 0 || v > maxs[col] {
			maxs[col] = v
		}
		sums[col] += float64(v) * float64(v)
		counts[col]++
		n++
	}
	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return "", nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	loud, _ := parseLoudness(errb.String())
	used := min(cols, (n+perCol-1)/perCol)
	img := drawWaveform(mins[:used], maxs[:used], sums[:used], counts[:used], cols, dur, loud)
	data, err := encodePNG(img)
	if err != nil {
		return "", nil, err
	}
	r, err := d.blobs().putBytes(data, ".png")
	if err != nil {
		return "", nil, err
	}
	return r, loud, d.putDerived(ctx, id, recipe, src, r, loud)
}

func drawWaveform(mins, maxs []float32, sums []float64, counts []int, cols int, dur float64, loud *Loudness) *image.RGBA {
	const head, foot = 40, 24
	h := waveformHeight
	img := image.NewRGBA(image.Rect(0, 0, cols, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{18, 18, 24, 255}), image.Point{}, draw.Src)
	top, bottom := head, h-foot
	mid := (top + bottom) / 2
	amp := float64(bottom-top) / 2
	peak := color.RGBA{90, 140, 210, 255}
	rms := color.RGBA{170, 210, 255, 255}
	axis := color.RGBA{60, 60, 72, 255}
	for x := 0; x < cols; x++ {
		img.Set(x, mid, axis)
	}
	for x := range mins {
		if counts[x] == 0 {
			continue
		}
		y0 := mid - int(math.Round(float64(clamp1(maxs[x]))*amp))
		y1 := mid - int(math.Round(float64(clamp1(mins[x]))*amp))
		for y := y0; y <= y1; y++ {
			img.Set(x, y, peak)
		}
		r := math.Sqrt(sums[x] / float64(counts[x]))
		ry := int(math.Round(math.Min(r, 1) * amp))
		for y := mid - ry; y <= mid+ry; y++ {
			img.Set(x, y, rms)
		}
	}
	// Ticks: about 10 across.
	step := niceStep(dur / 10)
	face := uiFace(12)
	grey := color.RGBA{150, 150, 165, 255}
	for t := 0.0; t <= dur+1e-9; t += step {
		x := int(t / dur * float64(cols-1))
		for y := bottom; y < bottom+5; y++ {
			img.Set(x, y, grey)
		}
		if face != nil && x < cols-40 {
			drawText(img, face, x+2, h-6, timecode(t, step < 1), grey)
		}
	}
	header := fmt.Sprintf("%s long", timecode(dur, true))
	if loud != nil {
		header = fmt.Sprintf("%.1f LUFS integrated  ·  LRA %.1f LU  ·  peak %.1f dBFS  ·  %s", loud.Integrated, loud.Range,
			loud.Peak, header)
	}
	if f := uiFace(16); f != nil {
		drawText(img, f, 10, 26, header, color.RGBA{230, 230, 240, 255})
	}
	return img
}

func clamp1(v float32) float32 { return max(-1, min(1, v)) }

// niceStep rounds a tick interval to 1, 2 or 5 times a power of ten (at least 0.1 s).
func niceStep(x float64) float64 {
	if x <= 0.1 {
		return 0.1
	}
	p := math.Pow(10, math.Floor(math.Log10(x)))
	for _, m := range []float64{1, 2, 5, 10} {
		if x <= m*p {
			return m * p
		}
	}
	return 10 * p
}

// Proxy settings: 540 lines, a short GOP for fast seeks.
const (
	proxyHeight = 540
	proxyGOP    = 15
)

// makeProxy makes a video's proxy and records it.
func (c *Catalog) makeProxy(ctx context.Context, d *db, id, file, src string) error {
	recipe := fmt.Sprintf("proxy h=%d gop=%d", proxyHeight, proxyGOP)
	r := d.derivedBlob(ctx, id, recipe, src)
	if r == "" {
		dir, done, err := d.scratch("proxy-*")
		if err != nil {
			return err
		}
		defer done()
		out := filepath.Join(dir, "proxy.mp4")
		_, err = c.tools.ffmpegRun(ctx, "-threads", "4", "-i", file, "-map", "0:v:0", "-map", "0:a:0?",
			"-vf", fmt.Sprintf("scale=-2:%d", proxyHeight), "-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
			"-g", strconv.Itoa(proxyGOP), "-bf", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "96k",
			"-movflags", "+faststart", out)
		if err != nil {
			return err
		}
		if r, err = d.blobs().putTemp(out, ".mp4"); err != nil {
			return err
		}
		if err := d.putDerived(ctx, id, recipe, src, r, nil); err != nil {
			return err
		}
	}
	path, err := d.blobs().path(r)
	if err != nil {
		return err
	}
	pm, err := c.tools.probe(ctx, path)
	if err != nil {
		return err
	}
	p := Proxy{Hash: r, GOP: proxyGOP}
	if pm.Size != nil {
		p.Size = *pm.Size
	}
	return d.setJSON(ctx, id, "proxy", p)
}
