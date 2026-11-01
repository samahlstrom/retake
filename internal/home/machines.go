package home

import (
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// Machine is a computer that edits the home's projects (spec/home.md, "machines").
type Machine struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Online bool           `json:"online"`
	Agents []MachineAgent `json:"agents"`
}

// MachineAgent is an agent a machine offers.
type MachineAgent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Machines groups the workers of the projects' stores (their heartbeats) into machines by name: a computer runs one
// worker per project. A machine is online while one of its workers polled within the worker lease. Online machines
// come first, then by name. It never returns nil.
func Machines(now time.Time, projects ...[]project.EditingWorker) []Machine {
	byKey := map[string]*Machine{}
	var order []string
	lease := int64(project.WorkerLease.Seconds())
	for _, workers := range projects {
		for _, w := range workers {
			key := strings.ToLower(strings.TrimSpace(w.Name))
			if key == "" {
				key = "\x00" + w.ID
			}
			m := byKey[key]
			if m == nil {
				name := strings.TrimSpace(w.Name)
				if name == "" {
					name = "Unnamed computer"
				}
				m = &Machine{ID: w.ID, Name: name, Agents: []MachineAgent{}}
				byKey[key] = m
				order = append(order, key)
			}
			if w.ID < m.ID {
				m.ID = w.ID
			}
			if w.Seen+lease > now.Unix() {
				m.Online = true
			}
			for _, a := range w.Agents {
				if !slices.ContainsFunc(m.Agents, func(x MachineAgent) bool { return x.ID == a.ID }) {
					m.Agents = append(m.Agents, MachineAgent{ID: a.ID, Name: a.Name})
				}
			}
		}
	}
	out := make([]Machine, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	slices.SortStableFunc(out, func(a, b Machine) int {
		if a.Online != b.Online {
			if a.Online {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}
