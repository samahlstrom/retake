package cli

import (
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/desktop"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/userdir"
)

// localSettings is what retake open <folder>'s settings page reads and writes (spec/home.md, "Settings"): the config
// file, the theme folder in effect, the agents this computer offers and the extensions installed here.
func localSettings(env Env, cfg config.Config, themeDir string, flagDirs []string, folder string) *hub.LocalSettings {
	path, err := config.Path(env.Getenv)
	if err != nil {
		path = ""
	}
	return &hub.LocalSettings{
		ConfigPath: path,
		ThemeDir:   themeDir,
		Home:       userdir.Home(env.Getenv),
		Try:        tryAgent(env, cfg),
		Places: func() []string {
			find := env.Find
			if find == nil {
				find = desktop.Find
			}
			if _, _, err := find("tmux"); err != nil {
				return []string{config.OpenHerdr, config.OpenBackground}
			}
			return []string{config.OpenHerdr, config.OpenTmux, config.OpenBackground}
		},
		Agents: func() []project.AgentProfile {
			var out []project.AgentProfile
			for _, p := range agentProfiles(env, reloadConfig(env, cfg)) {
				out = append(out, p.AgentProfile)
			}
			return out
		},
		Extensions: func() []*ext.Ext {
			user, err := ext.UserDir(env.Getenv)
			if err != nil {
				user = ""
			}
			return ext.Discover(ext.Sources{Flags: absDirs(env, ext.FlagDirs(flagDirs, env.Getenv)), User: user,
				UserWhere: "user", Bundled: bundled(), Retake: Version(), Project: folder})
		},
	}
}
