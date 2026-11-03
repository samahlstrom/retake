package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scratch makes a home with a video folder in it, and the options of a trash under a scratch XDG_DATA_HOME: never the
// real trash.
func scratch(t *testing.T, goos string) (Options, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	video := filepath.Join(home, "Videos", "deadlock", "item shop")
	for _, f := range []string{"v1/item_v1.mp4", "v2/item_v2.mp4", ".retake/project.db"} {
		p := filepath.Join(video, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := func() time.Time { return time.Date(2026, 11, 2, 21, 30, 5, 0, time.Local) }
	return Options{GOOS: goos, Home: home, DataHome: filepath.Join(root, "data"), UID: 1000, Now: now}, video
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMoveAndRestoreFreedesktop(t *testing.T) {
	o, video := scratch(t, "linux")
	m, err := Move(video, o)
	if err != nil {
		t.Fatal(err)
	}
	trash := filepath.Join(o.DataHome, "Trash")
	if m.To != filepath.Join(trash, "files", "item shop") || m.Info != filepath.Join(trash, "info", "item shop.trashinfo") {
		t.Fatalf("moved to %+v", m)
	}
	if _, err := os.Lstat(video); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the folder is still there")
	}
	if got := read(t, filepath.Join(m.To, "v2", "item_v2.mp4")); got != "v2/item_v2.mp4" {
		t.Fatal("contents changed", got)
	}
	info := read(t, m.Info)
	want := "[Trash Info]\nPath=" + strings.ReplaceAll(filepath.ToSlash(video), " ", "%20") +
		"\nDeletionDate=2026-11-02T21:30:05\n"
	if info != want {
		t.Fatalf("info file:\n%q\nwant\n%q", info, want)
	}

	if err := Restore(m); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(video, ".retake", "project.db")); got != ".retake/project.db" {
		t.Fatal("not restored", got)
	}
	if _, err := os.Lstat(m.Info); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the info file stayed")
	}
	if err := Restore(m); err == nil {
		t.Fatal("restored twice")
	}
}

func TestMoveTakenName(t *testing.T) {
	o, video := scratch(t, "linux")
	first, err := Move(video, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(video, 0o755); err != nil { // another folder of the same name
		t.Fatal(err)
	}
	second, err := Move(video, o)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second.To) != "item shop.2" || filepath.Base(second.Info) != "item shop.2.trashinfo" ||
		first.To == second.To {
		t.Fatalf("second: %+v", second)
	}
	// Something at the old place: Restore refuses and moves nothing.
	if err := os.MkdirAll(video, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Restore(first); !errors.Is(err, os.ErrExist) {
		t.Fatalf("restore over a folder: %v", err)
	}
	if _, err := os.Lstat(first.To); err != nil {
		t.Fatal("it left the trash", err)
	}
}

func TestMoveOtherDiskUsesItsOwnTrash(t *testing.T) {
	o, video := scratch(t, "linux")
	mount := filepath.Join(filepath.Dir(o.Home), "mnt")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(mount, "clip")
	if err := os.Rename(video, disk); err != nil {
		t.Fatal(err)
	}
	o.SameDisk = func(a, b string) bool { return !strings.HasPrefix(a, mount) }
	o.MountOf = func(string) string { return mount }
	m, err := Move(disk, o)
	if err != nil {
		t.Fatal(err)
	}
	if m.To != filepath.Join(mount, ".Trash-1000", "files", "clip") {
		t.Fatalf("moved to %s", m.To)
	}
	o.MountOf = func(string) string { return "" }
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Move(disk, o); !errors.Is(err, ErrOtherDisk) {
		t.Fatalf("no trash on its disk: %v", err)
	}
	if _, err := os.Lstat(disk); err != nil {
		t.Fatal("it moved anyway", err)
	}
}

func TestMoveMac(t *testing.T) {
	o, video := scratch(t, "darwin")
	m, err := Move(video, o)
	if err != nil {
		t.Fatal(err)
	}
	if m.To != filepath.Join(o.Home, ".Trash", "item shop") || m.Info != "" {
		t.Fatalf("moved to %+v", m)
	}
	if err := os.MkdirAll(video, 0o755); err != nil {
		t.Fatal(err)
	}
	m2, err := Move(video, o)
	if err != nil || filepath.Base(m2.To) != "item shop 2" {
		t.Fatalf("second: %+v %v", m2, err)
	}
	if err := Restore(m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(video, "v1", "item_v1.mp4")); err != nil {
		t.Fatal("not restored", err)
	}
}

func TestMoveRefusesARelativeOrMissingPath(t *testing.T) {
	o, _ := scratch(t, "linux")
	if _, err := Move("Videos/x", o); err == nil {
		t.Fatal("a relative path")
	}
	if _, err := Move(filepath.Join(o.Home, "nothing"), o); err == nil {
		t.Fatal("a missing path")
	}
}

func TestMoveAndRestoreRecycleBin(t *testing.T) {
	o, video := scratch(t, "windows")
	bin := filepath.Join(filepath.Dir(o.Home), "$Recycle.Bin", "S-1-5-21-1-2-3-1001")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	o.RecycleBin = func(string) (string, error) { return bin, nil }
	if o.Name() != "Recycle Bin" {
		t.Fatalf("name %q", o.Name())
	}
	m, err := Move(video, o)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(m.To)
	if filepath.Dir(m.To) != bin || len(base) != 8 || !strings.HasPrefix(base, "$R") ||
		m.Info != filepath.Join(bin, "$I"+base[2:]) || m.From != video {
		t.Fatalf("moved to %+v", m)
	}
	if got := read(t, filepath.Join(m.To, "v2", "item_v2.mp4")); got != "v2/item_v2.mp4" {
		t.Fatal("contents changed", got)
	}
	size := int64(len("v1/item_v1.mp4") + len("v2/item_v2.mp4") + len(".retake/project.db"))
	if got, want := read(t, m.Info), string(recycleRecord(video, size, o.Now())); got != want {
		t.Fatalf("$I record %x, want %x", got, want)
	}
	if err := Restore(m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(video, "v1", "item_v1.mp4")); err != nil {
		t.Fatal("not restored", err)
	}
	if _, err := os.Lstat(m.Info); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the $I record stayed")
	}
	// A file keeps its extension; a drive without a Recycle Bin keeps the folder where it is.
	clip := filepath.Join(video, "v1", "item_v1.mp4")
	if m, err = Move(clip, o); err != nil || filepath.Ext(m.To) != ".mp4" || filepath.Ext(m.Info) != ".mp4" {
		t.Fatalf("a file: %+v %v", m, err)
	}
	o.RecycleBin = func(string) (string, error) { return "", ErrOtherDisk }
	if _, err := Move(video, o); !errors.Is(err, ErrOtherDisk) {
		t.Fatalf("no Recycle Bin: %v", err)
	}
	if _, err := os.Lstat(video); err != nil {
		t.Fatal("it moved anyway", err)
	}
}

// TestRecycleRecord is the $I file byte for byte: format 2, size, FILETIME, path length with the NUL, UTF-16 path.
func TestRecycleRecord(t *testing.T) {
	at := time.Date(2026, 11, 2, 21, 30, 5, 0, time.UTC)
	got := recycleRecord(`C:\Vidéos\a`, 1234, at)
	want := []byte{
		2, 0, 0, 0, 0, 0, 0, 0, // format 2
		0xd2, 0x04, 0, 0, 0, 0, 0, 0, // 1234 bytes
	}
	ft := uint64(at.Unix())*10_000_000 + 116444736000000000
	for i := range 8 {
		want = append(want, byte(ft>>(8*i)))
	}
	want = append(want, 12, 0, 0, 0) // 11 characters and the NUL
	for _, c := range `C:\Vidéos\a` {
		want = append(want, byte(c), byte(c>>8))
	}
	want = append(want, 0, 0)
	if string(got) != string(want) {
		t.Fatalf("record\n%x\nwant\n%x", got, want)
	}
	if (Options{GOOS: "linux"}).Name() != "Trash" || (Options{GOOS: "darwin"}).Name() != "Trash" {
		t.Fatal("Trash elsewhere")
	}
}
