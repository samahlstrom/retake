package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/worker"
)

func workerCmd(ctx context.Context, args []string, env Env) int {
	var remote, root, name, profiles string
	f := flags{values: map[string]*string{"--remote": &remote, "--project": &root, "--name": &name, "--profiles": &profiles}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err != nil {
		return usageError(env, "worker", err)
	}
	if len(pos) != 0 || remote == "" || root == "" {
		return usageError(env, "worker", errors.New("use --remote NAME --project DIR [--name NAME] [--profiles FILE]"))
	}
	rs, _, err := loadRemotes(env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	r, ok := rs[remote]
	if !ok {
		return usageError(env, "worker", fmt.Errorf("remote %q does not exist; use retake remote add", remote))
	}
	cfg := loadConfig(env)
	ps := agentProfiles(env, cfg)
	if profiles != "" {
		ps = nil // --profiles replaces the configured and found agents
		b, e := os.ReadFile(absPath(profiles, env.Getenv))
		if e != nil {
			err = e
		} else {
			err = json.Unmarshal(b, &ps)
		}
		if err != nil {
			return usageError(env, "worker", err)
		}
		ps = openAgentsIn(ps, cfg)
	}
	err = worker.Run(ctx, worker.Options{URL: r.URL, Token: r.Token, Root: absPath(root, env.Getenv), Name: name,
		Profiles: ps, Editor: editorOf(env), Log: env.Stderr, Exts: localExts(env, cfg, nil, ""),
		Transcriber: fileTranscriber(cfg)})
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake worker: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// agentProfiles is what an editing worker may run: the config's [[agents]], then Claude Code and Codex on PATH unless
// find_agents = false or an entry has the same id. env.Agents replaces it all (tests).
func agentProfiles(env Env, cfg config.Config) []worker.Profile {
	if env.Agents != nil {
		return env.Agents()
	}
	var configured []worker.Profile
	for _, a := range cfg.Agents {
		configured = append(configured, worker.Profile{AgentProfile: project.AgentProfile{ID: a.ID, Name: a.Name,
			Model: a.Model}, Command: a.Command, Env: a.Env, OpenIn: a.OpenIn, Interactive: a.Interactive})
	}
	return defaultFirst(openAgentsIn(worker.Profiles(configured, cfg.FindAgents == nil || *cfg.FindAgents), cfg), cfg)
}

// defaultFirst moves the config's default_agent to the front, so a project's first poll picks it (spec/home.md,
// "Settings").
func defaultFirst(ps []worker.Profile, cfg config.Config) []worker.Profile {
	if cfg.DefaultAgent == nil || *cfg.DefaultAgent == "" {
		return ps
	}
	i := slices.IndexFunc(ps, func(p worker.Profile) bool { return p.ID == *cfg.DefaultAgent })
	if i <= 0 {
		return ps
	}
	out := append([]worker.Profile{ps[i]}, ps[:i]...)
	return append(out, ps[i+1:]...)
}

// openAgentsIn gives the profiles that don't say where they run the config's open_agents_in.
func openAgentsIn(ps []worker.Profile, cfg config.Config) []worker.Profile {
	if cfg.OpenAgentsIn == nil {
		return ps
	}
	for i := range ps {
		if ps[i].OpenIn == "" {
			ps[i].OpenIn = *cfg.OpenAgentsIn
		}
	}
	return ps
}

// reloadConfig reads the config file again (the settings page may have changed it since the start), keeping cfg when
// it can't be read now. Tests that give env.Agents never read it.
func reloadConfig(env Env, cfg config.Config) config.Config {
	if env.Agents != nil {
		return cfg
	}
	path, err := config.Path(env.Getenv)
	if err != nil {
		return cfg
	}
	c, _, err := config.Load(path)
	if err != nil {
		return cfg
	}
	return c
}

// localWorker is server.Options.LocalWorker for retake open and share: each project they serve gets its editing
// worker in this process, named after the machine (spec/automation.md, "Local mode").
func localWorker(env Env, cfg config.Config, exts *ext.Set) func(context.Context, string, http.Handler, func(string)) {
	return func(ctx context.Context, root string, h http.Handler, blocked func(string)) {
		worker.Local(ctx, worker.Options{Root: root, Editor: editorOf(env), Log: env.Stderr, Exts: exts,
			Transcriber: fileTranscriber(cfg)},
			func() []worker.Profile { return agentProfiles(env, reloadConfig(env, cfg)) }, h, blocked)
	}
}
