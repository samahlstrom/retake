package hub

import (
	"path/filepath"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// In retake open <folder>, the in-process worker registers through the normal worker path, so it shows up in the
// home's machines like a connected computer: online while it polls.
func TestFolderHomeMachines(t *testing.T) {
	x := newFT(t, nil)
	if h := x.home(t); h.Machines == nil || len(h.Machines) != 0 {
		t.Fatalf("no workers yet: %#v", h.Machines)
	}
	for _, p := range []string{"alpha", "bravo"} {
		st, err := project.Open(filepath.Join(x.dir, p))
		if err != nil {
			t.Fatal(err)
		}
		w := project.EditingWorker{ID: "w-" + p, Name: "sam-pc", Session: "s-" + p,
			Agents: []project.AgentProfile{{ID: "claude", Name: "Claude Code"}}}
		if _, err := st.PollWork(w, "", ""); err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
	}
	h := x.home(t)
	if len(h.Machines) != 1 || h.Machines[0].Name != "sam-pc" || !h.Machines[0].Online || h.Machines[0].ID != "w-alpha" ||
		len(h.Machines[0].Agents) != 1 || h.Machines[0].Agents[0].Name != "Claude Code" {
		t.Fatalf("machines: %+v", h.Machines)
	}
	// A share's guest doesn't see them.
	tok := homeToken(t, x.dir, team.Reviewer, "guest")
	if g := x.home(t, shared(), bearerTok(tok)); g.Machines == nil || len(g.Machines) != 0 {
		t.Fatalf("guest machines: %#v", g.Machines)
	}
}
