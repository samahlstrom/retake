// Package conformance runs the same tests against both storage drivers of retake serve (spec/server.md, "Storage
// drivers"): SQLite with local files, and Postgres with S3-compatible object storage (MinIO). The Postgres + MinIO
// run needs docker: TestMain starts both in containers (project-specific names and ports) and removes them after,
// or uses RETAKE_TEST_PG and RETAKE_TEST_S3 when they're set. Without docker it is skipped.
package conformance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/samahlstrom/retake/internal/blob"
)

// The containers, when TestMain starts them. Their names and host ports are this test's own.
const (
	pgName    = "retake3b-test-pg"
	minioName = "retake3b-test-minio"
	pgPort    = "5439"
	minioPort = "9039"
	pgImage   = "postgres:17-alpine"
	// Upstream minio/minio stopped publishing images in 2025; pgsty/minio is a drop-in community build.
	minioImage = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"
)

var (
	pgAdmin   string         // a DSN of the Postgres database to make test databases in; "" = no Postgres run
	s3Config  *blob.S3Config // the object storage; nil = no Postgres run
	skipWhy   string         // why there is no Postgres run
	dbCounter atomic.Int64
)

func TestMain(m *testing.M) {
	code := func() int {
		stop, err := setup()
		if err != nil {
			skipWhy = err.Error()
		}
		if stop != nil {
			defer stop()
		}
		return m.Run()
	}()
	os.Exit(code)
}

// setup finds or starts Postgres and MinIO. It returns how to stop what it started.
func setup() (func(), error) {
	if dsn, s3 := os.Getenv("RETAKE_TEST_PG"), os.Getenv("RETAKE_TEST_S3"); dsn != "" && s3 != "" {
		// RETAKE_TEST_S3=http://KEY:SECRET@host:port
		u, err := parseS3(s3)
		if err != nil {
			return nil, err
		}
		pgAdmin, s3Config = dsn, u
		return nil, nil
	}
	if os.Getenv("RETAKE_TEST_DOCKER") == "0" {
		return nil, fmt.Errorf("RETAKE_TEST_DOCKER=0")
	}
	if err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		return nil, fmt.Errorf("docker isn't available (%w)", err)
	}
	docker := func(args ...string) error {
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	stop := func() { _ = docker("rm", "-f", "-v", pgName, minioName) }
	stop() // leftovers of a killed run
	if err := docker("run", "-d", "--name", pgName, "-p", "127.0.0.1:"+pgPort+":5432", "-e", "POSTGRES_USER=retake",
		"-e", "POSTGRES_PASSWORD=retake", "-e", "POSTGRES_DB=retake", pgImage); err != nil {
		stop()
		return nil, err
	}
	if err := docker("run", "-d", "--name", minioName, "-p", "127.0.0.1:"+minioPort+":9000", "-e",
		"MINIO_ROOT_USER=retake", "-e", "MINIO_ROOT_PASSWORD=retake-secret", minioImage, "server", "/data"); err != nil {
		stop()
		return nil, err
	}
	dsn := "postgres://retake:retake@127.0.0.1:" + pgPort + "/retake?sslmode=disable"
	deadline := time.Now().Add(90 * time.Second)
	for {
		db, err := sql.Open("pgx", dsn)
		if err == nil {
			err = db.Ping()
			_ = db.Close()
		}
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			stop()
			return nil, fmt.Errorf("postgres didn't start: %w", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	for {
		res, err := http.Get("http://127.0.0.1:" + minioPort + "/minio/health/live") //nolint:noctx // a test probe
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			stop()
			return nil, fmt.Errorf("minio didn't start: %w", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	pgAdmin = dsn
	s3Config = &blob.S3Config{Endpoint: "127.0.0.1:" + minioPort, AccessKey: "retake", SecretKey: "retake-secret",
		Insecure: true}
	return stop, nil
}

func parseS3(raw string) (*blob.S3Config, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	creds, hostport, ok2 := strings.Cut(rest, "@")
	key, secret, ok3 := strings.Cut(creds, ":")
	if !ok || !ok2 || !ok3 {
		return nil, fmt.Errorf("RETAKE_TEST_S3 must look like http://KEY:SECRET@host:port")
	}
	return &blob.S3Config{Endpoint: hostport, AccessKey: key, SecretKey: secret, Insecure: scheme == "http"}, nil
}

var _ = stdlib.GetDefaultDriver // registers "pgx"

// driver is one storage configuration under test.
type driver struct {
	name     string
	postgres bool
}

// drivers returns the configurations to run: SQLite + local files always, Postgres + S3 when it's up.
func drivers(t *testing.T) []driver {
	ds := []driver{{name: "sqlite+local"}}
	if pgAdmin != "" {
		ds = append(ds, driver{name: "postgres+s3", postgres: true})
	} else {
		t.Logf("postgres+s3 skipped: %s", skipWhy)
	}
	return ds
}

// each runs fn once per driver.
func each(t *testing.T, fn func(t *testing.T, d driver)) {
	for _, d := range drivers(t) {
		t.Run(d.name, func(t *testing.T) { fn(t, d) })
	}
}

// database makes a fresh Postgres database for one test, dropped at its end, and returns its DSN.
func database(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("retake_t%d_%d", os.Getpid(), dbCounter.Add(1))
	admin, err := sql.Open("pgx", pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		_ = admin.Close()
	})
	return strings.Replace(pgAdmin, "/retake?", "/"+name+"?", 1)
}

// bucket returns the object storage with a fresh bucket for one test; its objects are deleted at the end.
func bucket(t *testing.T) (*blob.S3, blob.S3Config) {
	t.Helper()
	c := *s3Config
	c.Bucket = fmt.Sprintf("retake-t%d-%d", os.Getpid(), dbCounter.Add(1))
	s, err := blob.NewS3(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if objs, err := s.List(ctx, ""); err == nil {
			for _, o := range objs {
				_ = s.Delete(ctx, o.Key)
			}
		}
	})
	return s, c
}
