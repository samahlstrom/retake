package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/testmedia"
)

func ptr[T any](v T) *T { return &v }

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		want    Info
		wantErr bool
	}{
		{
			name: "full",
			out:  `{"streams":[{"width":1920,"height":1080,"avg_frame_rate":"30000/1001","r_frame_rate":"30/1"}],"format":{"duration":"40.0400001"}}`,
			want: Info{Duration: ptr(40.04), FPS: 29.97, Width: ptr(1920), Height: ptr(1080)},
		},
		{
			name: "avg 0/0 falls back to r_frame_rate",
			out:  `{"streams":[{"width":64,"height":36,"avg_frame_rate":"0/0","r_frame_rate":"25/1"}],"format":{"duration":"1.000000"}}`,
			want: Info{Duration: ptr(1.0), FPS: 25, Width: ptr(64), Height: ptr(36)},
		},
		{
			name: "no rate and no duration (still rendering)",
			out:  `{"streams":[{"width":64,"height":36}],"format":{}}`,
			want: Info{FPS: DefaultFPS, Width: ptr(64), Height: ptr(36)},
		},
		{name: "empty output", out: "", want: Info{FPS: DefaultFPS}},
		{name: "ffprobe error output", out: "{\n\n}\n", want: Info{FPS: DefaultFPS}},
		{name: "rate without slash is ignored", out: `{"streams":[{"avg_frame_rate":"30"}]}`, want: Info{FPS: DefaultFPS}},
		{name: "garbage", out: "not json", want: Info{FPS: DefaultFPS}, wantErr: true},
		{name: "bad duration", out: `{"format":{"duration":"N/A"}}`, want: Info{FPS: DefaultFPS}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.out))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			assertInfo(t, got, tc.want)
		})
	}
}

func assertInfo(t *testing.T, got, want Info) {
	t.Helper()
	eqF := func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	eqI := func(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	if !eqF(got.Duration, want.Duration) || got.FPS != want.FPS || !eqI(got.Width, want.Width) || !eqI(got.Height, want.Height) {
		t.Errorf("got %s, want %s", show(got), show(want))
	}
}

func show(i Info) string {
	val := func(p any) any {
		switch x := p.(type) {
		case *float64:
			if x != nil {
				return *x
			}
		case *int:
			if x != nil {
				return *x
			}
		}
		return nil
	}
	return fmt.Sprintf("{dur %v fps %v %vx%v}", val(i.Duration), i.FPS, val(i.Width), val(i.Height))
}

func TestProbeCachesPerPathMtimeSize(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "clip_v1.mp4")
	if err := os.WriteFile(f, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	p := NewWithRunner(func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return []byte(`{"streams":[{"width":2,"height":2,"avg_frame_rate":"30/1"}],"format":{"duration":"4.0"}}`), nil
	}, nil)

	for range 3 {
		if got := p.Probe(f); !got.HasDuration() || *got.Duration != 4 {
			t.Fatalf("probe = %s", show(got))
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("ffprobe ran %d times for an unchanged file, want 1", n)
	}

	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(f, later, later); err != nil {
		t.Fatal(err)
	}
	p.Probe(f)
	if n := calls.Load(); n != 2 {
		t.Fatalf("after a new mtime ffprobe ran %d times, want 2", n)
	}

	if err := os.WriteFile(f, []byte("aaaaaaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, later, later); err != nil { // same mtime, new size
		t.Fatal(err)
	}
	p.Probe(f)
	if n := calls.Load(); n != 3 {
		t.Fatalf("after a new size ffprobe ran %d times, want 3", n)
	}

	missing := filepath.Join(dir, "gone.mp4")
	if got := p.Probe(missing); got.HasDuration() || got.FPS != DefaultFPS {
		t.Fatalf("missing file probed as %s", show(got))
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("a missing file ran ffprobe (%d calls)", n)
	}
}

func TestProbeFailureIsCachedAndLogged(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.mp4")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var log bytes.Buffer
	p := NewWithRunner(func(context.Context, string) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("exec: ffprobe not found")
	}, &log)
	p.Probe(f)
	p.Probe(f)
	if calls.Load() != 1 {
		t.Errorf("failed probe not cached: %d calls", calls.Load())
	}
	if !bytes.Contains(log.Bytes(), []byte("ffprobe failed on x.mp4")) {
		t.Errorf("log = %q", log.String())
	}
}

func TestProbeRealFFprobe(t *testing.T) {
	testmedia.Need(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "clip_v1.mp4")
	testmedia.Video(t, good, 1)
	bad := filepath.Join(dir, "clip_v2.mp4")
	testmedia.Garbage(t, bad)
	hls := filepath.Join(dir, "hls")
	testmedia.HLS(t, hls, "clip_v3.m3u8", 2)

	var log bytes.Buffer
	p := New(&log)
	got := p.Probe(good)
	if !got.HasDuration() || *got.Duration < 0.9 || *got.Duration > 1.2 || got.FPS != 10 ||
		got.Width == nil || *got.Width != 64 || got.Height == nil || *got.Height != 36 {
		t.Errorf("good file: %s", show(got))
	}
	if got := p.Probe(bad); got.HasDuration() {
		t.Errorf("garbage file has a duration: %s", show(got))
	}
	if got := p.Probe(filepath.Join(hls, "clip_v3.m3u8")); !got.HasDuration() || *got.Duration < 1.5 {
		t.Errorf("playlist: %s", show(got))
	}
	if log.Len() != 0 {
		t.Errorf("unexpected log: %s", log.String())
	}
}
