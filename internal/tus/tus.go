// Package tus is the resumable upload protocol of spec/server.md, "Uploads": a tus 1.0.0 server
// (https://tus.io/protocols/resumable-upload) with the creation, termination and expiration extensions, which
// checks the whole file's SHA-256 when the last byte arrives, and the client `retake push --remote` sends versions
// with. It knows nothing about projects or tokens: the caller authenticates a request and names its owner.
package tus

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Version is the tus protocol version spoken here.
const Version = "1.0.0"

// Extensions are the tus extensions the server supports.
const Extensions = "creation,termination,expiration"

// Defaults of a Handler.
const (
	DefaultMaxSize int64 = 64 << 30       // the largest upload (64 GiB)
	DefaultTTL           = 24 * time.Hour // an upload expires this long after its last PATCH
)

// StatusChecksumMismatch is tus's answer to data whose checksum doesn't match (the checksum extension's 460).
const StatusChecksumMismatch = 460

// OffsetType is the content type of a PATCH body.
const OffsetType = "application/offset+octet-stream"

// ErrNotFound is returned for an unknown upload, one of another owner, or an expired one.
var ErrNotFound = errors.New("tus: no such upload")

// idRE is the shape of an upload id: "up_" and 128 random bits in unpadded base64url. Ids are checked against it
// before they touch the disk.
var idRE = regexp.MustCompile(`^up_[A-Za-z0-9_-]{22}$`)

// ValidID reports whether id has the shape of an upload id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// videoExts are the file name extensions an upload may have: the single-file videos of spec/api.md.
var videoExts = []string{".mp4", ".m4v", ".mov", ".webm", ".mkv"}

// shaRE is a SHA-256 in hex.
var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// emptySHA256 is the SHA-256 of no bytes.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Upload is one upload: what its creation said, and how far it got.
type Upload struct {
	ID       string
	Owner    string // the project it belongs to
	Filename string // the name the client gave, a video file name
	SHA256   string // the whole file's SHA-256, hex, lower case
	Size     int64  // Upload-Length
	Offset   int64  // the bytes received so far: the data file's size
	Complete bool   // every byte arrived and the SHA-256 matched
	Created  time.Time
	Expires  time.Time
}

// state is an upload's <id>.json. The offset isn't in it: the data file's size is the offset.
type state struct {
	ID       string    `json:"id"`
	Project  string    `json:"project"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	Complete bool      `json:"complete,omitempty"`
}

func (s state) upload(offset int64) Upload {
	return Upload{ID: s.ID, Owner: s.Project, Filename: s.Filename, SHA256: s.SHA256, Size: s.Size, Offset: offset,
		Complete: s.Complete, Created: s.Created, Expires: s.Expires}
}

// Handler serves the uploads of one server. Its zero value works once Dir is set; it must not be copied after use.
type Handler struct {
	Dir     string           // where uploads live: <id>.json (state) and <id><ext> (data); created on demand
	MaxSize int64            // the largest Upload-Length; 0 = DefaultMaxSize
	TTL     time.Duration    // an upload expires this long after its last PATCH; 0 = DefaultTTL
	Now     func() time.Time // nil = time.Now
	Log     io.Writer        // write failures; nil = discard

	locksMu sync.Mutex
	locks   map[string]*idLock
}

type idLock struct {
	mu sync.Mutex
	n  int // holders and waiters
}

// lock serializes everything done to one upload, across requests.
func (h *Handler) lock(id string) (unlock func()) {
	h.locksMu.Lock()
	if h.locks == nil {
		h.locks = map[string]*idLock{}
	}
	l := h.locks[id]
	if l == nil {
		l = &idLock{}
		h.locks[id] = l
	}
	l.n++
	h.locksMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		h.locksMu.Lock()
		if l.n--; l.n == 0 {
			delete(h.locks, id)
		}
		h.locksMu.Unlock()
	}
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) maxSize() int64 {
	if h.MaxSize > 0 {
		return h.MaxSize
	}
	return DefaultMaxSize
}

func (h *Handler) ttl() time.Duration {
	if h.TTL > 0 {
		return h.TTL
	}
	return DefaultTTL
}

func (h *Handler) logf(format string, args ...any) {
	if h.Log != nil {
		_, _ = fmt.Fprintf(h.Log, "retake: uploads: "+format+"\n", args...)
	}
}

func (h *Handler) statePath(id string) string { return filepath.Join(h.Dir, id+".json") }

func dataName(id, filename string) string { return id + strings.ToLower(filepath.Ext(filename)) }

// DataPath is where an upload's bytes are: Dir/<id><ext>, with the file name's extension.
func (h *Handler) DataPath(u Upload) string { return filepath.Join(h.Dir, dataName(u.ID, u.Filename)) }

func (h *Handler) dataPath(s state) string { return filepath.Join(h.Dir, dataName(s.ID, s.Filename)) }

// readState reads an upload's state file. A missing or broken one is ErrNotFound.
func (h *Handler) readState(id string) (state, error) {
	var s state
	b, err := os.ReadFile(h.statePath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil || s.ID != id || !validFilename(s.Filename) {
		return s, ErrNotFound
	}
	return s, nil
}

// writeState writes an upload's state file atomically.
func (h *Handler) writeState(s state) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.Dir, "."+s.ID+".json.*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(b, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), h.statePath(s.ID))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// load returns a live upload of owner and its offset (the data file's size). Call it holding the upload's lock.
// An upload whose last byte arrived but wasn't checked (the server stopped in between) is checked now.
func (h *Handler) load(owner, id string) (state, int64, error) {
	if !ValidID(id) {
		return state{}, 0, ErrNotFound
	}
	s, err := h.readState(id)
	if err != nil {
		return s, 0, err
	}
	if s.Project != owner || !h.now().Before(s.Expires) {
		return s, 0, ErrNotFound
	}
	st, err := os.Stat(h.dataPath(s))
	if errors.Is(err, fs.ErrNotExist) {
		return s, 0, ErrNotFound // pushed (moved away), or lost
	}
	if err != nil {
		return s, 0, err
	}
	offset := st.Size()
	if !s.Complete && offset == s.Size {
		if s, offset, err = h.finish(s); err != nil && !errors.Is(err, errMismatch) {
			return s, 0, err
		}
	}
	return s, offset, nil
}

var errMismatch = errors.New("sha256 mismatch")

// finish checks a fully received upload's SHA-256. A match completes it; a mismatch empties it (the offset goes back
// to 0) and returns an error wrapping errMismatch.
func (h *Handler) finish(s state) (state, int64, error) {
	got, err := FileSHA256(h.dataPath(s))
	if err != nil {
		return s, 0, err
	}
	if got != s.SHA256 {
		if err := os.Truncate(h.dataPath(s), 0); err != nil {
			return s, 0, err
		}
		return s, 0, fmt.Errorf("%w: got %s, want %s", errMismatch, got, s.SHA256)
	}
	s.Complete = true
	if err := h.writeState(s); err != nil {
		return s, s.Size, err
	}
	return s, s.Size, nil
}

// Lookup returns an upload of owner, complete or not. It is ErrNotFound for an unknown upload, one of another owner,
// an expired one, or one whose data is gone.
func (h *Handler) Lookup(owner, id string) (Upload, error) {
	if !ValidID(id) {
		return Upload{}, ErrNotFound
	}
	unlock := h.lock(id)
	defer unlock()
	s, offset, err := h.load(owner, id)
	if err != nil {
		return Upload{}, err
	}
	return s.upload(offset), nil
}

// Remove deletes an upload of owner: its state and its data (data that is already gone is fine).
func (h *Handler) Remove(owner, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	unlock := h.lock(id)
	defer unlock()
	s, err := h.readState(id)
	if err != nil {
		return err
	}
	if s.Project != owner {
		return ErrNotFound
	}
	return h.remove(s)
}

func (h *Handler) remove(s state) error {
	if err := os.Remove(h.dataPath(s)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(h.statePath(s.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Sweep deletes the expired uploads, the ones whose data is gone (a push moved it into the family), and data files
// that never got their state file and are older than the TTL. It returns how many uploads it deleted.
func (h *Handler) Sweep() (int, error) {
	entries, err := os.ReadDir(h.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	now := h.now()
	n := 0
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < 25 {
			continue
		}
		id := name[:25]
		if !ValidID(id) {
			continue
		}
		rest := name[25:]
		switch {
		case rest == ".json":
			unlock := h.lock(id)
			s, err := h.readState(id)
			if err == nil {
				_, statErr := os.Stat(h.dataPath(s))
				if !now.Before(s.Expires) || errors.Is(statErr, fs.ErrNotExist) {
					if err := h.remove(s); err != nil {
						note(err)
					} else {
						n++
					}
				}
			}
			unlock()
		case slices.Contains(videoExts, rest):
			unlock := h.lock(id)
			if _, err := os.Stat(h.statePath(id)); errors.Is(err, fs.ErrNotExist) {
				if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > h.ttl() {
					note(os.Remove(filepath.Join(h.Dir, name)))
				}
			}
			unlock()
		}
	}
	return n, firstErr
}

// newID returns a fresh upload id.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("making an upload id: %w", err)
	}
	return "up_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// validFilename reports whether name is a plain video file name: no folders, a video extension.
func validFilename(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	return slices.Contains(videoExts, strings.ToLower(filepath.Ext(name)))
}

// ParseMetadata parses an Upload-Metadata header: comma-separated "key base64value" pairs (the value may be left out).
func ParseMetadata(header string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(header) == "" {
		return out, nil
	}
	for pair := range strings.SplitSeq(header, ",") {
		pair = strings.TrimSpace(pair)
		key, val, _ := strings.Cut(pair, " ")
		if key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("bad Upload-Metadata pair %q", pair)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("Upload-Metadata names %q twice", key)
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
		if err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(val)); err != nil {
				return nil, fmt.Errorf("Upload-Metadata %q isn't base64", key)
			}
		}
		out[key] = string(b)
	}
	return out, nil
}

// EncodeMetadata makes an Upload-Metadata header, with the keys sorted.
func EncodeMetadata(meta map[string]string) string {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " " + base64.StdEncoding.EncodeToString([]byte(meta[k]))
	}
	return strings.Join(parts, ",")
}

// Serve answers one tus request. base is the collection's URL path (/p/<name>/api/uploads), owner the project, and id
// the path segment after base ("" for the collection itself). The caller has authenticated the request already.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, base, owner, id string) {
	hdr := w.Header()
	hdr.Set("Tus-Resumable", Version)
	if r.Method == http.MethodOptions {
		hdr.Set("Tus-Version", Version)
		hdr.Set("Tus-Extension", Extensions)
		hdr.Set("Tus-Max-Size", strconv.FormatInt(h.maxSize(), 10))
		hdr.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	allow := "OPTIONS, HEAD, PATCH, DELETE"
	if id == "" {
		allow = "OPTIONS, POST"
	}
	ok := r.Method == http.MethodPost && id == "" ||
		id != "" && (r.Method == http.MethodHead || r.Method == http.MethodPatch || r.Method == http.MethodDelete)
	if !ok {
		hdr.Set("Allow", allow)
		fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Header.Get("Tus-Resumable") != Version {
		hdr.Set("Tus-Version", Version)
		fail(w, r, http.StatusPreconditionFailed, "Tus-Resumable: "+Version+" required")
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.create(w, r, base, owner)
	case http.MethodHead:
		h.head(w, r, owner, id)
	case http.MethodPatch:
		h.patch(w, r, owner, id)
	case http.MethodDelete:
		h.delete(w, r, owner, id)
	}
}

// fail answers with a JSON error (no body for HEAD).
func fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	hdr := w.Header()
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	hdr.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *Handler) failed(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.logf("%s: %v", what, err)
	fail(w, r, http.StatusInternalServerError, what+" failed")
}

func setProgress(w http.ResponseWriter, s state, offset int64) {
	hdr := w.Header()
	hdr.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	hdr.Set("Upload-Expires", s.Expires.UTC().Format(http.TimeFormat))
	hdr.Set("Cache-Control", "no-store")
}

// create is POST: a new upload, with its length and metadata.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, base, owner string) {
	if r.Header.Get("Upload-Defer-Length") != "" {
		fail(w, r, http.StatusBadRequest, "Upload-Defer-Length isn't supported: send Upload-Length")
		return
	}
	size, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("Upload-Length")), 10, 64)
	if err != nil || size < 0 {
		fail(w, r, http.StatusBadRequest, "Upload-Length must be the file's size in bytes")
		return
	}
	if size > h.maxSize() {
		fail(w, r, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d bytes", h.maxSize()))
		return
	}
	meta, err := ParseMetadata(r.Header.Get("Upload-Metadata"))
	if err != nil {
		fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	name, sum := meta["filename"], strings.ToLower(strings.TrimSpace(meta["sha256"]))
	if !validFilename(name) {
		fail(w, r, http.StatusBadRequest, "Upload-Metadata filename must be a video file name (.mp4 .m4v .mov .webm .mkv)")
		return
	}
	if !shaRE.MatchString(sum) {
		fail(w, r, http.StatusBadRequest, "Upload-Metadata sha256 must be the file's SHA-256 in hex")
		return
	}
	if size == 0 && sum != emptySHA256 { // nothing will ever arrive to make it match
		fail(w, r, StatusChecksumMismatch, fmt.Sprintf("sha256 mismatch: got %s, want %s", emptySHA256, sum))
		return
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		h.failed(w, r, "creating the uploads folder", err)
		return
	}
	id, err := newID()
	if err != nil {
		h.failed(w, r, "creating the upload", err)
		return
	}
	now := h.now().UTC()
	s := state{ID: id, Project: owner, Filename: name, Size: size, SHA256: sum, Created: now, Expires: now.Add(h.ttl())}
	unlock := h.lock(id)
	defer unlock()
	f, err := os.OpenFile(h.dataPath(s), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		h.failed(w, r, "creating the upload", err)
		return
	}
	_ = f.Close()
	s.Complete = size == 0 // an empty file's SHA-256 was checked above
	if err := h.writeState(s); err != nil {
		_ = os.Remove(h.dataPath(s))
		h.failed(w, r, "creating the upload", err)
		return
	}
	setProgress(w, s, 0)
	w.Header().Set("Location", strings.TrimSuffix(base, "/")+"/"+id)
	w.WriteHeader(http.StatusCreated)
}

// head is HEAD /<id>: how far the upload got.
func (h *Handler) head(w http.ResponseWriter, r *http.Request, owner, id string) {
	unlock := h.lock(id)
	defer unlock()
	s, offset, err := h.load(owner, id)
	if errors.Is(err, ErrNotFound) {
		fail(w, r, http.StatusNotFound, "no such upload")
		return
	}
	if err != nil {
		h.failed(w, r, "reading the upload", err)
		return
	}
	setProgress(w, s, offset)
	hdr := w.Header()
	hdr.Set("Upload-Length", strconv.FormatInt(s.Size, 10))
	hdr.Set("Upload-Metadata", EncodeMetadata(map[string]string{"filename": s.Filename, "sha256": s.SHA256}))
	w.WriteHeader(http.StatusOK)
}

// patch is PATCH /<id>: more bytes at the upload's offset.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request, owner, id string) {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != OffsetType {
		fail(w, r, http.StatusUnsupportedMediaType, "Content-Type must be "+OffsetType)
		return
	}
	at, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("Upload-Offset")), 10, 64)
	if err != nil || at < 0 {
		fail(w, r, http.StatusBadRequest, "Upload-Offset must be a byte offset")
		return
	}
	unlock := h.lock(id)
	defer unlock()
	s, offset, err := h.load(owner, id)
	if errors.Is(err, ErrNotFound) {
		fail(w, r, http.StatusNotFound, "no such upload")
		return
	}
	if err != nil {
		h.failed(w, r, "reading the upload", err)
		return
	}
	if at != offset {
		setProgress(w, s, offset)
		fail(w, r, http.StatusConflict, fmt.Sprintf("Upload-Offset %d isn't the upload's offset %d", at, offset))
		return
	}
	if s.Complete {
		setProgress(w, s, offset)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	n, tooLong, readErr, err := h.appendBody(s, offset, r.Body)
	if err != nil {
		h.failed(w, r, "writing the upload", err)
		return
	}
	offset += n
	s.Expires = h.now().UTC().Add(h.ttl())
	if err := h.writeState(s); err != nil {
		h.failed(w, r, "writing the upload", err)
		return
	}
	if offset == s.Size {
		var ferr error
		s, offset, ferr = h.finish(s)
		if errors.Is(ferr, errMismatch) {
			setProgress(w, s, 0)
			fail(w, r, StatusChecksumMismatch, ferr.Error())
			return
		}
		if ferr != nil {
			h.failed(w, r, "checking the upload", ferr)
			return
		}
	}
	setProgress(w, s, offset)
	switch {
	case tooLong:
		fail(w, r, http.StatusRequestEntityTooLarge, fmt.Sprintf("the body goes past Upload-Length %d", s.Size))
	case readErr != nil:
		// The client is probably gone; whatever arrived is kept, and HEAD tells it where to go on from.
		fail(w, r, http.StatusBadRequest, "the body was cut off")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// appendBody writes the body at offset as it arrives, up to the upload's length. It returns how many bytes it
// wrote, whether the body went past the length, and the body's read error (the connection broke: what arrived is
// kept). err is a write error.
func (h *Handler) appendBody(s state, offset int64, body io.Reader) (n int64, tooLong bool, readErr, err error) {
	f, err := os.OpenFile(h.dataPath(s), os.O_WRONLY, 0)
	if err != nil {
		return 0, false, nil, err
	}
	defer func() {
		if serr := f.Sync(); err == nil {
			err = serr
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, false, nil, err
	}
	left := s.Size - offset
	buf := make([]byte, 256<<10)
	lr := io.LimitReader(body, left+1) // never trust Content-Length: read one byte past the end, at most
	for {
		k, rerr := lr.Read(buf)
		if k > 0 {
			keep := int64(k)
			if n+keep > left {
				keep, tooLong = left-n, true
			}
			if keep > 0 {
				if _, err := f.Write(buf[:keep]); err != nil {
					return n, tooLong, nil, err
				}
				n += keep
			}
			if tooLong {
				return n, true, nil, nil
			}
		}
		if errors.Is(rerr, io.EOF) {
			return n, false, nil, nil
		}
		if rerr != nil {
			return n, false, rerr, nil
		}
	}
}

// delete is DELETE /<id>: the termination extension.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, owner, id string) {
	if !ValidID(id) {
		fail(w, r, http.StatusNotFound, "no such upload")
		return
	}
	unlock := h.lock(id)
	defer unlock()
	s, _, err := h.load(owner, id)
	if errors.Is(err, ErrNotFound) {
		fail(w, r, http.StatusNotFound, "no such upload")
		return
	}
	if err != nil {
		h.failed(w, r, "reading the upload", err)
		return
	}
	if err := h.remove(s); err != nil {
		h.failed(w, r, "removing the upload", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a file the caller names
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
