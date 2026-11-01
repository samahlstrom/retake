package hub

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/accounts"
)

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

var formType = header("Content-Type", "application/x-www-form-urlencoded")

func TestSetupMakesTheOwnerOnce(t *testing.T) {
	x := newTH(t, Config{}, nil)
	link, err := x.h.SetupURL(x.url)
	if err != nil || !strings.HasPrefix(link, x.url+"/setup?code=su_") {
		t.Fatalf("%q %v", link, err)
	}
	if again, _ := x.h.SetupURL(x.url); again != link {
		t.Fatalf("a second call made another code: %q", again)
	}
	code := strings.TrimPrefix(link, x.url+"/setup?code=")

	// The sign-in page points at the setup link while there are no users.
	if r := x.do(http.MethodGet, "/signin", ""); !strings.Contains(r.body, "no accounts yet") {
		t.Fatalf("sign-in page: %s", r.body)
	}
	// Without the code, or with a wrong one, there's no form.
	for _, q := range []string{"", "?code=", "?code=su_" + strings.Repeat("A", 43), "?code=" + code + "x"} {
		if r := x.do(http.MethodGet, "/setup"+q, ""); r.code != 401 || !strings.Contains(r.body, "This setup link isn") ||
			strings.Contains(r.body, "<form") {
			t.Fatalf("GET /setup%s: %d %s", q, r.code, r.body)
		}
	}
	page := x.do(http.MethodGet, "/setup?code="+code, "")
	if page.code != 200 || !strings.Contains(page.body, "Create owner account") ||
		!strings.Contains(page.body, `name="email"`) || page.hdr.Get("Referrer-Policy") != "same-origin" ||
		page.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("form: %d %v %s", page.code, page.hdr, page.body)
	}

	// Another site can't post it, and a wrong code or a bad email makes no one.
	if r := x.do(http.MethodPost, "/setup", form("code", code, "email", "a@example.com"), formType,
		origin("https://evil.example")); r.code != 403 {
		t.Fatalf("cross-site post: %d", r.code)
	}
	if r := x.post("/setup", form("code", "su_"+strings.Repeat("B", 43), "email", "a@example.com"), formType); r.code != 401 {
		t.Fatalf("wrong code: %d", r.code)
	}
	if r := x.post("/setup", form("code", code, "name", "Ana", "email", "nope"), formType); r.code != 200 ||
		!strings.Contains(r.body, "an email address") || !strings.Contains(r.body, `value="Ana"`) {
		t.Fatalf("bad email: %d %s", r.code, r.body)
	}
	if n, _ := x.st.Accounts.CountUsers(); n != 0 {
		t.Fatalf("%d users", n)
	}

	r := x.post("/setup", form("code", code, "name", "Ana", "email", "Ana@Example.com"), formType)
	if r.code != 200 || !strings.Contains(r.body, "You're set up") ||
		!strings.Contains(r.body, "retake connect "+x.url+" --token rk_") {
		t.Fatalf("setup: %d %s", r.code, r.body)
	}
	c := setCookie(t, r, SessionCookie)
	if c == nil || !c.HttpOnly || c.Value == "" {
		t.Fatalf("no session: %+v", c)
	}
	u, err := x.st.Accounts.User("ana@example.com")
	if err != nil || !u.Admin || u.Name != "Ana" {
		t.Fatalf("%+v %v", u, err)
	}
	if me := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, c.Value)); me.code != 200 ||
		me.json(t)["email"] != "ana@example.com" {
		t.Fatalf("me: %d %s", me.code, me.body)
	}
	// The command's token is a working machine token.
	tok := r.body[strings.Index(r.body, "rk_"):]
	tok = tok[:46]
	if _, err := x.st.Accounts.CheckMachineToken(tok); err != nil {
		t.Fatalf("%q: %v", tok, err)
	}

	// The code is used up, and the page is gone.
	if r := x.post("/setup", form("code", code, "email", "eve@example.com"), formType); r.code != 404 ||
		!strings.Contains(r.body, "already set up") {
		t.Fatalf("second post: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/setup?code="+code, ""); r.code != 404 {
		t.Fatalf("GET after: %d", r.code)
	}
	if link, err := x.h.SetupURL(x.url); link != "" || err != nil {
		t.Fatalf("a setup link with a user: %q %v", link, err)
	}
	if n, _ := x.st.Accounts.CountUsers(); n != 1 {
		t.Fatalf("%d users", n)
	}
}

func TestSetupNeedsZeroUsers(t *testing.T) {
	x := newTH(t, Config{}, nil)
	link, _ := x.h.SetupURL(x.url) // the code exists, then someone is added from the CLI
	x.user("admin@example.com", true)
	code := strings.TrimPrefix(link, x.url+"/setup?code=")
	if r := x.do(http.MethodGet, "/setup?code="+code, ""); r.code != 404 || !strings.Contains(r.body, "already set up") {
		t.Fatalf("GET: %d %s", r.code, r.body)
	}
	if r := x.post("/setup", form("code", code, "email", "eve@example.com"), formType); r.code != 404 {
		t.Fatalf("POST: %d", r.code)
	}
	if _, err := x.st.Accounts.User("eve@example.com"); err == nil {
		t.Fatal("setup made a second user")
	}
	if l, _ := x.h.SetupURL(x.url); l != "" {
		t.Fatalf("setup link: %q", l)
	}
}

func TestSetupWithoutACodeAndRateLimit(t *testing.T) {
	x := newTH(t, Config{}, nil)
	// Before SetupURL there is no code at all: nothing opens the form.
	if r := x.do(http.MethodGet, "/setup?code=", ""); r.code != 401 {
		t.Fatalf("no code yet: %d", r.code)
	}
	link, _ := x.h.SetupURL(x.url)
	for range 8 { // and the one above: nine
		x.do(http.MethodGet, "/setup?code=su_"+strings.Repeat("C", 43), "")
	}
	if r := x.do(http.MethodGet, "/setup?code=wrong", ""); r.code != 401 {
		t.Fatalf("tenth: %d", r.code)
	}
	// Ten failures in a minute: even the right code waits.
	if r := x.do(http.MethodGet, strings.TrimPrefix(link, x.url), ""); r.code != 429 {
		t.Fatalf("limited: %d", r.code)
	}
	if _, err := x.st.Accounts.CreateOwner("a@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.Accounts.CreateOwner("b@example.com", ""); !errors.Is(err, accounts.ErrSetUp) {
		t.Fatalf("second owner: %v", err)
	}
}
