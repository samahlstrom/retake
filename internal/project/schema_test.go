package project

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var createRE = regexp.MustCompile(`(?s)CREATE TABLE (\w+) \((.*?)\n\);`)

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
		if len(lite) == 0 {
			t.Fatalf("step %d: no tables parsed", i+1)
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
