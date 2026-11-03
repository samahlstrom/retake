package thumb

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeRun writes a JPEG-looking file and counts its runs.
func fakeRun(n *atomic.Int32, fail error) func(ctx context.Context, video, out string) error {
	return func(_ context.Context, video, out string) error {
		n.Add(1)
		if fail != nil {
			return fail
		}
		return os.WriteFile(out, append([]byte{0xff, 0xd8, 0xff}, []byte(filepath.Base(video))...), 0o600)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFirstIsMadeOnceAndCached(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "v3", "clip_v3.mp4")
	write(t, video, "frames")
	var runs atomic.Int32
	m := &Maker{Run: fakeRun(&runs, nil)}
	ctx := context.Background()

	p1, err := m.For(ctx, root, video)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p1) != Dir(root) || filepath.Ext(p1) != ".jpg" {
		t.Errorf("cached at %s, want under %s", p1, Dir(root))
	}
	if want, _ := Cached(root, video); want != p1 {
		t.Errorf("Cached = %s, For = %s", want, p1)
	}
	// Many requests at once, and a new Maker (a restarted server): still the one frame.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if p, err := m.First(ctx, root, video); err != nil || p != p1 {
				t.Errorf("First = %s, %v", p, err)
			}
		})
	}
	wg.Wait()
	if p, err := (&Maker{Run: fakeRun(&runs, nil)}).First(ctx, root, video); err != nil || p != p1 {
		t.Errorf("a new maker: %s, %v", p, err)
	}
	if runs.Load() != 1 {
		t.Errorf("ffmpeg ran %d times, want 1", runs.Load())
	}

	// A changed file gets a new frame.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(video, later, later); err != nil {
		t.Fatal(err)
	}
	p2, err := m.First(ctx, root, video)
	if err != nil || p2 == p1 || runs.Load() != 2 {
		t.Errorf("after a change: %s (was %s), %v, %d runs", p2, p1, err, runs.Load())
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(Dir(root))
	if len(entries) != 2 {
		t.Errorf("thumbs folder: %v", entries)
	}
}

func TestPosterBesideTheFileWins(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "v4", "clip_v4.m3u8")
	write(t, video, "#EXTM3U\n")
	if got := PosterPath(video); got != filepath.Join(root, "v4", "clip_v4.poster.jpg") {
		t.Fatalf("PosterPath = %s", got)
	}
	write(t, PosterPath(video), "\xff\xd8poster")
	var runs atomic.Int32
	m := &Maker{Run: fakeRun(&runs, nil)}
	if p, err := m.For(context.Background(), root, video); err != nil || p != PosterPath(video) || runs.Load() != 0 {
		t.Errorf("For = %s, %v, %d runs", p, err, runs.Load())
	}
	// An empty poster (still being written) doesn't count.
	write(t, PosterPath(video), "")
	if p, err := m.For(context.Background(), root, video); err != nil || p == PosterPath(video) || runs.Load() != 1 {
		t.Errorf("empty poster: %s, %v, %d runs", p, err, runs.Load())
	}
}

func TestFailuresAreRemembered(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "clip_v1.mp4")
	write(t, video, "not a video")
	var runs atomic.Int32
	m := &Maker{Run: fakeRun(&runs, errors.New("invalid data"))}
	for range 3 {
		if _, err := m.For(context.Background(), root, video); !errors.Is(err, ErrNone) {
			t.Errorf("err = %v", err)
		}
	}
	if runs.Load() != 1 {
		t.Errorf("ffmpeg ran %d times on a broken file, want 1", runs.Load())
	}
	if _, err := m.For(context.Background(), root, filepath.Join(root, "gone_v2.mp4")); !errors.Is(err, ErrNone) {
		t.Errorf("a missing file: %v", err)
	}
	// A run that wrote nothing is a failure too.
	m2 := &Maker{Run: func(context.Context, string, string) error { return nil }}
	if _, err := m2.First(context.Background(), root, video); !errors.Is(err, ErrNone) {
		t.Errorf("no output: %v", err)
	}
	// A request that went away is not remembered as a failure.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m3 := &Maker{Run: func(ctx context.Context, _, _ string) error { return ctx.Err() }}
	if _, err := m3.First(ctx, root, video); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	if len(m3.failed) != 0 {
		t.Errorf("a canceled run was remembered: %v", m3.failed)
	}
}

func TestFFmpegMakesAJPEG(t *testing.T) {
	testmedia.Need(t)
	root := t.TempDir()
	video := filepath.Join(root, "v1", "clip_v1.mp4")
	testmedia.Video(t, video, 1)
	p, err := Default.For(context.Background(), root, video)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !bytes.HasPrefix(b, []byte{0xff, 0xd8}) {
		t.Fatalf("not a JPEG: %v %x", err, b[:min(4, len(b))])
	}
	hls := filepath.Join(root, "v2")
	testmedia.HLS(t, hls, "clip_v2.m3u8", 2)
	if _, err := Default.For(context.Background(), root, filepath.Join(hls, "clip_v2.m3u8")); err != nil {
		t.Errorf("a playlist: %v", err)
	}
}

func TestKeyedIsMadeOnceFromWhatItFetches(t *testing.T) {
	root := t.TempDir()
	var runs, fetches atomic.Int32
	var read []string
	m := &Maker{Run: func(_ context.Context, video, out string) error {
		runs.Add(1)
		b, err := os.ReadFile(video)
		if err != nil {
			return err
		}
		read = append(read, string(b))
		return os.WriteFile(out, []byte("\xff\xd8\xffframe"), 0o600)
	}}
	ctx := context.Background()
	offline := errors.New("editing machine is offline")
	fetch := func(fail error) func(context.Context, string) (string, error) {
		return func(_ context.Context, dir string) (string, error) {
			fetches.Add(1)
			if fail != nil {
				return "", fail
			}
			p := filepath.Join(dir, "first.mp4")
			return p, os.WriteFile(p, []byte("init+seg"), 0o600)
		}
	}
	// The media can't be reached: no poster, and it is asked for again next time.
	for range 2 {
		if _, err := m.Keyed(ctx, root, "render/r1/index.m3u8", fetch(offline)); !errors.Is(err, ErrNone) {
			t.Fatalf("offline: %v", err)
		}
	}
	if fetches.Load() != 2 || runs.Load() != 0 {
		t.Fatalf("offline: %d fetches, %d runs", fetches.Load(), runs.Load())
	}
	p, err := m.Keyed(ctx, root, "render/r1/index.m3u8", fetch(nil))
	if err != nil || filepath.Dir(p) != Dir(root) || read[0] != "init+seg" {
		t.Fatalf("Keyed = %s, %v, read %q", p, err, read)
	}
	// Made once: a new maker (a restarted server) finds it without fetching.
	if p2, err := (&Maker{Run: m.Run}).Keyed(ctx, root, "render/r1/index.m3u8", fetch(nil)); err != nil || p2 != p {
		t.Errorf("again: %s, %v", p2, err)
	}
	if fetches.Load() != 3 || runs.Load() != 1 {
		t.Errorf("%d fetches, %d runs, want 3 and 1", fetches.Load(), runs.Load())
	}
	// Another key is another frame; the scratch folders are gone.
	if p3, err := m.Keyed(ctx, root, "render/r2/video.mp4", fetch(nil)); err != nil || p3 == p {
		t.Errorf("another key: %s, %v", p3, err)
	}
	entries, _ := os.ReadDir(Dir(root))
	if len(entries) != 2 {
		t.Errorf("thumbs folder: %v", entries)
	}
	// What ffmpeg can't read is remembered, as for a file.
	bad := &Maker{Run: fakeRun(&runs, errors.New("invalid data"))}
	before := runs.Load()
	for range 2 {
		if _, err := bad.Keyed(ctx, root, "render/r3/index.m3u8", fetch(nil)); !errors.Is(err, ErrNone) {
			t.Errorf("unreadable: %v", err)
		}
	}
	if runs.Load()-before != 1 {
		t.Errorf("ffmpeg ran %d times on unreadable media, want 1", runs.Load()-before)
	}
}
