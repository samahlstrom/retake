// Package userdir names the per-user folders Retake keeps its files in. On Linux and macOS they are the XDG folders
// (each caller reads its $XDG_… variable and falls back to ~/.config, ~/.local/share, ~/.local/state or ~/.cache).
// On Windows, where those variables are normally unset, they are %APPDATA% (settings: config.toml, the theme,
// extensions) and %LOCALAPPDATA% (data, state and caches), and the home folder is %USERPROFILE%. An $XDG_… variable
// that is set still wins there.
//
// Each helper reads the environment through getenv (os.Getenv in production), and the Windows ones answer only when
// Retake runs on Windows and the variable is set, so callers keep their Unix behaviour exactly, and tests with a fake
// environment get the same paths on every system.
package userdir

import "runtime"

// goos is the system the folders are for; tests set it.
var goos = runtime.GOOS

// Home is the home folder: $HOME, else %USERPROFILE% on Windows. "" when neither is set (callers that want one fall
// back to os.UserHomeDir as before).
func Home(getenv func(string) string) string {
	if h := getenv("HOME"); h != "" {
		return h
	}
	if goos == "windows" {
		return getenv("USERPROFILE")
	}
	return ""
}

// Roaming is the folder of per-user settings on Windows: %APPDATA% (C:\Users\<you>\AppData\Roaming). "" on other
// systems, or when it isn't set. Retake's are in its retake\ folder: config.toml, theme\ and extensions\.
func Roaming(getenv func(string) string) string {
	if goos == "windows" {
		return getenv("APPDATA")
	}
	return ""
}

// Local is the folder of per-user data, state and caches on Windows: %LOCALAPPDATA% (C:\Users\<you>\AppData\Local).
// "" on other systems, or when it isn't set. Retake's are in its retake\ folder, which data, state and caches share:
// library\ and whisper.cpp\ (data), start.json and uploads.json (state), models\, link-preview\ and ffmpeg-caps.json
// (caches).
func Local(getenv func(string) string) string {
	if goos == "windows" {
		return getenv("LOCALAPPDATA")
	}
	return ""
}
