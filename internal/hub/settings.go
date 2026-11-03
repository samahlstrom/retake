package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/setup"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/theme"
	"github.com/samahlstrom/retake/internal/worker"
)

// The settings page (spec/home.md, "Settings"): GET and POST api/settings at the home, for the owner only. Locally
// (retake open <folder>) they are the user's config file; on the team server, the server's own settings. The default
// agent and its fallback also go to every project's automation config, which is where a task's agent comes from.

// LocalSettings is what retake open's settings read and write. The CLI fills it in.
type LocalSettings struct {
	ConfigPath string // config.toml, where changes go ($XDG_CONFIG_HOME/retake/config.toml)
	ThemeDir   string // the theme folder in effect; "" = theming is off
	// Agents are the agents this computer offers (the local worker's profiles, with the config read again).
	Agents func() []project.AgentProfile
	// Extensions are the extensions installed here (on or not); nil = none.
	Extensions func() []*ext.Ext
	// Home is the user's home folder, which paths show as ~ ("" = none).
	Home string
	// SetFolder makes retake start serve another videos folder (spec/setup.md); nil under retake open <folder>, where
	// the folder is its argument (read-only).
	SetFolder func(dir string) error
	// Try runs an agent's test (worker.TryAgent); nil: no Test.
	Try func(ctx context.Context, id string) worker.TryResult
	// Places are where agents can run on this computer (config.Open*: herdr, tmux when it's installed, background);
	// nil: all three.
	Places func() []string
}

// SettingsView is GET api/settings.
type SettingsView struct {
	Hosted bool `json:"hosted"`
	// Agents are the agents to choose from: this computer's (local), every connected computer's (hosted).
	Agents   []home.MachineAgent `json:"agents"`
	Agent    string              `json:"agent"`    // the default agent; "" = each computer's first
	Fallback string              `json:"fallback"` // "" = pause and ask
	// OpenIn is where agents run (background, herdr, tmux). On the team server it's each computer's own setting.
	OpenIn         string `json:"open_in"`
	OpenInEditable bool   `json:"open_in_editable"`
	// OpenInChoices are the places to offer here, in order: tmux only where it's installed (never on Windows).
	OpenInChoices []string `json:"open_in_choices"`
	// Extensions: the installed ones, on or off. Loaded is whether it's in effect now (a change applies at restart).
	Extensions         []SettingsExt  `json:"extensions"`
	ExtensionsEditable bool           `json:"extensions_editable"`
	Theme              SettingsTheme  `json:"theme"`
	Config             string         `json:"config,omitempty"` // the config file (local)
	Machines           []home.Machine `json:"machines"`
	// Folder is the videos folder (local; spec/setup.md). FolderEditable: retake start can switch it.
	Folder         string `json:"folder,omitempty"`
	FolderEditable bool   `json:"folder_editable"`
	// Test says the agents can be tested here (POST api/settings/test).
	Test bool `json:"test"`
	// Sharing is how a link made in the page reaches people (local only; spec/home.md, "Settings").
	Sharing *SettingsSharing `json:"sharing,omitempty"`
}

// SettingsExt is one extension on the settings page.
type SettingsExt struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	On          bool   `json:"on"`
	Loaded      bool   `json:"loaded"`
}

// SettingsTheme is the theme folder: where it is and whether one is there.
type SettingsTheme struct {
	Folder string `json:"folder"` // "" = theming is off
	Used   bool   `json:"used"`
	Name   string `json:"name,omitempty"`
}

// settingsChange is POST api/settings: only the fields given change.
type settingsChange struct {
	Agent      *string         `json:"agent"`
	Fallback   *string         `json:"fallback"`
	OpenIn     *string         `json:"open_in"`
	Extensions map[string]bool `json:"extensions"`
	Folder     *string         `json:"folder"`
	// ShareTunnel is "cloudflare" (a link may start a tunnel) or "off" ([share] tunnel).
	ShareTunnel *string `json:"share_tunnel"`
}

// readChange decodes a POST body: JSON, no unknown keys, at most 16 KiB.
func readChange(w http.ResponseWriter, r *http.Request) (settingsChange, error) {
	var c settingsChange
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, errors.New(`give {"agent", "fallback", "open_in", "extensions": {"<id>": true|false}, "folder"}`)
	}
	if dec.More() {
		return c, errors.New("one JSON object")
	}
	if len(c.Extensions) > 64 {
		return c, errors.New("too many extensions")
	}
	return c, nil
}

// checkAgents checks a change's agent and fallback against the agents offered, given the current ones.
func checkAgents(c settingsChange, offered []home.MachineAgent, agent, fallback string) (string, string, error) {
	has := func(id string) bool {
		return slices.ContainsFunc(offered, func(a home.MachineAgent) bool { return a.ID == id })
	}
	if c.Agent != nil {
		if *c.Agent != "" && !has(*c.Agent) {
			return "", "", fmt.Errorf("no agent %q here", *c.Agent)
		}
		agent = *c.Agent
	}
	if c.Fallback != nil {
		if *c.Fallback != "" && !has(*c.Fallback) {
			return "", "", fmt.Errorf("no agent %q here", *c.Fallback)
		}
		fallback = *c.Fallback
	}
	if fallback != "" && fallback == agent {
		if c.Fallback == nil {
			fallback = "" // a new default agent that was the fallback: no fallback
		} else {
			return "", "", errors.New("the fallback is another agent than the default")
		}
	}
	return agent, fallback, nil
}

// applyAgents sets a project's agent and fallback when its computer offers them: the default agent (when one is
// chosen) and the fallback ("" pauses and asks). A project no computer polled yet takes it at its first poll.
func applyAgents(st *project.Store, agent, fallback string) error {
	return st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		w := a.Worker(a.Config.Worker)
		if w == nil {
			return false, nil
		}
		changed := false
		if agent != "" && w.HasAgent(agent) && a.Config.Agent != agent {
			a.Config.Agent, changed = agent, true
		}
		fb := fallback
		if fb != "" && (!w.HasAgent(fb) || fb == a.Config.Agent) {
			fb = ""
		}
		if a.Config.Fallback != fb {
			a.Config.Fallback, changed = fb, true
		}
		return changed, nil
	})
}

// agentsOf is every agent the machines offer, once each, in order.
func agentsOf(ms []home.Machine) []home.MachineAgent {
	out := []home.MachineAgent{}
	for _, m := range ms {
		for _, a := range m.Agents {
			if !slices.ContainsFunc(out, func(x home.MachineAgent) bool { return x.ID == a.ID }) {
				out = append(out, a)
			}
		}
	}
	return out
}

// themeInfo says where the theme folder is and whether a theme is there.
func themeInfo(dir, homeDir string) SettingsTheme {
	t := SettingsTheme{Folder: tilde(dir, homeDir)}
	if th := theme.Load(dir); th != nil {
		t.Used, t.Name = true, th.Settings.Name
	}
	return t
}

// tilde shows a path under the home folder with ~.
func tilde(p, homeDir string) string {
	if homeDir == "" || p == "" {
		return p
	}
	if rel, err := filepath.Rel(homeDir, p); err == nil && filepath.IsLocal(rel) {
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}

// --- retake open <folder>

// settingsRoute is GET and POST api/settings of retake open <folder>: the owner's own config file. sub is "",
// "test" (POST: an agent's test) or "folders" (GET: the folder list of spec/setup.md).
func (f *Folder) settingsRoute(w http.ResponseWriter, r *http.Request, role team.Role, sub string) {
	ls := f.opts.Settings
	switch {
	case role != team.Owner:
		writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "only the owner changes the settings"})
		return
	case ls == nil:
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no settings here"})
		return
	case sub == "test" && r.Method == http.MethodPost && ls.Try != nil:
		setup.TryRoute(w, r, profilesOf(f.localAgents(ls)), ls.Try, f.trying, &f.tryMu)
		return
	case sub == "folders" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		setup.FoldersRoute(w, r, ls.Home)
		return
	case sub != "":
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	case r.Method == http.MethodPost:
		if err := f.changeSettings(w, r, ls); err != nil {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		writeJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
		return
	}
	v, err := f.settingsView(ls)
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, r, http.StatusOK, v)
}

// localConfig reads the config file now ("" path: an empty config).
func localConfig(ls *LocalSettings) (config.Config, error) {
	if ls.ConfigPath == "" {
		return config.Config{}, nil
	}
	c, _, err := config.Load(ls.ConfigPath)
	if err != nil {
		return c, fmt.Errorf("%s: %w", ls.ConfigPath, err)
	}
	return c, nil
}

// localAgents is the agents this computer offers.
func (f *Folder) localAgents(ls *LocalSettings) []home.MachineAgent {
	if ls.Agents == nil {
		return agentsOf(f.machines())
	}
	out := []home.MachineAgent{}
	for _, a := range ls.Agents() {
		out = append(out, home.MachineAgent{ID: a.ID, Name: a.Name})
	}
	return out
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (f *Folder) settingsView(ls *LocalSettings) (SettingsView, error) {
	c, err := localConfig(ls)
	if err != nil {
		return SettingsView{}, err
	}
	v := SettingsView{Agents: f.localAgents(ls), Agent: str(c.DefaultAgent), Fallback: str(c.FallbackAgent),
		OpenIn: str(c.OpenAgentsIn), OpenInEditable: ls.ConfigPath != "", ExtensionsEditable: ls.ConfigPath != "",
		Theme: themeInfo(ls.ThemeDir, ls.Home), Config: tilde(ls.ConfigPath, ls.Home), Machines: f.machines(),
		Extensions: []SettingsExt{}, Folder: tilde(f.opts.Dir, ls.Home), FolderEditable: ls.SetFolder != nil,
		Test: ls.Try != nil}
	if v.OpenIn == "" {
		v.OpenIn = config.OpenBackground
	}
	v.OpenInChoices = []string{config.OpenHerdr, config.OpenTmux, config.OpenBackground}
	if ls.Places != nil {
		v.OpenInChoices = ls.Places()
	}
	if !slices.Contains(v.OpenInChoices, v.OpenIn) {
		v.OpenInChoices = append(v.OpenInChoices, v.OpenIn) // what the config says, even if it's not here now
	}
	v.Sharing = sharingView(c, f.opts.Share.Cloudflared)
	if ls.Extensions != nil {
		seen := map[string]bool{}
		for _, e := range ls.Extensions() {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			v.Extensions = append(v.Extensions, SettingsExt{ID: e.ID, Name: extName(e), Description: e.Description,
				On: slices.Contains(c.Extensions.Enabled, e.ID), Loaded: f.opts.Exts.Get(e.ID) != nil})
		}
	}
	return v, nil
}

func extName(e *ext.Ext) string {
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}

// changeSettings validates a change in full, then writes it to the config file and applies the agents to the
// projects.
func (f *Folder) changeSettings(w http.ResponseWriter, r *http.Request, ls *LocalSettings) error {
	c, err := readChange(w, r)
	if err != nil {
		return err
	}
	if ls.ConfigPath == "" {
		return errors.New("there is no config file to write")
	}
	cur, err := localConfig(ls)
	if err != nil {
		return err
	}
	agent, fallback, err := checkAgents(c, f.localAgents(ls), str(cur.DefaultAgent), str(cur.FallbackAgent))
	if err != nil {
		return err
	}
	if c.OpenIn != nil && !config.ValidOpenIn(*c.OpenIn) {
		return fmt.Errorf("open_in %q isn't background, herdr or tmux", *c.OpenIn)
	}
	if c.ShareTunnel != nil && *c.ShareTunnel != ShareTunnelOn && *c.ShareTunnel != ShareTunnelOff {
		return fmt.Errorf("share_tunnel %q isn't cloudflare or off", *c.ShareTunnel)
	}
	installed := map[string]*ext.Ext{}
	if ls.Extensions != nil {
		for _, e := range ls.Extensions() {
			if installed[e.ID] == nil {
				installed[e.ID] = e
			}
		}
	}
	for id := range c.Extensions {
		if installed[id] == nil {
			return fmt.Errorf("no extension %q is installed", id)
		}
	}
	folder := ""
	if c.Folder != nil {
		if ls.SetFolder == nil {
			return errors.New("this folder was opened with retake open; the videos folder is retake start's")
		}
		if folder, err = setup.CheckFolder(*c.Folder, ls.Home, false); err != nil {
			return err
		}
	}
	// Valid: write it.
	if c.Agent != nil || c.Fallback != nil {
		if err := config.SetTop(ls.ConfigPath, "default_agent", agent); err != nil {
			return err
		}
		if err := config.SetTop(ls.ConfigPath, "fallback_agent", fallback); err != nil {
			return err
		}
	}
	if c.OpenIn != nil {
		v := *c.OpenIn
		if v == config.OpenBackground {
			v = "" // the default: no line
		}
		if err := config.SetTop(ls.ConfigPath, "open_agents_in", v); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(c.Extensions))
	for id := range c.Extensions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		e, pin := installed[id], ""
		if e.Where == ext.WhereProject {
			pin = e.Hash
		}
		if err := config.SetExtension(ls.ConfigPath, id, c.Extensions[id], pin); err != nil {
			return err
		}
	}
	if folder != "" {
		if err := config.SetTop(ls.ConfigPath, "folder", tilde(folder, ls.Home)); err != nil {
			return err
		}
	}
	if c.ShareTunnel != nil {
		off := *c.ShareTunnel == ShareTunnelOff
		if err := config.SetShareTunnel(ls.ConfigPath, off); err != nil {
			return err
		}
		f.menus.sharing.SetOff(off) // the next link goes by it at once
	}
	if c.Agent != nil || c.Fallback != nil {
		for _, fd := range f.rescan(false) {
			fp := f.projectOf(fd)
			if fp == nil {
				continue
			}
			if st := fp.projectStore(); st != nil {
				if err := applyAgents(st, agent, fallback); err != nil {
					f.logf("settings: %s: %v", fd.Name, err)
				}
			}
		}
	}
	if folder != "" && !family.SamePath(folder, f.opts.Dir) {
		// retake start serves the new folder once this answer is out (the page reloads to it).
		go func() { _ = ls.SetFolder(folder) }()
	}
	return nil
}

// profilesOf are the agents as profiles (their id and name).
func profilesOf(as []home.MachineAgent) []project.AgentProfile {
	out := make([]project.AgentProfile, 0, len(as))
	for _, a := range as {
		out = append(out, project.AgentProfile{ID: a.ID, Name: a.Name})
	}
	return out
}

// --- the team server

// settingsKey is the team server's settings row: {"agent", "fallback"}.
const settingsKey = "settings/agents"

type hostedAgents struct {
	Agent    string `json:"agent"`
	Fallback string `json:"fallback"`
}

func (h *Hub) hostedAgents() hostedAgents {
	var a hostedAgents
	if v, err := h.st.Accounts.Setting(settingsKey); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &a)
	}
	return a
}

// settingsRoute is GET and POST api/settings on the team server: admins only (the home's owner).
func (h *Hub) settingsRoute(w http.ResponseWriter, r *http.Request, s *session) {
	if !s.user.Admin {
		h.fail(w, r, http.StatusForbidden, "only the owner changes the settings")
		return
	}
	ps, err := h.st.Accounts.ProjectsOf(s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	var stores []*project.Store
	var workers [][]project.EditingWorker
	for _, p := range ps {
		if p.Role != team.Owner {
			continue
		}
		pr, err := h.project(p.Name)
		if err != nil {
			h.logf("project %s: %v", p.Name, err)
			continue
		}
		stores = append(stores, pr.store)
		if a, err := pr.store.Automation(); err == nil {
			workers = append(workers, a.Workers)
		}
	}
	machines := home.Machines(h.opts.Now(), workers...)
	cur := h.hostedAgents()
	switch r.Method {
	case http.MethodPost:
		c, err := readChange(w, r)
		if err == nil && c.OpenIn != nil {
			err = errors.New("where agents run is each computer's own setting (open_agents_in in its config.toml)")
		}
		if err == nil && c.Folder != nil {
			err = errors.New("the videos folder is each computer's own setting")
		}
		if err == nil && len(c.Extensions) > 0 {
			err = errors.New("the server's extensions are set where it runs (--extensions, RETAKE_EXTENSIONS)")
		}
		if err == nil && c.ShareTunnel != nil {
			err = errors.New("the team server's links use its own address")
		}
		var agent, fallback string
		if err == nil {
			agent, fallback, err = checkAgents(c, agentsOf(machines), cur.Agent, cur.Fallback)
		}
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, err.Error())
			return
		}
		cur = hostedAgents{Agent: agent, Fallback: fallback}
		raw, _ := json.Marshal(cur)
		if err := h.st.Accounts.SetSetting(settingsKey, string(raw)); err != nil {
			h.storeFailed(w, r, err)
			return
		}
		for _, st := range stores {
			if err := applyAgents(st, agent, fallback); err != nil {
				h.logf("settings: %v", err)
			}
		}
	case http.MethodGet, http.MethodHead:
	default:
		h.fail(w, r, http.StatusMethodNotAllowed, "GET or POST")
		return
	}
	v := SettingsView{Hosted: true, Agents: agentsOf(machines), Agent: cur.Agent, Fallback: cur.Fallback,
		OpenIn: config.OpenBackground, Theme: themeInfo(h.opts.Theme, ""), Machines: machines,
		Extensions: []SettingsExt{}}
	for _, e := range h.opts.Exts.List() {
		v.Extensions = append(v.Extensions, SettingsExt{ID: e.ID, Name: extName(e), Description: e.Description,
			On: true, Loaded: true})
	}
	h.json(w, r, http.StatusOK, v)
}
