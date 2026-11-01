// Package cli is the retake command line: open, sessions, version. cmd/retake only wires it to the process.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/server"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2 // bad arguments, a missing file, a busy --port, no free port
)

// version is set at build time: -ldflags "-X github.com/samahlstrom/retake/internal/cli.version=v0.1.0".
var version = ""

// Env is everything the CLI touches outside its arguments. Zero fields get the real thing.
type Env struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	OpenURL        func(url string) error // the OS opener
	PortTries      int                    // ports tried from the default one when it's busy; 0 = 20
	Ready          func(url string)       // called once the server listens (tests)
	Prober         family.Prober          // nil = ffprobe
	Clock          server.Clock           // nil = the real clock
	CheckEvery     time.Duration          // idle watchdog period; 0 = 10 s
	Stdin          io.Reader              // retake mcp reads its requests here; nil = os.Stdin
	// Engines returns the notes engine and the transcriber for the jobs and `retake notes build`; nil = app.Engines.
	Engines func(config.Config) jobs.Engines
	// SharePing is how often `retake share` pings the server it attached to; 0 = DefaultSharePing.
	SharePing time.Duration
}

func (e Env) withDefaults() Env {
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Stdin == nil {
		e.Stdin = os.Stdin
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.OpenURL == nil {
		e.OpenURL = openURL
	}
	return e
}

const usage = `retake: review a video by talking over it. Press R, talk and draw, press R again.

usage:
  retake open <video> [--versions a.mp4 b.mp4 ...] [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--allow-host H]... [--verbose]
      serve the review page for <video> and its version family (clip_v5, v6/clip_v6, ...)
  retake open <folder> [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--allow-host H]... [--verbose]
      review many videos: every project in <folder> and its folders, with a home screen of them all at /
  retake sessions <video> [--json]
      list the takes saved for <video>
  retake notes build <video | .review dir | take dir> [--window 2] [--max-span 8] [--video-transcript F] [--json]
      turn the takes into <stem>.review/notes.md, notes.json and snaps/ (reads transcript.json; never transcribes)
  retake version
      print the version
  retake assets <import|search|get|preview|span|set|resolve|license-report|plugins|plugin|kit|sql|mcp> ...
      the asset catalog: footage, audio, images, fonts, spans and licenses (retake assets help)
  retake lib <search|add|list|describe|pack|recipe export|lint> ...
      components, brand kits and edit recipes as shareable packages (retake lib help)

the agent loop (spec/notes.md; <video> is any version of the family, and every command takes --json):
  retake notes list <video> [--status S]... [--version vN]
      the notes: text, times, author, strokes, images, threads; and a cursor for notes wait
  retake notes wait <video> [--since CURSOR] [--timeout S]
      block until notes turn open or answers arrive after the cursor (0: from the start; timeout 0: no limit)
  retake notes sync <video>
      transcribe the takes that need it, run the notes engine for <video>'s version, and sync the notes
  retake ask <video> <note> "question" [--option vN]... [--version vN]
      ask the reviewer about a note (note 0: about a version as a whole)
  retake note <video> <id> --status answered --in vN --change "what changed"
      mark a note answered by a version that's already in the family
  retake push <video> <file> [--parent vN] [--label vN] [--change ID="summary"]... [--ask ID="question"]... [--move]
      put <file> in the family as the next v<N>. Every note open on the parent must be in --change or --ask
  retake push <video> --render [--draft] [--no-stream] [--export] [--parent vN] [--label vN] [--change ID="..."]...
      a timeline project (spec/bridge.md): render the timeline with retake-edit and push it with its render
      manifest; the patches since the parent answer their notes, so their changelog is written for you. It
      streams: v<N> is a playlist in its v<N>/ folder that plays within seconds and fills in as units render
      (--no-stream: one MP4, pushed when done; --export: also an MP4 beside the playlist at the end)
  retake versions <video>
      the versions, their parents and changelogs
  retake mcp <video>
      serve the same tools over MCP on stdin and stdout
  retake watch <video> --exec CMD [--since CURSOR] [--debounce S]
      run CMD through the shell whenever notes or answers land (one at a time), with RETAKE_VIDEO,
      RETAKE_PROJECT and RETAKE_SINCE set; Ctrl+C stops

sharing (spec/team.md; links and tokens belong to the project of <video>):
  retake share <video> [--role reviewer] [--expires 24h] [--label NAME] [--tunnel cloudflare|tailscale|none]
               [--host H] [--funnel] [--tunnel-bin PATH] [--port N] [--no-qr] [--json] [--verbose]
      share the review over an HTTPS tunnel: attaches to (or starts) the server and prints a link with a QR
      code. Roles: viewer (watch), reviewer (and record), approver (and approve, reopen). Ctrl+C stops sharing.
      Give a folder instead of a video to share its home: one link for every video in it (spec/home.md)
  retake share list <video> [--json]
      the project's tokens and running shares
  retake share revoke <video> <id> [--json]
      end a token now
  retake token create <video> --role viewer|reviewer|approver|agent [--label NAME] [--expires 30d] [--host H] [--json]
      make a token without a tunnel. For an agent, print the Claude Code and Codex setup for the /mcp route
  retake token list|revoke ...
      the same as share list and share revoke

the team server (spec/server.md; settings also come from RETAKE_* variables and config.toml [server]):
  retake serve --data DIR [--listen :8080] [--public-url URL] [--trust-proxy CIDR]... [--database postgres://...]
               [--s3-endpoint HOST:PORT [--s3-bucket B] [--s3-region R] [--s3-insecure]] [--ui-dir DIR] [--verbose]
      host many projects for a team: sign-in links, members, share links, uploads and remote MCP at /p/<project>/
  retake user add <email> [--name NAME] [--admin] [--expires 24h] [--json]
      add a person and print their one-time sign-in link (mailed instead when RETAKE_SMTP_URL is set)
  retake user link|remove|disable|enable|signout|sessions <email>   and   retake user list
      a new sign-in link; remove or disable someone; end their sessions; list them
  retake project add <name> [--title T] [--video FILE | --from FOLDER]   and   retake project list
      add a project (with its first version, or a copy of a retake project folder)
  retake project member add <project> <email> --role viewer|reviewer|approver|owner   (also remove, list)
  retake project token create <project> --role viewer|reviewer|approver|agent [--label L] [--expires 30d]
      a share link, or an agent token with its remote and MCP setup (also token list, token revoke <id>)
      every server command takes --data DIR and the database flags of retake serve
  retake remote add <name> <project URL> --token <agent token>   (also remote list, remote remove <name>)
  retake worker --remote NAME --project DIR [--name NAME] [--profiles FILE]
      connect this editing machine: run assigned agents and stream live source edits through the hosted project
  retake push --remote <name> <file> [--parent vN] [--label vN] [--change ID="summary"]... [--ask ID="q"]...
      upload <file> to the team server (resumable) and push it, with the same push rule

open:
  --versions F...   the family is exactly <video> plus these files (default: detect *_vN files)
  --port N          serve on port N, or exit 2 if it's busy (default 8777, or the next free one up to 8796)
  --no-open         don't open the page in the browser
  --idle-exit MIN   exit MIN minutes after the page is gone; 0 = never (default 15)
  --ui-dir DIR      serve the page from DIR instead of the one built into the binary
  --allow-host H    also answer at https://H, e.g. a reverse proxy's hostname (repeatable). Every request there
                    needs a token (retake token create); retake share does all of this for you
  --verbose         log every request

notes build:
  --window S        seconds between comments that still count as one moment (default 2)
  --max-span S      longest a moment may get, in video seconds (default 8)
  --video-transcript F  the edit's own voice-over words (JSON), quoted under (audio) comments

config: $XDG_CONFIG_HOME/retake/config.toml (default ~/.config/retake/config.toml), keys name, port,
idle_exit_minutes, open_browser, [transcribe] and [edit] retake_edit. Flags override it.
`

// Main runs the command line and returns the exit code.
func Main(ctx context.Context, args []string, env Env) int {
	env = env.withDefaults()
	if len(args) == 0 {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	switch args[0] {
	case "open":
		return openCmd(ctx, args[1:], env)
	case "sessions":
		return sessionsCmd(args[1:], env)
	case "notes":
		// notes list|wait|sync: the agent operations (internal/agent); notes build and help: the engine by hand.
		if len(args) < 2 || (args[1] != "list" && args[1] != "wait" && args[1] != "sync") {
			return notesCmd(ctx, args[1:], env)
		}
		return notesAgentCmd(ctx, args[1:], env)
	case "ask":
		return askCmd(ctx, args[1:], env)
	case "note":
		return noteCmd(ctx, args[1:], env)
	case "push":
		if pushesRemote(args[1:]) {
			return remotePushCmd(ctx, args[1:], env) // to a team server (spec/server.md)
		}
		return pushCmd(ctx, args[1:], env)
	case "versions":
		return versionsCmd(ctx, args[1:], env)
	case "mcp":
		return mcpCmd(ctx, args[1:], env)
	case "watch":
		return watchCmd(ctx, args[1:], env)
	case "transcribe":
		return transcribeCmd(ctx, args[1:], env)
	case "share":
		return shareCmd(ctx, args[1:], env)
	case "token":
		return tokenCmd(ctx, args[1:], env)
	case "assets":
		return assetsCmd(ctx, args[1:], env)
	case "lib":
		return libCmd(ctx, args[1:], env)
	case "serve":
		return serveCmd(ctx, args[1:], env)
	case "user":
		return userCmd(ctx, args[1:], env)
	case "project":
		return projectCmd(ctx, args[1:], env)
	case "remote":
		return remoteCmd(ctx, args[1:], env)
	case "worker":
		return workerCmd(ctx, args[1:], env)
	case "version", "--version":
		_, _ = fmt.Fprintf(env.Stdout, "retake %s\n", Version())
		return ExitOK
	case "help", "-h", "--help":
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stderr, "retake: unknown command %q\n\n%s", args[0], usage)
	return ExitUsage
}

// Version returns the build's version, semver without a leading "v": the -X value (`make build` and the release
// builds set it from scripts/version.sh), else the module version (`go install <module>/cmd/retake@v0.1.0` gives
// 0.1.0), else "dev-" and the VCS revision, else "dev".
func Version() string {
	bi, _ := debug.ReadBuildInfo() // nil when the binary has none
	return versionOf(version, bi)
}

// versionOf picks the version from the -X value and the build info (nil when there is none).
func versionOf(ldflag string, bi *debug.BuildInfo) string {
	if ldflag != "" {
		return strings.TrimPrefix(ldflag, "v")
	}
	if bi == nil {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev-" + rev
}

// absPath expands a leading ~, makes p absolute and resolves symlinks when the file exists (Python's
// Path.expanduser().resolve()).
func absPath(p string, getenv func(string) string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home := getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home != "" {
			p = filepath.Join(home, p[1:])
		}
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// fmtDur formats seconds like the legacy player: m:ss.s, or h:mm:ss.s from an hour; "?" for none or zero.
func fmtDur(t float64, ok bool) string {
	if !ok || t == 0 {
		return "?"
	}
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%04.1f", int64(t/3600), int64(modf(t, 3600)/60), modf(t, 60))
	}
	return fmt.Sprintf("%d:%04.1f", int64(t/60), modf(t, 60))
}

func modf(a, b float64) float64 {
	m := a - b*float64(int64(a/b))
	if m < 0 {
		m += b
	}
	return m
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
