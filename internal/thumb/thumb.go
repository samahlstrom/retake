// Package thumb makes a version's poster: the first frame of its video as a JPEG, for the review page's
// <video poster> and the home screen's cards (spec/home.md, "Thumbnails and the poster"). The streaming side's
// poster beside the file wins; otherwise ffmpeg makes one frame at t = 0, once, cached under the project root's
// .retake/thumbs/.
package thumb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/family"
)

// Width is the widest a made thumbnail gets, in pixels.
const Width = 640

// Timeout bounds one ffmpeg run.
const Timeout = 30 * time.Second

// ErrNone says a version has no poster: it has no frame ffmpeg can read (yet), or ffmpeg isn't installed.
var ErrNone = errors.New("no poster")

// Dir is where made thumbnails are cached, under the project root.
func Dir(root string) string { return filepath.Join(root, ".retake", "thumbs") }

// PosterPath is where the streaming side puts a version's poster: <stem>.poster.jpg beside its file.
func PosterPath(video string) string {
	return filepath.Join(filepath.Dir(video), family.Stem(filepath.Base(video))+".poster.jpg")
}

// Maker makes and caches thumbnails. The zero value is ready to use.
type Maker struct {
	// Run makes one frame of video into out (a JPEG); nil runs ffmpeg. Tests swap it.
	Run func(ctx context.Context, video, out string) error

	mu     sync.Mutex
	locks  map[string]*sync.Mutex // one generation at a time per cached file
	failed map[string]error       // files ffmpeg couldn't read, not tried again
}

// Default is the process's Maker.
var Default = &Maker{}

// For returns the poster of a version file: the streaming side's when it exists, else the cached first frame (made
// now if needed). root is the project root, whose .retake/thumbs/ holds the cache. ErrNone when there is none.
func (m *Maker) For(ctx context.Context, root, video string) (string, error) {
	if st, err := os.Stat(PosterPath(video)); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
		return PosterPath(video), nil
	}
	return m.First(ctx, root, video)
}

// key names a version file's cached frame: the SHA-256 of its path, size and modification time.
func key(video string, st os.FileInfo) string {
	h := sha256.Sum256([]byte(video + "\x00" + strconv.FormatInt(st.Size(), 10) + "\x00" +
		strconv.FormatInt(st.ModTime().UnixNano(), 10)))
	return hex.EncodeToString(h[:])
}

// Cached returns the path the first frame of video is cached at, whether or not it exists yet.
func Cached(root, video string) (string, error) {
	st, err := os.Stat(video)
	if err != nil {
		return "", ErrNone
	}
	return filepath.Join(Dir(root), key(video, st)+".jpg"), nil
}

// First returns the first frame of video as a JPEG in the cache, making it once. A file whose size or modification
// time changed gets a new one. A file ffmpeg couldn't read gives ErrNone without trying again.
func (m *Maker) First(ctx context.Context, root, video string) (string, error) {
	out, err := Cached(root, video)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil
	}
	lock := m.lock(out)
	lock.Lock()
	defer lock.Unlock()
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil // another request made it meanwhile
	}
	m.mu.Lock()
	ferr := m.failed[out]
	m.mu.Unlock()
	if ferr != nil {
		return "", ferr
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".tmp.jpg")
	run := m.Run
	if run == nil {
		run = ffmpeg
	}
	rctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	err = run(rctx, video, tmp)
	if err == nil {
		if st, serr := os.Stat(tmp); serr != nil || st.Size() == 0 {
			err = errors.New("ffmpeg wrote no frame")
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		if ctx.Err() != nil {
			return "", ctx.Err() // the request went away: try again next time
		}
		ferr := fmt.Errorf("%w: %s: %w", ErrNone, filepath.Base(video), err)
		m.mu.Lock()
		if m.failed == nil {
			m.failed = map[string]error{}
		}
		m.failed[out] = ferr
		m.mu.Unlock()
		return "", ferr
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return out, nil
}

func (m *Maker) lock(out string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l, ok := m.locks[out]
	if !ok {
		l = &sync.Mutex{}
		m.locks[out] = l
	}
	return l
}

// ffmpeg writes the frame at t = 0 of video, at most Width pixels wide, as a JPEG.
func ffmpeg(ctx context.Context, video, out string) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("ffmpeg not found on $PATH")
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-threads", "2", "-ss", "0", "-i", video, "-map", "0:v:0",
		"-frames:v", "1", "-vf", "scale=min(" + strconv.Itoa(Width) + "\\,iw):-2", "-q:v", "4", "-f", "image2",
		"-c:v", "mjpeg", out}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // ffmpeg on a version file of the family
	b, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
