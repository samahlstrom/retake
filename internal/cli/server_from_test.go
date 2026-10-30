package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// A folder made by retake open becomes a project as it is: its family, its takes, and (with SQLite) its store.
func TestProjectAddFrom(t *testing.T) {
	w := newWorld(t)
	data := filepath.Join(t.TempDir(), "data")
	st, err := project.Open(w.root)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, _ := team.NewToken()
	if _, err := st.CreateToken(project.NewToken{Hash: hash, Role: "viewer", Label: "kept"}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	take := filepath.Join(w.root, "v6", "clip_v6.review", "2026-10-30_10-00-00")
	_ = os.MkdirAll(take, 0o755)
	_ = os.WriteFile(filepath.Join(take, "session.json"), []byte("{}"), 0o600)

	if code := w.run("project", "add", "imported", "--from", w.root, "--data", data); code != ExitOK {
		t.Fatalf("project add --from: %d %s", code, w.stderr)
	}
	ws := filepath.Join(data, "projects", "imported")
	for _, rel := range []string{"v5/clip_v5.mp4", "v6/clip_v6.mp4", "v7/clip_v7.mp4",
		"v6/clip_v6.review/2026-10-30_10-00-00/session.json", ".retake/project.db"} {
		if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if got := hub.Anchor(ws); got != filepath.Join(ws, "v7", "clip_v7.mp4") {
		t.Fatalf("anchor: %s", got)
	}
	copied, err := project.OpenExisting(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copied.Close() }()
	if ts, _ := copied.Tokens(); len(ts) != 1 || ts[0].Label != "kept" {
		t.Fatalf("the store came along: %+v", ts)
	}
	if code := w.run("project", "add", "again", "--from", w.root, "--video", w.video, "--data", data); code !=
		ExitUsage || !strings.Contains(w.stderr.String(), "not both") {
		t.Fatalf("--from and --video: %d", code)
	}
}
