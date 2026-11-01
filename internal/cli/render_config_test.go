package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// [render] encoder reaches retake-edit as RETAKE_ENCODER; without it retake-edit picks (auto).
func TestEditorPassesTheEncoder(t *testing.T) {
	dir := t.TempDir()
	vars := map[string]string{"XDG_CONFIG_HOME": dir, "HOME": dir}
	env := Env{Getenv: func(k string) string { return vars[k] }, Stderr: io.Discard}
	if e := editorOf(env); len(e.Env) != 0 {
		t.Fatalf("no config: env %v", e.Env)
	}
	if err := os.MkdirAll(filepath.Join(dir, "retake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "retake", "config.toml"), []byte("[render]\nencoder = \"x264\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e := editorOf(env); !slices.Equal(e.Env, []string{"RETAKE_ENCODER=x264"}) {
		t.Fatalf("env %v", e.Env)
	}
}
