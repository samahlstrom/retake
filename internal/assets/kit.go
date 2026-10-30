package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// KitSchema is a brand kit's kit.json schema (spec/assets.md, "Brand kits").
const KitSchema = "retake-kit/0.1"

// KitAsset is one file a kit lists.
type KitAsset struct {
	Key     string       `json:"key"`
	Path    string       `json:"path"`
	Title   string       `json:"title,omitempty"`
	Tags    []string     `json:"tags,omitempty"`
	License *LicenseArgs `json:"license,omitempty"`
}

// Kit is a kit.json.
type Kit struct {
	Schema  string         `json:"schema"`
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Title   string         `json:"title,omitempty"`
	License *LicenseArgs   `json:"license,omitempty"`
	Tokens  map[string]any `json:"tokens,omitempty"`
	Assets  []KitAsset     `json:"assets"`
}

var (
	kitNameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	kitVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// KitResult is an installed kit.
type KitResult struct {
	Kit    string         `json:"kit"` // brand:<name>@<version>
	Tokens map[string]any `json:"tokens"`
	ImportResult
}

// InstallKit installs a brand kit folder into the library: each listed file is copied into the store as
// brand:<name>@<version>/<key>, with previews, and the kit's tokens are recorded.
func (c *Catalog) InstallKit(ctx context.Context, dir string) (KitResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return KitResult{}, wrap(err)
	}
	data, err := os.ReadFile(filepath.Join(abs, "kit.json")) //nolint:gosec // a kit folder the user named
	if err != nil {
		return KitResult{}, errorf(CodeNotFound, "no kit.json in %s", abs)
	}
	var k Kit
	if err := json.Unmarshal(data, &k); err != nil {
		return KitResult{}, errorf(CodeBadRequest, "kit.json: %v", err)
	}
	switch {
	case k.Schema != "" && k.Schema != KitSchema:
		return KitResult{}, errorf(CodeBadRequest, "kit.json schema %q, this retake reads %s", k.Schema, KitSchema)
	case !kitNameRE.MatchString(k.Name):
		return KitResult{}, errorf(CodeBadRequest, "kit name %q: use a-z, 0-9 and -", k.Name)
	case !kitVersionRE.MatchString(k.Version):
		return KitResult{}, errorf(CodeBadRequest, "kit version %q: use letters, digits, . _ and -", k.Version)
	}
	if k.License != nil {
		if err := k.License.check(); err != nil {
			return KitResult{}, err
		}
	}
	seen := map[string]bool{}
	for _, a := range k.Assets {
		if !validName(a.Key) || strings.Contains(a.Key, "@") || seen[a.Key] {
			return KitResult{}, errorf(CodeBadRequest, "kit asset key %q is missing, repeated or not an id", a.Key)
		}
		seen[a.Key] = true
		p := filepath.Join(abs, filepath.FromSlash(a.Path))
		if rel, err := filepath.Rel(abs, p); err != nil || strings.HasPrefix(rel, "..") {
			return KitResult{}, errorf(CodeBadRequest, "kit asset %s: %s is outside the kit", a.Key, a.Path)
		}
		if _, err := os.Stat(p); err != nil {
			return KitResult{}, errorf(CodeNotFound, "kit asset %s: no file %s", a.Key, a.Path)
		}
		if a.License != nil {
			if err := a.License.check(); err != nil {
				return KitResult{}, err
			}
		}
	}
	d, err := c.catalogFor(ScopeLib, true)
	if err != nil {
		return KitResult{}, wrap(err)
	}
	kd := &db{rw: d.rw, ro: d.ro, dir: d.dir, scope: ScopeBrand, now: d.now}
	res := KitResult{Kit: prefixBrand + k.Name + "@" + k.Version, Tokens: orEmpty(k.Tokens)}
	for _, a := range k.Assets {
		lic := LicenseArgs{}
		if k.License != nil {
			lic = *k.License
		}
		if a.License != nil {
			lic = *a.License
		}
		name := k.Name + "@" + k.Version + "/" + a.Key
		id := MakeID(ScopeBrand, name)
		if _, err := d.getRow(ctx, id); err == nil {
			res.add(ImportItem{Path: a.Path, ID: id, Status: StatusSkipped, Reason: "already installed"})
			continue
		}
		p := filepath.Join(abs, filepath.FromSlash(a.Path))
		it := c.importKitFile(ctx, kd, p, name, ImportArgs{License: lic, Tags: append([]string{"brand", k.Name}, a.Tags...),
			Title: a.Title, Importer: "kit:" + k.Name + "@" + k.Version})
		res.add(it)
	}
	err = d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kits (name, version, title, tokens, manifest, installed)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (name, version) DO UPDATE SET title = excluded.title,
			tokens = excluded.tokens, manifest = excluded.manifest, installed = excluded.installed`,
			k.Name, k.Version, k.Title, mustJSON(orEmpty(k.Tokens)), string(data), d.stamp())
		return err
	})
	return res, wrap(err)
}

// importKitFile imports a kit file: always into the store, never deduplicated away (a kit owns its copy).
func (c *Catalog) importKitFile(ctx context.Context, kd *db, path, name string, a ImportArgs) ImportItem {
	it := c.importFile(ctx, kd, path, name, ImportArgs{License: a.License, Tags: a.Tags, Title: a.Title,
		Importer: a.Importer, Proxy: ProxyNever, kit: true})
	if it.Status == StatusImported && it.ID != MakeID(ScopeBrand, name) {
		it.Warnings = append(it.Warnings, fmt.Sprintf("installed as %s", it.ID))
	}
	return it
}
