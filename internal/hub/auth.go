package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// Accounts, sessions, sign-in links and who a request is (spec/server.md, "Accounts", "Sessions", "Who a request
// is").

// Cookie names.
const (
	SessionCookie = "retake_session"
	ShareCookie   = "retake_share" // scoped to /p/<name>/
)

// session is a request's signed-in user, if any.
type session struct {
	sess accounts.Session
	user accounts.User
}

// session returns the request's session: nil when there's no session cookie or it doesn't check out (then the
// cookie is cleared). A rotated session's new value is set on w.
func (h *Hub) session(w http.ResponseWriter, r *http.Request) (*session, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil //nolint:nilnil // no session is not an error
	}
	sess, u, rotated, err := h.st.Accounts.CheckSession(c.Value)
	if errors.Is(err, accounts.ErrNoSession) {
		h.clearCookie(w, SessionCookie, "/")
		return nil, nil //nolint:nilnil // no session is not an error
	}
	if err != nil {
		return nil, err
	}
	if rotated != "" {
		h.setSessionCookie(w, rotated, sess.ExpiresAt())
	}
	return &session{sess: sess, user: u}, nil
}

func (h *Hub) setSessionCookie(w http.ResponseWriter, value string, expires time.Time) {
	age := int(expires.Sub(h.opts.Now()).Seconds())
	//nolint:gosec // Secure unless the admin chose a plain-http public URL off loopback (spec/server.md)
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: value, Path: "/", MaxAge: max(1, age), HttpOnly: true,
		Secure: h.secure, SameSite: http.SameSiteLaxMode})
}

func (h *Hub) clearCookie(w http.ResponseWriter, name, path string) {
	same := http.SameSiteLaxMode
	if name == ShareCookie {
		same = http.SameSiteStrictMode
	}
	//nolint:gosec // as setSessionCookie
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, Secure: h.secure,
		SameSite: same})
}

// limited answers 429 when the client has failed too often. It returns true when it did.
func (h *Hub) limited(w http.ResponseWriter, r *http.Request, ri reqInfo, l *team.Limiter, page bool) bool {
	blocked, wait := l.Blocked(ri.client, h.opts.Now())
	if !blocked {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	if page {
		h.page(w, r, http.StatusTooManyRequests, "Retake", "<p>Too many tries. Wait a minute, then try again.</p>")
	} else {
		h.fail(w, r, http.StatusTooManyRequests, "too many attempts")
	}
	return true
}

// signinRoute is /signin and /signin/<code>.
func (h *Hub) signinRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, rest []string) {
	switch {
	case len(rest) == 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.signinPage(w, r, "")
	case len(rest) == 0 && r.Method == http.MethodPost:
		h.mailLink(w, r, ri)
	case len(rest) == 1 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.linkPage(w, r, ri, rest[0])
	case len(rest) == 1 && r.Method == http.MethodPost:
		h.useLink(w, r, ri, rest[0])
	default:
		h.notFound(w, r)
	}
}

const badLinkText = "This sign-in link has expired or was used. Ask for a new one."

// linkPage is GET /signin/<code>: a page with one button that signs in. The code is used only by the button's
// POST, so a mail scanner that opens links doesn't use it up.
func (h *Hub) linkPage(w http.ResponseWriter, r *http.Request, ri reqInfo, code string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if h.limited(w, r, ri, h.tokenFails, true) {
		return
	}
	u, err := h.st.Accounts.PeekLink(code)
	if errors.Is(err, accounts.ErrBadLink) {
		h.tokenFails.Fail(ri.client, h.opts.Now())
		h.page(w, r, http.StatusUnauthorized, "Sign in", "<p>"+esc(badLinkText)+"</p>")
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.page(w, r, http.StatusOK, "Sign in", `<form method="post" action="/signin/`+esc(url.PathEscape(code))+`">`+
		`<button type="submit" autofocus>Sign in as `+esc(u.Email)+`</button></form>`)
}

// useLink is POST /signin/<code>: it uses the code, starts a session and goes to the projects page.
func (h *Hub) useLink(w http.ResponseWriter, r *http.Request, ri reqInfo, code string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if h.limited(w, r, ri, h.tokenFails, true) {
		return
	}
	u, err := h.st.Accounts.UseLink(code)
	if errors.Is(err, accounts.ErrBadLink) {
		h.tokenFails.Fail(ri.client, h.opts.Now())
		h.page(w, r, http.StatusUnauthorized, "Sign in", "<p>"+esc(badLinkText)+"</p>")
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	// A new session every time: whatever session cookie the browser had is replaced, never reused.
	if c, err := r.Cookie(SessionCookie); err == nil {
		if old, _, _, err := h.st.Accounts.CheckSession(c.Value); err == nil {
			_ = h.st.Accounts.EndSession(old.ID)
		}
	}
	value, sess, err := h.st.Accounts.StartSession(u.ID, r.UserAgent(), ri.client)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.setSessionCookie(w, value, sess.ExpiresAt())
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// mailLink is POST /signin: it mails a sign-in link to an address that has an account, and says the same thing
// either way.
func (h *Hub) mailLink(w http.ResponseWriter, r *http.Request, ri reqInfo) {
	if h.opts.Mailer == nil {
		h.signinPage(w, r, "")
		return
	}
	if h.limited(w, r, ri, h.signinMails, true) {
		return
	}
	h.signinMails.Fail(ri.client, h.opts.Now()) // every request counts here, good address or not
	if err := r.ParseForm(); err != nil {
		h.signinPage(w, r, "")
		return
	}
	email, err := accounts.NormalizeEmail(r.PostFormValue("email"))
	if err != nil {
		h.signinPage(w, r, "That isn't an email address.")
		return
	}
	if u, err := h.st.Accounts.User(email); err == nil && u.Disabled == "" {
		if err := h.SendLink(r.Context(), u, h.baseURL(ri), 0); err != nil {
			h.logf("mailing a sign-in link to %s: %v", email, err)
		}
	}
	h.signinPage(w, r, "If "+email+" has an account, a sign-in link is on its way.")
}

// SendLink makes a sign-in link for u and mails it.
func (h *Hub) SendLink(ctx context.Context, u accounts.User, base string, ttl time.Duration) error {
	code, exp, err := h.st.Accounts.CreateLink(u.ID, ttl)
	if err != nil {
		return err
	}
	return MailLink(ctx, h.opts.Mailer, u, LinkURL(base, code), exp)
}

// LinkURL is a sign-in link.
func LinkURL(base, code string) string { return strings.TrimSuffix(base, "/") + "/signin/" + code }

// signout is POST /signout.
func (h *Hub) signout(w http.ResponseWriter, r *http.Request, _ reqInfo) {
	if r.Method != http.MethodPost {
		h.notFound(w, r)
		return
	}
	s, err := h.session(w, r)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if s != nil {
		if err := h.st.Accounts.EndSession(s.sess.ID); err != nil {
			h.storeFailed(w, r, err)
			return
		}
	}
	h.clearCookie(w, SessionCookie, "/")
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// who is who a project request acts as.
type who struct {
	role   team.Role
	author string
	via    string // bearer | session | share
	label  string // a token's label
	user   *accounts.User
}

var errRefused = errors.New("refused") // who answered the request itself

// identify decides who a request to a project is (spec/server.md, "Who a request is"). bearerOnly is set for the
// agent routes, where only a Bearer token counts. It answers refused requests itself and returns errRefused.
func (h *Hub) identify(w http.ResponseWriter, r *http.Request, ri reqInfo, p *proj, page, bearerOnly bool) (who, error) {
	if tok := bearer(r); tok != "" {
		t, ok := h.checkToken(w, r, ri, p, tok, page, false)
		if !ok {
			return who{}, errRefused
		}
		return who{role: team.Role(t.Role), author: guestName(t.Label), via: "bearer", label: t.Label}, nil
	}
	if bearerOnly {
		w.Header().Set("WWW-Authenticate", "Bearer")
		h.fail(w, r, http.StatusUnauthorized, "token required")
		return who{}, errRefused
	}
	s, err := h.session(w, r)
	if err != nil {
		h.storeFailed(w, r, err)
		return who{}, errRefused
	}
	if s != nil {
		role, ok, err := h.st.Accounts.Role(p.row.ID, s.user)
		if err != nil {
			h.storeFailed(w, r, err)
			return who{}, errRefused
		}
		if !ok {
			if page {
				h.page(w, r, http.StatusForbidden, "Retake", "<p>You aren't a member of this project. Ask its owner to "+
					"add you.</p>"+backHome)
			} else {
				h.fail(w, r, http.StatusForbidden, "not a member")
			}
			return who{}, errRefused
		}
		u := s.user
		return who{role: role, author: u.Author(), via: "session", user: &u}, nil
	}
	if c, err := r.Cookie(ShareCookie); err == nil && c.Value != "" {
		t, ok := h.checkToken(w, r, ri, p, c.Value, page, true)
		if !ok {
			return who{}, errRefused
		}
		return who{role: team.Role(t.Role), author: guestName(t.Label), via: "share", label: t.Label}, nil
	}
	if page {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
	} else {
		w.Header().Set("WWW-Authenticate", "Bearer")
		h.fail(w, r, http.StatusUnauthorized, "sign in required")
	}
	return who{}, errRefused
}

func guestName(label string) string {
	if strings.TrimSpace(label) == "" {
		return "guest"
	}
	return label
}

// checkToken checks a project token under the rate limit; a bad one gets 401 (and a share cookie is cleared).
func (h *Hub) checkToken(w http.ResponseWriter, r *http.Request, ri reqInfo, p *proj, tok string, page,
	cookie bool) (project.Token, bool) {
	if h.limited(w, r, ri, h.tokenFails, page) {
		return project.Token{}, false
	}
	t, err := p.store.TokenByHash(team.Hash(tok))
	bad := !team.WellFormed(tok) || errors.Is(err, project.ErrNotFound) || err == nil && !t.Valid(h.opts.Now())
	if bad {
		h.tokenFails.Fail(ri.client, h.opts.Now())
		if cookie {
			h.clearCookie(w, ShareCookie, p.base+"/")
		}
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		if page {
			h.page(w, r, http.StatusUnauthorized, "Retake", "<p>This link has expired or was revoked. Ask whoever sent "+
				"it for a new one.</p>")
		} else {
			h.fail(w, r, http.StatusUnauthorized, "bad token")
		}
		return project.Token{}, false
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return project.Token{}, false
	}
	return t, true
}

// maxCookieAge is the longest a browser keeps a cookie (400 days, as Chrome caps it).
const maxCookieAge = 400 * 24 * time.Hour

// bouncePage takes the browser from a share link to the project's page with a same-site navigation, so the
// SameSite=Strict cookie goes along (spec/team.md, "Share links").
func bouncePage(to string) string {
	q := strconv.Quote(to)
	return `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="referrer" content="no-referrer">` +
		`<title>Retake</title><script>location.replace(` + q + `)</script>` +
		`<meta http-equiv="refresh" content="0;url=` + esc(to) + `"><a href="` + esc(to) + `">Open the review</a>`
}

// shareLink is GET /p/<name>/s/<token>: it checks the token, sets the project's share cookie and bounces to the
// project's page.
func (h *Hub) shareLink(w http.ResponseWriter, r *http.Request, ri reqInfo, p *proj, tok string) {
	t, ok := h.checkToken(w, r, ri, p, tok, true, false)
	if !ok {
		return
	}
	if team.Role(t.Role) == team.Agent {
		h.page(w, r, http.StatusForbidden, "Retake", "<p>This is an agent's token, not a share link.</p>")
		return
	}
	age := maxCookieAge
	if exp, ok := t.ExpiresAt(); ok {
		age = min(age, exp.Sub(h.opts.Now()))
	}
	//nolint:gosec // as setSessionCookie
	http.SetCookie(w, &http.Cookie{Name: ShareCookie, Value: tok, Path: p.base + "/", MaxAge: max(1, int(age.Seconds())),
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteStrictMode})
	body := bouncePage(p.base + "/")
	hd := w.Header()
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

// ShareURL is a project's share link.
func ShareURL(base, name, token string) string {
	return fmt.Sprintf("%s/p/%s/s/%s", strings.TrimSuffix(base, "/"), name, token)
}
