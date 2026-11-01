package home

import (
	"fmt"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

func TestMachinesGroupsWorkersByName(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh, stale := now.Unix()-5, now.Unix()-int64(project.WorkerLease.Seconds())-1
	claude, codex := project.AgentProfile{ID: "claude", Name: "Claude Code"}, project.AgentProfile{ID: "codex", Name: "Codex"}
	got := Machines(now,
		[]project.EditingWorker{{ID: "w9", Name: "zeta", Seen: fresh, Agents: []project.AgentProfile{codex}}},
		[]project.EditingWorker{{ID: "w5", Name: "Sam-PC", Seen: stale, Agents: []project.AgentProfile{claude}},
			{ID: "w7", Name: "old", Seen: stale}},
		[]project.EditingWorker{{ID: "w2", Name: "sam-pc", Seen: fresh, Agents: []project.AgentProfile{codex, claude}},
			{ID: "w8", Name: "", Seen: fresh}},
	)
	want := "[{w2 Sam-PC true [{claude Claude Code} {codex Codex}]} {w8 Unnamed computer true []} " +
		"{w9 zeta true [{codex Codex}]} {w7 old false []}]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
	if m := Machines(now); m == nil || len(m) != 0 {
		t.Fatalf("no workers: %#v", m)
	}
}
