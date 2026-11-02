package server

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/internal/ext"
)

// Ignition is Scripts now (spec/extensions.md, "Renamed ids"): a computer that still runs a retake from before reports
// the extension and its kind under the old id, and the page sees them under the new one.
func TestRenamedExtensionFromAnOlderComputer(t *testing.T) {
	manifest := `api = 1
id = "scripts"
name = "Scripts"
version = "1.0.0"
[ui]
module = "ui/scripts.js"
[[kinds]]
id = "doc"
folder = "v0"
marker = "*_v0.md"
label = "Doc"
[files]
owns = ["v0/**"]
`
	e := ext.Load(fstest.MapFS{ext.ManifestFile: {Data: []byte(manifest)}, "ui/scripts.js": {Data: []byte("export function version() {}\n")}},
		"", ext.WhereBundled, "scripts")
	set, warnings := ext.Resolve([]*ext.Ext{e}, []string{"ignition"}, nil)
	if set.Get("scripts") == nil {
		t.Fatalf("not on: %v %v", warnings, e.Problems)
	}
	h := start(t, Options{Root: t.TempDir(), Exts: set, RemoteFiles: true})
	t.Cleanup(func() { _ = h.srv.Close() })
	token := makeToken(t, h, "agent", "worker", 0)
	headers := map[string]string{"Authorization": "Bearer " + token, "X-Retake-Worker": "machine", "X-Retake-Session": "session"}
	if r := h.post("/api/worker/poll", `{"worker":{"id":"machine","session":"session","name":"Old computer","agents":[{"id":"codex"}],`+
		`"extensions":[{"id":"ignition","version":"1.0.0","api":1}]}}`, headers); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	if r := h.post("/api/worker/files", `{"files":[{"label":"v0","n":0,"name":"v0","kind":"ignition.doc","modified":1700000000000}]}`,
		headers); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	info := h.get("/api/info", nil).json(t)
	if vs, _ := info["versions"].([]any); len(vs) != 1 || vs[0].(map[string]any)["kind"] != "scripts.doc" {
		t.Errorf("v0's kind: %v", info["versions"])
	}
	if r := h.get("/api/ext", nil); r.code != 200 || !strings.Contains(string(r.body), `"machine":true`) {
		t.Errorf("the computer has it under the old id: %d %s", r.code, r.body)
	}
}
