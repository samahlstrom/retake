package stream

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeEditor stands in for `retake-edit render <timeline> --doc <snapshot> --hls <playlist> --progress --json`: it
// resumes after the playlist's last "# unit N" mark, publishes a unit every $FAKE_SLEEP seconds up to $FAKE_TOTAL,
// and logs where each run started to $FAKE_LOG. With $FAKE_FAIL it fails on the second unit.
const fakeEditor = `#!/bin/sh
[ "$1" = render ] || exit 9
shift 2
pl=""; doc=""
while [ $# -gt 0 ]; do
  case "$1" in --hls) pl="$2"; shift ;; --doc) doc="$2"; shift ;; esac
  shift
done
dir=$(dirname "$pl"); total=${FAKE_TOTAL:-4}
start=$(sed -n 's/^# unit //p' "$pl" | tail -1); start=${start:-0}
echo "start $start $(basename "$doc")" >> "$FAKE_LOG"
echo '{"progress": "plan", "units": '$total', "reused": 1, "published": '$start'}' >&2
i=$start
while [ $i -lt $total ]; do
  i=$((i+1))
  if [ $i -eq 3 ] && [ -n "$FAKE_PAUSE_THIRD" ]; then sleep "$FAKE_PAUSE_THIRD"; fi
  sleep "${FAKE_SLEEP:-0.05}"
  if [ -n "$FAKE_FAIL" ] && [ $i -eq 2 ]; then
    echo '{"ok": false, "errors": [{"code": "ffmpeg_failed", "message": "ffmpeg failed (encoding u2)"}]}'
    exit 1
  fi
  printf seg > "$dir/seg_$i.m4s"
  cp "$pl" "$pl.tmp"; printf '#EXTINF:2.000000,\nseg_%s.m4s\n# unit %s\n' $i $i >> "$pl.tmp"; mv "$pl.tmp" "$pl"
  echo '{"progress": "publish", "done": '$i', "total": '$total', "segments": '$i', "seconds": '$((i*2))', "elapsed": 0.1}' >&2
done
cp "$pl" "$pl.tmp"; printf '#EXT-X-ENDLIST\n' >> "$pl.tmp"; mv "$pl.tmp" "$pl"
echo '{"ok": true, "units": '$total', "reused": 1, "rendered": 3,
 "stream": {"playlist": "'"$pl"'", "segments": '$total', "first_s": 0.1, "done_s": 0.4}}'
`

type fixture struct {
	t      *testing.T
	root   string
	target Target
	log    string
	events *eventLog
}

type eventLog struct {
	mu  sync.Mutex
	evs []project.VersionEvent
}

func (l *eventLog) add(ev project.VersionEvent) {
	l.mu.Lock()
	l.evs = append(l.evs, ev)
	l.mu.Unlock()
}

func (l *eventLog) whys() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var s []string
	for _, e := range l.evs {
		if len(s) == 0 || s[len(s)-1] != e.Why {
			s = append(s, e.Why)
		}
	}
	return strings.Join(s, " ")
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newFixture is a project with a pending streamed v7 and a fake retake-edit; it returns a Manager on it.
func newFixture(t *testing.T, lease time.Duration, env ...string) (*fixture, *Manager) {
	t.Helper()
	testmedia.NeedShell(t)
	root := t.TempDir()
	f := &fixture{t: t, root: root, log: filepath.Join(root, "editor.log"), events: &eventLog{}}
	write(t, filepath.Join(root, "edit", "timeline.json"), `{"rev": 3}`)
	pl := filepath.Join(root, "v7", "clip_v7.m3u8")
	write(t, pl, "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:6\n")
	write(t, family.Sidecar(pl, ".timeline.json"), `{"rev": 3}`)
	rec := bridge.PushRecord{Version: "v7", From: "v6", Rev: 3, Timeline: "edit/timeline.json",
		Stream: &family.StreamRecord{State: family.StatePending, Playlist: "clip_v7.m3u8", Doc: "clip_v7.timeline.json",
			Poster: "clip_v7.poster.jpg", Duration: 8, FPS: 25, Total: 4}}
	if err := bridge.WritePushRecord(pl, rec); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "tools", "retake-edit")
	write(t, bin, fakeEditor)
	f.target = Target{Label: "v7", Playlist: pl, Root: root, Emit: f.events.add}
	m := New(Options{Editor: bridge.Editor{Configured: bin, Env: append([]string{"FAKE_LOG=" + f.log}, env...)},
		Lease: lease, Progress: time.Millisecond})
	t.Cleanup(m.Close)
	return f, m
}

func (f *fixture) record() *family.StreamRecord {
	f.t.Helper()
	rec, err := bridge.ReadPushRecord(f.target.Playlist)
	if err != nil || rec == nil || rec.Stream == nil {
		f.t.Fatalf("record: %+v %v", rec, err)
	}
	return rec.Stream
}

func (f *fixture) playlist() family.Playlist {
	f.t.Helper()
	pl, err := family.ReadPlaylist(f.target.Playlist)
	if err != nil {
		f.t.Fatal(err)
	}
	return pl
}

// starts are the renders the fake editor ran: "start <published units> <snapshot>" each.
func (f *fixture) starts() []string {
	b, _ := os.ReadFile(f.log)
	if s := strings.TrimSpace(string(b)); s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProgressPublishesBurstBeforeNextSlowUnit(t *testing.T) {
	f, m := newFixture(t, 10*time.Second, "FAKE_SLEEP=0.005", "FAKE_PAUSE_THIRD=1")
	m.o.Progress = 100 * time.Millisecond
	if _, err := m.Watch(f.target, "page"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cached burst's progress", func() bool {
		f.events.mu.Lock()
		defer f.events.mu.Unlock()
		for _, e := range f.events.evs {
			if e.Why == project.WhyProgress && e.Stream != nil && e.Stream.Done == 2 {
				return true
			}
		}
		return false
	})
	if got := f.playlist().Units; got != 2 {
		t.Fatalf("progress should arrive before the slow third unit: got %d", got)
	}
}

func TestWatchRendersStopsAndResumes(t *testing.T) {
	f, m := newFixture(t, 10*time.Second, "FAKE_SLEEP=0.15")
	pl := f.target.Playlist
	if st, err := m.State(pl); err != nil || st.State != family.StatePending || st.Total != 4 {
		t.Fatalf("before = %+v %v", st, err)
	}
	if _, err := os.Stat(f.log); err == nil {
		t.Fatal("nothing renders before someone watches")
	}

	// Opened: it renders, from the snapshot, and the page hears how far it is.
	if _, err := m.Watch(f.target, "phone"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two units", func() bool { return f.playlist().Units >= 2 })
	if st, _ := m.State(pl); st.State != family.StateRendering || st.Done < 2 || float64(st.Seconds) < 4 {
		t.Fatalf("rendering = %+v", st)
	}
	if s := f.record(); s.State != family.StateRendering || s.Renders != 1 || s.FirstS == nil {
		t.Fatalf("record = %+v", s)
	}
	if got := f.starts(); len(got) != 1 || got[0] != "start 0 clip_v7.timeline.json" {
		t.Fatalf("starts = %q", got)
	}

	// Closed: the last viewer leaves, the render stops; what it published stays, and it's pending again.
	m.Unwatch(pl, "phone")
	waitFor(t, "the render to stop", func() bool { return !m.Rendering(pl) })
	stopped := f.playlist()
	if stopped.Ended || stopped.Units == 0 || stopped.Units == 4 {
		t.Fatalf("stopped = %+v", stopped)
	}
	if s := f.record(); s.State != family.StatePending || s.Done != stopped.Units {
		t.Fatalf("record after the stop = %+v", s)
	}
	if family.RenderLive(pl) {
		t.Fatal("the lock is released")
	}
	time.Sleep(300 * time.Millisecond)
	if f.playlist().Units != stopped.Units {
		t.Fatal("nothing renders once nobody watches")
	}
	if !strings.Contains(f.events.whys(), "rendering progress") || !strings.HasSuffix(f.events.whys(), "stopped") {
		t.Fatalf("events = %q", f.events.whys())
	}

	// Opened again: it resumes after the units it published, and completes.
	if _, err := m.Watch(f.target, "desk"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the end", func() bool { return f.playlist().Ended })
	waitFor(t, "the render to finish", func() bool { return !m.Rendering(pl) })
	if got := f.starts(); len(got) != 2 || got[1] != "start "+strconv.Itoa(stopped.Units)+" clip_v7.timeline.json" {
		t.Fatalf("resumed after unit %d: %q", stopped.Units, got)
	}
	if s := f.record(); s.State != family.StateComplete || s.Done != 4 || s.Renders != 2 || s.DoneS == nil ||
		*s.DoneS != 0.4 {
		t.Fatalf("record when complete = %+v", s)
	}
	if !strings.HasSuffix(f.events.whys(), "rendering progress complete") {
		t.Fatalf("events = %q", f.events.whys())
	}
	var last project.VersionEvent
	f.events.mu.Lock()
	last = f.events.evs[len(f.events.evs)-1]
	f.events.mu.Unlock()
	if last.Label != "v7" || last.Stream == nil || last.Stream.State != family.StateComplete || last.Stream.Done != 4 {
		t.Fatalf("complete event = %+v", last)
	}
	js, _ := json.Marshal(last)
	if !strings.Contains(string(js), `"stream":{"state":"complete","done":4,"total":4,"seconds":8`) {
		t.Fatalf("event json = %s", js)
	}

	// Complete: watching it never renders again.
	n := len(f.starts())
	if st, err := m.Watch(f.target, "desk"); err != nil || st.State != family.StateComplete {
		t.Fatalf("complete = %+v %v", st, err)
	}
	time.Sleep(100 * time.Millisecond)
	if len(f.starts()) != n || m.Rendering(pl) {
		t.Fatal("a complete version doesn't render")
	}
}

func TestViewersAndLeases(t *testing.T) {
	f, m := newFixture(t, 300*time.Millisecond, "FAKE_SLEEP=0.2", "FAKE_TOTAL=40")
	pl := f.target.Playlist
	for _, v := range []string{"a", "b"} {
		if _, err := m.Watch(f.target, v); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "a unit", func() bool { return f.playlist().Units >= 1 })
	m.Unwatch(pl, "a")
	time.Sleep(50 * time.Millisecond)
	if !m.Rendering(pl) {
		t.Fatal("one viewer left, one still watches: it renders on")
	}
	// b's lease runs out without a heartbeat (a phone that went to sleep): it stops on its own.
	waitFor(t, "the lease to run out", func() bool { return !m.Rendering(pl) })
	if f.playlist().Ended {
		t.Fatal("stopped, not finished")
	}
	// A heartbeat keeps it going past the lease.
	if _, err := m.Watch(f.target, "c"); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		time.Sleep(100 * time.Millisecond)
		if _, err := m.Watch(f.target, "c"); err != nil {
			t.Fatal(err)
		}
	}
	if !m.Rendering(pl) {
		t.Fatal("heartbeats keep it rendering")
	}
	m.Unwatch(pl, "c")
	waitFor(t, "the stop", func() bool { return !m.Rendering(pl) })

	// Another process renders it (it holds the lock): this one doesn't start a second render.
	unlock, err := family.LockRender(pl)
	if err != nil {
		t.Fatal(err)
	}
	n := len(f.starts())
	if st, err := m.Watch(f.target, "d"); err != nil || st.State != family.StateRendering {
		t.Fatalf("someone else renders it: %+v %v", st, err)
	}
	time.Sleep(100 * time.Millisecond)
	if len(f.starts()) != n {
		t.Fatal("a second render started")
	}
	unlock()
}

func TestFailuresAndNotStreamed(t *testing.T) {
	f, m := newFixture(t, 10*time.Second, "FAKE_FAIL=1")
	if _, err := m.Watch(f.target, "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure", func() bool { return strings.HasSuffix(f.events.whys(), "failed") })
	waitFor(t, "the render to end", func() bool { return !m.Rendering(f.target.Playlist) })
	if s := f.record(); s.State != family.StatePending || !strings.Contains(s.Error, "encoding u2") {
		t.Fatalf("record = %+v", s)
	}
	f.events.mu.Lock()
	last := f.events.evs[len(f.events.evs)-1]
	f.events.mu.Unlock()
	if !strings.Contains(last.Error, "encoding u2") {
		t.Fatalf("failed event = %+v", last)
	}
	other := filepath.Join(f.root, "v6", "clip_v6.mp4")
	write(t, other, "six")
	if _, err := m.Watch(Target{Label: "v6", Playlist: other, Root: f.root}, "a"); !errors.Is(err, ErrNotStreamed) {
		t.Fatalf("not streamed: %v", err)
	}
}
