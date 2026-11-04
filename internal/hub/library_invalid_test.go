package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryInvalidManifestReportsErrorWithoutOverwriting(t *testing.T) {
	for _, body := range []string{"{broken", "{}"} {
		t.Run(body, func(t *testing.T) {
			x := newFT(t, nil)
			path := x.f.libraryPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			restartLibraryFT(t, x)
			for _, route := range []string{"/api/library", "/api/home"} {
				if r := x.get(route); r.code < 400 {
					t.Errorf("invalid manifest looked like a deliberate empty library at %s: %d %s", route, r.code, r.body)
				}
			}
			if r := replaceLibrary(t, x, 0, []map[string]any{}); r.code < 400 {
				t.Errorf("silently overwrote invalid manifest: %d %s", r.code, r.body)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != body {
				t.Fatalf("manifest was changed: %q %v", got, err)
			}
		})
	}
}
