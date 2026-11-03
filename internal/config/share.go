package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// SetShareTunnel writes [share] tunnel in the config file at path (Settings' Sharing, spec/home.md): `tunnel = "off"`
// when off, and no tunnel line (the default: a link may start a tunnel) when on. The rest of the file, comments
// included, stays as it is; the file and its folder are made when missing.
func SetShareTunnel(path string, off bool) error {
	b, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(b)
	c, _, err := Parse(text)
	if err != nil {
		return fmt.Errorf("%s: %w (fix it first)", path, err)
	}
	if c.Share.TunnelOff() == off && (off || c.Share.Tunnel == nil) {
		return nil // already so
	}
	lines := strings.Split(text, "\n")
	header, end, ok := region(lines, "share")
	i := -1
	if ok {
		i = keyLine(lines, header+1, end, "tunnel")
	}
	switch {
	case off && i >= 0:
		if lines, err = replaceValue(lines, i, `tunnel = "off"`); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case off && ok:
		lines = slices.Insert(lines, header+1, `tunnel = "off"`)
	case off:
		lines = appendBlock(lines, []string{"[share]", `tunnel = "off"`})
	case i >= 0:
		lines = slices.Delete(lines, i, i+1)
	}
	out := strings.Join(lines, "\n")
	got, _, err := Parse(out)
	if err != nil || got.Share.TunnelOff() != off {
		return fmt.Errorf("%s: can't edit [share] here: set tunnel = %q by hand", path, map[bool]string{true: "off"}[off])
	}
	return writeConfig(path, out)
}
