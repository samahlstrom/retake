package project

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var imageNameRE = regexp.MustCompile(`^img_\d{2,3}\.(png|jpg|webp|gif)$`)

// ValidImageName reports whether name is a take image's file name: img_01.png (or .jpg, .webp, .gif).
func ValidImageName(name string) bool { return imageNameRE.MatchString(name) }

// ImageTypes maps a take image's extension to its content type.
var ImageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

// TakeImage is one image dropped on the video while recording (an entry of the take's images.json).
type TakeImage struct {
	Seg    string     `json:"seg"`
	File   string     `json:"file"`
	VideoT float64    `json:"video_t"`
	Box    [4]float64 `json:"box"` // [x, y, w, h], normalized 0-1 to the frame
}

// ReadImages reads a take's images.json. Entries without a valid file name are skipped; a missing or broken file
// is no images.
func ReadImages(takeDir string) []TakeImage {
	b, err := os.ReadFile(filepath.Join(takeDir, "images.json")) //nolint:gosec // a file of a take folder
	if err != nil {
		return nil
	}
	var raw []struct {
		Seg    json.RawMessage `json:"seg"`
		File   string          `json:"file"`
		VideoT *float64        `json:"video_t"`
		Box    []float64       `json:"box"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	var out []TakeImage
	for _, r := range raw {
		if !ValidImageName(r.File) {
			continue
		}
		img := TakeImage{Seg: rawString(r.Seg), File: r.File}
		if r.VideoT != nil {
			img.VideoT = *r.VideoT
		}
		if len(r.Box) == 4 {
			copy(img.Box[:], r.Box)
		}
		out = append(out, img)
	}
	return out
}

// rawString reads a JSON string or number as a string ("S1", 1 -> "1").
func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

// ImagesOf returns the images of the given notes, by note id. Each image goes to one note among all the notes of its
// take (AttachImages), so a note's images don't depend on which other notes are asked for.
func (s *Store) ImagesOf(notes []Note, takeDir func(Note) string) (map[int64][]TakeImage, error) {
	var takes []string
	seen := map[string]bool{}
	for _, n := range notes {
		if !seen[n.Take] {
			seen[n.Take] = true
			takes = append(takes, n.Take)
		}
	}
	if len(takes) == 0 {
		return map[int64][]TakeImage{}, nil
	}
	all, err := s.Notes(NoteFilter{Takes: takes})
	if err != nil {
		return nil, err
	}
	return AttachImages(all, takeDir), nil
}

// AttachImages gives each image of the notes' takes to the note it belongs to: of the notes of the same take and
// segment, the one whose time span holds the image's video time, else the nearest. takeDir says where a note's
// take folder is ("" = unknown). It returns the images by note id.
func AttachImages(notes []Note, takeDir func(Note) string) map[int64][]TakeImage {
	type group struct{ take, seg string }
	groups := map[group][]Note{}
	dirs := map[string]string{}
	for _, n := range notes {
		g := group{n.Take, n.Seg}
		groups[g] = append(groups[g], n)
		if _, ok := dirs[n.Take]; !ok {
			dirs[n.Take] = takeDir(n)
		}
	}
	out := map[int64][]TakeImage{}
	for take, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, img := range ReadImages(dir) {
			cands := groups[group{take, img.Seg}]
			if len(cands) == 0 {
				continue
			}
			best, bestD := cands[0].ID, math.Inf(1)
			for _, n := range cands {
				d := 0.0
				switch {
				case img.VideoT < n.T0:
					d = n.T0 - img.VideoT
				case img.VideoT > n.T1:
					d = img.VideoT - n.T1
				}
				if d < bestD {
					best, bestD = n.ID, d
				}
			}
			out[best] = append(out[best], img)
		}
	}
	return out
}
