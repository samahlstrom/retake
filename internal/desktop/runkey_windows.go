//go:build windows

package desktop

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// hkcuRun is the signed-in person's Run key (HKEY_CURRENT_USER: no administrator needed).
type hkcuRun struct{}

func systemRunKey() RunKey { return hkcuRun{} }

func (hkcuRun) Value(name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", fs.ErrNotExist
		}
		return "", err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", fs.ErrNotExist
	}
	return v, err
}

func (hkcuRun) SetValue(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue(name, value)
}

func (hkcuRun) DeleteValue(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
