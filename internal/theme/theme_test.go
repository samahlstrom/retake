package theme

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/web"
)

// write makes the files of a theme folder (name → contents) and returns the folder.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func kinds(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Kind+":"+p.Name)
	}
	return out
}

func TestDir(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, tc := range []struct{ flag, env, def, want string }{
		{"", "", "/home/a/.config/retake/theme", "/home/a/.config/retake/theme"},
		{"", "/srv/look", "/def", "/srv/look"},
		{"./mine", "/srv/look", "/def", "./mine"},
		{"none", "/srv/look", "/def", ""},
		{"", "NONE", "/def", ""},
		{"", "  ", "/def", "/def"},
	} {
		if got := Dir(tc.flag, env(tc.env), tc.def); got != tc.want {
			t.Errorf("Dir(%q, %q, %q) = %q, want %q", tc.flag, tc.env, tc.def, got, tc.want)
		}
	}
	getenv := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if d, _ := LocalDir(getenv(map[string]string{"XDG_CONFIG_HOME": "/x/conf", "HOME": "/h"})); d != "/x/conf/retake/theme" {
		t.Errorf("LocalDir with XDG = %q", d)
	}
	if d, _ := LocalDir(getenv(map[string]string{"XDG_CONFIG_HOME": "relative", "HOME": "/h"})); d != "/h/.config/retake/theme" {
		t.Errorf("LocalDir without an absolute XDG = %q", d)
	}
	if d := HostedDir("/data"); d != "/data/theme" {
		t.Errorf("HostedDir = %q", d)
	}
}

func TestLoad(t *testing.T) {
	if Load("") != nil || Load(filepath.Join(t.TempDir(), "missing")) != nil {
		t.Fatal("no folder must be no theme")
	}
	t.Run("everything set", func(t *testing.T) {
		dir := write(t, map[string]string{
			CSSFile:        ":root { --accent: #2f6fed; }",
			TOMLFile:       "name = \" Acme Review \"\nlogo = \"img/logo.svg\"\nfavicon = \"favicon.ico\"\nmode = \"Light\"\n",
			"img/logo.svg": "<svg/>", "favicon.ico": "ico",
		})
		th := Load(dir)
		want := Settings{Name: "Acme Review", Logo: "img/logo.svg", Favicon: "favicon.ico", Mode: ModeLight}
		if th.Settings != want || !th.CSS || len(th.Problems) != 0 {
			t.Errorf("Load = %+v, problems %v", th.Settings, th.Problems)
		}
	})
	t.Run("an empty folder is the dark look with no name", func(t *testing.T) {
		th := Load(t.TempDir())
		if th == nil || th.CSS || th.Settings != (Settings{Mode: ModeDark}) || len(th.Problems) != 0 {
			t.Errorf("Load(empty) = %+v", th)
		}
	})
	t.Run("what's wrong is a problem, the rest applies", func(t *testing.T) {
		dir := write(t, map[string]string{
			CSSFile: "", "evil.js": "alert(1)", "logo.png": "png", "outside.svg": "",
			TOMLFile: "name = \"Acme\"\nlogo = \"evil.js\"\nfavicon = \"../outside.svg\"\nmode = \"sepia\"\ncolour = 1\n",
		})
		th := Load(dir)
		if th.Settings != (Settings{Name: "Acme", Mode: ModeDark}) || !th.CSS {
			t.Errorf("Settings = %+v", th.Settings)
		}
		if got := kinds(th.Problems); !slices.Equal(got, []string{"toml:colour", "toml:mode", "toml:logo", "toml:favicon"}) {
			t.Errorf("problems = %v", got)
		}
		dir = write(t, map[string]string{TOMLFile: "logo = \"nothere.svg\""})
		if got := kinds(Load(dir).Problems); !slices.Equal(got, []string{"toml:logo"}) {
			t.Errorf("a missing logo: %v", got)
		}
	})
	t.Run("a broken theme.toml is ignored, theme.css still loads", func(t *testing.T) {
		th := Load(write(t, map[string]string{TOMLFile: "name = \"unterminated", CSSFile: "a{}"}))
		if !th.CSS || th.Settings.Name != "" || th.Settings.Mode != ModeDark || len(th.Problems) != 1 {
			t.Errorf("Load = %+v", th)
		}
	})
}

const page = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>Projects · Retake</title>
    <link rel="stylesheet" crossorigin href="./assets/home-1.css">
  </head>
  <body><div id="home"></div></body>
</html>
`

func TestInject(t *testing.T) {
	dir := write(t, map[string]string{
		CSSFile: "a{}", TOMLFile: "name = \"A&B <Review>\"\nlogo = \"my logo.svg\"\nfavicon = \"fav.png\"\nmode = \"auto\"",
		"my logo.svg": "<svg/>", "fav.png": "png",
	})
	out := string(Inject([]byte(page), Load(dir)))
	for _, want := range []string{
		`<html lang="en" data-theme="auto">`,
		`<title>Projects · A&amp;B &lt;Review&gt;</title>`,
		`<meta name="color-scheme" content="light dark">`,
		`<meta name="retake-brand" content="A&amp;B &lt;Review&gt;">`,
		`<meta name="retake-logo" content="theme/my%20logo.svg">`,
		`<link rel="icon" href="theme/fav.png">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	// The theme's stylesheet comes after the built-in one, so it wins on order too.
	builtIn, mine := strings.Index(out, "assets/home-1.css"), strings.Index(out, `<link rel="stylesheet" href="theme/theme.css">`)
	if builtIn < 0 || mine < builtIn || mine > strings.Index(out, "</head>") {
		t.Errorf("theme.css at %d, built-in CSS at %d:\n%s", mine, builtIn, out)
	}
	// A name without "Retake" in the title goes after it.
	review := strings.Replace(page, "Projects · Retake", "Review", 1)
	if out := string(Inject([]byte(review), Load(dir))); !strings.Contains(out, "<title>Review · A&amp;B &lt;Review&gt;</title>") {
		t.Errorf("review title: %s", out)
	}
	// No theme.css, no name: only the mode.
	bare := string(Inject([]byte(page), Load(t.TempDir())))
	if strings.Contains(bare, "theme/theme.css") || strings.Contains(bare, "retake-brand") ||
		!strings.Contains(bare, `data-theme="dark"`) || !strings.Contains(bare, "<title>Projects · Retake</title>") {
		t.Errorf("an empty theme folder: %s", bare)
	}
	if got := Inject([]byte("<p>no head"), Load(dir)); string(got) != "<p>no head" {
		t.Errorf("HTML without </head> changed: %s", got)
	}
	if got := Inject([]byte(page), nil); string(got) != page {
		t.Error("no theme changed the page")
	}
}

func TestPage(t *testing.T) {
	built := fstest.MapFS{
		"index.html":       {Data: []byte(strings.Replace(page, "Projects · Retake", "Review", 1))},
		"home.html":        {Data: []byte(page)},
		"assets/index.css": {Data: []byte("body{}")},
	}
	dir := write(t, map[string]string{
		CSSFile: ":root{--accent:red}", "fonts/Brand.woff2": "w2", "logo.svg": "<svg/>", "big.png": "",
		".secret.css": "x", "evil.js": "alert(1)", "page.html": "<p>", "notes.txt": "hi",
	})
	if err := os.Truncate(filepath.Join(dir, "big.png"), MaxFile+1); err != nil {
		t.Fatal(err)
	}
	outside := write(t, map[string]string{"stolen.css": "secret"})
	if err := os.Symlink(filepath.Join(outside, "stolen.css"), filepath.Join(dir, "link.css")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, same := Page(built, "").(fstest.MapFS); !same || Page(nil, dir) != nil {
		t.Fatal("theming off or no page must change nothing")
	}
	p := Page(built, dir)
	read := func(name string) (string, error) {
		b, err := fs.ReadFile(p, name)
		return string(b), err
	}
	for name, want := range map[string]string{
		"theme/theme.css":         ":root{--accent:red}",
		"theme/fonts/Brand.woff2": "w2",
		"theme/logo.svg":          "<svg/>",
		"assets/index.css":        "body{}",
	} {
		if got, err := read(name); err != nil || got != want {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"theme/evil.js", "theme/page.html", "theme/notes.txt", "theme/.secret.css",
		"theme/link.css", "theme/linked/stolen.css", "theme/../stolen.css", "theme/big.png", "theme/fonts",
		"theme/missing.css", "theme", "theme/"} {
		if _, err := read(name); err == nil {
			t.Errorf("%s was served", name)
		}
	}
	home, err := read("home.html")
	if err != nil || !strings.Contains(home, `<link rel="stylesheet" href="theme/theme.css">`) {
		t.Errorf("home.html = %q, %v", home, err)
	}
	if index, _ := read("index.html"); !strings.Contains(index, `data-theme="dark"`) {
		t.Errorf("index.html = %q", index)
	}
	// The folder is read on every load: removing it gives the page back byte for byte.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if home, _ := read("home.html"); home != page {
		t.Errorf("without its folder the page changed: %q", home)
	}
	// A file opened through the wrapper can be seeked (the servers' static handlers need it).
	f, err := Page(built, write(t, map[string]string{CSSFile: "0123456789"})).Open("theme/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if s, ok := f.(io.Seeker); !ok {
		t.Error("a theme file can't seek")
	} else if _, err := s.Seek(5, io.SeekStart); err != nil {
		t.Error(err)
	}
}

func TestHeaders(t *testing.T) {
	for name, ct := range map[string]string{
		"theme/theme.css": "text/css; charset=utf-8", "theme/f/a.woff2": "font/woff2", "theme/logo.svg": "image/svg+xml",
		"theme/fav.ico": "image/x-icon", "theme/a.JPG": "image/jpeg",
	} {
		h := http.Header{"Content-Type": {"application/octet-stream"}}
		if !Headers(h, name) || h.Get("Content-Type") != ct || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != CSP || h.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %v", name, h)
		}
	}
	if !strings.Contains(CSP, "sandbox") || strings.Contains(CSP, "script-src") {
		t.Errorf("the CSP must forbid scripts: %s", CSP)
	}
	h := http.Header{}
	if Headers(h, "assets/index.js") || len(h) != 0 {
		t.Errorf("a page file got theme headers: %v", h)
	}
}

func TestCheck(t *testing.T) {
	css := `
/* --old-commented: 1px; is ignored */
:root {
  --accent: #2f6fed;            /* a token */
  --brand-blue: #123456;        /* the theme's own: set and read */
  --text: var(--brand-blue);
  --bar-heihgt: 70px;           /* misspelled */
}
[data-part~='bar'] { color: var(--muted); }
[data-part="comment"][data-state='done'] { color: var(--no-such-token); }
[data-part=toast], [data-part~="sidebarr"] { }
`
	dir := write(t, map[string]string{CSSFile: css, TOMLFile: "mode = \"dim\"", "x/tool.js": "", ".git/hook.js": ""})
	r := Check(dir)
	if !slices.Equal(r.Tokens, []string{"--accent", "--text"}) {
		t.Errorf("tokens = %v", r.Tokens)
	}
	if !slices.Equal(r.Parts, []string{"bar", "comment", "toast"}) {
		t.Errorf("parts = %v", r.Parts)
	}
	want := []string{"toml:mode", "unknown-token:--bar-heihgt", "unknown-token:--no-such-token", "unknown-part:sidebarr",
		"file:x/tool.js"}
	if got := kinds(r.Problems); !slices.Equal(got, want) {
		t.Errorf("problems = %v, want %v", got, want)
	}
	if r.API != API || r.Dir != dir {
		t.Errorf("report = %+v", r)
	}
	b, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(b), `"kind":"unknown-token"`) {
		t.Errorf("json = %s, %v", b, err)
	}
	if got := kinds(Check(filepath.Join(dir, "missing")).Problems); !slices.Equal(got, []string{"folder:"}) {
		t.Errorf("a missing folder: %v", got)
	}
	if r := Check(write(t, map[string]string{CSSFile: ":root{--bg:#fff}"})); len(r.Problems) != 0 {
		t.Errorf("a good theme: %v", r.Problems)
	}
}

// A token a major release renamed still works for a major, and check says what to use instead.
func TestCheckDeprecated(t *testing.T) {
	api := strings.Replace(string(web.ThemeAPI), `"tokens": {}`,
		`"tokens": {"--bar": {"use": "--surface", "since": "2.0.0"}}`, 1)
	// A part renamed before the first release (api.json lists agent-status → send-notice), and one more.
	api = strings.Replace(api, `"agent-status": {`, `"comment-card": {"use": "comment", "since": "2.0.0"}, "agent-status": {`, 1)
	spec, err := parseSpec(web.ThemeTokens, []byte(api))
	if err != nil {
		t.Fatal(err)
	}
	r := Report{}
	dir := write(t, map[string]string{CSSFile: ":root{--bar:#111}\n[data-part~=comment-card]{}\n[data-part~=agent-status]{}"})
	r.checkCSS(spec, dir)
	want := []string{"deprecated-token:--bar", "deprecated-part:agent-status", "deprecated-part:comment-card"}
	if got := kinds(r.Problems); !slices.Equal(got, want) {
		t.Errorf("problems = %v, want %v", got, want)
	}
	if !strings.Contains(r.Problems[1].Message, `use "send-notice"`) {
		t.Errorf("message = %s", r.Problems[1].Message)
	}
	if !strings.Contains(r.Problems[0].Message, "use --surface") {
		t.Errorf("message = %s", r.Problems[0].Message)
	}
}

// A new version must never silently ignore someone's look: a token this version removed is reported.
func TestCheckRemovedToken(t *testing.T) {
	spec, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	// A theme made by `retake theme init` on this version, every token uncommented.
	css := regexp.MustCompile(`/\* (--[a-z0-9-]+: [^*]+;) \*/`).ReplaceAllString(StarterCSS(spec), "$1")
	dir := write(t, map[string]string{CSSFile: css})
	if r := Check(dir); len(r.Problems) != 0 || len(r.Tokens) != len(spec.Tokens) {
		t.Fatalf("a full theme of this version: %d tokens, problems %v", len(r.Tokens), r.Problems)
	}
	// The next version drops --link: the same theme now has a problem naming it.
	next := regexp.MustCompile(`(?m)^\s*--link:.*\n`).ReplaceAllString(web.ThemeTokens, "")
	nextSpec, err := parseSpec(next, web.ThemeAPI)
	if err != nil {
		t.Fatal(err)
	}
	r := Report{}
	r.checkCSS(nextSpec, dir)
	if got := kinds(r.Problems); !slices.Equal(got, []string{"unknown-token:--link"}) {
		t.Errorf("after --link is gone: %v", got)
	}
}

func TestCurrent(t *testing.T) {
	s, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if s.API != API {
		t.Errorf("api.json says API %d, the code %d", s.API, API)
	}
	byName := map[string]Token{}
	for _, tk := range s.Tokens {
		byName[tk.Name] = tk
	}
	for name, want := range map[string]Token{
		"--bg":         {Name: "--bg", Default: "#09090a", Group: "surfaces"},
		"--bar-height": {Name: "--bar-height", Default: "64px", Wide: "76px", Group: "sizes and spacing of the key parts"},
		"--font-ui":    {Name: "--font-ui", Default: "Inter, 'Adwaita Sans', system-ui, 'Noto Sans', sans-serif", Group: "type"},
	} {
		if byName[name] != want {
			t.Errorf("%s = %+v, want %+v", name, byName[name], want)
		}
	}
	if s.Parts[0].Name != "stage" || !s.HasPart("video-card") || s.HasPart("nope") || !s.HasToken("--agent") {
		t.Errorf("parts = %v", s.Parts)
	}
}

// spec/theme.md documents every token and part of this version, so the docs can't fall behind the page.
func TestSpecDocumentsEverything(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "theme.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	s, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range s.Tokens {
		if !strings.Contains(doc, "| `"+tk.Name+"` |") {
			t.Errorf("spec/theme.md has no row for the token %s", tk.Name)
		}
	}
	for _, p := range s.Parts {
		if !strings.Contains(doc, "| `"+p.Name+"` |") {
			t.Errorf("spec/theme.md has no row for the part %s", p.Name)
		}
	}
	for _, m := range regexp.MustCompile("(?m)^\\| `(--[a-z0-9-]+)` \\|").FindAllStringSubmatch(doc, -1) {
		if !s.HasToken(m[1]) {
			t.Errorf("spec/theme.md documents %s, which isn't a token", m[1])
		}
	}
}

func TestInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "theme")
	wrote, err := Init(dir, false)
	if err != nil || len(wrote) != 2 {
		t.Fatalf("Init = %v, %v", wrote, err)
	}
	css, _ := os.ReadFile(filepath.Join(dir, CSSFile))
	spec, _ := Current()
	for _, tk := range spec.Tokens {
		if !strings.Contains(string(css), "/* "+tk.Name+": "+tk.Default+"; */") {
			t.Errorf("theme.css lacks %s", tk.Name)
		}
	}
	for _, p := range spec.Parts {
		if !strings.Contains(string(css), "[data-part~='"+p.Name+"']") {
			t.Errorf("theme.css lacks the part %s", p.Name)
		}
	}
	// It changes nothing until a line is uncommented, and check is happy with it.
	if r := Check(dir); len(r.Problems) != 0 || len(r.Tokens) != 0 {
		t.Errorf("a fresh theme: %+v", r)
	}
	if th := Load(dir); th.Settings != (Settings{Mode: ModeDark}) {
		t.Errorf("a fresh theme.toml set %+v", th.Settings)
	}
	if err := os.WriteFile(filepath.Join(dir, CSSFile), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir, false); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Errorf("Init over a theme = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, CSSFile)); string(b) != "mine" {
		t.Error("Init overwrote a theme without --force")
	}
	if _, err := Init(dir, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, CSSFile)); string(b) == "mine" {
		t.Error("--force didn't replace it")
	}
}

func TestWarnings(t *testing.T) {
	if line, ps := Warnings(filepath.Join(t.TempDir(), "x")); line != "" || ps != nil {
		t.Errorf("no folder: %q %v", line, ps)
	}
	dir := write(t, map[string]string{CSSFile: ":root{--nope:1px}", TOMLFile: `name = "Acme"`})
	line, ps := Warnings(dir)
	if !strings.Contains(line, "Acme") || !strings.Contains(line, CSSFile) || len(ps) != 1 {
		t.Errorf("Warnings = %q %v", line, ps)
	}
}

// The example themes (examples/themes) use only this version's tokens and parts, so they can't fall behind.
func TestExamplesCheck(t *testing.T) {
	for _, name := range []string{"light", "brand"} {
		dir := filepath.Join("..", "..", "examples", "themes", name)
		r := Check(dir)
		if len(r.Problems) != 0 || len(r.Tokens) < 20 || len(r.Parts) == 0 {
			t.Errorf("%s: %d tokens, parts %v, problems %v", name, len(r.Tokens), r.Parts, r.Problems)
		}
		th := Load(dir)
		if th.Settings.Name == "" || !th.CSS {
			t.Errorf("%s: %+v", name, th.Settings)
		}
	}
}
