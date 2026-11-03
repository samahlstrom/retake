package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoredPathsAreRelativeToTheRoot(t *testing.T) {
	s, _ := newStore(t)
	root := s.Root()
	inside := filepath.Join(root, "v6", "demo_v6.review", "snaps", "01.png")
	// An absolute path outside the project, on the root's own drive (/elsewhere/snap.png on Unix, C:\elsewhere\… on
	// Windows, where /elsewhere isn't absolute).
	elsewhere := filepath.Join(filepath.VolumeName(root)+string(filepath.Separator), "elsewhere", "snap.png")
	for _, c := range []struct{ in, want string }{
		{inside, "v6/demo_v6.review/snaps/01.png"},
		{elsewhere, elsewhere},                                 // outside: kept as given
		{"v6/already/relative.png", "v6/already/relative.png"}, // relative: as it is
		{"", ""},
	} {
		if got := s.Rel(c.in); got != c.want {
			t.Errorf("Rel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := s.Abs("v6/demo_v6.review/snaps/01.png"); got != inside {
		t.Errorf("Abs = %q, want %q", got, inside)
	}
	if got := s.Abs(elsewhere); got != elsewhere {
		t.Errorf("Abs of an absolute path = %q", got)
	}

	// Sync stores a snapshot inside the project relative, and a rebuild that gives the same absolute path again
	// changes nothing.
	run := Run{Version: "v6", Comments: []Comment{comment(take1, "S1", 0, 1, "circle this")}}
	run.Comments[0].Detail = Detail{Snapshot: inside}
	if _, err := s.Sync(run); err != nil {
		t.Fatal(err)
	}
	if raw := rawDetail(t, s, 1); raw != `{"snapshot":"v6/demo_v6.review/snaps/01.png"}` {
		t.Fatalf("stored detail = %s", raw)
	}
	if res, err := s.Sync(run); err != nil || len(res.Updated) != 0 {
		t.Fatalf("the same snapshot again: %+v %v", res, err)
	}

	// A pushed version's file and a job's take folder are kept relative too, and given back whole.
	file := filepath.Join(root, "v7", "demo_v7.mp4")
	if _, err := s.RecordPush(Push{Label: "v7", Path: file, Parent: "v6", Scope: Scope{Version: "v6"},
		Changelog: []ChangeEntry{{Note: 1, Summary: "circled"}}}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT path FROM versions WHERE label = 'v7'`).Scan(&stored); err != nil || stored != "v7/demo_v7.mp4" {
		t.Fatalf("stored version path %q %v", stored, err)
	}
	if v, err := s.Version("v7"); err != nil || v.Path != file {
		t.Fatalf("Version = %+v %v", v, err)
	}
	if vs, err := s.Versions(); err != nil || len(vs) != 1 || vs[0].Path != file {
		t.Fatalf("Versions = %+v %v", vs, err)
	}
	takeDir := filepath.Join(root, "v6", "demo_v6.review", take1)
	jobs, err := s.QueueTake("v6", take1, takeDir)
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].Args.Dir != "v6/demo_v6.review/"+take1 || s.Abs(jobs[0].Args.Dir) != takeDir {
		t.Fatalf("job dir %q", jobs[0].Args.Dir)
	}
}

// An older store's absolute paths become relative when it is opened: inside the project directly; outside it (the
// folder was moved, renamed or copied) by the part under the old project folder, when that file is in this project.
// A path to a file this project doesn't have is left alone.
func TestOpenRelocatesAnOlderStoresAbsolutePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "itemshop-copy")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join("v6", "itemshop_v6.review", "snaps", "02_00-20-1.png")
	touch(t, filepath.Join(root, snap))
	takeDir := filepath.Join("v6", "itemshop_v6.review", take1)
	if err := os.MkdirAll(filepath.Join(root, takeDir), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(string(filepath.Separator)+"media", "old-disk", "itemshop") // where it was made
	details := map[int64]Detail{
		1: {Snapshot: filepath.Join(root, snap), Strokes: []Stroke{{Shape: "circle", Color: "red"}}}, // inside
		2: {Snapshot: filepath.Join(old, snap)},                                                      // moved
		3: {Snapshot: filepath.Join(old, "v6", "itemshop_v6.review", "snaps", "09_gone.png")},        // not here
		4: {Snapshot: `D:\Projects\itemshop\v6\itemshop_v6.review\snaps\02_00-20-1.png`},             // another OS
		5: {},
	}
	for id := int64(1); id <= 5; id++ {
		b, _ := json.Marshal(details[id])
		if _, err := s.db.Exec(`INSERT INTO notes (id, key, version, take, seg, t0, t1, text, status, detail, created,
			updated) VALUES (?, ?, 'v6', ?, 'S1', 1, 2, 'x', 'open', ?, 'then', 'then')`, id, take1+"/S1/"+string(rune('0'+id)),
			take1, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO versions (label, path, sha256, created) VALUES ('v6', ?, 'x', 'then')`,
		filepath.Join(old, "v6", "itemshop_v6.mp4")); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(root, "v6", "itemshop_v6.mp4"))
	args, _ := json.Marshal(JobArgs{Version: "v6", Take: take1, Dir: filepath.Join(old, takeDir)})
	if _, err := s.db.Exec(`INSERT INTO jobs (kind, args, created, updated) VALUES ('transcribe', ?, 'then', 'then')`,
		string(args)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	want := map[int64]string{
		1: "v6/itemshop_v6.review/snaps/02_00-20-1.png",
		2: "v6/itemshop_v6.review/snaps/02_00-20-1.png",
		3: details[3].Snapshot,
		4: "v6/itemshop_v6.review/snaps/02_00-20-1.png",
		5: "",
	}
	for id, w := range want {
		n, err := s.Note(id)
		if err != nil || n.Detail.Snapshot != w {
			t.Errorf("note %d: snapshot %q, want %q (%v)", id, n.Detail.Snapshot, w, err)
		}
	}
	if n, _ := s.Note(1); len(n.Detail.Strokes) != 1 || n.Updated != "then" {
		t.Errorf("relocating changed more than the path: %+v", n)
	}
	if rel, _, ok := s.SnapshotFile(mustNote(t, s, 2)); !ok || rel != filepath.FromSlash(want[2]) {
		t.Errorf("note 2's file: %q %v", rel, ok)
	}
	if _, _, ok := s.SnapshotFile(mustNote(t, s, 3)); ok {
		t.Error("a snapshot outside the project has a file")
	}
	if v, err := s.Version("v6"); err != nil || v.Path != filepath.Join(root, "v6", "itemshop_v6.mp4") {
		t.Errorf("version v6: %+v %v", v, err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT args FROM jobs`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var a JobArgs
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.Dir != filepath.ToSlash(takeDir) {
		t.Errorf("job dir %q %v", a.Dir, err)
	}
	// Opening again writes nothing: it's relative already.
	before := rawDetail(t, s, 2)
	_ = s.Close()
	if s, err = OpenExisting(root); err != nil {
		t.Fatal(err)
	}
	if after := rawDetail(t, s, 2); after != before {
		t.Errorf("reopening changed %s to %s", before, after)
	}
}

func TestFewerPointsKeepsTheEnds(t *testing.T) {
	var pts [][2]float64
	for i := range 200 {
		pts = append(pts, [2]float64{float64(i) / 199, 0.123456789})
	}
	got := FewerPoints(pts)
	if len(got) != MaxStrokePoints || got[0] != [2]float64{0, 0.1235} || got[len(got)-1] != [2]float64{1, 0.1235} {
		t.Fatalf("%d points, %v ... %v", len(got), got[0], got[len(got)-1])
	}
	if got := FewerPoints(pts[:3]); len(got) != 3 || got[1] != [2]float64{0.005, 0.1235} {
		t.Fatalf("few points: %v", got)
	}
	if FewerPoints(nil) != nil {
		t.Fatal("no points")
	}
}

func rawDetail(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT detail FROM notes WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustNote(t *testing.T, s *Store, id int64) Note {
	t.Helper()
	n, err := s.Note(id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
