package relay

import (
	"bufio"
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Cache keeps chunks of relayed media on the host's disk (spec/automation.md, "The server's cache"), so later
// viewers of the same bytes don't cost the worker's upload again. Each chunk is one file, <dir>/<scope>/<hash>: a
// JSON header line (Meta), then the bytes. Once the files hold more than the cache's size, the least recently used
// go first. A scope is a project: Drop deletes all of it.
type Cache struct {
	dir string
	max int64

	mu      sync.Mutex
	used    int64
	lru     *list.List               // *cached; the front is the most recently used
	files   map[string]*list.Element // by scope/name
	flights map[string]chan struct{} // the chunks being fetched, closed when done
}

type cached struct {
	rel  string // scope/name
	size int64  // on disk, header included
}

// Meta describes a cached chunk.
type Meta struct {
	Type   string `json:"type"`
	Start  int64  `json:"start"`  // its first byte in the file
	Length int64  `json:"length"` // its bytes
	Total  int64  `json:"total"`  // the file's size
}

// maxHeader bounds a chunk file's header line.
const maxHeader = 4096

// OpenCache opens (making it) the cache in dir, keeping at most size bytes. What an earlier process left there is
// kept, oldest out first; unfinished chunks are deleted.
func OpenCache(dir string, size int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, max: size, lru: list.New(), files: map[string]*list.Element{},
		flights: map[string]chan struct{}{}}
	type found struct {
		rel  string
		size int64
		at   time.Time
	}
	var all []found
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.HasPrefix(d.Name(), ".") || strings.Count(rel, string(filepath.Separator)) != 1 {
			_ = os.Remove(p) //nolint:gosec // the cache's own folder, which only the server writes: an unfinished chunk
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // gone meanwhile
		}
		all = append(all, found{filepath.ToSlash(rel), info.Size(), info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b found) int { return a.at.Compare(b.at) })
	for _, f := range all {
		c.files[f.rel] = c.lru.PushFront(&cached{rel: f.rel, size: f.size})
		c.used += f.size
	}
	c.mu.Lock()
	c.evict()
	c.mu.Unlock()
	return c, nil
}

// Used is how many bytes the cache holds now.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Busy is how many chunks are being fetched into the cache right now. A served chunk is committed after the browser
// has its part, so tests wait for 0 before counting on what the cache holds.
func (c *Cache) Busy() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.flights)
}

// validScope accepts a project name: one plain path element.
func validScope(scope string) bool {
	return scope != "" && !strings.HasPrefix(scope, ".") && !strings.ContainsAny(scope, "/\\\x00") && filepath.IsLocal(scope)
}

func rel(scope, key string) string {
	sum := sha256.Sum256([]byte(key))
	return scope + "/" + hex.EncodeToString(sum[:20])
}

// Hit is a cached chunk, open for reading.
type Hit struct {
	Meta
	f   *os.File
	off int64
}

// Body reads the chunk's bytes from its first one.
func (h *Hit) Body() io.Reader { return io.NewSectionReader(h.f, h.off, h.Length) }

// Close closes the chunk's file.
func (h *Hit) Close() { _ = h.f.Close() }

// Get opens a cached chunk, and makes it the most recently used.
func (c *Cache) Get(scope, key string) (*Hit, bool) {
	if c == nil || !validScope(scope) {
		return nil, false
	}
	r := rel(scope, key)
	c.mu.Lock()
	el, ok := c.files[r]
	if ok {
		c.lru.MoveToFront(el)
	}
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	p := filepath.Join(c.dir, filepath.FromSlash(r))
	f, err := os.Open(p) //nolint:gosec // a name the cache made: scope/hex
	if err == nil {
		h, herr := readHit(f)
		if herr == nil {
			now := time.Now()
			_ = os.Chtimes(p, now, now) // so the order survives a restart
			return h, true
		}
		_ = f.Close()
	}
	c.mu.Lock()
	c.remove(r) // broken or gone: forget it
	c.mu.Unlock()
	return nil, false
}

func readHit(f *os.File) (*Hit, error) {
	br := bufio.NewReaderSize(io.LimitReader(f, maxHeader), maxHeader)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var m Meta
	if err = json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := int64(len(line))
	if m.Length < 0 || st.Size() != off+m.Length {
		return nil, errors.New("a cached chunk of the wrong size")
	}
	return &Hit{Meta: m, f: f, off: off}, nil
}

// Fill is a chunk being fetched by the one request that fetches it. Writes never fail: a chunk that couldn't be
// written just isn't kept.
type Fill struct {
	c    *Cache
	rel  string
	tmp  *os.File
	meta Meta
	n    int64
	err  error
	once sync.Once
}

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// Begin starts fetching a chunk that isn't cached. The caller that gets a Fill fetches it (Open, Write, then Commit
// or Abort); every other caller gets a channel that closes when that one is done, and then calls Get again. The
// channel is closed at once when the chunk is cached meanwhile.
func (c *Cache) Begin(scope, key string) (*Fill, <-chan struct{}) {
	if c == nil || !validScope(scope) {
		return nil, nil
	}
	r := rel(scope, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.files[r]; ok {
		return nil, closedChan
	}
	if ch, ok := c.flights[r]; ok {
		return nil, ch
	}
	c.flights[r] = make(chan struct{})
	return &Fill{c: c, rel: r}, nil
}

// Open starts the chunk's file with its header.
func (f *Fill) Open(m Meta) {
	f.meta = m
	dir := filepath.Join(f.c.dir, filepath.Dir(filepath.FromSlash(f.rel)))
	if f.err = os.MkdirAll(dir, 0o700); f.err != nil {
		return
	}
	if f.tmp, f.err = os.CreateTemp(dir, ".fill-*"); f.err != nil {
		return
	}
	head, _ := json.Marshal(m)
	_, f.err = f.tmp.Write(append(head, '\n'))
}

// Write adds bytes to the chunk.
func (f *Fill) Write(p []byte) (int, error) {
	if f.tmp != nil && f.err == nil {
		var n int
		n, f.err = f.tmp.Write(p)
		f.n += int64(n)
	}
	return len(p), nil
}

// Commit keeps the chunk when all of its bytes were written, and lets the waiting callers go on.
func (f *Fill) Commit() {
	f.finish(f.err == nil && f.tmp != nil && f.n == f.meta.Length)
}

// Abort drops the chunk, and lets the waiting callers go on.
func (f *Fill) Abort() { f.finish(false) }

func (f *Fill) finish(keep bool) {
	f.once.Do(func() {
		c := f.c
		var size int64
		if f.tmp != nil {
			if err := f.tmp.Close(); err != nil {
				keep = false
			}
			if st, err := os.Stat(f.tmp.Name()); err == nil {
				size = st.Size()
			}
			if keep && os.Rename(f.tmp.Name(), filepath.Join(c.dir, filepath.FromSlash(f.rel))) != nil {
				keep = false
			}
			if !keep {
				_ = os.Remove(f.tmp.Name())
			}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if keep {
			c.remove(f.rel)
			c.files[f.rel] = c.lru.PushFront(&cached{rel: f.rel, size: size})
			c.used += size
			c.evict()
		}
		if ch, ok := c.flights[f.rel]; ok {
			close(ch)
			delete(c.flights, f.rel)
		}
	})
}

// evict deletes the least recently used chunks while the cache is over its size. With c.mu held.
func (c *Cache) evict() {
	for c.used > c.max && c.lru.Len() > 0 {
		e := c.lru.Back().Value.(*cached)
		c.remove(e.rel)
		_ = os.Remove(filepath.Join(c.dir, filepath.FromSlash(e.rel)))
	}
}

// remove forgets a chunk. With c.mu held.
func (c *Cache) remove(r string) {
	if el, ok := c.files[r]; ok {
		c.used -= el.Value.(*cached).size
		c.lru.Remove(el)
		delete(c.files, r)
	}
}

// Drop deletes a scope's chunks (a deleted project's).
func (c *Cache) Drop(scope string) error {
	if c == nil || !validScope(scope) {
		return nil
	}
	c.mu.Lock()
	for r := range c.files {
		if strings.HasPrefix(r, scope+"/") {
			c.remove(r)
		}
	}
	c.mu.Unlock()
	return os.RemoveAll(filepath.Join(c.dir, scope))
}
