package ext

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/internal/theme"
)

// uiExt is the hello extension on disk with more ui/ files, and the same as an embedded (in-memory) one.
func uiExts(t *testing.T) (disk, embedded *Ext) {
	t.Helper()
	root := t.TempDir()
	dir := copyHello(t, root, "hello")
	files := map[string]string{
		"ui/data.json": `{"a":1}`, "ui/mod.mjs": "export const a = 1;", "ui/x.wasm": "\x00asm", "ui/icon.svg": "<svg/>",
		"ui/font.woff2": "font", "ui/pic.png": "png", "ui/page.html": "<script>alert(1)</script>",
		"ui/.secret.js": "hidden", "ui/notes.txt": "text", "ui/sub/deep.js": "export {}", "secret.js": "outside ui",
		"bin/tool.js": "not ui",
	}
	writeTree(t, dir, files)
	writeTree(t, root, map[string]string{"outside.js": "outside the folder"})
	if err := os.Symlink(filepath.Join(root, "outside.js"), filepath.Join(dir, "ui", "out.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hello.js", filepath.Join(dir, "ui", "alias.js")); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(dir, "ui", "big.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(MaxUIFile + 1); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	disk = LoadDir(dir)
	mem := fstest.MapFS{}
	_ = fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && p != "ui/big.js" {
			b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
			mem[p] = &fstest.MapFile{Data: b}
		}
		return nil
	})
	embedded = Load(mem, "", WhereBundled, "hello")
	return disk, embedded
}

func TestServeUI(t *testing.T) {
	disk, embedded := uiExts(t)
	for name, e := range map[string]*Ext{"disk": disk, "embedded": embedded} {
		t.Run(name, func(t *testing.T) {
			s, warnings := Resolve([]*Ext{e}, []string{"hello"}, nil)
			if len(warnings) != 0 {
				t.Fatal(warnings)
			}
			get := func(method, id, rel string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				s.ServeUI(w, httptest.NewRequest(method, "/ext/"+id+"/"+rel, nil), id, rel)
				return w
			}
			for rel, ct := range map[string]string{
				"ui/hello.js": "text/javascript; charset=utf-8", "ui/mod.mjs": "text/javascript; charset=utf-8",
				"ui/hello.css": "text/css; charset=utf-8", "ui/data.json": "application/json", "ui/x.wasm": "application/wasm",
				"ui/icon.svg": "image/svg+xml", "ui/font.woff2": "font/woff2", "ui/pic.png": "image/png",
				"ui/sub/deep.js": "text/javascript; charset=utf-8",
			} {
				w := get(http.MethodGet, "hello", rel)
				h := w.Header()
				if w.Code != http.StatusOK || h.Get("Content-Type") != ct || h.Get("X-Content-Type-Options") != "nosniff" ||
					h.Get("Cache-Control") != "no-cache" {
					t.Errorf("%s: %d %v", rel, w.Code, h)
				}
			}
			if w := get(http.MethodGet, "hello", "ui/hello.js"); !strings.Contains(w.Body.String(), "export function page") {
				t.Errorf("body %q", w.Body)
			}
			if w := get(http.MethodGet, "hello", "ui/icon.svg"); !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Error("svg without its CSP")
			}
			if w := get(http.MethodHead, "hello", "ui/hello.css"); w.Code != http.StatusOK {
				t.Errorf("HEAD %d", w.Code)
			}
			for _, rel := range []string{
				"ui/page.html", "ui/notes.txt", "ui/.secret.js", "secret.js", "extension.toml", "bin/tool.js",
				"ui/../secret.js", "ui//hello.js", "./ui/hello.js", "ui/gone.js", "ui/sub", "ui/big.js", "ui/out.js", "",
			} {
				w := get(http.MethodGet, "hello", rel)
				if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json" {
					t.Errorf("%q: %d %s", rel, w.Code, w.Header().Get("Content-Type"))
				}
				var body map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != "not found" {
					t.Errorf("%q: body %s", rel, w.Body)
				}
			}
			if w := get(http.MethodGet, "nope", "ui/hello.js"); w.Code != http.StatusNotFound {
				t.Errorf("unknown id: %d", w.Code)
			}
			if w := get(http.MethodPost, "hello", "ui/hello.js"); w.Code != http.StatusMethodNotAllowed {
				t.Errorf("POST: %d", w.Code)
			}
		})
	}
	// A symlink inside the folder is served as its target.
	s, _ := Resolve([]*Ext{disk}, []string{"hello"}, nil)
	w := httptest.NewRecorder()
	s.ServeUI(w, httptest.NewRequest(http.MethodGet, "/ext/hello/ui/alias.js", nil), "hello", "ui/alias.js")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "export function page") {
		t.Errorf("alias: %d", w.Code)
	}
	var nilSet *Set
	w = httptest.NewRecorder()
	nilSet.ServeUI(w, httptest.NewRequest(http.MethodGet, "/ext/hello/ui/hello.js", nil), "hello", "ui/hello.js")
	if w.Code != http.StatusNotFound {
		t.Errorf("nil set: %d", w.Code)
	}
}

func TestStylesCSS(t *testing.T) {
	disk, _ := uiExts(t)
	disk.Version = "0.1.0+build.1"
	s, _ := Resolve([]*Ext{disk, plainExt("bare", WhereUser)}, []string{"bare"}, nil)
	want := "@layer retake, ext;\n" + `@import url("hello/ui/hello.css?v=0.1.0%2Bbuild.1") layer(ext.hello);` + "\n"
	if got := string(s.StylesCSS()); got != want {
		t.Errorf("styles.css:\n%s\nwant\n%s", got, want)
	}
}

func TestAPIList(t *testing.T) {
	fsys := manifestFS(topKeys, fullTables)
	e := Load(fsys, "", WhereBundled, "x")
	s, _ := Resolve([]*Ext{e, plainExt("bare", WhereUser)}, []string{"x", "bare"}, nil)
	b, err := json.Marshal(s.APIList(func(id string) bool { return id == "x" }))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		API        int              `json:"api"`
		Extensions []map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.API != 1 || len(got.Extensions) != 2 {
		t.Fatalf("%s", b)
	}
	bare, x := got.Extensions[0], got.Extensions[1]
	if bare["id"] != "bare" || bare["machine"] != false || bare["nav"] != nil || bare["files"] != nil || bare["server"] != nil ||
		len(bare["kinds"].([]any)) != 0 || len(bare["slots"].([]any)) != 0 || bare["base"] != "ext/bare/" {
		t.Errorf("bare: %v", bare)
	}
	if x["base"] != "ext/x/" || x["module"] != "ui/x.js" || x["style"] != "ui/x.css" || x["machine"] != true {
		t.Errorf("x: %v", x)
	}
	kind := x["kinds"].([]any)[0].(map[string]any)
	if kind["id"] != "x.doc" || kind["kind"] != "doc" || kind["folder"] != "v0" ||
		fmt.Sprint(kind["marker"]) != "[idea.md]" ||
		kind["label"] != "Script" || kind["status"] != "Idea" || kind["record"] != true {
		t.Errorf("kind: %v", kind)
	}
	slots := x["slots"].([]any)
	if s0 := slots[0].(map[string]any); s0["slot"] != "page" || s0["kind"] != "" || s0["when"] != "always" {
		t.Errorf("page slot: %v", s0)
	}
	if s1 := slots[1].(map[string]any); s1["slot"] != "version" || s1["kind"] != "x.doc" {
		t.Errorf("version slot: %v", s1)
	}
	if s2 := slots[2].(map[string]any); s2["title"] != "X" || s2["when"] != "has-kind" {
		t.Errorf("tab slot: %v", s2)
	}
	nav := x["nav"].(map[string]any)
	if nav["title"] != "X" || nav["icon"] != "ui/icon.svg" || nav["list"] != "kind-only" ||
		nav["action"].(map[string]any)["route"] != "new" {
		t.Errorf("nav: %v", nav)
	}
	if f := x["files"].(map[string]any); f["read"] != "watch" || f["write"] != "record" || len(f["owns"].([]any)) != 2 {
		t.Errorf("files: %v", f)
	}
	if srv := x["server"].(map[string]any); srv["runs_on"] != "machine" || srv["needs"] != "watch" || srv["command"] != nil {
		t.Errorf("server: %v", srv)
	}
	if strings.Contains(string(b), "agent/task.md") || strings.Contains(string(b), "bin/tool") {
		t.Errorf("the page got agent or server details: %s", b)
	}
}

func TestPage(t *testing.T) {
	page := fstest.MapFS{
		"index.html":  {Data: []byte("<!doctype html><html><head><title>Retake</title></HEAD><body></body></html>")},
		"home.html":   {Data: []byte("<html><head></head><body>home</body></html>")},
		"other.html":  {Data: []byte("<html><head></head></html>")},
		"nohead.html": {Data: []byte("<p>")},
		"app.js":      {Data: []byte("console.log(1)")},
	}
	if Page(nil) != nil {
		t.Error("Page(nil)")
	}
	p := Page(page)
	for name, want := range map[string]string{
		"index.html": "<!doctype html><html><head><title>Retake</title>" + HeadTags + "</HEAD><body></body></html>",
		"home.html":  "<html><head>" + HeadTags + "</head><body>home</body></html>",
		"other.html": "<html><head></head></html>",
		"app.js":     "console.log(1)",
	} {
		b, err := fs.ReadFile(p, name)
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", name, b, err)
		}
	}
	if string(Inject([]byte("<p>"))) != "<p>" {
		t.Error("Inject without </head>")
	}
	f, err := p.Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	b, _ := io.ReadAll(f)
	if st.Size() != int64(len(b)) || st.Name() != "index.html" || st.IsDir() {
		t.Errorf("stat %v %d", st.Size(), len(b))
	}
	_ = f.Close()
	if _, err := p.Open("gone.html"); err == nil {
		t.Error("opened a missing file")
	}
	// Inside theme.Page, the theme's stylesheet comes after ext/styles.css, so the theme wins.
	themeDir := t.TempDir()
	writeTree(t, themeDir, map[string]string{theme.CSSFile: ":root{}"})
	b, err = fs.ReadFile(theme.Page(Page(page), themeDir), "home.html")
	if err != nil {
		t.Fatal(err)
	}
	ext, th := strings.Index(string(b), StylesFile), strings.Index(string(b), "theme/theme.css")
	if ext < 0 || th < 0 || ext > th {
		t.Errorf("order: %s", b)
	}
}
