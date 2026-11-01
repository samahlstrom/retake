package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/theme"
)

func TestThemeInitAndCheck(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "theme")
	if code := w.run("theme", "init"); code != ExitOK {
		t.Fatalf("init: %d %s", code, w.stderr)
	}
	if !strings.Contains(w.stdout.String(), "retake open and retake share use this folder") {
		t.Errorf("stdout = %s", w.stdout)
	}
	for _, f := range []string{theme.CSSFile, theme.TOMLFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	if code := w.run("theme", "init"); code != ExitError || !strings.Contains(w.stderr.String(), "already there") {
		t.Errorf("init again: %d %s", code, w.stderr)
	}
	if code := w.run("theme", "check"); code != ExitOK || !strings.Contains(w.stdout.String(), "theme ok: 0 tokens, 0 parts") {
		t.Errorf("check a fresh theme: %d %s", code, w.stdout)
	}

	// A theme made for another version: a token this one doesn't have is reported, and the exit code says so.
	css := ":root { --accent: #2f6fed; --gone-token: 3px; }\n[data-part~='bar'] { color: var(--muted); }\n"
	if err := os.WriteFile(filepath.Join(dir, theme.CSSFile), []byte(css), 0o600); err != nil {
		t.Fatal(err)
	}
	w.stdout = &buffer{}
	if code := w.run("theme", "check", dir, "--json"); code != ExitError {
		t.Errorf("check --json: %d", code)
	}
	var r theme.Report
	if err := json.Unmarshal([]byte(w.stdout.String()), &r); err != nil {
		t.Fatalf("%v: %s", err, w.stdout)
	}
	if !slices.Equal(r.Tokens, []string{"--accent"}) || !slices.Equal(r.Parts, []string{"bar"}) ||
		len(r.Problems) != 1 || r.Problems[0].Name != "--gone-token" {
		t.Errorf("report = %+v", r)
	}
	w.stdout = &buffer{}
	if code := w.run("theme", "check"); code != ExitError || !strings.Contains(w.stdout.String(), "--gone-token isn't a token") {
		t.Errorf("check: %d %s", code, w.stdout)
	}
	if code := w.run("theme", "check", filepath.Join(w.root, "nowhere")); code != ExitError {
		t.Errorf("check a missing folder: %d", code)
	}
	other := filepath.Join(w.root, "brand")
	w.stdout = &buffer{}
	if code := w.run("theme", "init", other); code != ExitOK || !strings.Contains(w.stdout.String(), "--theme "+other) {
		t.Errorf("init elsewhere: %d %s", code, w.stdout)
	}
	for _, args := range [][]string{{"theme"}, {"theme", "paint"}, {"theme", "check", "a", "b"}, {"theme", "init", "--nope"}} {
		if code := w.run(args...); code != ExitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
	w.env[theme.EnvVar] = "none"
	if code := w.run("theme", "check"); code != ExitUsage || !strings.Contains(w.stderr.String(), "theming is off") {
		t.Errorf("check with theming off: %d", code)
	}
}

// retake open finds the theme in ~/.config/retake/theme with no flag, --theme picks another folder, none turns it
// off, and the page the binary serves (here a --ui-dir page) gets it.
func TestOpenWithTheme(t *testing.T) {
	w := newWorld(t)
	uiDir := filepath.Join(w.root, "ui")
	writeFile(t, filepath.Join(uiDir, "index.html"), "<!doctype html><html><head><title>Review</title></head></html>")
	local := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "theme")
	writeFile(t, filepath.Join(local, theme.CSSFile), ":root{--accent:#123456}")
	writeFile(t, filepath.Join(local, theme.TOMLFile), "name = \"Local Look\"\nmode = \"glow\"\n")
	brand := filepath.Join(w.root, "brand")
	writeFile(t, filepath.Join(brand, theme.CSSFile), ":root{--accent:#654321}")

	get := func(base, path string) (string, http.Header) {
		t.Helper()
		resp, err := http.Get(strings.TrimSuffix(base, "/") + path) //nolint:noctx // the test's own server
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "", resp.Header
		}
		return string(b), resp.Header
	}
	for _, tc := range []struct {
		name   string
		flags  []string
		env    string
		accent string // "" = no theme
		title  string
	}{
		{"the default folder", nil, "", "#123456", "Review · Local Look"},
		{"--theme", []string{"--theme", brand}, "", "#654321", "Review"},
		{"RETAKE_THEME", nil, brand, "#654321", "Review"},
		{"--theme none", []string{"--theme", "none"}, brand, "", "Review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w.env[theme.EnvVar] = tc.env
			w.stdout, w.stderr = &buffer{}, &buffer{}
			args := append([]string{"open", w.video, "--port", "0", "--no-open", "--ui-dir", uiDir}, tc.flags...)
			url, stop, done := w.serve(t, w.environment(nil), args...)
			page, _ := get(url, "/")
			css, hdr := get(url, "/theme/theme.css")
			stop()
			wait(t, done)
			if !strings.Contains(page, "<title>"+tc.title+"</title>") {
				t.Errorf("page = %s", page)
			}
			if tc.accent == "" {
				if strings.Contains(page, "theme/theme.css") || css != "" {
					t.Errorf("theming off, but page = %s, theme.css = %q", page, css)
				}
				return
			}
			if !strings.Contains(page, `<link rel="stylesheet" href="theme/theme.css">`) || !strings.Contains(css, tc.accent) ||
				hdr.Get("Content-Security-Policy") != theme.CSP {
				t.Errorf("page = %s\ntheme.css = %q %v", page, css, hdr)
			}
			if !strings.Contains(w.stdout.String(), "retake: theme ") {
				t.Errorf("no theme line at startup: %s", w.stdout)
			}
		})
	}
	// A theme.toml problem is a warning at startup; the server still runs.
	w.env[theme.EnvVar] = ""
	w.stderr = &buffer{}
	_, stop, done := w.serve(t, w.environment(nil), "open", w.video, "--port", "0", "--no-open", "--ui-dir", uiDir)
	stop()
	wait(t, done)
	if !strings.Contains(w.stderr.String(), `retake: theme: theme.toml: mode "glow" isn't dark, light or auto`) {
		t.Errorf("stderr = %s", w.stderr)
	}
	// A folder named with --theme that isn't there: a warning, the built-in look.
	w.stderr = &buffer{}
	_, stop, done = w.serve(t, w.environment(nil), "open", w.video, "--port", "0", "--no-open", "--ui-dir", uiDir,
		"--theme", filepath.Join(w.root, "typo"))
	stop()
	wait(t, done)
	if !strings.Contains(w.stderr.String(), "retake: theme: no folder at") {
		t.Errorf("stderr = %s", w.stderr)
	}
}

func TestParseOpenTheme(t *testing.T) {
	a, err := parseOpen([]string{"a.mp4", "--theme", "~/look"})
	if err != nil || a.theme != "~/look" {
		t.Errorf("parseOpen = %+v, %v", a, err)
	}
	if _, err := parseOpen([]string{"a.mp4", "--theme", " "}); err == nil {
		t.Error("an empty --theme was accepted")
	}
}
