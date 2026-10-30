package lib

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
)

// AddArgs are lib add's options.
type AddArgs struct {
	Ref      string // name or name@version
	Registry string // a path or URL
	Timeline string // the project's timeline: packages go to components/ beside it
	Force    bool   // replace a project copy that differs
}

// Added is one package lib add handled.
type Added struct {
	Ref     string `json:"ref"`
	Type    string `json:"type"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	License string `json:"license"`
	// Status is added, unchanged (the project already had this exact copy) or replaced (with --force).
	Status string `json:"status"`
	// Because names the package that required it ("" for the one asked for).
	Because string `json:"because,omitempty"`
}

// AddResult is lib add's answer.
type AddResult struct {
	Packages []Added  `json:"packages"`
	Lock     string   `json:"lock"`
	Use      string   `json:"use,omitempty"` // how to use what was added
	Warnings []string `json:"warnings,omitempty"`
}

// Add copies a registry package (and the packages it requires) into the project's components/, checks every file and
// the package against their hashes, and pins it in the timeline's lockfile with its source. The project owns the copy.
func Add(ctx context.Context, a AddArgs) (AddResult, error) {
	if a.Timeline == "" {
		return AddResult{}, errorf(CodeNotFound, "no timeline: lib add puts packages next to the project's timeline (--timeline)")
	}
	tl, err := filepath.Abs(a.Timeline)
	if err != nil {
		return AddResult{}, wrap(err)
	}
	name, version, err := ParseRef(a.Ref)
	if err != nil {
		return AddResult{}, err
	}
	r, err := LoadRegistry(ctx, a.Registry)
	if err != nil {
		return AddResult{}, err
	}
	lockPath := LockPath(tl)
	lock, err := ReadLock(lockPath)
	if err != nil {
		return AddResult{}, err
	}
	res := AddResult{Lock: lockPath}
	source := "registry:" + a.Registry
	if !isURL(a.Registry) {
		if abs, err := filepath.Abs(a.Registry); err == nil {
			if rel, err := filepath.Rel(filepath.Dir(tl), abs); err == nil {
				source = "registry:" + filepath.ToSlash(rel) // relative: a lockfile carries no home folder
			}
		}
	}
	type want struct{ name, version, because string }
	queue := []want{{name, version, ""}}
	done := map[string]bool{}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		e, err := r.Find(w.name, w.version)
		if err != nil {
			if w.because != "" {
				err = errorf(CodeNotFound, "%s requires %s@%s: %v", w.because, w.name, w.version, err)
			}
			return res, err
		}
		if done[e.Ref()] {
			continue
		}
		done[e.Ref()] = true
		ad, err := install(ctx, r, e, tl, a.Force)
		if err != nil {
			return res, err
		}
		ad.Because = w.because
		res.Packages = append(res.Packages, ad)
		lock.Packages[e.Ref()] = LockEntry{Type: e.Type, Hash: e.Hash, Path: ad.Path, Source: source, License: e.License}
		for _, req := range e.Requires {
			n, v, err := ParseRef(req)
			if err != nil {
				return res, errorf(CodeInvalid, "%s requires %q: %v", e.Ref(), req, err)
			}
			queue = append(queue, want{n, v, e.Ref()})
		}
	}
	if err := lock.Write(lockPath); err != nil {
		return res, wrap(err)
	}
	first := res.Packages[0]
	if first.Type == TypeKit {
		res.Use = fmt.Sprintf(`add "%s" to the timeline's "kits" (a patch: {"op": "set", "path": "kits", "value": [...]})`, first.Ref)
	} else {
		res.Use = fmt.Sprintf(`use it where a composition goes: {"use": "%s", "props": {...}} (retake lib describe %s)`, first.Ref, first.Ref)
	}
	return res, nil
}

// install writes one package into <timeline dir>/components/<ref>/ (via a temporary folder, so a failed check leaves
// nothing behind).
func install(ctx context.Context, r *Registry, e Entry, tl string, force bool) (Added, error) {
	if !licenseAllowed(e.License) {
		return Added{}, errorf(CodeLicense, "%s is licensed %q; packages need one of %s", e.Ref(), e.License,
			strings.Join(AllowedLicenses, ", "))
	}
	comps := filepath.Join(filepath.Dir(tl), "components")
	dest := filepath.Join(comps, e.Ref())
	rel := "components/" + e.Ref()
	ad := Added{Ref: e.Ref(), Type: e.Type, Path: rel, Hash: e.Hash, License: e.License, Status: "added"}
	if _, err := os.Stat(dest); err == nil {
		h, err := PackageHash(dest)
		if err != nil {
			return ad, wrap(err)
		}
		if h == e.Hash {
			ad.Status = "unchanged"
			return ad, nil
		}
		if !force {
			return ad, errorf(CodeConflict, "%s differs from the registry's %s (%s here, %s there): the project's copy "+
				"was changed; keep it, or replace it with --force", rel, e.Ref(), h, e.Hash)
		}
		ad.Status = "replaced"
	}
	if err := os.MkdirAll(comps, 0o755); err != nil { //nolint:gosec // a project folder
		return ad, wrap(err)
	}
	tmp, err := os.MkdirTemp(comps, ".add-"+e.Name+"-")
	if err != nil {
		return ad, wrap(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, f := range e.Files {
		clean := filepath.Clean(filepath.FromSlash(f.Path))
		if !filepath.IsLocal(clean) {
			return ad, errorf(CodeInvalid, "%s lists a file outside the package: %s", e.Ref(), f.Path)
		}
		b, err := r.Fetch(ctx, e.Path+"/"+f.Path)
		if err != nil {
			return ad, err
		}
		if h := assets.HashBytes(b); h != f.Hash {
			return ad, errorf(CodeHash, "%s/%s is %s, the registry says %s", e.Ref(), f.Path, h, f.Hash)
		}
		p := filepath.Join(tmp, clean)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // a project folder
			return ad, wrap(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // package files aren't secrets
			return ad, wrap(err)
		}
	}
	h, err := PackageHash(tmp)
	if err != nil {
		return ad, wrap(err)
	}
	if h != e.Hash {
		return ad, errorf(CodeHash, "%s is %s once copied, the registry says %s (a file is missing from its list?)",
			e.Ref(), h, e.Hash)
	}
	// Previews aren't part of the hash; bring the sheet along when the registry has one.
	if s := e.Previews["sheet"]; s != "" {
		if b, err := r.Fetch(ctx, s); err == nil {
			_ = os.MkdirAll(filepath.Join(tmp, "previews"), 0o755)                  //nolint:gosec // a project folder
			_ = os.WriteFile(filepath.Join(tmp, "previews", "sheet.png"), b, 0o644) //nolint:gosec // an image
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return ad, wrap(err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return ad, wrap(err)
	}
	return ad, nil
}
