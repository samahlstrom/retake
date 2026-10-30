package team

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRolesAndPerms(t *testing.T) {
	want := map[Role]string{ // the perms each role has, in order any, watch, record, approve, agent
		Viewer:   "YYNNN",
		Reviewer: "YYYNN",
		Approver: "YYYYN",
		Owner:    "YYYYN",
		Agent:    "YNNNY",
		"":       "NNNNN",
	}
	for r, w := range want {
		var got strings.Builder
		for _, p := range []Perm{PermAny, PermWatch, PermRecord, PermApprove, PermAgent} {
			if r.Can(p) {
				got.WriteByte('Y')
			} else {
				got.WriteByte('N')
			}
		}
		if got.String() != w {
			t.Errorf("%q: %s, want %s", r, got.String(), w)
		}
	}
	for _, s := range []string{"viewer", " Reviewer ", "APPROVER", "agent"} {
		if _, err := ParseRole(s); err != nil {
			t.Errorf("ParseRole(%q): %v", s, err)
		}
	}
	for _, s := range []string{"owner", "admin", ""} {
		if _, err := ParseRole(s); err == nil {
			t.Errorf("ParseRole(%q) accepted it", s)
		}
	}
	if PermRecord.String() != "record" || Perm(9).String() != "perm(9)" {
		t.Error("Perm.String")
	}
}

func TestTokens(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		tok, hash, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if !WellFormed(tok) || len(tok) != 46 || !strings.HasPrefix(tok, "rt_") {
			t.Fatalf("token %q", tok)
		}
		if hash != Hash(tok) || len(hash) != 64 || strings.Contains(hash, tok) {
			t.Fatalf("hash %q of %q", hash, tok)
		}
		if seen[tok] {
			t.Fatal("a token repeated")
		}
		seen[tok] = true
	}
	// The SHA-256 of the empty string, so the hash is the documented one.
	if Hash("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Error("Hash is not SHA-256 hex")
	}
	for _, bad := range []string{"", "rt_", "rt_" + strings.Repeat("a", 42), "rt_" + strings.Repeat("a", 44),
		"xx_" + strings.Repeat("a", 43), "rt_" + strings.Repeat("a", 42) + "/", "rt_" + strings.Repeat("a", 42) + "="} {
		if WellFormed(bad) {
			t.Errorf("WellFormed(%q)", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	l.Max, l.Window = 3, time.Minute
	t0 := time.Unix(1_800_000_000, 0)
	for i := range 3 {
		if blocked, _ := l.Blocked("a", t0); blocked {
			t.Fatalf("blocked after %d failures", i)
		}
		l.Fail("a", t0.Add(time.Duration(i)*10*time.Second))
	}
	blocked, wait := l.Blocked("a", t0.Add(25*time.Second))
	if !blocked || wait != 35*time.Second {
		t.Fatalf("after 3 failures: %v %v", blocked, wait)
	}
	if blocked, _ := l.Blocked("b", t0.Add(25*time.Second)); blocked {
		t.Error("another client is blocked too")
	}
	// The oldest failure leaves the window at 60 s: one more try is allowed, and failing it blocks again.
	if blocked, _ := l.Blocked("a", t0.Add(60*time.Second)); blocked {
		t.Error("still blocked after the window")
	}
	l.Fail("a", t0.Add(61*time.Second))
	if blocked, wait := l.Blocked("a", t0.Add(61*time.Second)); !blocked || wait != 9*time.Second {
		t.Errorf("after failing again: %v %v", blocked, wait)
	}
	// Everything ages out, and the client is forgotten.
	if blocked, _ := l.Blocked("a", t0.Add(10*time.Minute)); blocked || l.Len() != 0 {
		t.Errorf("after 10 min: blocked %v, %d clients", blocked, l.Len())
	}

	// A bounded number of clients: the stalest goes first.
	l = NewLimiter()
	l.Clients = 3
	for i := range 5 {
		l.Fail(fmt.Sprint("c", i), t0.Add(time.Duration(i)*time.Second))
	}
	if l.Len() != 3 {
		t.Fatalf("%d clients tracked", l.Len())
	}
	l.mu.Lock()
	_, old := l.fails["c0"]
	_, newest := l.fails["c4"]
	l.mu.Unlock()
	if old || !newest {
		t.Errorf("kept the wrong clients: c0 %v, c4 %v", old, newest)
	}
}
