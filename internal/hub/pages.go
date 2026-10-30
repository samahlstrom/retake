package hub

import (
	"html"
	"net/http"
	"strconv"
	"strings"
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
	`form.inline{display:inline}.muted{color:#888}`

// page sends one short HTML page. body is HTML; callers escape what they put in it.
func (h *Hub) page(w http.ResponseWriter, r *http.Request, code int, title, body string) {
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
			b.WriteString(" Add one with <code>retake project add &lt;name&gt;</code>.")
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
