package bridge

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLineWriterSplitsLinesAcrossWrites(t *testing.T) {
	var rest bytes.Buffer
	var took []string
	w := &lineWriter{rest: &rest, take: func(l []byte) bool {
		if bytes.HasPrefix(l, []byte("{")) {
			took = append(took, string(l))
			return true
		}
		return false
	}}
	for _, chunk := range []string{`{"a"`, ":1}\nnoise\n{\"b\":2}", "\n", "tail"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(took, "|") != `{"a":1}|{"b":2}` || rest.String() != "noise\n" || string(w.partial) != "tail" {
		t.Fatalf("took %q, rest %q, partial %q", took, rest.String(), w.partial)
	}
}

func TestRenderStreamReportsProgressAsItComes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "retake-edit")
	script := `#!/bin/sh
echo "$@" > "$ARGS"
echo '{"progress": "plan", "units": 2, "reused": 1, "duration": 8, "fps": 25, "size": [64, 36]}' >&2
echo 'a warning' >&2
echo '{"progress": "publish", "done": 1, "total": 2, "seconds": 4, "elapsed": 0.5}' >&2
if [ -n "$FAIL" ]; then echo 'ffmpeg exploded' >&2; exit 3; fi
echo '{"progress": "end", "segments": 2, "seconds": 8, "elapsed": 1}' >&2
echo '{"ok": true, "units": 2, "reused": 1, "rendered": 1, "stream": {"playlist": "x.m3u8", "segments": 2, "first_s": 0.5, "done_s": 1}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(t.TempDir(), "args")
	e := Editor{Configured: bin, Env: []string{"ARGS=" + args}}
	var got []Progress
	rep, err := e.RenderStream(context.Background(), bin, StreamArgs{Timeline: "t.json", Doc: "v2/x_v2.timeline.json",
		Playlist: "v2/x_v2.m3u8", Export: "v2/x_v2.export.mp4", Draft: true}, func(p Progress) { got = append(got, p) })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) !=
		"render t.json --hls v2/x_v2.m3u8 --progress --json --doc v2/x_v2.timeline.json --draft -o v2/x_v2.export.mp4 "+
			"--export-loudness=-14" {
		t.Fatalf("args: %s", b)
	}
	if len(got) != 3 || got[0].Progress != "plan" || got[0].Units != 2 || got[0].Duration != 8 || got[1].Done != 1 ||
		got[1].Seconds != 4 || got[2].Progress != "end" {
		t.Fatalf("progress = %+v", got)
	}
	if rep.Stream == nil || rep.Stream.Segments != 2 || rep.Stream.FirstS != 0.5 || rep.Units != 2 {
		t.Fatalf("report = %+v", rep)
	}

	// A failure keeps the other stderr lines as its message, not the progress.
	e.Env = append(e.Env, "FAIL=1")
	_, err = e.RenderStream(context.Background(), bin, StreamArgs{Timeline: "t.json", Playlist: "x.m3u8"}, nil)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg exploded") || !strings.Contains(err.Error(), "a warning") ||
		strings.Contains(err.Error(), "progress") {
		t.Fatalf("failure: %v", err)
	}
}
