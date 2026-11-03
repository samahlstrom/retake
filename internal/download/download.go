// Package download gets a version ready to download as one MP4 that YouTube takes as it is (spec/api.md,
// "Download"): H.264 High yuv420p BT.709, AAC-LC 48 kHz stereo at -14 LUFS with the true peak at or below -1 dBTP,
// faststart. A version file that is all that already is served as it is; any other is made once with ffmpeg into
// <root>/.retake/downloads/. A rendered version downloads the renderer's export, packaged when it's missing.
//
// The retake server uses it for the versions on its own disk, and an editing worker for its project's, which it
// relays to a hosted server.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
)

// States of a download.
const (
	StateNone        = "none"        // nothing checked or made yet
	StateChecking    = "checking"    // looking at the file
	StateMaking      = "making"      // making the MP4
	StateReady       = "ready"       // File is it
	StateFailed      = "failed"      // Error says why; getting it ready again tries again
	StateUnavailable = "unavailable" // Error says why there is nothing to download (still rendering, not a video)
)

// recipe names what this package makes: a change to what it writes gets new cache keys.
const recipe = "download/1"

// Source is one version to download.
type Source struct {
	// Root is the project root: checks and made files go in <Root>/.retake/downloads/.
	Root string
	// Path is the version's file (a playlist for a rendered version).
	Path string
	// Render, for a rendered version of a timeline project, is how to package its export; nil for a version file.
	Render *Render
	// Keep says the audio keeps its own level: the timeline's master sets a loudness.
	Keep bool
	// Env is the editor's environment (RETAKE_ENCODER, from [render] encoder), for the video encoder.
	Env []string
	// Manifest is the version's render manifest, for its license report; "" = none.
	Manifest string
	// Unavailable, when not "", is why there is nothing to download (StateUnavailable's error).
	Unavailable string
}

// Render is how a rendered version's export is packaged (spec/bridge.md, "Download").
type Render struct {
	Editor   bridge.Editor
	Bin      string // retake-edit; "" = Editor.Find()
	Timeline string // the timeline file
	Doc      string // the version's snapshot of it
	Export   string // the export: <stem>.export.mp4 beside the playlist
	Draft    bool
}

// Status is where a version's download is (GET /api/download, without the label and name the server adds).
type Status struct {
	State    string   `json:"state"`
	Progress *float64 `json:"progress"`
	Size     int64    `json:"size"`
	AsIs     bool     `json:"as_is"`
	Error    string   `json:"error"`
	License  string   `json:"license"`
	// Modified is the ready file's mtime (Unix ms), so a relayed download made again is a new file to a cache.
	Modified int64 `json:"modified,omitempty"`
	// File is the ready file, on this computer.
	File string `json:"-"`
}

// Manager runs the checks and makes the files, one job per file at a time, and keeps what's running in memory (what
// is done is on disk). The zero value is ready to use.
type Manager struct {
	FFmpeg, FFprobe string              // "" = on PATH
	Getenv          func(string) string // nil = os.Getenv
	Log             io.Writer           // nil = discard
	// Jobs is how many checks or makes run at once; 0 = 2.
	Jobs int

	mu       sync.Mutex
	jobs     map[string]*job
	failed   map[string]string // the last failure per key, until it is tried again
	slots    chan struct{}
	licenses map[string]license
}

// Default is the process's Manager.
var Default = &Manager{}

type job struct {
	state    string
	progress *float64
}

type license struct {
	modified time.Time
	line     string
}

func (m *Manager) ffmpeg() string {
	if m.FFmpeg != "" {
		return m.FFmpeg
	}
	return "ffmpeg"
}

func (m *Manager) ffprobe() string {
	if m.FFprobe != "" {
		return m.FFprobe
	}
	return "ffprobe"
}

func (m *Manager) getenv(k string) string {
	if m.Getenv != nil {
		return m.Getenv(k)
	}
	return os.Getenv(k)
}

func (m *Manager) logf(format string, a ...any) {
	if m.Log != nil {
		_, _ = fmt.Fprintf(m.Log, format, a...)
	}
}

// Dir is where checks and made files are kept, under the project root.
func Dir(root string) string { return filepath.Join(root, ".retake", "downloads") }

// record is a file's check, kept as <key>.json: whether it is a download as it is, and what making one does.
type record struct {
	AsIs  bool   `json:"as_is"`
	Check Check  `json:"check"`
	Made  bool   `json:"made,omitempty"` // this package (or the renderer, for it) made the file
	At    string `json:"at"`
}

// keyOf names a file's check and made download: its path, size and mtime, and the recipe.
func keyOf(path string, st os.FileInfo, keep bool) string {
	h := sha256.Sum256([]byte(recipe + "\x00" + path + "\x00" + strconv.FormatInt(st.Size(), 10) + "\x00" +
		strconv.FormatInt(st.ModTime().UnixNano(), 10) + "\x00" + strconv.FormatBool(keep)))
	return hex.EncodeToString(h[:16])
}

// target is the file a source's download checks: the version file, or a rendered version's export.
func (s Source) target() string {
	if s.Render != nil {
		return s.Render.Export
	}
	return s.Path
}

// jobKey is the source's job: one per version file or export, whatever its contents.
func (s Source) jobKey() string { return "job:" + s.target() }

// Ready is Status without the license line, for reading the ready file.
func (m *Manager) Ready(s Source) Status { return m.status(s) }

// Status says where a source's download is, starting nothing.
func (m *Manager) Status(ctx context.Context, s Source) Status {
	st := m.status(s)
	st.License = m.License(ctx, s)
	return st
}

func (m *Manager) status(s Source) Status {
	if s.Unavailable != "" {
		return Status{State: StateUnavailable, Error: s.Unavailable}
	}
	m.mu.Lock()
	var running *Status
	if j := m.jobs[s.jobKey()]; j != nil {
		running = &Status{State: j.state}
		if j.progress != nil {
			p := *j.progress
			running.Progress = &p
		}
	}
	failed, isFailed := m.failed[s.jobKey()]
	m.mu.Unlock()
	if running != nil {
		return *running
	}
	if isFailed {
		return Status{State: StateFailed, Error: failed}
	}
	return m.ready(s)
}

// ready is the download when it's ready on disk, else StateNone.
func (m *Manager) ready(s Source) Status {
	none := Status{State: StateNone}
	path := s.target()
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		if s.Render == nil {
			return Status{State: StateFailed, Error: "Can't read the version's file"}
		}
		return none // an export to package
	}
	key := keyOf(path, fi, s.Keep)
	rec, ok := readRecord(filepath.Join(Dir(s.Root), key+".json"))
	if !ok {
		return none
	}
	if rec.AsIs {
		return Status{State: StateReady, File: path, Size: fi.Size(), AsIs: s.Render == nil && !rec.Made,
			Modified: fi.ModTime().UnixMilli()}
	}
	if s.Render != nil {
		return none // an export that misses the mark: packaged again
	}
	made := filepath.Join(Dir(s.Root), key+".mp4")
	if mf, err := os.Stat(made); err == nil && mf.Mode().IsRegular() {
		return Status{State: StateReady, File: made, Size: mf.Size(), Modified: mf.ModTime().UnixMilli()}
	}
	return none
}

func readRecord(path string) (record, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // our own cache file
	if err != nil {
		return record{}, false
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return record{}, false
	}
	return r, true
}

func writeRecord(path string, r record) error {
	r.At = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a project folder others may read
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil { //nolint:gosec // as the posters: the project's own cache
		return err
	}
	return os.Rename(tmp, path)
}

// Start gets a source's download ready in the background when it's StateNone or StateFailed, and says where it is.
func (m *Manager) Start(ctx context.Context, s Source) Status {
	st := m.status(s)
	if st.State == StateNone || st.State == StateFailed {
		m.mu.Lock()
		if m.jobs == nil {
			m.jobs, m.failed = map[string]*job{}, map[string]string{}
		}
		if m.jobs[s.jobKey()] == nil {
			delete(m.failed, s.jobKey())
			j := &job{state: StateChecking}
			m.jobs[s.jobKey()] = j
			go m.run(s, j) //nolint:gosec // the job outlives the request that started it: closing the menu doesn't stop it
		}
		m.mu.Unlock()
		st = m.status(s)
	}
	st.License = m.License(ctx, s)
	return st
}

// Wait gets a source's download ready and waits for it (tests, the CLI).
func (m *Manager) Wait(ctx context.Context, s Source) Status {
	st := m.Start(ctx, s)
	for st.State == StateChecking || st.State == StateMaking {
		select {
		case <-ctx.Done():
			return Status{State: StateFailed, Error: ctx.Err().Error()}
		case <-time.After(50 * time.Millisecond):
		}
		st = m.Status(ctx, s)
	}
	return st
}

func (m *Manager) set(j *job, state string, progress *float64) {
	m.mu.Lock()
	j.state, j.progress = state, progress
	m.mu.Unlock()
}

// run is one job: check the file, and make the download when it isn't one.
func (m *Manager) run(s Source, j *job) {
	m.mu.Lock()
	if m.slots == nil {
		n := m.Jobs
		if n <= 0 {
			n = 2
		}
		m.slots = make(chan struct{}, n)
	}
	slots := m.slots
	m.mu.Unlock()
	slots <- struct{}{}
	err := m.prepare(context.Background(), s, j)
	<-slots
	m.mu.Lock()
	delete(m.jobs, s.jobKey())
	if err != nil {
		m.failed[s.jobKey()] = sentence(err)
	}
	m.mu.Unlock()
	if err != nil {
		m.logf("retake: download of %s: %v\n", s.Path, err)
	}
}

func (m *Manager) prepare(ctx context.Context, s Source, j *job) error {
	if s.Render != nil {
		return m.prepareRender(ctx, s, j)
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		return errors.New("can't read the version's file")
	}
	key := keyOf(s.Path, fi, s.Keep)
	c, err := m.check(ctx, s.Path, s.Keep)
	if err != nil {
		return err
	}
	rec := record{AsIs: len(c.Problems) == 0, Check: c}
	if err := writeRecord(filepath.Join(Dir(s.Root), key+".json"), rec); err != nil {
		return err
	}
	if rec.AsIs {
		return nil
	}
	return m.make(ctx, s.Path, filepath.Join(Dir(s.Root), key+".mp4"), c, s.Env, j)
}

// prepareRender reuses a rendered version's export when it passes the check, else packages it again.
func (m *Manager) prepareRender(ctx context.Context, s Source, j *job) error {
	r := s.Render
	if fi, err := os.Stat(r.Export); err == nil && fi.Mode().IsRegular() {
		c, err := m.check(ctx, r.Export, s.Keep)
		if err == nil && len(c.Problems) == 0 {
			return writeRecord(filepath.Join(Dir(s.Root), keyOf(r.Export, fi, s.Keep)+".json"), record{AsIs: true, Check: c})
		}
	}
	m.set(j, StateMaking, nil)
	if _, err := r.Editor.Find(); err != nil && r.Bin == "" {
		// No renderer here: ffmpeg makes it from the playlist instead.
		c, err := m.check(ctx, s.Path, s.Keep)
		if err != nil {
			return err
		}
		if err := m.make(ctx, s.Path, r.Export, c, s.Env, j); err != nil {
			return err
		}
	} else if err := m.renderExport(ctx, r); err != nil {
		return fmt.Errorf("couldn't package the export: %w", err)
	}
	fi, err := os.Stat(r.Export)
	if err != nil {
		return errors.New("the export wasn't written")
	}
	// What was just made is the download: it isn't measured again.
	return writeRecord(filepath.Join(Dir(s.Root), keyOf(r.Export, fi, s.Keep)+".json"), record{AsIs: true, Made: true})
}

// check probes a file, reads its box order, measures its loudness (unless keep), and judges it.
func (m *Manager) check(ctx context.Context, path string, keep bool) (Check, error) {
	c, err := m.probe(ctx, path)
	if err != nil {
		return c, err
	}
	c.FastStart = fastStart(path)
	if c.Audio != nil && !keep {
		if c.Loudness, err = m.measure(ctx, path); err != nil {
			return c, err
		}
	}
	c.judge(keep)
	return c, nil
}

// make writes the download of src at out with ffmpeg.
func (m *Manager) make(ctx context.Context, src, out string, c Check, env []string, j *job) error {
	if c.Plan == nil {
		c.judge(true)
	}
	zero := 0.0
	m.set(j, StateMaking, &zero)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a project folder others may read
		return err
	}
	return m.ffmpegMake(ctx, src, out, c, m.encoder(env), func(p float64) { m.set(j, StateMaking, &p) })
}

// Check looks at a file as a download would (tests, tools): its streams, box order and loudness, and what keeps it
// from being a download as it is.
func (m *Manager) Check(ctx context.Context, path string, keep bool) (Check, error) {
	return m.check(ctx, path, keep)
}

// sentence is an error as one plain line for the page.
func sentence(err error) string {
	s := err.Error()
	if s == "" {
		return "Couldn't make the MP4"
	}
	return "Couldn't make the MP4: " + s
}
