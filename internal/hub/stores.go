// Package hub is `retake serve` (spec/server.md): one server that hosts many projects for a team. Each project is
// a version family with its store, served by an internal/server.Server mounted at /p/<name>/. The hub signs people
// in (one-time links, sessions), decides who each request is and what it may do, checks origins, takes uploads
// (tus) and pushes, serves remote MCP per project, and keeps media in local files or S3.
package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/sqldb"
	"github.com/samahlstrom/retake/internal/tus"
)

// Config says where a server keeps its data: the data folder, and the drivers (spec/server.md, "Storage drivers").
type Config struct {
	Data     string         // the data folder (absolute)
	Database string         // "" = SQLite under Data; postgres://... = Postgres
	S3       *blob.S3Config // nil = media in local files only
}

// Stores are a server's open stores: the server store, the media store, and how to open project stores.
type Stores struct {
	cfg      Config
	Accounts *accounts.Store
	Blobs    blob.Store // nil: media lives only in the workspaces
}

// OpenStores opens (creating) the server store and the media store of cfg, and the data folder's layout.
func OpenStores(ctx context.Context, cfg Config) (*Stores, error) {
	if cfg.Data == "" {
		return nil, errors.New("no data folder: give --data DIR (or set RETAKE_DATA)")
	}
	for _, d := range []string{cfg.Data, filepath.Join(cfg.Data, "projects"), filepath.Join(cfg.Data, "uploads")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, fmt.Errorf("the data folder: %w", err)
		}
	}
	st := &Stores{cfg: cfg}
	var err error
	switch {
	case cfg.Database == "":
		st.Accounts, err = accounts.OpenSQLite(filepath.Join(cfg.Data, "server.db"))
	case sqldb.IsPostgresURL(cfg.Database):
		st.Accounts, err = accounts.OpenPostgres(ctx, cfg.Database)
	default:
		err = fmt.Errorf("the database %q isn't a postgres:// URL (leave it empty for SQLite)", redact(cfg.Database))
	}
	if err != nil {
		return nil, err
	}
	if cfg.S3 != nil {
		s3, err := blob.NewS3(ctx, *cfg.S3)
		if err != nil {
			_ = st.Accounts.Close()
			return nil, fmt.Errorf("object storage: %w", err)
		}
		st.Blobs = s3
	}
	return st, nil
}

// Close closes the server store.
func (st *Stores) Close() error { return st.Accounts.Close() }

// Config returns the configuration the stores were opened with.
func (st *Stores) Config() Config { return st.cfg }

// Describe says which drivers are in use, for the startup lines: "sqlite, local files" or "postgres, s3 (bucket)".
func (st *Stores) Describe() string {
	db := "sqlite"
	if st.cfg.Database != "" {
		db = "postgres"
	}
	media := "local files"
	if st.cfg.S3 != nil {
		media = "s3 bucket " + st.cfg.S3.Bucket + " at " + st.cfg.S3.Endpoint
	}
	return db + ", " + media
}

// Workspace returns a project's folder: <data>/projects/<name>.
func (st *Stores) Workspace(name string) string { return filepath.Join(st.cfg.Data, "projects", name) }

// UploadsDir is where tus uploads live.
func (st *Stores) UploadsDir() string { return filepath.Join(st.cfg.Data, "uploads") }

// Uploads returns the tus handler of the data folder.
func (st *Stores) Uploads() *tus.Handler { return &tus.Handler{Dir: st.UploadsDir()} }

// projectSchema is a project's Postgres schema.
func projectSchema(p accounts.Project) string { return "retake_p" + strconv.FormatInt(p.ID, 10) }

// ProjectStore opens a project's store with the configured driver, creating its workspace and store if needed.
func (st *Stores) ProjectStore(ctx context.Context, p accounts.Project) (*project.Store, error) {
	root := st.Workspace(p.Name)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	if st.cfg.Database == "" {
		return project.Open(root)
	}
	return project.OpenPostgres(ctx, st.cfg.Database, projectSchema(p), root)
}

// CreateProject adds a project: its row, its workspace and its store.
func (st *Stores) CreateProject(ctx context.Context, name, title string) (accounts.Project, error) {
	p, err := st.Accounts.CreateProject(name, title)
	if err != nil {
		return p, err
	}
	ps, err := st.ProjectStore(ctx, p)
	if err != nil {
		return p, err
	}
	return p, ps.Close()
}

// redact hides a URL's password.
func redact(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.LastIndex(u, "@"); j > i {
			return u[:i+3] + "…@" + u[j+1:]
		}
	}
	return u
}

// MediaDir is the folder of a project's versions, inside its workspace.
const MediaDir = "media"

var labelNumRE = regexp.MustCompile(`(?i)^v(\d+)$`)

// FirstVersion puts the first version of a project into its workspace, as media/<name>_v<N><ext> (N from label,
// default 1), and records it as pushed with no parent. With move the file is moved, else copied. sum is the file's
// SHA-256 when it is known already ("" = computed here).
func FirstVersion(st *project.Store, root, name, src, label, by, sum string, move bool) (project.VersionRow, error) {
	if label == "" {
		label = "v1"
	}
	m := labelNumRE.FindStringSubmatch(label)
	if m == nil {
		return project.VersionRow{}, fmt.Errorf("label %q must be v<N>", label)
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return project.VersionRow{}, fmt.Errorf("label %q has no usable number", label)
	}
	ext := strings.ToLower(filepath.Ext(src))
	if !family.IsVideo(src) || family.IsPlaylist(src) {
		return project.VersionRow{}, fmt.Errorf("%s is not a single video file (.mp4 .m4v .mov .webm .mkv)",
			filepath.Base(src))
	}
	if anchor := Anchor(root); anchor != "" {
		return project.VersionRow{}, fmt.Errorf("the project has versions already (%s)", filepath.Base(anchor))
	}
	dest := filepath.Join(root, MediaDir, fmt.Sprintf("%s_v%d%s", name, n, ext))
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return project.VersionRow{}, err
	}
	label = "v" + strconv.Itoa(n)
	push := project.Push{Label: label, Path: dest, By: by, Scope: project.Scope{Version: label}}
	if err := st.CheckPush(push); err != nil {
		return project.VersionRow{}, err
	}
	if sum == "" {
		var err error
		if sum, err = tus.FileSHA256(src); err != nil {
			return project.VersionRow{}, err
		}
	}
	if err := place(src, dest, move); err != nil {
		return project.VersionRow{}, err
	}
	push.SHA256 = sum
	res, err := st.RecordPush(push)
	if err != nil {
		_ = os.Remove(dest)
		return project.VersionRow{}, err
	}
	return res.Version, nil
}

// place moves (when it can, with move) or copies src to a new file dest.
func place(src, dest string, move bool) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	if move && os.Rename(src, dest) == nil {
		return nil
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	if move {
		_ = os.Remove(src)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // a file the admin or an upload named
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".retake-tmp")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640) //nolint:gosec // inside the data folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// CopyTree copies a retake project folder into a new workspace. Without keepStore its .retake/ folder (the SQLite
// store) stays behind.
func CopyTree(src, dest string, keepStore bool) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() && rel == project.Dir && !keepStore {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // a link could point anywhere; copy only real files
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if rel == filepath.Join(project.Dir, "jobs.lock") || strings.HasSuffix(rel, "-shm") ||
			strings.HasSuffix(rel, "-wal") {
			return nil
		}
		return copyFile(path, target)
	})
}

// Anchor finds the video a project's family is detected from: of the video files in the workspace (its top folder
// and two levels of subfolders, skipping hidden and .review folders), the one with the highest version number,
// a plain name before a _draft one. "" when there is none: the project has no versions yet.
func Anchor(root string) string {
	best, bestN, bestPlain := "", -1, false
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(dir, name)
			if e.IsDir() {
				if depth < 2 && !strings.HasSuffix(name, ".review") {
					walk(p, depth+1)
				}
				continue
			}
			if !e.Type().IsRegular() || !family.IsVideo(name) {
				continue
			}
			nm, ok := family.ParseStem(family.Stem(name))
			n, plain := 0, true
			if ok {
				n, plain = nm.N, nm.Rest == ""
			}
			if n > bestN || n == bestN && plain && !bestPlain {
				best, bestN, bestPlain = p, n, plain
			}
		}
	}
	walk(root, 0)
	return best
}
