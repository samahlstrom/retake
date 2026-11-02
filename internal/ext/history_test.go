package ext

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readMaybe(t *testing.T, p string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func historyDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(HistoryDir)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// An agent attempt that changes, makes and removes files: the history keeps what changed, and Undo puts each back.
func TestHistory(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"v0/idea.md": "# Idea\n", "v0/script.md": "## Cold open\n", "v0/old.md": "old\n", "v1/x_v1.mp4": "video",
	})
	owns := []string{"v0/**"}
	f := Folder{Root: root, Owns: owns}
	snap, err := Snapshot(root, owns, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	// The agent's attempt.
	writeTree(t, root, map[string]string{"v0/script.md": "## Cold open\nJump.\n", "v0/outline.md": "## Beats\n"})
	if err := os.Remove(filepath.Join(root, "v0", "old.md")); err != nil {
		t.Fatal(err)
	}
	edits, err := snap.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 3 {
		t.Fatalf("edits %+v", edits)
	}
	want := map[string][2]string{
		"v0/old.md":     {sum("old\n"), ""},
		"v0/outline.md": {"", sum("## Beats\n")},
		"v0/script.md":  {sum("## Cold open\n"), sum("## Cold open\nJump.\n")},
	}
	for _, e := range edits {
		w, ok := want[e.Path]
		if !ok || e.Before != w[0] || e.After != w[1] || e.Task != "task-7" || e.At == 0 || e.ID != edits[0].ID {
			t.Errorf("edit %+v", e)
		}
	}
	dir := filepath.Join(root, filepath.FromSlash(HistoryDir), edits[0].ID)
	if _, ok := readMaybe(t, filepath.Join(dir, "files", "v0", "idea.md")); ok {
		t.Error("an unchanged file's copy was kept")
	}
	if b, ok := readMaybe(t, filepath.Join(dir, "files", "v0", "script.md")); !ok || b != "## Cold open\n" {
		t.Errorf("script.md's copy: %q", b)
	}
	if _, ok := readMaybe(t, filepath.Join(dir, "edit.json")); !ok {
		t.Error("no edit.json")
	}
	all, err := f.History("")
	if err != nil || len(all) != 3 {
		t.Fatalf("History: %v %v", all, err)
	}
	if one, _ := f.History("v0/script.md"); len(one) != 1 || one[0].Path != "v0/script.md" {
		t.Errorf("History(script.md) = %v", one)
	}
	if _, err := f.History("v1/x_v1.mp4"); !errors.Is(err, ErrNotOwned) {
		t.Errorf("History of a version file: %v", err)
	}

	// Undo: the changed file comes back, the made one goes, the removed one returns.
	info, err := f.Undo(edits[0].ID, "v0/script.md")
	if err != nil || info.SHA256 != sum("## Cold open\n") {
		t.Errorf("undo script.md: %+v %v", info, err)
	}
	if b, _ := readMaybe(t, filepath.Join(root, "v0", "script.md")); b != "## Cold open\n" {
		t.Errorf("script.md = %q", b)
	}
	if info, err := f.Undo(edits[0].ID, "v0/outline.md"); err != nil || !info.Deleted {
		t.Errorf("undo outline.md: %+v %v", info, err)
	}
	if _, ok := readMaybe(t, filepath.Join(root, "v0", "outline.md")); ok {
		t.Error("outline.md is still there")
	}
	if left, _ := f.History(""); len(left) != 1 || left[0].Path != "v0/old.md" {
		t.Errorf("history after two undos: %v", left)
	}
	if _, err := f.Undo(edits[0].ID, "v0/script.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("undo twice: %v", err)
	}
	if _, err := f.Undo(edits[0].ID, "v0/old.md"); err != nil {
		t.Errorf("undo old.md: %v", err)
	}
	if b, _ := readMaybe(t, filepath.Join(root, "v0", "old.md")); b != "old\n" {
		t.Errorf("old.md = %q", b)
	}
	if dirs := historyDirs(t, root); len(dirs) != 0 {
		t.Errorf("an edit with nothing left to undo stays: %v", dirs)
	}
	for _, id := range []string{"../../v1", "x", ""} {
		if _, err := f.Undo(id, "v0/script.md"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Undo(%q) = %v", id, err)
		}
	}
}

func TestHistoryRefusesAfterLaterChange(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"v0/script.md": "one\n"})
	owns := []string{"v0/**"}
	f := Folder{Root: root, Owns: owns}
	snap, _ := Snapshot(root, owns, "")
	writeTree(t, root, map[string]string{"v0/script.md": "two\n", "v0/made.md": "made\n"})
	edits, err := snap.Finish()
	if err != nil || len(edits) != 2 {
		t.Fatalf("%v %v", edits, err)
	}
	// The person edits after the agent: undoing the agent's edit would lose theirs.
	writeTree(t, root, map[string]string{"v0/script.md": "three\n"})
	var changed *ChangedError
	if _, err := f.Undo(edits[1].ID, "v0/script.md"); !errors.As(err, &changed) || changed.SHA256 != sum("three\n") {
		t.Errorf("undo after a later change: %v", err)
	}
	if b, _ := readMaybe(t, filepath.Join(root, "v0", "script.md")); b != "three\n" {
		t.Errorf("script.md = %q", b)
	}
	if left, _ := f.History("v0/script.md"); len(left) != 1 {
		t.Error("a refused undo dropped the edit")
	}
	// A made file that's gone since can't be undone either.
	if err := os.Remove(filepath.Join(root, "v0", "made.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Undo(edits[0].ID, "v0/made.md"); !errors.As(err, &changed) || changed.SHA256 != "" {
		t.Errorf("undo of a made file that's gone: %v", err)
	}
}

func TestHistoryNothingChanged(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"v0/script.md": "one\n"})
	snap, err := Snapshot(root, []string{"v0/**"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if dirs := historyDirs(t, root); len(dirs) != 1 {
		t.Errorf("snapshot dirs %v", dirs)
	}
	edits, err := snap.Finish()
	if err != nil || edits != nil {
		t.Errorf("%v %v", edits, err)
	}
	if dirs := historyDirs(t, root); len(dirs) != 0 {
		t.Errorf("an unchanged snapshot stays: %v", dirs)
	}
	var none *Snap
	if edits, err := none.Finish(); edits != nil || err != nil {
		t.Error("nil snap")
	}
}

// A file over 1 MiB isn't copied, so its change isn't kept (it couldn't be undone).
func TestHistoryLargeFile(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("x", maxSnapFile+1)
	writeTree(t, root, map[string]string{"v0/big.txt": big, "v0/small.md": "a"})
	snap, _ := Snapshot(root, []string{"v0/**"}, "")
	writeTree(t, root, map[string]string{"v0/big.txt": big + "y", "v0/small.md": "b"})
	edits, err := snap.Finish()
	if err != nil || len(edits) != 1 || edits[0].Path != "v0/small.md" {
		t.Errorf("%+v %v", edits, err)
	}
}

func TestHistoryPrunes(t *testing.T) {
	if testing.Short() {
		t.Skip("makes 105 edits")
	}
	root := t.TempDir()
	owns := []string{"v0/**"}
	for i := range MaxHistory + 5 {
		snap, err := Snapshot(root, owns, "")
		if err != nil {
			t.Fatal(err)
		}
		writeTree(t, root, map[string]string{"v0/n.md": strings.Repeat("n", i+1)})
		if _, err := snap.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	dirs := historyDirs(t, root)
	if len(dirs) != MaxHistory {
		t.Fatalf("%d edits kept", len(dirs))
	}
	all, _ := Folder{Root: root, Owns: owns}.History("v0/n.md")
	if len(all) != MaxHistory || all[0].After != sum(strings.Repeat("n", MaxHistory+5)) {
		t.Errorf("newest first: %+v", all[0])
	}
	if all[len(all)-1].After != sum(strings.Repeat("n", 6)) {
		t.Errorf("oldest kept: %+v", all[len(all)-1])
	}
}
