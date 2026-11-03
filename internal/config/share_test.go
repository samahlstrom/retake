package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetShareTunnel(t *testing.T) {
	for _, tc := range []struct{ name, in, off, on string }{
		{"no file", "", "[share]\ntunnel = \"off\"\n", "[share]\n"},
		{"a share table", "name = \"Ana\"\n\n[share]\ncloudflared = \"/opt/cf\" # mine\n",
			"name = \"Ana\"\n\n[share]\ntunnel = \"off\"\ncloudflared = \"/opt/cf\" # mine\n",
			"name = \"Ana\"\n\n[share]\ncloudflared = \"/opt/cf\" # mine\n"},
		{"already a tunnel line", "[share]\ntunnel = \"\"  # the default\n[render]\nencoder = \"x264\"\n",
			"[share]\ntunnel = \"off\"  # the default\n[render]\nencoder = \"x264\"\n",
			"[share]\n[render]\nencoder = \"x264\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "retake", "config.toml")
			if tc.in != "" {
				_ = os.MkdirAll(filepath.Dir(path), 0o700)
				if err := os.WriteFile(path, []byte(tc.in), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := SetShareTunnel(path, true); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != tc.off {
				t.Fatalf("off:\n%q\nwant\n%q", b, tc.off)
			}
			c, _, _ := Parse(string(b))
			if !c.Share.TunnelOff() {
				t.Fatal("not off")
			}
			if err := SetShareTunnel(path, false); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			if string(b) != tc.on {
				t.Fatalf("on:\n%q\nwant\n%q", b, tc.on)
			}
		})
	}
}
