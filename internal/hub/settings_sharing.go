package hub

import (
	"os/exec"
	"runtime"

	"github.com/samahlstrom/retake/internal/config"
)

// The values of share_tunnel in POST api/settings.
const (
	ShareTunnelOn  = "cloudflare"
	ShareTunnelOff = "off"
)

// SettingsSharing is Settings' Sharing row (spec/home.md, "Settings"): whether a link may start a tunnel, whether
// cloudflared is here, and the command that installs it.
type SettingsSharing struct {
	Tunnel      string `json:"tunnel"`
	Cloudflared bool   `json:"cloudflared"`
	Install     string `json:"install"`
}

func sharingView(c config.Config, bin string) *SettingsSharing {
	v := &SettingsSharing{Tunnel: ShareTunnelOn, Install: cloudflaredInstall(runtime.GOOS, runtime.GOARCH)}
	if c.Share.TunnelOff() {
		v.Tunnel = ShareTunnelOff
	}
	if bin == "" {
		bin = "cloudflared"
	}
	_, err := exec.LookPath(bin)
	v.Cloudflared = err == nil
	return v
}

// cloudflaredInstall is the one line that installs cloudflared: Homebrew on macOS, winget on Windows, else Cloudflare's
// own binary downloaded into ~/.local/bin.
func cloudflaredInstall(goos, goarch string) string {
	switch goos {
	case "darwin":
		return "brew install cloudflared"
	case "windows":
		return "winget install --id Cloudflare.cloudflared"
	}
	arch := "amd64"
	if goarch == "arm64" || goarch == "arm" || goarch == "386" {
		arch = goarch
	}
	return "mkdir -p ~/.local/bin && curl -fsSL -o ~/.local/bin/cloudflared " +
		"https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-" + goos + "-" + arch +
		" && chmod +x ~/.local/bin/cloudflared"
}
