package hub

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/samahlstrom/retake/internal/accounts"
)

// First run: the owner creates their account in the browser with the setup code the server printed when it started
// (spec/server.md, "First run: the owner").

// SetupURL returns the first-run setup link while the server has no users, making the code on the first call. It is
// "" once someone has an account. The code lives only in this process: a restart makes a new one.
func (h *Hub) SetupURL(base string) (string, error) {
	n, err := h.st.Accounts.CountUsers()
	if err != nil || n > 0 {
		return "", err
	}
	h.setupMu.Lock()
	defer h.setupMu.Unlock()
	if h.setupCode == "" {
		code, _, err := accounts.NewSecret(accounts.SetupPrefix)
		if err != nil {
			return "", err
		}
		h.setupCode = code
	}
	return strings.TrimSuffix(base, "/") + "/setup?code=" + h.setupCode, nil
}

// setupOK reports whether code is this process's setup code.
func (h *Hub) setupOK(code string) bool {
	h.setupMu.Lock()
	defer h.setupMu.Unlock()
	return h.setupCode != "" && accounts.WellFormed(accounts.SetupPrefix, code) &&
		subtle.ConstantTimeCompare([]byte(code), []byte(h.setupCode)) == 1
}

const (
	badSetupText = "This setup link isn't valid. Use the link the server printed when it started."
	setUpText    = "This server is already set up."
)

// setupRoute is GET and POST /setup.
func (h *Hub) setupRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, rest []string) {
	get, post := r.Method == http.MethodGet || r.Method == http.MethodHead, r.Method == http.MethodPost
	if len(rest) != 0 || !get && !post {
		h.notFound(w, r)
		return
	}
	// As the sign-in link's page: no-referrer would make the browser post "Origin: null", which the CSRF check refuses.
	w.Header().Set("Referrer-Policy", "same-origin")
	n, err := h.st.Accounts.CountUsers()
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if n > 0 {
		h.setupGone(w, r)
		return
	}
	if h.limited(w, r, ri, h.tokenFails, true) {
		return
	}
	code := r.URL.Query().Get("code")
	if post {
		if err := r.ParseForm(); err == nil {
			code = r.PostFormValue("code")
		}
	}
	if !h.setupOK(code) {
		h.tokenFails.Fail(ri.client, h.opts.Now())
		h.page(w, r, http.StatusUnauthorized, "Set up Retake", "<h1>Set up Retake</h1><p>"+esc(badSetupText)+"</p>")
		return
	}
	if get {
		h.setupForm(w, r, code, "", "", "")
		return
	}
	name, email := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("email")
	if _, err := accounts.NormalizeEmail(email); err != nil {
		h.setupForm(w, r, code, name, email, "That isn't an email address.")
		return
	}
	u, err := h.st.Accounts.CreateOwner(email, name)
	if errors.Is(err, accounts.ErrSetUp) {
		h.setupGone(w, r)
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.setupMu.Lock()
	h.setupCode = "" // used up
	h.setupMu.Unlock()
	h.logf("set up: %s is the owner", u.Email)
	value, sess, err := h.st.Accounts.StartSession(u.ID, r.UserAgent(), ri.client)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.setSessionCookie(w, value, sess.ExpiresAt())
	body := "<h1>You're set up</h1><p>You're signed in as " + esc(u.Email) + ", the owner of this server.</p>"
	if cmd, err := h.connectCommand(u, h.baseURL(ri)); err == nil {
		body += connectHTML(cmd)
	} else {
		h.logf("making a machine token: %v", err)
	}
	body += `<p><a href="/">Go to your projects</a></p>`
	h.page(w, r, http.StatusOK, "Set up Retake", body)
}

// setupGone is /setup once the server has a user: the page is gone.
func (h *Hub) setupGone(w http.ResponseWriter, r *http.Request) {
	h.page(w, r, http.StatusNotFound, "Retake", "<p>"+esc(setUpText)+`</p><p><a href="/signin">Sign in</a></p>`)
}

// setupForm is the owner's form: name and email.
func (h *Hub) setupForm(w http.ResponseWriter, r *http.Request, code, name, email, note string) {
	body := `<h1>Set up Retake</h1><p class="muted">Create the owner account. There are no passwords: you sign in ` +
		`with links.</p>`
	if note != "" {
		body += "<p>" + esc(note) + "</p>"
	}
	body += `<form method="post" action="/setup"><input type="hidden" name="code" value="` + esc(code) + `">` +
		`<p class="field"><label for="name">Name</label><input id="name" name="name" autocomplete="name" value="` +
		esc(name) + `" autofocus></p>` +
		`<p class="field"><label for="email">Email</label><input id="email" type="email" name="email" required ` +
		`autocomplete="email" placeholder="you@example.com" value="` + esc(email) + `"></p>` +
		`<p><button type="submit">Create owner account</button></p></form>`
	h.page(w, r, http.StatusOK, "Set up Retake", body)
}

// connectHTML is the "Connect your computer" part of a page, with the command to copy.
func connectHTML(cmd string) string {
	return `<h2>Connect your computer</h2><p>Run this in the folder that holds your video projects. Your footage ` +
		`stays on your computer: the server only streams what someone is watching.</p><pre><code>` + esc(cmd) +
		`</code></pre><p class="muted">This token is shown only once. Connect your computer makes a new one any time.</p>`
}
