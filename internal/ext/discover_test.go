package ext

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// manifestOf is a minimal valid manifest with the given id.
func manifestOf(id string) string {
	return "api = 1\nid = \"" + id + "\"\nname = \"" + id + "\"\nversion = \"1.0.0\"\n"
}

// The spec's order: --ext folders, the project's, the user's, the bundled ones; the first of an id wins and the
// others are its Shadowed.
func TestDiscoverOrderAndShadowing(t *testing.T) {
	root := t.TempDir()
	flagOne := copyHello(t, root, "dev-hello")
	flagMany := filepath.Join(root, "many")
	writeTree(t, flagMany, map[string]string{
		"b/" + ManifestFile: manifestOf("bee"), "a/" + ManifestFile: manifestOf("ant"), "notes/readme.md": "not one",
		".hidden/" + ManifestFile: manifestOf("hidden"),
	})
	opened := filepath.Join(root, "videos")
	writeTree(t, ProjectDir(opened), map[string]string{
		"hello/" + ManifestFile: manifestOf("hello"), "proj/" + ManifestFile: manifestOf("proj"),
	})
	user := filepath.Join(root, "config", "retake", "extensions")
	writeTree(t, user, map[string]string{
		"scripts/" + ManifestFile: manifestOf("scripts"), "proj/" + ManifestFile: manifestOf("proj"),
	})
	bundled := fstest.MapFS{
		"scripts/" + ManifestFile: {Data: []byte(manifestOf("scripts"))},
		"bundled/" + ManifestFile: {Data: []byte(manifestOf("bundled"))},
		"src/main.ts":             {Data: []byte("not an extension")},
	}
	found := Discover(Sources{Flags: []string{flagOne, flagMany}, Project: opened, User: user, Bundled: bundled})
	var got []string
	for _, e := range found {
		got = append(got, e.ID+"@"+e.Where)
	}
	want := []string{"hello@flag", "ant@flag", "bee@flag", "proj@project", "scripts@user", "bundled@bundled"}
	if !slices.Equal(got, want) {
		t.Fatalf("found %v, want %v", got, want)
	}
	byID := map[string]*Ext{}
	for _, e := range found {
		byID[e.ID] = e
	}
	if s := byID["hello"].Shadowed; !slices.Equal(s, []string{filepath.Join(ProjectDir(opened), "hello")}) {
		t.Errorf("hello shadows %v", s)
	}
	if s := byID["proj"].Shadowed; !slices.Equal(s, []string{filepath.Join(user, "proj")}) {
		t.Errorf("proj shadows %v", s)
	}
	if s := byID["scripts"].Shadowed; !slices.Equal(s, []string{"built in"}) {
		t.Errorf("scripts shadows %v", s)
	}
	if e := byID["hello"]; e.Dir != flagOne || e.Name != "Hello" || !e.OK() {
		t.Errorf("hello: %+v", e)
	}
	if e := byID["bundled"]; e.Dir != "" || e.Display() != "built in" {
		t.Errorf("bundled: dir %q", e.Dir)
	}
	// The user's folder as a server's data dir.
	found = Discover(Sources{User: user, UserWhere: WhereData})
	if len(found) != 2 || found[0].Where != WhereData {
		t.Errorf("data: %v", found)
	}
}

func TestDiscoverMissingFlag(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	found := Discover(Sources{Flags: []string{filepath.Join(root, "nowhere"), empty}})
	if len(found) != 2 || found[0].OK() || found[1].OK() {
		t.Fatalf("%v", found)
	}
	if !strings.Contains(found[0].Problems[0].Message, "no folder at") ||
		!strings.Contains(found[1].Problems[0].Message, "no extension.toml in") {
		t.Errorf("%v %v", found[0].Problems, found[1].Problems)
	}
}

func TestFlagDirsAndFolders(t *testing.T) {
	env := map[string]string{EnvPath: "/a" + string(os.PathListSeparator) + " " + string(os.PathListSeparator) + "/b"}
	if got := FlagDirs([]string{"/x"}, func(k string) string { return env[k] }); !slices.Equal(got, []string{"/x", "/a", "/b"}) {
		t.Errorf("FlagDirs = %v", got)
	}
	if got := ProjectDir("/v"); got != filepath.Join("/v", ".retake", "extensions") {
		t.Errorf("ProjectDir = %q", got)
	}
	if got := ProjectDir(filepath.Join("/v", ".retake", "extensions")); got != filepath.Join("/v", ".retake", "extensions") {
		t.Errorf("ProjectDir of the folder itself = %q", got)
	}
	if got := DataDir("/data"); got != filepath.Join("/data", "extensions") {
		t.Errorf("DataDir = %q", got)
	}
	// An absolute path on this system: /cfg, or C:\cfg on Windows.
	abs := func(p string) string { return filepath.Join(filepath.VolumeName(os.TempDir()), filepath.FromSlash(p)) }
	xdg := map[string]string{"XDG_CONFIG_HOME": abs("/cfg"), "HOME": abs("/home/u")}
	if got, _ := UserDir(func(k string) string { return xdg[k] }); got != filepath.Join(abs("/cfg"), "retake", "extensions") {
		t.Errorf("UserDir = %q", got)
	}
	home := map[string]string{"XDG_CONFIG_HOME": "relative", "HOME": abs("/home/u")}
	if got, _ := UserDir(func(k string) string { return home[k] }); got != filepath.Join(abs("/home/u"), ".config", "retake", "extensions") {
		t.Errorf("UserDir from HOME = %q", got)
	}
}

func TestHash(t *testing.T) {
	dir := copyHello(t, t.TempDir(), "hello")
	h1 := LoadDir(dir).Hash
	if h1 != LoadDir(dir).Hash {
		t.Fatal("hash isn't stable")
	}
	writeTree(t, dir, map[string]string{".git/HEAD": "ref", ".DS_Store": "x"})
	if LoadDir(dir).Hash != h1 {
		t.Error("hidden files changed the hash")
	}
	writeTree(t, dir, map[string]string{"ui/hello.js": "export function page() {}\n"})
	if LoadDir(dir).Hash == h1 {
		t.Error("an edit didn't change the hash")
	}
	// Moving bytes between files changes it too.
	a := copyHello(t, t.TempDir(), "a")
	writeTree(t, a, map[string]string{"x": "ab", "y": "c"})
	b := copyHello(t, t.TempDir(), "b")
	writeTree(t, b, map[string]string{"x": "a", "y": "bc"})
	if LoadDir(a).Hash == LoadDir(b).Hash {
		t.Error("files that differ hash the same")
	}
}

// DirFS keeps every open inside its folder: a symlink out isn't followed.
func TestDirFS(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "secret.txt")
	writeTree(t, root, map[string]string{"secret.txt": "secret", "ext/ui/in.js": "in"})
	if err := os.Symlink(outside, filepath.Join(root, "ext", "ui", "out.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("in.js", filepath.Join(root, "ext", "ui", "alias.js")); err != nil {
		t.Fatal(err)
	}
	fsys := DirFS(filepath.Join(root, "ext"))
	if _, err := fs.ReadFile(fsys, "ui/out.js"); err == nil {
		t.Error("read through a symlink out of the folder")
	}
	if b, err := fs.ReadFile(fsys, "ui/alias.js"); err != nil || string(b) != "in" {
		t.Errorf("symlink inside: %q %v", b, err)
	}
	if _, err := fs.ReadFile(fsys, "../secret.txt"); err == nil {
		t.Error("read ..")
	}
	if err := fstest.TestFS(DirFS(copyHello(t, root, "clean")), "extension.toml", "ui/hello.js"); err != nil {
		t.Error(err)
	}
}
