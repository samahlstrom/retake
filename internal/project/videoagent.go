package project

import "fmt"

// An agent for one video (spec/automation.md): the owner's pick for this video, which a task queued without an agent
// takes at its claim. A held Send's agent still wins for its own send, and Config.Agent (Settings' default) applies
// when the video has none.

// SetVideoAgent sets the video's own agent ("" clears it). The selected worker (before one is selected, any worker)
// must offer it, else nothing changes.
func (s *Store) SetVideoAgent(agent string) error {
	return s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		if agent != "" && !a.offers(agent) {
			return false, fmt.Errorf("no computer here offers the agent %q", agent)
		}
		if a.VideoAgent == agent {
			return false, nil
		}
		a.VideoAgent = agent
		return true, nil
	})
}

// offers reports whether the selected worker, or any worker before one is selected, offers an agent.
func (a *AutomationState) offers(agent string) bool {
	for i := range a.Workers {
		w := &a.Workers[i]
		if (a.Config.Worker == "" || w.ID == a.Config.Worker) && w.HasAgent(agent) {
			return true
		}
	}
	return false
}
