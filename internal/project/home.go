package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The home store of a folder served by retake open <folder> (spec/home.md, "Sharing the home"): the project store's
// schema in <folder>/.retake/home.db, of which only the tokens and hosts tables are used. Its tokens are the home's:
// they open the folder's home and every project in it, and nothing else.

// HomeDBPath returns the path of a folder's home store.
func HomeDBPath(dir string) string { return filepath.Join(dir, Dir, "home.db") }

// OpenHome opens the home store of the folder dir, creating it if needed.
func OpenHome(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return nil, fmt.Errorf("creating the home store: %w", err)
	}
	return openFile(HomeDBPath(dir), dir, migrations)
}

// OpenHomeExisting opens the home store of the folder dir if it exists, else returns ErrNoProject.
func OpenHomeExisting(dir string) (*Store, error) {
	if _, err := os.Stat(HomeDBPath(dir)); errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoProject
	}
	return openFile(HomeDBPath(dir), dir, migrations)
}
