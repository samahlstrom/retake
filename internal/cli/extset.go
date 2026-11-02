package cli

import (
	"fmt"
	"io/fs"

	"github.com/samahlstrom/retake/extensions"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// The extensions in effect for a command (spec/extensions.md, "Where extensions are found" and "Turning them on").

// localExts is the extensions of retake open, share, connect and worker: --ext's folders and RETAKE_EXT_PATH's, the
// opened folder's own (pinned), the user's and the bundled ones, turned on by config.toml's [extensions] (and --ext
// for its own). Problems are warned about on stderr; they never stop the command.
func localExts(env Env, cfg config.Config, flagDirs []string, folder string) *ext.Set {
	user, err := ext.UserDir(env.Getenv)
	if err != nil {
		user = ""
	}
	src := ext.Sources{Flags: absDirs(env, ext.FlagDirs(flagDirs, env.Getenv)), User: user, UserWhere: "user",
		Bundled: bundled(), Retake: Version()}
	if folder != "" {
		src.Project = folder
	}
	set, warnings := ext.Resolve(ext.Discover(src), cfg.Extensions.Enabled, cfg.Extensions.Pins)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(env.Stderr, "retake: %s\n", w)
	}
	return set
}

// hostedExts is retake serve's: only what the admin allows, from the server's own install (its data folder's and the
// bundled ones, and --ext's while developing one), never a project's.
func hostedExts(env Env, data string, flagDirs, allowed []string) *ext.Set {
	src := ext.Sources{Flags: absDirs(env, ext.FlagDirs(flagDirs, env.Getenv)), User: ext.DataDir(data),
		UserWhere: "data", Bundled: bundled(), Retake: Version()}
	set, warnings := ext.Resolve(ext.Discover(src), allowed, nil)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(env.Stderr, "retake: %s\n", w)
	}
	return set
}

// bundled is the extensions built into retake: extensions/ (Scripts).
func bundled() fs.FS { return extensions.FS }

func absDirs(env Env, dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, absPath(d, env.Getenv))
	}
	return out
}

// fileTranscriber is the transcriber of [transcribe], asked for each owned audio file (a talk-out).
func fileTranscriber(cfg config.Config) func() (transcribe.Transcriber, error) {
	return func() (transcribe.Transcriber, error) { return transcribe.FromConfig(cfg) }
}
