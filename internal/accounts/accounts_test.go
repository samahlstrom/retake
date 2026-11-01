package accounts

import (
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/team"
)

// clock is a settable time for the store.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)} }
func openTest(t *testing.T) (*Store, *clock) {
	return openAt(t, filepath.Join(t.TempDir(), "server.db"))
}

func openAt(t *testing.T, path string) (*Store, *clock) {
	t.Helper()
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := newClock()
	s.Now = c.now
	return s, c
}

func TestSecrets(t *testing.T) {
	sec, hash, err := NewSecret(LinkPrefix)
	if err != nil || !WellFormed(LinkPrefix, sec) || WellFormed(SessionPrefix, sec) || hash != Hash(sec) || len(hash) != 64 {
		t.Fatalf("%q %q %v", sec, hash, err)
	}
	for _, bad := range []string{"", "rs_", "rs_short", sec + "x", "RS" + sec[2:], strings.Replace(sec, "_", "-", 1)} {
		if WellFormed(LinkPrefix, bad) {
			t.Errorf("%q should be malformed", bad)
		}
	}
}

func TestNamesEmailsRoles(t *testing.T) {
	for _, ok := range []string{"demo", "a", "my-film-2", strings.Repeat("a", 63)} {
		if !ValidName(ok) {
			t.Errorf("%q should be a valid name", ok)
		}
	}
	for _, bad := range []string{"", "-a", "Demo", "a_b", "a/b", "a.b", strings.Repeat("a", 64), ".."} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if e, err := NormalizeEmail("  Ana@Example.COM "); err != nil || e != "ana@example.com" {
		t.Fatalf("%q %v", e, err)
	}
	for _, bad := range []string{"", "ana", "Ana <ana@example.com>", "a@b@c"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if r, err := ParseRole(" Owner "); err != nil || r != team.Owner {
		t.Fatal(r, err)
	}
	if _, err := ParseRole("agent"); err == nil {
		t.Fatal("agent is a token role, not a member role")
	}
	if (User{Email: "a@b.c"}).Author() != "a@b.c" || (User{Email: "a@b.c", Name: " Ana "}).Author() != "Ana" {
		t.Fatal("Author")
	}
}

func TestUsersProjectsMembers(t *testing.T) {
	s, _ := openTest(t)
	ana, err := s.CreateUser("Ana@example.com", "Ana", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("ana@example.com", "", false); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate user: %v", err)
	}
	boss, _ := s.CreateUser("boss@example.com", "", true)
	if n, _ := s.CountUsers(); n != 2 {
		t.Fatalf("count %d", n)
	}
	demo, err := s.CreateProject("demo", " Demo ")
	if err != nil || demo.Title != "Demo" {
		t.Fatal(demo, err)
	}
	if _, err := s.CreateProject("demo", ""); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate project: %v", err)
	}
	if _, err := s.CreateProject("Bad Name", ""); err == nil {
		t.Fatal("bad name accepted")
	}
	other, _ := s.CreateProject("other", "")
	if _, ok, err := s.Role(demo.ID, ana); ok || err != nil {
		t.Fatal("not a member yet")
	}
	if r, ok, _ := s.Role(demo.ID, boss); !ok || r != team.Owner {
		t.Fatal("an admin owns every project")
	}
	if err := s.SetMember(demo.ID, ana.ID, team.Reviewer); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember(demo.ID, ana.ID, team.Approver); err != nil { // a change, not a second row
		t.Fatal(err)
	}
	if err := s.SetMember(demo.ID, ana.ID, team.Agent); err == nil {
		t.Fatal("agent isn't a member role")
	}
	if r, ok, _ := s.Role(demo.ID, ana); !ok || r != team.Approver {
		t.Fatal(r)
	}
	ms, _ := s.Members(demo.ID)
	if len(ms) != 1 || ms[0].User.Email != "ana@example.com" || ms[0].Role != team.Approver {
		t.Fatalf("%+v", ms)
	}
	// Where a connected computer's project is in its folder: none at first, then what was set.
	if demo.Path != "" || demo.ProjectPath != "" {
		t.Fatalf("a new project has a place: %+v", demo)
	}
	if err := s.SetProjectPlace("demo", "item-shop/shorts", "item-shop"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectPlace("gone", "a", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("place of a missing project: %v", err)
	}
	if p, _ := s.Project("demo"); p.Path != "item-shop/shorts" || p.ProjectPath != "item-shop" {
		t.Fatalf("place %+v", p)
	}
	mine, _ := s.ProjectsOf(ana)
	if len(mine) != 1 || mine[0].Name != "demo" || mine[0].Role != team.Approver || mine[0].Path != "item-shop/shorts" ||
		mine[0].ProjectPath != "item-shop" {
		t.Fatalf("%+v", mine)
	}
	all, _ := s.ProjectsOf(boss)
	if len(all) != 2 || all[1].Name != other.Name || all[1].Role != team.Owner {
		t.Fatalf("%+v", all)
	}
	if err := s.RemoveMember(demo.ID, ana.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(demo.ID, ana.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if u, err := s.UpdateUser("ana@example.com", "Ana B", true); err != nil || !u.Admin || u.Name != "Ana B" {
		t.Fatal(u, err)
	}
	if err := s.RemoveUser("ana@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User("ana@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSignInLinks(t *testing.T) {
	s, c := openTest(t)
	u, _ := s.CreateUser("ana@example.com", "", false)
	code, exp, err := s.CreateLink(u.ID, 0)
	if err != nil || !exp.Equal(c.t.Add(DefaultLinkTTL)) || !WellFormed(LinkPrefix, code) {
		t.Fatal(code, exp, err)
	}
	if _, _, err := s.CreateLink(u.ID, 8*24*time.Hour); err == nil {
		t.Fatal("a link over 7 days")
	}
	if got, err := s.PeekLink(code); err != nil || got.ID != u.ID {
		t.Fatal(got, err)
	}
	if got, err := s.UseLink(code); err != nil || got.ID != u.ID {
		t.Fatal(got, err)
	}
	if _, err := s.UseLink(code); !errors.Is(err, ErrBadLink) {
		t.Fatalf("second use: %v", err)
	}
	if _, err := s.PeekLink(code); !errors.Is(err, ErrBadLink) {
		t.Fatalf("peek after use: %v", err)
	}
	late, _, _ := s.CreateLink(u.ID, time.Hour)
	c.add(time.Hour)
	if _, err := s.UseLink(late); !errors.Is(err, ErrBadLink) {
		t.Fatalf("expired: %v", err)
	}
	off, _, _ := s.CreateLink(u.ID, time.Hour)
	if _, err := s.SetDisabled("ana@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseLink(off); !errors.Is(err, ErrBadLink) {
		t.Fatalf("disabled user: %v", err)
	}
	for _, bad := range []string{"", "rs_x", "ss_" + strings.Repeat("A", 43), "rs_" + strings.Repeat("A", 43)} {
		if _, err := s.UseLink(bad); !errors.Is(err, ErrBadLink) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSessions(t *testing.T) {
	s, c := openTest(t)
	u, _ := s.CreateUser("ana@example.com", "", false)
	val, sess, err := s.StartSession(u.ID, "test-agent", "203.0.113.5")
	if err != nil || !WellFormed(SessionPrefix, val) || sess.ID == 0 {
		t.Fatal(val, sess, err)
	}
	got, who, rot, err := s.CheckSession(val)
	if err != nil || got.ID != sess.ID || who.ID != u.ID || rot != "" {
		t.Fatal(got, who, rot, err)
	}
	if _, _, _, err := s.CheckSession("ss_" + strings.Repeat("B", 43)); !errors.Is(err, ErrNoSession) {
		t.Fatal(err)
	}

	// Rotation: after a day of use the value is replaced; the old one works for a minute more.
	c.add(RotateEvery)
	_, _, rot, err = s.CheckSession(val)
	if err != nil || rot == "" || rot == val {
		t.Fatalf("rotation: %q %v", rot, err)
	}
	if _, _, again, err := s.CheckSession(val); err != nil || again != "" {
		t.Fatalf("old value in the grace window: %q %v", again, err)
	}
	if _, _, again, err := s.CheckSession(rot); err != nil || again != "" {
		t.Fatalf("new value: %q %v", again, err)
	}
	c.add(RotateGrace)
	if _, _, _, err := s.CheckSession(val); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old value after the grace window: %v", err)
	}
	val = rot

	// Idle: a week without use ends it.
	c.add(IdleTTL - 2*time.Minute) // last use was the rotation, a minute before the grace check
	if _, _, _, err := s.CheckSession(val); err != nil {
		t.Fatalf("just inside the idle limit: %v", err)
	}
	c.add(IdleTTL)
	if _, _, _, err := s.CheckSession(val); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle: %v", err)
	}

	// Absolute: 30 days at most, however busy.
	c2 := newClock()
	s.Now = c2.now
	v2, _, _ := s.StartSession(u.ID, "", "")
	for range 31 {
		c2.add(24 * time.Hour)
		_, _, r, err := s.CheckSession(v2)
		if err != nil {
			if c2.t.Sub(newClock().t) < SessionTTL {
				t.Fatalf("ended early at %v: %v", c2.t, err)
			}
			break
		}
		if r != "" {
			v2 = r
		}
	}
	if _, _, _, err := s.CheckSession(v2); !errors.Is(err, ErrNoSession) {
		t.Fatalf("past 30 days: %v", err)
	}

	// Revocation: one, then all.
	a, sa, _ := s.StartSession(u.ID, "", "")
	b, _, _ := s.StartSession(u.ID, "", "")
	if err := s.EndSession(sa.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.CheckSession(a); !errors.Is(err, ErrNoSession) {
		t.Fatalf("revoked: %v", err)
	}
	if list, _ := s.Sessions(u.ID); len(list) != 1 {
		t.Fatalf("live sessions: %+v", list)
	}
	if n, err := s.EndSessions(u.ID); err != nil || n < 1 { // b, and the ended ones not revoked yet
		t.Fatal(n, err)
	}
	if _, _, _, err := s.CheckSession(b); !errors.Is(err, ErrNoSession) {
		t.Fatalf("all revoked: %v", err)
	}

	// Disabling a user ends their sessions; removing them too.
	d, _, _ := s.StartSession(u.ID, "", "")
	_, _ = s.SetDisabled(u.Email, true)
	if _, _, _, err := s.CheckSession(d); !errors.Is(err, ErrNoSession) {
		t.Fatalf("disabled: %v", err)
	}
	_, _ = s.SetDisabled(u.Email, false)
	e, _, _ := s.StartSession(u.ID, "", "")
	_ = s.RemoveUser(u.Email)
	if _, _, _, err := s.CheckSession(e); !errors.Is(err, ErrNoSession) {
		t.Fatalf("removed: %v", err)
	}
	if n, err := s.Prune(); err != nil || n < 0 {
		t.Fatal(n, err)
	}
}

func TestSettings(t *testing.T) {
	s, _ := openTest(t)
	if v, err := s.Setting(SettingPublicURL); v != "" || err != nil {
		t.Fatal(v, err)
	}
	_ = s.SetSetting(SettingPublicURL, "https://a.example")
	_ = s.SetSetting(SettingPublicURL, "https://b.example")
	if v, _ := s.Setting(SettingPublicURL); v != "https://b.example" {
		t.Fatal(v)
	}
}

var (
	createRE = regexp.MustCompile(`(?s)CREATE TABLE (\w+) \((.*?)\n\);`)
	alterRE  = regexp.MustCompile(`ALTER TABLE (\w+) ADD COLUMN (\w+)`)
)

// columns lists the columns a step makes or adds, by table.
func columns(step string) map[string][]string {
	out := map[string][]string{}
	for _, m := range alterRE.FindAllStringSubmatch(step, -1) {
		out[m[1]] = append(out[m[1]], m[2])
	}
	for _, m := range createRE.FindAllStringSubmatch(step, -1) {
		var cols []string
		for line := range strings.SplitSeq(m[2], "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || f[0] == "PRIMARY" || f[0] == "CHECK" {
				continue
			}
			cols = append(cols, f[0])
		}
		out[m[1]] = cols
	}
	return out
}

func TestMigrationListsMatch(t *testing.T) {
	if len(migrations) != len(pgMigrations) {
		t.Fatalf("%d SQLite steps, %d Postgres steps", len(migrations), len(pgMigrations))
	}
	for i := range migrations {
		a, b := columns(migrations[i]), columns(pgMigrations[i])
		if len(a) == 0 || len(a) != len(b) {
			t.Fatalf("step %d: tables %v vs %v", i+1, a, b)
		}
		for name, cols := range a {
			if !slices.Equal(cols, b[name]) {
				t.Errorf("step %d, %s: %v vs %v", i+1, name, cols, b[name])
			}
		}
	}
}
