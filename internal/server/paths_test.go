package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/sqldb"
)

// copyTree copies a project folder, as cp -a would.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A project copied to another folder keeps its snapshots: its notes' stills are the copy's, also a note an older store
// saved with an absolute path, and nothing is read from the original folder (spec/notes.md, "Paths").
func TestCopiedProjectKeepsItsSnapshots(t *testing.T) {
	f := newFixture(t)
	snaps := filepath.Join(f.root, "v6", "clip_v6.review", "snaps")
	stills := map[int64][]byte{1: append(bytes.Clone(pngBody), 'a'), 2: append(bytes.Clone(pngBody), 'b')}
	for id, b := range stills {
		if err := os.MkdirAll(snaps, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(snaps, "0"+strconv.FormatInt(id, 10)+".png"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := project.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sync(project.Run{Version: "v6", Comments: []project.Comment{
		{Take: take, Seg: "S1", N: 0, T0: 1, T1: 2, Detail: project.Detail{Snapshot: filepath.Join(snaps, "01.png"),
			Strokes: []project.Stroke{{Shape: "circle", Color: "red", Box: [4]float64{0.1, 0.1, 0.2, 0.2}}}}},
		{Take: take, Seg: "S1", N: 1, T0: 3, T1: 4, Text: "this", Detail: project.Detail{Snapshot: filepath.Join(snaps, "02.png")}},
		{Take: take, Seg: "S1", N: 2, T0: 5, T1: 6, Text: "not ours", Detail: project.Detail{Snapshot: filepath.Join(t.TempDir(), "secret.png")}},
	}}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	// Note 2 as a store from before paths were relative kept it: absolute, in the original folder.
	db, err := sqldb.OpenSQLite(project.DBPath(f.root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE notes SET detail = ? WHERE id = 2`, `{"snapshot":"`+filepath.ToSlash(filepath.Join(snaps, "02.png"))+`"}`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	copied := filepath.Join(t.TempDir(), "renamed copy")
	copyTree(t, f.root, copied)
	for id := range stills { // the original's stills change: the copy must not show them
		if err := os.WriteFile(filepath.Join(snaps, "0"+strconv.FormatInt(id, 10)+".png"), append(bytes.Clone(pngBody), 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	h := start(t, Options{Video: filepath.Join(copied, "v6", "clip_v6.mp4"), EventPoll: 5 * time.Millisecond})
	t.Cleanup(func() { _ = h.srv.Close() })
	var side sidebarResponse
	if r := h.get("/api/notes?v=v6", nil); r.code != 200 || json.Unmarshal(r.body, &side) != nil || len(side.Items) != 3 {
		t.Fatalf("sidebar: %d %s", r.code, r.body)
	}
	for _, it := range side.Items {
		want, ok := stills[it.ID]
		if !ok {
			if it.Snapshot != "" {
				t.Errorf("note %d's snapshot outside the project has a URL: %s", it.ID, it.Snapshot)
			}
			if r := h.get("/api/notes/"+strconv.FormatInt(it.ID, 10)+"/snapshot", nil); r.code != 404 {
				t.Errorf("note %d's snapshot outside the project: %d", it.ID, r.code)
			}
			continue
		}
		if it.Snapshot == "" {
			t.Fatalf("note %d has no snapshot URL", it.ID)
		}
		r := h.get(it.Snapshot, nil)
		if r.code != 200 || !bytes.Equal(r.body, want) || r.header.Get("Content-Type") != "image/png" {
			t.Errorf("note %d's snapshot %s: %d %q", it.ID, it.Snapshot, r.code, r.body)
		}
	}
	if r := h.get("/api/notes/99/snapshot", nil); r.code != 404 {
		t.Errorf("unknown note: %d", r.code)
	}
	copyStore, err := project.OpenExisting(copied)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyStore.Close() }()
	for id := range stills {
		if n, err := copyStore.Note(id); err != nil || n.Detail.Snapshot != "v6/clip_v6.review/snaps/0"+strconv.FormatInt(id, 10)+".png" {
			t.Errorf("note %d keeps %q (%v)", id, n.Detail.Snapshot, err)
		}
	}
}
