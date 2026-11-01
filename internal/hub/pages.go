package hub

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/theme"
)

// The server's own pages: the projects page, the sign-in pages and short messages. They are plain HTML in the
// review page's colors, with no script.

func esc(s string) string { return html.EscapeString(s) }

const backHome = `<p><a href="/">Your projects</a></p>`

const pageStyle = `body{margin:0;min-height:100vh;display:grid;place-items:center;padding:0 16px;background:#0b0b0c;` +
	`color:#ddd;font:16px/1.5 system-ui,sans-serif}main{max-width:32rem;width:100%}h1{font-size:1.25rem;font-weight:600}` +
	`a{color:#9cf}ul{list-style:none;padding:0}li{padding:.4rem 0;border-bottom:1px solid #222;display:flex;` +
	`justify-content:space-between;gap:1rem}.role{color:#888}button,input{font:inherit;padding:.5rem .9rem;` +
	`border-radius:6px;border:1px solid #333;background:#1a1a1c;color:#eee}button{cursor:pointer}` +
	`form.inline{display:inline}.muted{color:#888}h2{font-size:1rem;font-weight:600;margin-top:1.75rem}` +
	`.field label{display:block;margin-bottom:.25rem;color:#aaa}.field input{width:100%;box-sizing:border-box}` +
	`pre{white-space:pre-wrap;word-break:break-all;background:#1a1a1c;border:1px solid #333;border-radius:6px;` +
	`padding:.75rem;user-select:all}code{font:14px/1.5 ui-monospace,monospace;font-variant-ligatures:none}`

// page sends one short HTML page. body is HTML; callers escape what they put in it.
func (h *Hub) page(w http.ResponseWriter, r *http.Request, code int, title, body string) {
	writePage(w, r, code, title, body)
}

// writePage sends one short HTML page in the review page's colors, with no script.
func writePage(w http.ResponseWriter, r *http.Request, code int, title, body string) {
	doc := `<!doctype html><html lang="en"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>` + esc(title) + `</title>` +
		`<style>` + pageStyle + `</style><main>` + body + `</main>`
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Length", strconv.Itoa(len(doc)))
	hd.Set("Cache-Control", "no-store")
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; "+
		"frame-ancestors 'none'")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(doc)) //nolint:gosec // callers escape what they put in (esc)
	}
}

// signinPage is GET /signin: with mail set up, a form that mails a link; without, where links come from.
func (h *Hub) signinPage(w http.ResponseWriter, r *http.Request, note string) {
	body := "<h1>Sign in to Retake</h1>"
	if note != "" {
		body += "<p>" + esc(note) + "</p>"
	}
	if n, err := h.st.Accounts.CountUsers(); err == nil && n == 0 {
		body += `<p>This server has no accounts yet. Open the setup link that <code>retake serve</code> printed when ` +
			`it started (with Docker: <code>docker compose logs retake</code>).</p>`
		h.page(w, r, http.StatusOK, "Sign in", body)
		return
	}
	if h.opts.Mailer != nil {
		body += `<form method="post" action="/signin"><p><input type="email" name="email" required ` +
			`placeholder="you@example.com" autocomplete="email"> <button type="submit">Email me a link</button></p></form>`
	} else {
		body += `<p class="muted">Retake has no passwords. Ask your admin for a sign-in link.</p>`
	}
	h.page(w, r, http.StatusOK, "Sign in", body)
}

// home is GET /: the projects of the signed-in person, or the sign-in page.
func (h *Hub) home(w http.ResponseWriter, r *http.Request, _ reqInfo) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.notFound(w, r)
		return
	}
	s, err := h.session(w, r)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if s == nil {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if page := h.uiFile(HomePage); page != nil { // the home screen, as retake open <folder> has it
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(page)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(page)
		}
		return
	}
	ps, err := h.st.Accounts.ProjectsOf(s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	var b strings.Builder
	b.WriteString("<h1>Your projects</h1>")
	if len(ps) == 0 {
		b.WriteString(`<p class="muted">No projects yet.`)
		if s.user.Admin {
			b.WriteString(" Run <code>retake connect</code> on the computer that holds your projects, or add one with " +
				"<code>retake project add &lt;name&gt;</code>.")
		} else {
			b.WriteString(" Ask a project's owner to add you.")
		}
		b.WriteString("</p>")
	} else {
		b.WriteString("<ul>")
		for _, p := range ps {
			title := p.Title
			if title == "" {
				title = p.Name
			}
			b.WriteString(`<li><a href="/p/` + esc(p.Name) + `/">` + esc(title) + `</a><span class="role">` +
				esc(string(p.Role)) + "</span></li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`<p class="muted">Signed in as ` + esc(s.user.Email) + `. <form class="inline" method="post" ` +
		`action="/signout"><button type="submit">Sign out</button></form></p>`)
	h.page(w, r, http.StatusOK, "Retake", b.String())
}

// uiFile returns one of the page's files, or nil without a built page.
func (h *Hub) uiFile(name string) []byte {
	if h.opts.UI == nil {
		return nil
	}
	b, err := fs.ReadFile(h.opts.UI, name)
	if err != nil {
		return nil
	}
	return b
}

// static is GET /assets/... and /theme/...: the page's own files and the host's theme (spec/theme.md), for the home
// screen. They are the same for everyone, so they need no session. Hidden files and folders are never served.
func (h *Hub) static(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.notFound(w, r)
		return
	}
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || strings.ContainsAny(p, "\\:\x00") {
			h.notFound(w, r)
			return
		}
	}
	name := path.Join(parts...)
	b := h.uiFile(name)
	if b == nil || !fs.ValidPath(name) {
		h.notFound(w, r)
		return
	}
	var mod time.Time
	if st, err := fs.Stat(h.opts.UI, name); err == nil {
		mod = st.ModTime()
	}
	w.Header().Set("Content-Type", server.MediaType(name))
	w.Header().Set("Cache-Control", "no-cache")
	theme.Headers(w.Header(), name) // a theme file: its own type, nosniff and a CSP
	http.ServeContent(w, r, name, mod, bytes.NewReader(b))
}

// serveIfFound serves a request with srv unless srv answers 404, and reports whether it did. Only for small answers
// (a poster), since the answer is held in memory.
func serveIfFound(w http.ResponseWriter, r *http.Request, srv http.Handler, rest string) bool {
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r2)
	if rec.Code == http.StatusNotFound {
		return false
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
	return true
}
