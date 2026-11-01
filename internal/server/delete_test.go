package server

import (
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

func TestDeleteAndRestoreComment(t *testing.T) {
	h, f := startM2(t, Options{})
	st := seed(t, f)
	for _, action := range []string{"delete", "restore"} {
		for _, role := range []string{"viewer", "reviewer", "agent"} {
			token := makeToken(t, h, role, "Guest", 0)
			r := h.post("/api/notes/1/"+action, "", map[string]string{"Authorization": "Bearer " + token})
			if r.code != 403 {
				t.Fatalf("%s %s: %d %s", role, action, r.code, r.body)
			}
		}
		r := h.post("/api/notes/1/"+action, "", map[string]string{"Origin": "http://elsewhere.example"})
		if r.code != 403 {
			t.Fatal(r)
		}
		if r = h.post("/api/notes/1/"+action, "", h.origin()); r.code != 200 {
			t.Fatal(r)
		}
		n, _ := st.Note(1)
		want := project.StatusRetracted
		if action == "restore" {
			want = project.StatusAnswered
		}
		if n.Status != want {
			t.Fatal(n)
		}
	}
	if r := h.post("/api/notes/99/delete", "", nil); r.code != 404 {
		t.Fatal(r)
	}
}
