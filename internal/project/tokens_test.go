package project

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	s, c := newStore(t)
	viewer, err := s.CreateToken(NewToken{Hash: "aa", Role: "viewer", Label: "alex", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateToken(NewToken{Hash: "bb", Role: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if viewer.ID != 1 || agent.ID != 2 || viewer.Created != "2026-10-30T12:00:00.000Z" ||
		viewer.Expires != "2026-10-30T13:00:00.000Z" || agent.Expires != "" {
		t.Fatalf("rows: %+v %+v", viewer, agent)
	}
	if _, err := s.CreateToken(NewToken{Hash: "aa", Role: "viewer"}); err == nil {
		t.Error("a duplicate hash was stored")
	}
	if _, err := s.CreateToken(NewToken{Hash: "cc", Role: "admin"}); err == nil {
		t.Error("an unknown role was stored")
	}
	if _, err := s.CreateToken(NewToken{Role: "viewer"}); err == nil {
		t.Error("a token without a hash was stored")
	}

	got, err := s.TokenByHash("aa")
	if err != nil || got != viewer {
		t.Fatalf("TokenByHash = %+v, %v", got, err)
	}
	if _, err := s.TokenByHash("zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	if st := got.State(c.now()); st != TokenActive {
		t.Errorf("fresh token: %s", st)
	}
	if st := got.State(c.now().Add(time.Hour)); st != TokenExpired {
		t.Errorf("at its expiry: %s", st)
	}
	if !agent.Valid(c.now().Add(1000 * time.Hour)) {
		t.Error("a token without expiry expired")
	}
	if (Token{Expires: "garbled"}).Valid(c.now()) {
		t.Error("a garbled expiry counts as valid")
	}

	c.add(time.Minute)
	r, err := s.RevokeToken(agent.ID)
	if err != nil || r.Revoked != "2026-10-30T12:01:00.000Z" || r.State(c.now()) != TokenRevoked {
		t.Fatalf("revoke: %+v %v", r, err)
	}
	c.add(time.Minute)
	if r2, err := s.RevokeToken(agent.ID); err != nil || r2.Revoked != r.Revoked {
		t.Errorf("revoking again moved the time: %+v %v", r2, err)
	}
	if _, err := s.RevokeToken(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking an unknown token: %v", err)
	}
	all, err := s.Tokens()
	if err != nil || len(all) != 2 || all[0].ID != 1 || all[1].Revoked == "" {
		t.Errorf("Tokens = %+v, %v", all, err)
	}

	// Only hashes are stored: no column holds anything but the hash we gave.
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM tokens WHERE hash IN ('aa', 'bb')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("hashes: %d %v", n, err)
	}
}

func TestHosts(t *testing.T) {
	s, c := newStore(t)
	if ok, err := s.HostAllowed("abc.trycloudflare.com"); ok || err != nil {
		t.Fatalf("empty table: %v %v", ok, err)
	}
	if err := s.AddHost("ABC.trycloudflare.com", "cloudflare", 100); err != nil {
		t.Fatal(err)
	}
	c.add(time.Second)
	if err := s.AddHost("box.tail1.ts.net", "tailscale", 200); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HostAllowed("abc.TRYCLOUDFLARE.com"); !ok || err != nil {
		t.Errorf("added host: %v %v", ok, err)
	}
	hosts, err := s.Hosts()
	if err != nil || len(hosts) != 2 || hosts[0].Host != "box.tail1.ts.net" || hosts[1].PID != 100 {
		t.Fatalf("Hosts = %+v, %v", hosts, err)
	}
	// Another process's row of the same host is left alone; its own goes.
	if err := s.RemoveHost("abc.trycloudflare.com", 999); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HostAllowed("abc.trycloudflare.com"); !ok {
		t.Error("another process removed the host")
	}
	if err := s.RemoveHost("abc.trycloudflare.com", 100); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HostAllowed("abc.trycloudflare.com"); ok {
		t.Error("the host is still allowed after its share stopped")
	}
	// A share that starts again for the same host takes the row over.
	if err := s.AddHost("box.tail1.ts.net", "tailscale", 300); err != nil {
		t.Fatal(err)
	}
	hosts, _ = s.Hosts()
	if len(hosts) != 1 || hosts[0].PID != 300 || !strings.HasPrefix(hosts[0].Created, "2026-10-30T12:00:01") {
		t.Errorf("after re-adding: %+v", hosts)
	}
	if err := s.RemoveHost("box.tail1.ts.net", 0); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 0 {
		t.Errorf("pid 0 didn't remove it: %+v", hosts)
	}
}
