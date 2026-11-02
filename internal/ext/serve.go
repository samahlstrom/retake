package ext

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Prefix is where extensions' files are served, relative to the page: ext/<id>/<path>, and ext/styles.css.
const Prefix = "ext/"

// StylesFile is the stylesheet that layers every extension's style (StylesCSS), relative to the page.
const StylesFile = Prefix + "styles.css"

// svgCSP is the Content-Security-Policy of an SVG served from ui/: opened on its own, it can't run a script.
const svgCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// ServeUI answers GET ext/<id>/<rel> (spec/extensions.md, "Loading and mounting"): a file under the extension's ui/
// of an allowed type, at most MaxUIFile bytes, no hidden segment, nothing outside its folder; nosniff and no-cache.
// Anything else is a JSON 404.
func (s *Set) ServeUI(w http.ResponseWriter, r *http.Request, id, rel string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	e := s.Get(id)
	ct := contentType(rel)
	if e == nil || e.FS == nil || !ValidPath(rel) || !strings.HasPrefix(rel, "ui/") || ct == "" {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	f, err := e.FS.Open(rel)
	if err != nil {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > MaxUIFile {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(io.LimitReader(f, MaxUIFile+1))
		if err != nil || len(b) > MaxUIFile {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		rs = bytes.NewReader(b)
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	if strings.HasSuffix(strings.ToLower(rel), ".svg") {
		h.Set("Content-Security-Policy", svgCSP)
	}
	http.ServeContent(w, r, "", st.ModTime(), rs)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// StylesCSS is ext/styles.css: the layer order, then each extension's style in its own layer, so its rules beat the
// built-in ones and the user's theme beats both (spec/extensions.md, "Loading and mounting").
func (s *Set) StylesCSS() []byte {
	var b strings.Builder
	b.WriteString("@layer retake, ext;\n")
	for _, e := range s.List() {
		if e.UI.Style == "" {
			continue
		}
		b.WriteString(`@import url("` + e.ID + "/" + escapePath(e.UI.Style) + "?v=" + url.QueryEscape(e.Version) +
			`") layer(ext.` + e.ID + ");\n")
	}
	return []byte(b.String())
}

// escapePath escapes each segment of a slash path for a URL.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// APIBody is GET api/ext's body.
type APIBody struct {
	API        int      `json:"api"`
	Extensions []APIExt `json:"extensions"`
}

// APIExt is one extension in GET api/ext: what the page needs, never its commands or agent text.
type APIExt struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description,omitempty"`
	Base        string     `json:"base"` // ext/<id>/
	Module      string     `json:"module"`
	Style       string     `json:"style"`
	Nav         *Nav       `json:"nav"`
	Kinds       []APIKind  `json:"kinds"`
	Slots       []APISlot  `json:"slots"`
	Files       *Files     `json:"files"`
	Server      *APIServer `json:"server"`
	Machine     bool       `json:"machine"` // false when the video's editing computer lacks it
}

// APIKind is a kind in GET api/ext.
type APIKind struct {
	ID     string `json:"id"`   // the full id: ignition.doc
	Kind   string `json:"kind"` // its own id: doc
	Folder string `json:"folder"`
	Marker string `json:"marker"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Record bool   `json:"record"`
}

// APISlot is a slot in GET api/ext.
type APISlot struct {
	Slot  string `json:"slot"`
	Kind  string `json:"kind"` // the version slot's full kind, else ""
	Title string `json:"title"`
	When  string `json:"when"` // always or has-kind
}

// APIServer is the part of [server] the page sees.
type APIServer struct {
	RunsOn string `json:"runs_on"`
	Needs  string `json:"needs"`
}

// APIList is GET api/ext's body (an APIBody) for the extensions in effect. machine says whether the video's editing
// computer has an extension; nil = every one.
func (s *Set) APIList(machine func(id string) bool) any {
	body := APIBody{API: API, Extensions: []APIExt{}}
	for _, e := range s.List() {
		x := APIExt{ID: e.ID, Name: e.Name, Version: e.Version, Description: e.Description, Base: Prefix + e.ID + "/",
			Module: e.UI.Module, Style: e.UI.Style, Nav: e.Nav, Kinds: []APIKind{}, Slots: []APISlot{}, Files: e.Files,
			Machine: machine == nil || machine(e.ID)}
		for _, k := range e.Kinds {
			x.Kinds = append(x.Kinds, APIKind{ID: e.FullKind(k), Kind: k.ID, Folder: k.Folder, Marker: k.Marker,
				Label: k.Label, Status: k.Status, Record: k.Record})
		}
		for _, sl := range e.Slots {
			if _, ok := Slots[sl.Slot]; !ok {
				continue
			}
			a := APISlot{Slot: sl.Slot, Title: sl.Title, When: sl.When}
			if a.When == "" {
				a.When = "always"
			}
			if sl.Slot == "version" && sl.Kind != "" {
				a.Kind = e.ID + "." + sl.Kind
			}
			if a.Title == "" && (sl.Slot == "sidebar-tab" || sl.Slot == "review-panel") {
				a.Title = e.Name
			}
			x.Slots = append(x.Slots, a)
		}
		if e.Server != nil {
			x.Server = &APIServer{RunsOn: e.Server.RunsOn, Needs: e.Server.Needs}
		}
		body.Extensions = append(body.Extensions, x)
	}
	return body
}

// Pages are the page's two HTML entries, which get the extension API's meta and ext/styles.css.
var Pages = []string{"index.html", "home.html"}

// Page wraps the page's files so index.html and home.html carry `<meta name="retake-ext-api" content="1">` and link
// ext/styles.css before </head> (spec/extensions.md, "Versioning"). Wrap it inside theme.Page, so the theme's link
// comes after and wins. nil page: nil.
func Page(page fs.FS) fs.FS {
	if page == nil {
		return nil
	}
	return withExt{page: page}
}

type withExt struct{ page fs.FS }

func (p withExt) Open(name string) (fs.File, error) {
	isPage := false
	for _, n := range Pages {
		isPage = isPage || name == n
	}
	if !isPage {
		return p.page.Open(name)
	}
	f, err := p.page.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return &memFile{Reader: bytes.NewReader(Inject(b)), name: name, mod: st.ModTime()}, nil
}

// HeadTags is what Inject adds before </head>.
const HeadTags = `<meta name="retake-ext-api" content="1"><link rel="stylesheet" href="` + StylesFile + `">`

var headClose = regexp.MustCompile(`(?i)</head\s*>`)

// Inject adds HeadTags before a page's </head>. HTML without one comes back unchanged.
func Inject(page []byte) []byte {
	loc := headClose.FindIndex(page)
	if loc == nil {
		return page
	}
	out := make([]byte, 0, len(page)+len(HeadTags))
	out = append(out, page[:loc[0]]...)
	out = append(out, HeadTags...)
	return append(out, page[loc[0]:]...)
}

// memFile is a page with the additions.
type memFile struct {
	*bytes.Reader
	name string
	mod  time.Time
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct{ f *memFile }

func (i memInfo) Name() string       { return i.f.name }
func (i memInfo) Size() int64        { return i.f.Size() }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return i.f.mod }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
