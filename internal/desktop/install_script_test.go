package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// install.sh against a release in a folder (INSTALL_BASE_URL=file://…): a bundle is unpacked into a
// versioned folder with its commands linked and a menu entry, a plain archive is copied, and a bad checksum installs
// nothing (docs/releasing.md, "The installer").

// release writes retake_<version>_<os>_<arch>.tar.gz with files, and checksums.txt, into a new folder.
func release(t *testing.T, version string, files map[string]string, sum func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	osName := map[string]string{"linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	name := fmt.Sprintf("retake_%s_%s_%s.tar.gz", version, osName, runtime.GOARCH)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, body := range files {
		mode := int64(0o644)
		if !strings.Contains(n, ".") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(buf.Bytes())
	s := hex.EncodeToString(h[:])
	if sum != nil {
		s = sum(s)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(s+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runInstall(t *testing.T, home, base, version string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script, "--version", version}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "INSTALL_BASE_URL=file://" + base}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallScript(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the installer is for Linux and macOS")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	bundle := map[string]string{
		"retake":             "#!/bin/sh\necho retake 9.9.9\n",
		"retake-edit":        "#!/bin/sh\n",
		"herdr":              "not run",
		"retake-bundle.json": `{"bundle": 1, "tools": {}}`,
		"licenses/herdr.txt": "Apache-2.0",
	}
	home := t.TempDir()
	old := filepath.Join(home, ".local", "share", "retake", "9.9.8")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runInstall(t, home, release(t, "9.9.9", bundle, nil), "9.9.9")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	target := filepath.Join(home, ".local", "share", "retake", "9.9.9")
	for _, bin := range []string{"retake", "retake-edit"} {
		link := filepath.Join(home, ".local", "bin", bin)
		if dest, err := os.Readlink(link); err != nil || dest != filepath.Join(target, bin) {
			t.Errorf("%s links to %q (%v)", link, dest, err)
		}
	}
	for _, f := range []string{"herdr", "retake-bundle.json", "licenses/herdr.txt"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("%s isn't installed: %v", f, err)
		}
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the older version's folder is still there")
	}
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile(filepath.Join(home, ".local", "share", "applications", "retake.desktop"))
		if err != nil || !strings.Contains(string(b), "Exec="+filepath.Join(home, ".local", "bin", "retake")+" start --app\n") {
			t.Errorf("menu entry: %v\n%s", err, b)
		}
	}
	if !strings.Contains(out, "start it: run retake") {
		t.Errorf("doesn't say how to start it:\n%s", out)
	}
	// Again: same result, nothing left over.
	if out, err := runInstall(t, home, release(t, "9.9.9", bundle, nil), "9.9.9", "--no-menu"); err != nil {
		t.Fatalf("again: %v\n%s", err, out)
	}

	// A plain archive: the binary is copied.
	plainHome := t.TempDir()
	if out, err := runInstall(t, plainHome, release(t, "1.0.0", map[string]string{"retake": "#!/bin/sh\n"}, nil), "1.0.0"); err != nil {
		t.Fatalf("plain: %v\n%s", err, out)
	}
	if st, err := os.Lstat(filepath.Join(plainHome, ".local", "bin", "retake")); err != nil || !st.Mode().IsRegular() {
		t.Errorf("plain install: %v", err)
	}

	// A checksum that doesn't match installs nothing.
	badHome := t.TempDir()
	bad := release(t, "9.9.9", bundle, func(s string) string { return strings.Repeat("0", len(s)) })
	if out, err := runInstall(t, badHome, bad, "9.9.9"); err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Errorf("bad checksum: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(badHome, ".local")); err == nil {
		t.Error("a bad download installed something")
	}
}
