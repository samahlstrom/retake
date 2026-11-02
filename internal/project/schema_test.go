package project

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	createRE = regexp.MustCompile(`(?s)CREATE TABLE (\w+) \((.*?)\n\);`)
	dropRE   = regexp.MustCompile(`DROP TABLE (?:IF EXISTS )?(\w+)`)
)

// drops returns the tables a migration step drops, in order.
func drops(step string) []string {
	var out []string
	for _, m := range dropRE.FindAllStringSubmatch(step, -1) {
		out = append(out, m[1])
	}
	return out
}

// tables returns each table a migration step creates, with its column names in order.
func tables(step string) map[string][]string {
	out := map[string][]string{}
	for _, m := range createRE.FindAllStringSubmatch(step, -1) {
		var cols []string
		for line := range strings.SplitSeq(m[2], "\n") {
			f := strings.Fields(strings.TrimSpace(line))
			if len(f) < 2 || strings.HasPrefix(f[0], "--") || f[0] == "CHECK" || f[0] == "PRIMARY" {
				continue
			}
			cols = append(cols, f[0])
		}
		out[m[1]] = cols
	}
	return out
}

// The Postgres schema mirrors the SQLite one step by step: the same tables with the same columns, so the queries
// are the same SQL and a new step can't be written for one dialect only.
func TestMigrationListsMatch(t *testing.T) {
	if len(migrations) != len(pgMigrations) {
		t.Fatalf("%d SQLite steps, %d Postgres steps: write each step for both", len(migrations), len(pgMigrations))
	}
	for i := range migrations {
		lite, pg := tables(migrations[i]), tables(pgMigrations[i])
		if len(lite) == 0 && len(drops(migrations[i])) == 0 {
			t.Fatalf("step %d: no tables parsed", i+1)
		}
		if a, b := drops(migrations[i]), drops(pgMigrations[i]); !slices.Equal(a, b) {
			t.Errorf("step %d: SQLite drops %v, Postgres %v", i+1, a, b)
		}
		for name, cols := range lite {
			if !slices.Equal(cols, pg[name]) {
				t.Errorf("step %d, table %s: SQLite columns %v, Postgres %v", i+1, name, cols, pg[name])
			}
		}
		for name := range pg {
			if _, ok := lite[name]; !ok {
				t.Errorf("step %d: Postgres has table %s, SQLite doesn't", i+1, name)
			}
		}
		for _, w := range []string{"AUTOINCREMENT", " REAL", "INTEGER"} {
			if strings.Contains(pgMigrations[i], w) {
				t.Errorf("step %d: Postgres step uses SQLite's %q", i+1, strings.TrimSpace(w))
			}
		}
	}
}

// Step 6 drops the table of the project organization from a store made before it, and keeps the tokens' scopes: a
// project:/group: link stays recognizable, so the server refuses it with a page that says why.
func TestOrganizationTableIsDropped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := open(root, migrations[:5])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO workspace (id, revision, data) VALUES (1, 3, '{"projects":[]}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateToken(NewToken{Hash: "h", Role: "reviewer", Scope: "group:default/review"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'workspace'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("workspace table still there: %d %v", n, err)
	}
	if tok, err := s.TokenByHash("h"); err != nil || tok.Scope != "group:default/review" {
		t.Fatalf("token after migrating: %+v %v", tok, err)
	}
}
