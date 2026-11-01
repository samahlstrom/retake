package accounts

import (
	"errors"
	"testing"
	"time"
)

func TestCreateOwnerOnlyOnAFreshServer(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.CreateOwner("not an email", "x"); err == nil {
		t.Fatal("a bad email made an owner")
	}
	u, err := s.CreateOwner("Owner@Example.com", " Ana ")
	if err != nil || !u.Admin || u.Email != "owner@example.com" || u.Name != "Ana" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.CreateOwner("second@example.com", ""); !errors.Is(err, ErrSetUp) {
		t.Fatalf("a second owner: %v", err)
	}
	if n, _ := s.CountUsers(); n != 1 {
		t.Fatalf("%d users", n)
	}
}

func TestMachineTokens(t *testing.T) {
	s, c := openTest(t)
	admin, _ := s.CreateUser("admin@example.com", "", true)
	member, _ := s.CreateUser("member@example.com", "", false)
	if _, _, err := s.CreateMachineToken(member.ID); err == nil {
		t.Fatal("a member made a machine token")
	}
	tok, row, err := s.CreateMachineToken(admin.ID)
	if err != nil || !WellFormed(MachinePrefix, tok) || row.ID == 0 || row.CreatedBy != admin.ID {
		t.Fatalf("%q %+v %v", tok, row, err)
	}
	got, err := s.CheckMachineToken(tok)
	if err != nil || got.ID != row.ID || got.Used == "" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "rk_short", "rt_" + tok[3:], tok[:len(tok)-1] + "x"} {
		if _, err := s.CheckMachineToken(bad); !errors.Is(err, ErrBadMachine) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	// Last use is written at most once a minute.
	first := got.Used
	c.add(10 * time.Second)
	if again, _ := s.CheckMachineToken(tok); again.Used != first {
		t.Fatalf("used moved within the minute: %s %s", first, again.Used)
	}
	c.add(time.Minute)
	if again, _ := s.CheckMachineToken(tok); again.Used == first {
		t.Fatal("used didn't move after a minute")
	}

	// Demoting, disabling or removing the admin ends the token; enabling them brings it back.
	if _, err := s.UpdateUser(admin.Email, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckMachineToken(tok); !errors.Is(err, ErrBadMachine) {
		t.Fatalf("a demoted admin's token: %v", err)
	}
	_, _ = s.UpdateUser(admin.Email, "", true)
	_, _ = s.SetDisabled(admin.Email, true)
	if _, err := s.CheckMachineToken(tok); !errors.Is(err, ErrBadMachine) {
		t.Fatalf("a disabled admin's token: %v", err)
	}
	_, _ = s.SetDisabled(admin.Email, false)
	if _, err := s.CheckMachineToken(tok); err != nil {
		t.Fatalf("enabled again: %v", err)
	}

	// Revoking.
	list, err := s.MachineTokens()
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	r, err := s.RevokeMachineToken(row.ID)
	if err != nil || r.Revoked == "" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.CheckMachineToken(tok); !errors.Is(err, ErrBadMachine) {
		t.Fatalf("a revoked token: %v", err)
	}
	if _, err := s.RevokeMachineToken(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}

	// Removing the admin removes their tokens.
	tok2, _, _ := s.CreateMachineToken(admin.ID)
	if err := s.RemoveUser(admin.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckMachineToken(tok2); !errors.Is(err, ErrBadMachine) {
		t.Fatalf("a removed admin's token: %v", err)
	}
	if list, _ := s.MachineTokens(); len(list) != 0 {
		t.Fatalf("tokens left: %+v", list)
	}
}
