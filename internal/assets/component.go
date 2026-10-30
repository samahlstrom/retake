package assets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"lukechampine.com/blake3"
)

// KindComponent is a component package (spec/components.md): a folder with a component.json.
const KindComponent = "component"

// Component preview kinds.
const (
	PreviewStorybook = "storybook" // each example at five moments (PNG)
	PreviewAnimation = "animation" // two seconds of the first example (animated WebP)
)

// componentLicenses maps the SPDX ids a package may carry to a catalog status.
var componentLicenses = map[string]string{"CC0-1.0": LicenseCleared, "MIT": LicenseCleared,
	"Apache-2.0": LicenseCleared, "BSD-2-Clause": LicenseCleared, "BSD-3-Clause": LicenseCleared, "ISC": LicenseCleared,
	"OFL-1.1": LicenseCleared, "CC-BY-4.0": LicenseAttribution}

// KindOf guesses a file's kind from its extension: video, audio, image, font, or "" when unknown.
func KindOf(path string) string { return kindOf(path) }

// Probe runs ffprobe (or reads an image or a font) on a file.
func (c *Catalog) Probe(ctx context.Context, path string) (Media, error) {
	switch kindOf(path) {
	case KindImage:
		_, m, err := c.loadImage(ctx, path)
		return m, err
	case KindFont:
		return fontMedia(path)
	}
	return c.tools.probe(ctx, path)
}

// PackageFiles lists a package folder's files: slash-separated relative paths, sorted, without previews/, hidden
// files and folders, and links.
func PackageFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(de.Name(), ".") || (de.IsDir() && rel == "previews") {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if de.Type().IsRegular() {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// PackageHash is a package folder's content hash (spec/components.md, "The lockfile"): BLAKE3 over, for each file of
// PackageFiles, its path, NUL, its BLAKE3 in hex and a newline. The editor computes the same.
func PackageHash(dir string) (string, error) {
	files, err := PackageFiles(dir)
	if err != nil {
		return "", err
	}
	h := blake3.New(32, nil)
	for _, rel := range files {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // a package file
		if err != nil {
			return "", err
		}
		fh, _, err := hashReader(f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", rel, strings.TrimPrefix(fh, HashPrefix))
	}
	return fmt.Sprintf("%s%x", HashPrefix, h.Sum(nil)), nil
}

// componentMeta is the part of a component.json the catalog records.
type componentMeta struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	License     string   `json:"license"`
	Authors     []string `json:"authors"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category"`
	UseAs       []string `json:"use_as"`
}

// isComponentDir reports whether a folder is a component package.
func isComponentDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "component.json"))
	return err == nil && st.Mode().IsRegular()
}

// importComponent imports a component folder as one entry of kind component: `components/<name>@<version>` in the
// scope. A project entry references the folder in place; a library entry copies it into <library>/components/. Its
// previews come from retake-edit (component preview), else from the package's committed previews/sheet.png.
func (c *Catalog) importComponent(ctx context.Context, d *db, dir string, a ImportArgs) ImportItem {
	it := ImportItem{Path: dir, Kind: KindComponent}
	b, err := os.ReadFile(filepath.Join(dir, "component.json")) //nolint:gosec // the folder the user named
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	var m componentMeta
	if err := json.Unmarshal(b, &m); err != nil || m.Name == "" || m.Version == "" {
		it.Status, it.Reason = StatusSkipped, fmt.Sprintf("component.json has no name and version (%v)", err)
		return it
	}
	ref := m.Name + "@" + m.Version
	hash, err := PackageHash(dir)
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.Hash = hash
	if d.scope == ScopeLib { // the library keeps its own copy, where lib search and the editor look
		dst := filepath.Join(c.o.Library, "components", ref)
		if dst != dir {
			if h, err := PackageHash(dst); err == nil && h != hash {
				it.Status, it.Reason = StatusSkipped, fmt.Sprintf("the library already has another %s at %s: a "+
					"released version never changes", ref, dst)
				return it
			} else if err != nil {
				if err := copyTree(dir, dst); err != nil {
					it.Status, it.Reason = StatusSkipped, "copying into the library: "+err.Error()
					return it
				}
			}
			dir = dst
		}
	}
	id := MakeID(d.scope, "components/"+ref)
	if r, err := d.getRow(ctx, id); err == nil {
		if r.hash.Valid && r.hash.String == hash {
			it.Status, it.ID, it.DuplicateOf = StatusDuplicate, id, id
			return it
		}
		it.Status, it.Reason = StatusSkipped, fmt.Sprintf("%s is already in the catalog with another hash: a released "+
			"version never changes (bump its version)", id)
		return it
	}
	lic := LicenseArgs{Status: LicenseReviewOnly, Note: "no license in component.json", Source: m.License}
	if s, ok := componentLicenses[m.License]; ok {
		lic = LicenseArgs{Status: s, Source: "SPDX:" + m.License}
		if s == LicenseAttribution {
			lic.Credit = fmt.Sprintf("%q by %s, %s", m.Title, strings.Join(m.Authors, ", "), m.License)
		}
	} else if m.License != "" {
		lic.Note = "license " + m.License + " isn't one a shared package may carry"
	}
	files, _ := PackageFiles(dir)
	var size int64
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
			size += st.Size()
		}
	}
	tags := normTags(append(append([]string{"component"}, m.Category), append(m.Tags, a.Tags...)...))
	meta := map[string]any{"component": map[string]any{"name": m.Name, "version": m.Version, "ref": ref,
		"category": m.Category, "use_as": m.UseAs, "hash": hash, "path": dir}}
	title := firstNonEmpty(a.Title, m.Title, ref)
	prov := Provenance{Importer: firstNonEmpty(a.Importer, "cli"), Source: dir, Seen: d.stamp()}
	err = d.write(ctx, func(tx *sql.Tx) error {
		now := d.stamp()
		_, err := tx.ExecContext(ctx, `INSERT INTO assets (id, scope, kind, hash, size, mtime, locator, in_store, media,
			title, tags, caption, license, credit, license_source, license_note, content_id_risk, provenance, meta,
			created, updated) VALUES (?, ?, ?, ?, ?, NULL, ?, 0, '{}', ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?)`,
			id, d.scope, KindComponent, hash, size, fileLocator(filepath.Join(dir, "component.json")), title,
			mustJSON(tags), firstNonEmpty(a.Caption, m.Description), lic.Status, lic.Credit, lic.Source, lic.Note,
			mustJSON(prov), mustJSON(meta), now, now)
		if err != nil {
			return err
		}
		return indexAsset(ctx, tx, id)
	})
	if err != nil {
		it.Status, it.Reason = StatusSkipped, err.Error()
		return it
	}
	it.ID, it.Status = id, StatusImported
	pv, warns := c.componentPreviews(ctx, d, id, dir, hash)
	it.Warnings = append(it.Warnings, warns...)
	if err := d.setJSON(ctx, id, "previews", pv); err != nil {
		it.Warnings = append(it.Warnings, err.Error())
	}
	return it
}

// componentPreviews makes a component's storybook sheet and animation with retake-edit, or takes the committed sheet.
func (c *Catalog) componentPreviews(ctx context.Context, d *db, id, dir, hash string) (Previews, []string) {
	var pv Previews
	var warns []string
	put := func(kind, file, ext string) {
		ref, _, _, err := d.blobs().putFile(file, ext)
		if err == nil {
			err = d.putDerived(ctx, id, kind, hash, ref, nil)
		}
		if err != nil {
			warns = append(warns, kind+": "+err.Error())
			return
		}
		switch kind {
		case PreviewStorybook:
			pv.Storybook = ref
		case PreviewAnimation:
			pv.Animation = ref
		}
	}
	tmp, err := os.MkdirTemp("", "retake-component-preview-")
	if err != nil {
		return pv, []string{err.Error()}
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := c.runPreview(ctx, dir, tmp); err == nil {
		put(PreviewStorybook, filepath.Join(tmp, "sheet.png"), ".png")
		if _, err := os.Stat(filepath.Join(tmp, "preview.webp")); err == nil {
			put(PreviewAnimation, filepath.Join(tmp, "preview.webp"), ".webp")
		}
		return pv, warns
	} else if committed := filepath.Join(dir, "previews", "sheet.png"); fileExistsAt(committed) {
		put(PreviewStorybook, committed, ".png")
		if webp := filepath.Join(dir, "previews", "preview.webp"); fileExistsAt(webp) {
			put(PreviewAnimation, webp, ".webp")
		}
		return pv, append(warns, "previews from the package's previews/ ("+err.Error()+")")
	} else {
		return pv, append(warns, "no previews: "+err.Error())
	}
}

func fileExistsAt(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// runPreview runs `retake-edit component preview <dir> -o <out> --json`.
func (c *Catalog) runPreview(ctx context.Context, dir, out string) error {
	bin := c.o.EditBin
	if bin == "" {
		bin = c.o.Getenv("RETAKE_EDIT")
	}
	if bin == "" {
		p, err := exec.LookPath("retake-edit")
		if err != nil {
			return fmt.Errorf("retake-edit isn't installed")
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "component", "preview", dir, "-o", out, "--json") //nolint:gosec // argv
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var res struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(stdout.Bytes(), &res) == nil && len(res.Errors) > 0 {
			return fmt.Errorf("retake-edit component preview: %s", res.Errors[0].Message)
		}
		return fmt.Errorf("retake-edit component preview: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if !fileExistsAt(filepath.Join(out, "sheet.png")) {
		return fmt.Errorf("retake-edit component preview made no sheet")
	}
	return nil
}

// copyTree copies a package folder (its files, previews included; hidden entries skipped).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel != "." && strings.HasPrefix(de.Name(), ".") {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if de.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // a library folder
		}
		if !de.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path) //nolint:gosec // a package file
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.Create(target) //nolint:gosec // inside the library
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

// componentPreviewKinds are the previews a component has.
var componentPreviewKinds = []string{PreviewStorybook, PreviewAnimation}

func isComponentPreview(kind string) bool { return slices.Contains(componentPreviewKinds, kind) }
