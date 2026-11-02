package ext

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// kindExt is an extension with a v0 kind (marker idea.md).
func kindExt(id, where string) *Ext {
	body := manifestOf(id) + "\n[[kinds]]\nid = \"doc\"\nfolder = \"v0\"\nmarker = \"idea.md\"\n"
	return Load(fstest.MapFS{ManifestFile: {Data: []byte(body)}}, "", where, id)
}

func plainExt(id, where string) *Ext {
	return Load(fstest.MapFS{ManifestFile: {Data: []byte(manifestOf(id))}}, "", where, id)
}

func ids(s *Set) []string {
	var out []string
	for _, e := range s.List() {
		out = append(out, e.ID)
	}
	return out
}

func TestResolve(t *testing.T) {
	proj := plainExt("proj", WhereProject)
	api2 := Load(fstest.MapFS{ManifestFile: {Data: []byte(strings.Replace(manifestOf("future"), "api = 1", "api = 2", 1))}},
		"", WhereUser, "future")
	broken := Load(fstest.MapFS{ManifestFile: {Data: []byte(manifestOf("broken") + "[ui]\nmodule = \"ui/gone.js\"\n")}},
		"", WhereUser, "broken")
	found := []*Ext{
		plainExt("dev", WhereFlag), proj, plainExt("off", WhereUser), plainExt("on", WhereBundled), api2, broken,
		kindExt("zeta", WhereUser), kindExt("alpha", WhereUser),
	}
	for _, tc := range []struct {
		name     string
		enabled  []string
		pins     map[string]string
		on       []string
		warnings []string
	}{
		{name: "flags are always on", on: []string{"dev"}},
		{name: "enabled", enabled: []string{"on"}, on: []string{"dev", "on"}},
		{name: "a project's needs its pin", enabled: []string{"proj"}, on: []string{"dev"},
			warnings: []string{"proj is the folder's own extension and isn't pinned: run retake ext enable proj to trust it"}},
		{name: "pinned", enabled: []string{"proj"}, pins: map[string]string{"proj": proj.Hash}, on: []string{"dev", "proj"}},
		{name: "changed since pinned", enabled: []string{"proj"}, pins: map[string]string{"proj": "b3:" + strings.Repeat("0", 64)},
			on: []string{"dev"}, warnings: []string{"proj changed since you enabled it: run retake ext enable proj again"}},
		{name: "api mismatch", enabled: []string{"future"}, on: []string{"dev"},
			warnings: []string{"future 1.0.0 needs extension API 2; this retake has 1"}},
		{name: "broken", enabled: []string{"broken"}, on: []string{"dev"},
			warnings: []string{"broken isn't loaded (user): extension.toml: ui.module names ui/gone.js, which isn't there"}},
		{name: "two kinds on v0: the second by id is off", enabled: []string{"zeta", "alpha"}, on: []string{"alpha", "dev"},
			warnings: []string{"zeta isn't loaded: its kind zeta.doc claims v0/, which alpha.doc already claims"}},
		{name: "not found", enabled: []string{"ghost"}, on: []string{"dev"},
			warnings: []string{"ghost is enabled but wasn't found (retake ext list shows what's found)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, warnings := Resolve(found, tc.enabled, tc.pins)
			if got := ids(s); !slices.Equal(got, tc.on) {
				t.Errorf("on = %v, want %v", got, tc.on)
			}
			if !slices.Equal(warnings, tc.warnings) {
				t.Errorf("warnings = %q, want %q", warnings, tc.warnings)
			}
		})
	}
}

// A nil *Set is no extensions, and every method works on it.
func TestNilSet(t *testing.T) {
	var s *Set
	if s.List() != nil || s.Get("x") != nil || s.Without(nil) != nil || s.KindAt(t.TempDir()) != "" {
		t.Error("nil set has something")
	}
	if _, _, ok := s.Kind("ignition.doc"); ok {
		t.Error("nil set has a kind")
	}
	if string(s.StylesCSS()) != "@layer retake, ext;\n" {
		t.Errorf("styles %q", s.StylesCSS())
	}
	if b := s.APIList(nil).(APIBody); b.API != 1 || b.Extensions == nil || len(b.Extensions) != 0 {
		t.Errorf("api %+v", b)
	}
}

func TestSetLookups(t *testing.T) {
	s, _ := Resolve([]*Ext{kindExt("ignition", WhereBundled), plainExt("hello", WhereFlag)}, []string{"ignition"}, nil)
	if !slices.Equal(ids(s), []string{"hello", "ignition"}) || s.Get("ignition") == nil || s.Get("nope") != nil {
		t.Fatalf("set %v", ids(s))
	}
	if e, k, ok := s.Kind("ignition.doc"); !ok || e.ID != "ignition" || k.Marker != "idea.md" {
		t.Errorf("Kind: %v %+v %v", e, k, ok)
	}
	for _, full := range []string{"ignition.talk", "hello.doc", "ignition", ""} {
		if _, _, ok := s.Kind(full); ok {
			t.Errorf("Kind(%q) found", full)
		}
	}
	if got := ids(s.Without(map[string]bool{"ignition": true})); !slices.Equal(got, []string{"hello"}) {
		t.Errorf("Without = %v", got)
	}
	if got := ids(s); len(got) != 2 {
		t.Errorf("Without changed the set: %v", got)
	}
}

func TestKindAt(t *testing.T) {
	s, _ := Resolve([]*Ext{kindExt("ignition", WhereBundled)}, []string{"ignition"}, nil)
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"idea/v0/idea.md":            "# Idea\n",
		"cut/v0/idea.md":             "# Idea\n",
		"cut/v1/cut_v1.mp4":          "video",
		"video/v0/idea.md":           "# Not a kind\n",
		"video/v0/video_v0.mp4":      "video",
		"plain/v0/notes.md":          "no marker",
		"dirmarker/v0/idea.md/x":     "a folder named like the marker",
		"refsvideo/v0/idea.md":       "# Idea\n",
		"refsvideo/v0/refs/a_v1.mp4": "a clip in a subfolder doesn't count",
	})
	if err := os.Symlink(filepath.Join(root, "idea", "v0", "idea.md"), filepath.Join(root, "plain", "v0", "idea.md")); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		"idea": "ignition.doc", "cut": "ignition.doc", "video": "", "plain": "", "dirmarker": "", "refsvideo": "ignition.doc",
		"nowhere": "",
	} {
		if got := s.KindAt(filepath.Join(root, dir)); got != want {
			t.Errorf("KindAt(%s) = %q, want %q", dir, got, want)
		}
	}
}
