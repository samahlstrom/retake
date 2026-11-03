//go:build !windows

package desktop

import "errors"

// noRunKey: there's no registry here (Autostart only uses it when its GOOS says Windows).
type noRunKey struct{}

func systemRunKey() RunKey { return noRunKey{} }

var errNoRegistry = errors.New("there's no Windows registry on this system")

func (noRunKey) Value(string) (string, error)  { return "", errNoRegistry }
func (noRunKey) SetValue(string, string) error { return errNoRegistry }
func (noRunKey) DeleteValue(string) error      { return errNoRegistry }
