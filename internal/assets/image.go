package assets

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/gif"  // GIF decoding for thumbnails and hashes
	_ "image/jpeg" // JPEG decoding
	_ "image/png"  // PNG decoding
	"math/bits"
	"os"
	"sort"
	"strconv"

	_ "golang.org/x/image/bmp" // BMP decoding
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // TIFF decoding
	_ "golang.org/x/image/webp" // WebP decoding
)

// NearDistance is the largest dHash Hamming distance reported as a near duplicate.
const NearDistance = 10

// ThumbSize is the longest side of an image thumbnail.
const ThumbSize = 512

// loadImage decodes an image with Go's decoders, falling back to ffmpeg for formats Go can't read (AVIF, HEIC).
func (c *Catalog) loadImage(ctx context.Context, path string) (image.Image, Media, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the file being imported
	if err != nil {
		return nil, Media{}, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		out, ferr := c.tools.ffmpegRun(ctx, "-i", path, "-frames:v", "1", "-vf", "scale='min(2048,iw)':-2",
			"-f", "image2pipe", "-c:v", "png", "pipe:1")
		if ferr != nil {
			return nil, Media{}, fmt.Errorf("can't decode the image: %w", err)
		}
		if img, _, err = image.Decode(bytes.NewReader(out)); err != nil {
			return nil, Media{}, fmt.Errorf("can't decode the image: %w", err)
		}
		if m, err := c.tools.probe(ctx, path); err == nil && m.Size != nil {
			return img, Media{Size: m.Size}, nil
		}
	}
	b := img.Bounds()
	return img, Media{Size: &[2]int{b.Dx(), b.Dy()}}, nil
}

// thumbnail scales img to at most ThumbSize on its long side. It keeps PNG when the image has transparency.
func thumbnail(img image.Image) ([]byte, string, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > ThumbSize || h > ThumbSize {
		if w >= h {
			w, h = ThumbSize, max(1, h*ThumbSize/w)
		} else {
			w, h = max(1, w*ThumbSize/h), ThumbSize
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	if !dst.Opaque() {
		data, err := encodePNG(dst)
		return data, ".png", err
	}
	data, err := encodeJPEG(dst)
	return data, ".jpg", err
}

// dHash is a 64-bit difference hash: the image reduced to 9×8 grayscale by area averaging, one bit per horizontal
// neighbor pair (left darker than right).
func dHash(img image.Image) uint64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return 0
	}
	var cells [8][9]float64
	// Sample at most ~256 points per axis: enough for an average over each of the 9×8 cells.
	sx, sy := max(1, w/256), max(1, h/256)
	var counts [8][9]float64
	for y := 0; y < h; y += sy {
		cy := y * 8 / h
		for x := 0; x < w; x += sx {
			cx := x * 9 / w
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// Transparent pixels count as white, as a viewer on a light page would see them.
			af := float64(a) / 0xffff
			lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 0xffff
			lum = lum + (1-af)*1
			cells[cy][cx] += lum
			counts[cy][cx]++
		}
	}
	var hash uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			l, r := cells[y][x]/max(1, counts[y][x]), cells[y][x+1]/max(1, counts[y][x+1])
			hash <<= 1
			if l < r {
				hash |= 1
			}
		}
	}
	return hash
}

func hashHex(h uint64) string { return fmt.Sprintf("%016x", h) }

// nearImages stores the image's dHash and returns the images within NearDistance in the visible catalogs, closest
// first (at most five). It records them on the entry too.
func (c *Catalog) nearImages(ctx context.Context, d *db, id string, img image.Image) ([]Near, error) {
	h := dHash(img)
	err := d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE assets SET phash = ? WHERE id = ?`, hashHex(h), id)
		return err
	})
	if err != nil {
		return nil, err
	}
	cats, err := c.visible()
	if err != nil {
		return nil, err
	}
	var near []Near
	for _, cat := range cats {
		rows, err := cat.rw.QueryContext(ctx, `SELECT id, phash FROM assets WHERE kind = 'image' AND phash != '' AND id != ?`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var oid, ph string
			if err := rows.Scan(&oid, &ph); err != nil {
				_ = rows.Close()
				return nil, err
			}
			o, err := strconv.ParseUint(ph, 16, 64)
			if err != nil {
				continue
			}
			if dist := bits.OnesCount64(h ^ o); dist <= NearDistance {
				near = append(near, Near{ID: oid, Distance: dist})
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(near, func(i, j int) bool {
		if near[i].Distance != near[j].Distance {
			return near[i].Distance < near[j].Distance
		}
		return near[i].ID < near[j].ID
	})
	if len(near) > 5 {
		near = near[:5]
	}
	if len(near) > 0 {
		if err := d.setJSON(ctx, id, "near", near); err != nil {
			return near, err
		}
	}
	return near, nil
}
