// Package store reads and writes takes: <version dir>/<version stem>.review/<YYYY-MM-DD_HH-MM-SS>/. It writes the
// take format of spec/take-format.md unchanged (the legacy player's), plus the server-set "author".
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
)

var (
	takeRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`)
	segRE  = regexp.MustCompile(`^seg_\d{2,3}\.(webm|ogg|m4a)$`)
)

// FileMode is the mode of files the store creates. Takes are plain user documents that other tools read, so they
// get the usual 0644 (before umask), like the legacy player's.
const FileMode fs.FileMode = 0o644

// DirMode is the mode of folders the store creates.
const DirMode fs.FileMode = 0o755

// ValidTakeID reports whether id is a take folder name (YYYY-MM-DD_HH-MM-SS).
func ValidTakeID(id string) bool { return takeRE.MatchString(id) }

// ValidSegName reports whether name is an audio segment file name (seg_01.webm, seg_123.ogg, ...).
func ValidSegName(name string) bool { return segRE.MatchString(name) }

// ReviewDir returns the folder that holds a version's takes: <dir>/<stem>.review.
func ReviewDir(video string) string {
	return filepath.Join(filepath.Dir(video), family.Stem(filepath.Base(video))+".review")
}

// TakeDir returns the folder of one take of a version.
func TakeDir(video, take string) string {
	return filepath.Join(ReviewDir(video), take)
}

// WriteFileAtomic writes data to path through a temp file in the same folder and a rename, so a reader never sees
// a half-written file.
func WriteFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, FileMode) //nolint:gosec // a take file; callers validate the name
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// WriteJSON writes a raw JSON value to path atomically, indented like the legacy player's files.
func WriteJSON(path string, raw []byte) error {
	b, err := jsonx.Indent1(raw)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, b)
}

// OffsetError is returned by WriteAudio when the offset is negative or past the end of the file. Size is the
// file's current size, from which the client resumes.
type OffsetError struct{ Size int64 }

func (e *OffsetError) Error() string { return fmt.Sprintf("offset past end (size %d)", e.Size) }

// WriteAudio writes body at offset into path and truncates the file after it, so a resent chunk replaces what was
// there. It returns the new size, offset+len(body). The take folder is created if needed, but not for an
// OffsetError.
func WriteAudio(path string, offset int64, body []byte) (int64, error) {
	var size int64
	st, err := os.Stat(path)
	switch {
	case err == nil:
		size = st.Size()
	case !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}
	if offset < 0 || offset > size {
		return 0, &OffsetError{Size: size}
	}
	if err := MkdirTake(filepath.Dir(path)); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, FileMode) //nolint:gosec // seg_NN.<ext>, validated by the server
	if err != nil {
		return 0, err
	}
	if _, err := f.WriteAt(body, offset); err != nil {
		_ = f.Close()
		return 0, err
	}
	end := offset + int64(len(body))
	if err := f.Truncate(end); err != nil {
		_ = f.Close()
		return 0, err
	}
	return end, f.Close()
}

// ErrBadJSON is returned by SaveData for a body that isn't a JSON object.
var ErrBadJSON = errors.New("bad json")

// VersionRef is one entry of session.json "versions".
type VersionRef struct {
	Label    string       `json:"label"`
	Path     string       `json:"path"`
	Duration *jsonx.Float `json:"duration"`
}

// VideoRef is session.json "video": the version the take was recorded on.
type VideoRef struct {
	Path     string       `json:"path"`
	Name     string       `json:"name"`
	Duration *jsonx.Float `json:"duration"`
	FPS      jsonx.Float  `json:"fps"`
	Width    *int         `json:"width"`
	Height   *int         `json:"height"`
}

// Author is session.json "author" (new in v1).
type Author struct {
	Name string `json:"name"`
}

// ServerFields are the session.json fields the server sets over the client's.
type ServerFields struct {
	Version  string
	Versions []VersionRef
	Video    VideoRef
	Author   *Author // nil leaves "author" out
}

// MkdirTake creates a take folder (and its .review parent) if needed.
func MkdirTake(dir string) error {
	return os.MkdirAll(dir, DirMode)
}

// SaveData writes a take's JSON files from a /data request body into dir:
// session.json (the client's session plus fields), segments.json, events.json, strokes.json and voice.json.
// Each is written atomically. Missing or empty values become [] or {}, as in the legacy player. The body is
// checked before anything is written, so a bad body (ErrBadJSON) creates no folder.
func SaveData(dir string, body []byte, sf ServerFields) error {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if len(body) == 0 {
		body = []byte("{}")
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		return ErrBadJSON
	}
	data, err := jsonx.ParseObject(body)
	if err != nil {
		return ErrBadJSON
	}
	meta := jsonx.NewObject()
	if raw, ok := data.Get("session"); ok && jsonx.Truthy(raw) {
		if meta, err = jsonx.ParseObject(raw); err != nil {
			return ErrBadJSON
		}
	}
	if err := meta.SetValue("version", sf.Version); err != nil {
		return err
	}
	versions := sf.Versions
	if versions == nil {
		versions = []VersionRef{}
	}
	if err := meta.SetValue("versions", versions); err != nil {
		return err
	}
	if err := meta.SetValue("video", sf.Video); err != nil {
		return err
	}
	if sf.Author != nil {
		if err := meta.SetValue("author", sf.Author); err != nil {
			return err
		}
	}
	session, err := meta.MarshalJSON()
	if err != nil {
		return err
	}
	if err := MkdirTake(dir); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(dir, "session.json"), session); err != nil {
		return err
	}
	for _, f := range []struct{ key, empty string }{
		{"segments", "[]"}, {"events", "[]"}, {"strokes", "[]"}, {"voice", "{}"},
	} {
		raw, ok := data.Get(f.key)
		if !ok || !jsonx.Truthy(raw) {
			raw = json.RawMessage(f.empty)
		}
		if err := WriteJSON(filepath.Join(dir, f.key+".json"), raw); err != nil {
			return err
		}
	}
	return nil
}
