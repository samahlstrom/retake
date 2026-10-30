package assets

import (
	"context"
	"fmt"
	"image"
	"os"
	"slices"
	"strings"
)

// Preview kinds for PreviewArgs.Kind.
const (
	PreviewContactSheet = "contact_sheet"
	PreviewStrip        = "strip"
	PreviewWaveform     = "waveform"
	PreviewThumbnail    = "thumbnail"
	PreviewSpecimen     = "specimen"
	PreviewFrames       = "frames"
)

// PreviewArgs ask for an asset's previews.
type PreviewArgs struct {
	ID   string
	Kind string    // "" = the asset's main preview (or frames when At is given, a strip when Span is)
	At   []float64 // frames at these asset times
	Span string    // a span id: its strip
}

// PreviewFile is one preview image.
type PreviewFile struct {
	Kind   string   `json:"kind"`
	Path   string   `json:"path"`
	Ref    string   `json:"ref"`
	T      *float64 `json:"t,omitempty"`
	Span   string   `json:"span,omitempty"`
	EveryS float64  `json:"every_s,omitempty"`
	Width  int      `json:"width,omitempty"`
	Height int      `json:"height,omitempty"`
}

// PreviewResult lists preview files.
type PreviewResult struct {
	ID    string        `json:"id"`
	Kind  string        `json:"kind"`
	Files []PreviewFile `json:"files"`
}

// entrySource is an entry's content identity for derived files.
func entrySource(e Entry) string {
	if e.Blob.Hash != nil {
		return *e.Blob.Hash
	}
	if st, err := os.Stat(e.Blob.Path); err == nil {
		return sourceIdentity("", st.Size(), st.ModTime().UnixNano())
	}
	return "missing"
}

// frameSource is the file to grab frames from: the proxy when there is one, else the asset itself.
func (c *Catalog) frameSource(d *db, e Entry) string {
	if e.Proxy != nil {
		if p, err := d.blobs().path(e.Proxy.Hash); err == nil {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return e.Blob.Path
}

// Preview returns an asset's preview files, making the ones that are missing: the contact sheet, waveform,
// thumbnail or specimen; a span's strip; or frames at times (from the proxy when there is one).
func (c *Catalog) Preview(ctx context.Context, a PreviewArgs) (PreviewResult, error) {
	d, r, err := c.find(ctx, a.ID)
	if err != nil {
		return PreviewResult{}, wrap(err)
	}
	e, err := d.entry(ctx, r)
	if err != nil {
		return PreviewResult{}, wrap(err)
	}
	kind := a.Kind
	if kind == "" {
		switch {
		case len(a.At) > 0:
			kind = PreviewFrames
		case a.Span != "":
			kind = PreviewStrip
		default:
			kind = map[string]string{KindVideo: PreviewContactSheet, KindAudio: PreviewWaveform,
				KindImage: PreviewThumbnail, KindFont: PreviewSpecimen}[e.Kind]
		}
	}
	allowed := map[string][]string{
		KindVideo: {PreviewContactSheet, PreviewStrip, PreviewFrames, PreviewWaveform},
		KindAudio: {PreviewWaveform},
		KindImage: {PreviewThumbnail},
		KindFont:  {PreviewSpecimen},
	}[e.Kind]
	if kind == "" || !slices.Contains(allowed, kind) {
		if len(allowed) == 0 {
			return PreviewResult{}, errorf(CodeBadRequest, "%s is a %s file: there are no previews for it", e.ID, e.Kind)
		}
		return PreviewResult{}, errorf(CodeBadRequest, "a %s has previews %s, not %q", e.Kind,
			strings.Join(allowed, ", "), kind)
	}
	if !e.Blob.Exists {
		// Stored previews can still be shown; anything new needs the file.
		if f, ok := storedPreview(d, e, kind); ok {
			return PreviewResult{ID: e.ID, Kind: kind, Files: []PreviewFile{f}}, nil
		}
		return PreviewResult{}, errorf(CodeNotFound, "%s: its file isn't there (%s)", e.ID, e.Blob.Path)
	}
	src := entrySource(e)
	res := PreviewResult{ID: e.ID, Kind: kind, Files: []PreviewFile{}}
	add := func(ref string, f PreviewFile) {
		f.Ref = ref
		f.Path, _ = d.blobs().path(ref)
		f.Width, f.Height = imageSize(f.Path)
		res.Files = append(res.Files, f)
	}
	switch kind {
	case PreviewContactSheet:
		if e.Previews.ContactSheet != "" {
			if f, ok := storedPreview(d, e, kind); ok {
				res.Files = append(res.Files, f)
				return res, nil
			}
		}
		if e.Media.duration() <= 0 || !e.Media.hasVideo() {
			return res, errorf(CodeFailed, "%s has no video duration to make a contact sheet from", e.ID)
		}
		every := sheetEvery(e.Media.duration())
		ref, err := c.contactSheet(ctx, d, e.ID, c.frameSource(d, e), e.Media, every, src)
		if err != nil {
			return res, wrap(err)
		}
		pv := e.Previews
		pv.Paths = nil
		pv.ContactSheet, pv.EveryS = ref, every
		if err := d.setJSON(ctx, e.ID, "previews", pv); err != nil {
			return res, wrap(err)
		}
		add(ref, PreviewFile{Kind: kind, EveryS: every})
	case PreviewWaveform:
		if e.Previews.Waveform != "" {
			if f, ok := storedPreview(d, e, kind); ok {
				res.Files = append(res.Files, f)
				return res, nil
			}
		}
		ref, loud, err := c.waveform(ctx, d, e.ID, e.Blob.Path, e.Media, src)
		if err != nil {
			return res, wrap(err)
		}
		pv := e.Previews
		pv.Paths = nil
		pv.Waveform, pv.Loudness = ref, loud
		if err := d.setJSON(ctx, e.ID, "previews", pv); err != nil {
			return res, wrap(err)
		}
		add(ref, PreviewFile{Kind: kind})
	case PreviewThumbnail, PreviewSpecimen:
		if f, ok := storedPreview(d, e, kind); ok {
			res.Files = append(res.Files, f)
			return res, nil
		}
		var img image.Image
		if kind == PreviewThumbnail {
			if img, _, err = c.loadImage(ctx, e.Blob.Path); err != nil {
				return res, wrap(err)
			}
		}
		pv, warns := c.makePreviews(ctx, d, e.ID, e.Blob.Path, e.Kind, e.Media, img, src, ImportArgs{})
		if len(warns) > 0 {
			return res, errorf(CodeFailed, "%s", strings.Join(warns, "; "))
		}
		if err := d.setJSON(ctx, e.ID, "previews", pv); err != nil {
			return res, wrap(err)
		}
		ref := pv.Thumbnail + pv.Specimen
		add(ref, PreviewFile{Kind: kind})
	case PreviewStrip:
		if a.Span == "" {
			return res, errorf(CodeBadRequest, "a strip is a span's: give span (sp:<n>)")
		}
		sp, err := c.GetSpan(ctx, a.Span)
		if err != nil {
			return res, err
		}
		if sp.Asset != e.ID {
			return res, errorf(CodeBadRequest, "span %s is on %s, not %s", a.Span, sp.Asset, e.ID)
		}
		_, n, _ := ParseSpanID(sp.Span)
		ref, err := c.strip(ctx, d, e.ID, c.frameSource(d, e), e.Media, n, sp.T0, sp.T1, src)
		if err != nil {
			return res, wrap(err)
		}
		add(ref, PreviewFile{Kind: kind, Span: sp.Span})
	case PreviewFrames:
		if len(a.At) == 0 {
			return res, errorf(CodeBadRequest, "frames need times: at [12.5, 30]")
		}
		if len(a.At) > 24 {
			return res, errorf(CodeBadRequest, "at most 24 frames at a time (got %d)", len(a.At))
		}
		dur := e.Media.duration()
		for _, t := range a.At {
			if t < 0 || (dur > 0 && t > dur) {
				return res, errorf(CodeBadRequest, "%.3f is outside %s (0 to %.3f s)", t, e.ID, dur)
			}
		}
		for _, t := range a.At {
			ref, err := c.frame(ctx, d, e.ID, c.frameSource(d, e), e.Media, t, src)
			if err != nil {
				return res, wrap(err)
			}
			tt := t
			add(ref, PreviewFile{Kind: kind, T: &tt})
		}
	}
	return res, nil
}

// storedPreview returns an entry's stored preview of a kind if its file is there.
func storedPreview(d *db, e Entry, kind string) (PreviewFile, bool) {
	ref := map[string]string{PreviewContactSheet: e.Previews.ContactSheet, PreviewWaveform: e.Previews.Waveform,
		PreviewThumbnail: e.Previews.Thumbnail, PreviewSpecimen: e.Previews.Specimen}[kind]
	if ref == "" {
		return PreviewFile{}, false
	}
	p, err := d.blobs().path(ref)
	if err != nil {
		return PreviewFile{}, false
	}
	if _, err := os.Stat(p); err != nil {
		return PreviewFile{}, false
	}
	f := PreviewFile{Kind: kind, Path: p, Ref: ref}
	if kind == PreviewContactSheet {
		f.EveryS = e.Previews.EveryS
	}
	f.Width, f.Height = imageSize(p)
	return f, true
}

// imageSize reads an image file's dimensions (0, 0 if it can't).
func imageSize(path string) (int, int) {
	f, err := os.Open(path) //nolint:gosec // a preview blob
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// describePreview is a one-line summary of a preview file, for the CLI.
func describePreview(f PreviewFile) string {
	s := fmt.Sprintf("%s %dx%d %s", f.Kind, f.Width, f.Height, f.Path)
	if f.T != nil {
		s = fmt.Sprintf("frame at %s %dx%d %s", timecode(*f.T, true), f.Width, f.Height, f.Path)
	}
	return s
}

// DescribePreview is describePreview for other packages.
func DescribePreview(f PreviewFile) string { return describePreview(f) }
