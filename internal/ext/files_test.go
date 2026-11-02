package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// videoFolder is a video's folder with an idea (v0), a cut (v1), footage and .retake/, and a file outside it.
func videoFolder(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "jump")
	outside = filepath.Join(base, "outside.md")
	writeTree(t, base, map[string]string{"outside.md": "outside"})
	writeTree(t, root, map[string]string{
		"v0/idea.md": "# Jump\n", "v0/voice/a.webm": "opus", "v0/.hidden.md": "hidden", "vo/a.wav": "wav",
		"vo/b.mp3": "mp3", "v1/jump_v1.mp4": "video", "v1/notes.md": "v1 notes", ".retake/project.db": "db",
		"footage/raw.mp4": "raw", "readme.md": "readme",
	})
	if err := os.Symlink(outside, filepath.Join(root, "v0", "out.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("idea.md", filepath.Join(root, "v0", "alias.md")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func paths(files []FileInfo) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestFolderList(t *testing.T) {
	root, _ := videoFolder(t)
	f := Folder{Root: root, Owns: []string{"v0/**", "vo/*.wav"}}
	files, err := f.List("")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(files); !slices.Equal(got, []string{"v0/alias.md", "v0/idea.md", "v0/voice/a.webm", "vo/a.wav"}) {
		t.Errorf("List = %v", got)
	}
	if fi := files[1]; fi.Size != 7 || fi.SHA256 != sum("# Jump\n") || fi.Modified == 0 {
		t.Errorf("idea.md: %+v", fi)
	}
	if files, _ := f.List("v0/*.md"); !slices.Equal(paths(files), []string{"v0/alias.md", "v0/idea.md"}) {
		t.Errorf("List(v0/*.md) = %v", paths(files))
	}
	// Owning everything still never reaches .retake/, a cut's folder or a family video file.
	all := Folder{Root: root, Owns: []string{"**"}}
	files, _ = all.List("")
	if got := paths(files); !slices.Equal(got, []string{"footage/raw.mp4", "readme.md", "v0/alias.md", "v0/idea.md",
		"v0/voice/a.webm", "vo/a.wav", "vo/b.mp3"}) {
		t.Errorf("List(**) = %v", got)
	}
	// A v0/ that holds a video version isn't an extension's.
	writeTree(t, root, map[string]string{"v0/jump_v0.mp4": "video"})
	if files, _ := f.List(""); !slices.Equal(paths(files), []string{"vo/a.wav"}) {
		t.Errorf("List with a video v0 = %v", paths(files))
	}
	if _, err := f.Stat("v0/idea.md"); !errors.Is(err, ErrNotOwned) {
		t.Errorf("Stat in a video v0: %v", err)
	}
	if _, err := (Folder{Root: filepath.Join(root, "gone"), Owns: []string{"**"}}).List(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("List of a missing folder: %v", err)
	}
}

func TestFolderOpenAndStat(t *testing.T) {
	root, _ := videoFolder(t)
	f := Folder{Root: root, Owns: []string{"v0/**", "vo/*.wav"}}
	file, info, err := f.Open("v0/idea.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(file)
	_ = file.Close()
	if string(b) != "# Jump\n" || info.SHA256 != sum("# Jump\n") || info.Path != "v0/idea.md" {
		t.Errorf("Open: %q %+v", b, info)
	}
	if info, err := f.Stat("v0/alias.md"); err != nil || info.SHA256 != sum("# Jump\n") {
		t.Errorf("a symlink inside: %+v %v", info, err)
	}
	for p, want := range map[string]error{
		"v0/out.md":          ErrNotFound, // a symlink out of the folder
		"v0/gone.md":         ErrNotFound,
		"v0/voice":           ErrNotFound, // a folder
		"vo/b.mp3":           ErrNotOwned,
		"v1/notes.md":        ErrNotOwned,
		"v0/../v1/notes.md":  ErrNotOwned,
		"v0/.hidden.md":      ErrNotOwned,
		".retake/project.db": ErrNotOwned,
		"/etc/passwd":        ErrNotOwned,
		"v0/clip_v0.mp4":     ErrNotOwned,
	} {
		if _, err := f.Stat(p); !errors.Is(err, want) {
			t.Errorf("Stat(%q) = %v, want %v", p, err, want)
		}
		if _, _, err := f.Open(p); !errors.Is(err, want) {
			t.Errorf("Open(%q) = %v, want %v", p, err, want)
		}
	}
}

func TestFolderWrite(t *testing.T) {
	root, outside := videoFolder(t)
	f := Folder{Root: root, Owns: []string{"v0/**"}}
	info, err := f.Write("v0/script.md", strings.NewReader("## Cold open\n"), "*")
	if err != nil || info.SHA256 != sum("## Cold open\n") || info.Size != 13 {
		t.Fatalf("new: %+v %v", info, err)
	}
	var changed *ChangedError
	if _, err := f.Write("v0/script.md", strings.NewReader("again"), "*"); !errors.As(err, &changed) ||
		changed.SHA256 != info.SHA256 {
		t.Errorf("* over a file: %v", err)
	}
	if _, err := f.Write("v0/script.md", strings.NewReader("stale"), sum("old")); !errors.As(err, &changed) ||
		changed.SHA256 != info.SHA256 || err.Error() != "changed" {
		t.Errorf("stale If-Match: %v", err)
	}
	if _, err := f.Write("v0/new.md", strings.NewReader("x"), sum("old")); !errors.As(err, &changed) || changed.SHA256 != "" {
		t.Errorf("If-Match on a missing file: %v", err)
	}
	info, err = f.Write("v0/script.md", strings.NewReader("## Cold open\nJump.\n"), `"`+info.SHA256+`"`)
	if err != nil || info.SHA256 != sum("## Cold open\nJump.\n") {
		t.Errorf("matching If-Match: %+v %v", info, err)
	}
	if _, err := f.Write("v0/script.md", strings.NewReader("free"), ""); err != nil {
		t.Errorf("no If-Match: %v", err)
	}
	if _, err := f.Write("v0/refs/deep/links.md", strings.NewReader("- a\n"), ""); err != nil {
		t.Errorf("folders made: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "v0", "refs", "deep", "links.md")); string(b) != "- a\n" {
		t.Errorf("links.md = %q", b)
	}
	// Too large: refused, nothing left behind.
	if _, err := f.Write("v0/big.bin", io.LimitReader(zeros{}, MaxWrite+1), ""); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := f.Write("v0/full.bin", io.LimitReader(zeros{}, MaxWrite), ""); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	// Through a symlink out of the folder: refused, and the file outside is unchanged.
	if _, err := f.Write("v0/out.md", strings.NewReader("pwned"), ""); err == nil {
		t.Error("wrote through a symlink out")
	}
	if b, _ := os.ReadFile(outside); string(b) != "outside" {
		t.Errorf("outside = %q", b)
	}
	for _, p := range []string{"v1/notes.md", "v0/../v1/x.md", ".retake/x", "v0/.x", "readme.md", "v0/jump_v0.mp4"} {
		if _, err := f.Write(p, strings.NewReader("x"), ""); !errors.Is(err, ErrNotOwned) {
			t.Errorf("Write(%q) = %v", p, err)
		}
	}
	if _, err := f.Write("v0/voice", strings.NewReader("x"), ""); err == nil {
		t.Error("wrote over a folder")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "v0"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestFolderRemove(t *testing.T) {
	root, outside := videoFolder(t)
	f := Folder{Root: root, Owns: []string{"v0/**"}}
	var changed *ChangedError
	if err := f.Remove("v0/idea.md", sum("other")); !errors.As(err, &changed) || changed.SHA256 != sum("# Jump\n") {
		t.Errorf("stale: %v", err)
	}
	if err := f.Remove("v0/idea.md", sum("# Jump\n")); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := f.Remove("v0/idea.md", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove again: %v", err)
	}
	if err := f.Remove("v1/notes.md", ""); !errors.Is(err, ErrNotOwned) {
		t.Errorf("remove v1: %v", err)
	}
	if err := f.Remove("v0/out.md", "*"); err == nil {
		t.Error("removed a symlink out with *")
	}
	if b, _ := os.ReadFile(outside); string(b) != "outside" {
		t.Errorf("outside = %q", b)
	}
}
