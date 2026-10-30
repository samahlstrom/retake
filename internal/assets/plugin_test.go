package assets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePlugin is a plug-in written as a shell script: it answers by matching the request text.
const fakePlugin = `#!/bin/sh
req=$(cat)
case "$req" in
*'"method":"tool"'*)
  case "$req" in
  *'"tool":"find"'*)
    echo '{"type":"log","message":"finding kills"}'
    echo '{"type":"result","result":{"rows":[{"span_key":"m1:kill:100","clock":"0:50"},{"span_key":"nope","clock":"9:99"}]}}' ;;
  *'"tool":"boom"'*) echo '{"type":"error","code":"bad_request","message":"unknown hero paigee"}' ;;
  *'"tool":"crash"'*) echo "it broke badly" >&2; exit 3 ;;
  *'"tool":"chatty"'*) echo 'not json at all' ;;
  *'"tool":"silent"'*) echo '{"type":"log","message":"thinking"}' ;;
  esac ;;
*'"method":"index"'*)
  label="Sam kills Mina"
  case "$req" in *'"v":2'*) label="Sam kills Mina (again)" ;; esac
  echo '{"type":"asset","key":"rec1","kind":"video","path":"rec1.mp4","title":"Recording one","tags":["obs"],"license":{"status":"owned"},"media":{"duration":600,"size":[1920,1080],"fps":60}}'
  echo '{"type":"span","key":"m1:kill:100","asset":"rec1","t0":100,"t1":110,"span_type":"kill","label":"'"$label"'","data":{"match_id":1,"hero":"Paige"},"source_of_label":"match_data","confidence":"high"}'
  echo '{"type":"span","key":"m1:death:340","asset":"rec1","t0":335,"t1":345,"span_type":"death","label":"Sam dies to Celeste","data":{"match_id":1},"source_of_label":"match_data","confidence":"high"}'
  echo '{"type":"span","key":"m1:dance:1","asset":"rec1","t0":1,"t1":2,"span_type":"dance","label":"undeclared"}'
  echo '{"type":"span","key":"m1:kill:bad","asset":"rec1","t0":5,"t1":6,"span_type":"kill","label":"bad data","data":{"match_id":"one"}}'
  echo '{"type":"span","key":"m1:kill:orphan","asset":"rec9","t0":5,"t1":6,"span_type":"kill","data":{"match_id":1}}'
  echo '{"type":"clock","asset":"rec1","domain":"game:match/1","epochs":[{"asset_t":[50,300],"domain_t0":0,"rate":1},{"asset_t":[330,600],"domain_t0":250}],"confidence":"high","method":"test"}'
  echo '{"type":"log","message":"1 match"}'
  echo '{"type":"done","stats":{"spans":2}}' ;;
esac
`

const fakeManifest = `{
  "schema": "retake-plugin/0.1",
  "name": "game",
  "version": "0.1.0",
  "description": "a fake game plug-in",
  "exec": ["./plugin.sh"],
  "span_types": [
    {"type": "kill", "description": "a kill", "data_schema": {"type": "object", "required": ["match_id"],
      "properties": {"match_id": {"type": "integer"}}}},
    {"type": "death", "description": "a death"}
  ],
  "clock_domains": [{"domain": "game:match", "description": "game clock"}],
  "indexers": [{"name": "matches", "description": "every match"}],
  "views": [{"name": "game_kills", "sql": "SELECT id AS span, asset, t0, t1, json_extract(data, '$.match_id') AS match_id, json_extract(data, '$.hero') AS hero FROM spans WHERE plugin = 'game' AND type = 'kill'"}],
  "tools": [{"name": "find", "description": "find kills", "input_schema": {"type": "object", "properties": {"hero": {"type": "string"}}}},
            {"name": "boom"}, {"name": "crash"}, {"name": "chatty"}, {"name": "silent"}]
}`

// installFake writes the fake plug-in into dir/game and returns its folder.
func installFake(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plug-in is a shell script")
	}
	p := filepath.Join(dir, "game")
	write(t, filepath.Join(p, "plugin.json"), fakeManifest)
	write(t, filepath.Join(p, "plugin.sh"), fakePlugin)
	if err := os.Chmod(filepath.Join(p, "plugin.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(p, "rec1.mp4"), "pretend this is a long recording")
	return p
}

func TestPluginProtocol(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := installFake(t, filepath.Join(e.root, ".retake", "plugins"))
	// A broken plug-in on the path is reported, not fatal.
	broken := filepath.Join(t.TempDir(), "Bad Name")
	write(t, filepath.Join(broken, "plugin.json"), `{"name": "Bad Name", "exec": ["x"]}`)
	e.vars["RETAKE_PLUGIN_PATH"] = broken
	var log bytes.Buffer
	e.c.o.Log = &log

	ps := e.c.Plugins()
	if len(ps.Plugins) != 1 || ps.Plugins[0].Name != "game" || ps.Plugins[0].Dir != dir || len(ps.Problems) != 1 ||
		!strings.Contains(ps.Problems[0], "Bad Name") {
		t.Fatalf("plugins = %+v", ps)
	}

	res, err := e.c.Import(ctx, ImportArgs{Via: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || res.Spans != 2 || res.Clocks != 1 || res.Skipped != 3 || res.Plugin != "game" ||
		len(res.Log) != 1 || res.Stats["spans"] != float64(2) {
		t.Fatalf("index = %+v", res)
	}
	reasons := ""
	for _, it := range res.Items {
		reasons += it.Reason + "\n"
	}
	for _, want := range []string{`doesn't declare span type "dance"`, "data doesn't fit kill", `no asset "rec9"`} {
		if !strings.Contains(reasons, want) {
			t.Errorf("skipped reasons miss %q:\n%s", want, reasons)
		}
	}

	// The recording is metadata only: no hash, nothing read, nothing copied.
	rec, err := e.c.Get(ctx, "proj:game/rec1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Blob.Hash != nil || rec.Blob.InStore || rec.Blob.Path != filepath.Join(dir, "rec1.mp4") || !rec.Blob.Exists ||
		rec.Plugin != "game" || rec.Spans != 2 || len(rec.Clocks) != 1 || rec.License.Status != LicenseOwned ||
		rec.Media.Duration == nil || *rec.Media.Duration != 600 {
		t.Fatalf("rec = %+v", rec)
	}

	// Spans carry their clock times.
	found, err := e.c.SpanFind(ctx, SpanFindArgs{Type: []string{"kill"}})
	if err != nil || len(found.Spans) != 1 {
		t.Fatalf("span find = %+v, %v", found, err)
	}
	kill := found.Spans[0]
	if kill.Span != "sp:1" || kill.Asset != "proj:game/rec1" || kill.Data["hero"] != "Paige" || kill.Plugin != "game" ||
		kill.Key != "m1:kill:100" || len(kill.Clocks) != 1 || kill.Clocks[0] != (SpanClock{"game:match/1", 50, 60}) {
		t.Fatalf("kill = %+v", kill)
	}
	// Near a domain time: game 4:20 is asset 340 (second epoch), where the death is.
	near, err := e.c.SpanFind(ctx, SpanFindArgs{Near: "game:match/1@4:20", Window: 10})
	if err != nil || len(near.Spans) != 1 || near.Spans[0].Type != "death" {
		t.Fatalf("near = %+v, %v", near, err)
	}
	near, err = e.c.SpanFind(ctx, SpanFindArgs{Near: "proj:game/rec1@105"})
	if err != nil || len(near.Spans) != 1 || near.Spans[0].Type != "kill" {
		t.Fatalf("near an asset time = %+v, %v", near, err)
	}
	if _, err := e.c.SpanFind(ctx, SpanFindArgs{Near: "game:match/1@2:00:00"}); !isCode(err, CodeNotFound) {
		t.Fatalf("near outside every epoch: %v", err)
	}

	// The plug-in's view is in the catalog, usable from SQL and from --where.
	rows, err := e.c.SQL(ctx, "proj", "SELECT span, match_id, hero FROM game_kills", 0)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][2] != "Paige" {
		t.Fatalf("view = %+v, %v", rows, err)
	}
	hits, err := e.c.Search(ctx, SearchArgs{Where: "id IN (SELECT span FROM game_kills WHERE hero = 'Paige')"})
	if err != nil || len(hits.Results) != 1 || len(hits.Results[0].Spans) != 1 || hits.Results[0].Spans[0].Span != "sp:1" {
		t.Fatalf("where search = %+v, %v", hits, err)
	}
	if _, err := e.c.SQL(ctx, "proj", "DELETE FROM spans", 0); !isCode(err, CodeBadRequest) {
		t.Fatalf("SQL write: %v", err)
	}
	if _, err := e.c.Search(ctx, SearchArgs{Where: "1); DELETE FROM spans; --"}); !isCode(err, CodeBadRequest) {
		t.Fatalf("where with two statements: %v", err)
	}
	if _, err := e.c.Search(ctx, SearchArgs{Where: "(SELECT changes()) = 0 AND type = 'kill'"}); err != nil {
		t.Fatalf("a read-only where: %v", err)
	}

	// Re-indexing updates in place: same ids, new label.
	res, err = e.c.Import(ctx, ImportArgs{Via: "game/matches", Args: map[string]any{"v": 2}})
	if err != nil || res.Updated != 1 || res.Imported != 0 || res.Spans != 2 {
		t.Fatalf("re-index = %+v, %v", res, err)
	}
	sp, err := e.c.GetSpan(ctx, "sp:1")
	if err != nil || sp.Label != "Sam kills Mina (again)" {
		t.Fatalf("re-indexed span = %+v, %v", sp, err)
	}
	if got, _ := e.c.Get(ctx, "proj:game/rec1"); got.Spans != 2 {
		t.Fatalf("re-index duplicated spans: %d", got.Spans)
	}
	if hits, _ := e.c.Search(ctx, SearchArgs{Query: "again"}); len(hits.Results) != 1 {
		t.Fatalf("the search index follows the re-index: %+v", hits)
	}

	// Tools: the answer, with span ids linked in.
	q, err := e.c.PluginQuery(ctx, PluginQueryArgs{Plugin: "game", Tool: "find", Args: map[string]any{"hero": "Paige"}})
	if err != nil {
		t.Fatal(err)
	}
	rowsOut := q.Result.(map[string]any)["rows"].([]any)
	first, second := rowsOut[0].(map[string]any), rowsOut[1].(map[string]any)
	if first["span"] != "sp:1" || first["asset"] != "proj:game/rec1" || second["span"] != nil {
		t.Fatalf("tool result = %+v", q.Result)
	}
	if !strings.Contains(log.String(), "[game] finding kills") {
		t.Errorf("log lines aren't passed on: %q", log.String())
	}
	for tool, want := range map[string]struct{ code, text string }{
		"boom":   {CodeBadRequest, "unknown hero paigee"},
		"crash":  {CodePlugin, "it broke badly"},
		"chatty": {CodePlugin, "isn't a JSON object"},
		"silent": {CodePlugin, "without a result or error line"},
		"nope":   {CodeBadRequest, "has no tool"},
	} {
		_, err := e.c.PluginQuery(ctx, PluginQueryArgs{Plugin: "game", Tool: tool})
		if !isCode(err, want.code) || !strings.Contains(err.Error(), want.text) {
			t.Errorf("tool %s: %v", tool, err)
		}
	}
	if _, err := e.c.PluginQuery(ctx, PluginQueryArgs{Plugin: "nope", Tool: "x"}); !isCode(err, CodeNotFound) {
		t.Errorf("unknown plug-in: %v", err)
	}

	// A metadata-only asset is hashed on first resolve, once.
	r, err := e.c.Resolve(ctx, "proj:game/rec1")
	if err != nil || r.Hash != HashBytes([]byte("pretend this is a long recording")) || r.Kind != KindVideo || r.Stale {
		t.Fatalf("resolve = %+v, %v", r, err)
	}
	if got, _ := e.c.Get(ctx, "proj:game/rec1"); got.Blob.Hash == nil || *got.Blob.Hash != r.Hash {
		t.Fatalf("resolve didn't record the hash: %+v", got.Blob)
	}
}

func TestPluginInstallAndPathOrder(t *testing.T) {
	e := newEnv(t)
	src := installFake(t, t.TempDir())
	p, err := e.c.InstallPlugin(src)
	if err != nil || p.Name != "game" {
		t.Fatalf("install = %+v, %v", p, err)
	}
	if _, err := e.c.InstallPlugin(src); err != nil {
		t.Fatalf("installing the same folder twice: %v", err)
	}
	e.reopen(t)
	ps := e.c.Plugins()
	if len(ps.Plugins) != 1 || ps.Plugins[0].Dir != filepath.Join(e.lib, "plugins", "game") {
		t.Fatalf("plugins = %+v", ps)
	}
	// A plug-in on RETAKE_PLUGIN_PATH comes before the library's.
	other := installFake(t, t.TempDir())
	e.vars["RETAKE_PLUGIN_PATH"] = filepath.Dir(other)
	e.reopen(t)
	ps = e.c.Plugins()
	if len(ps.Plugins) != 1 || ps.Plugins[0].Dir != other || len(ps.Problems) != 1 {
		t.Fatalf("path order = %+v", ps)
	}
	// Bad manifests are refused.
	bad := filepath.Join(t.TempDir(), "bad")
	write(t, filepath.Join(bad, "plugin.json"), `{"name": "bad", "exec": ["x"], "views": [{"name": "other_v", "sql": "SELECT 1"}]}`)
	if _, err := e.c.InstallPlugin(bad); !isCode(err, CodeBadRequest) || !strings.Contains(err.Error(), `start with "bad_"`) {
		t.Fatalf("bad view name: %v", err)
	}
	write(t, filepath.Join(bad, "plugin.json"), `{"name": "bad", "exec": ["x"], "views": [{"name": "bad_v", "sql": "DROP TABLE spans"}]}`)
	if _, err := e.c.InstallPlugin(bad); !isCode(err, CodeBadRequest) {
		t.Fatalf("a view that isn't a SELECT: %v", err)
	}
}

func TestPluginFullImportAndPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if runtime.GOOS == "windows" {
		t.Skip("shell plug-in")
	}
	pdir := filepath.Join(e.root, ".retake", "plugins", "lister")
	write(t, filepath.Join(pdir, "plugin.json"), `{"name": "lister", "exec": ["sh", "list.sh"],
		"indexers": [{"name": "csv", "accepts": [".csv"]}]}`)
	// The plug-in echoes one full-import record per line of the CSV it's given (paths relative to the CSV).
	write(t, filepath.Join(pdir, "list.sh"), `req=$(cat)
path=$(printf '%s' "$req" | sed 's/.*"path":"\([^"]*\)".*/\1/')
while IFS=, read -r file title; do
  [ "$file" = file ] && continue
  printf '{"type":"asset","key":"%s","path":"%s","import":"full","title":"%s","license":{"status":"cleared","credit":"CC0"}}\n' "$file" "$file" "$title"
done < "$path"
echo '{"type":"done"}'
`)
	data := t.TempDir()
	picture(t, filepath.Join(data, "a.png"), 50, 50, 0)
	picture(t, filepath.Join(data, "b.png"), 60, 40, 1)
	write(t, filepath.Join(data, "index.csv"), "file,title\na.png,First picture\nb.png,Second picture\nc.png,Missing\n")

	if _, err := e.c.Import(ctx, ImportArgs{Via: "lister"}); !isCode(err, CodeBadRequest) {
		t.Fatalf("an indexer that reads a file needs one: %v", err)
	}
	if _, err := e.c.Import(ctx, ImportArgs{Via: "lister", Paths: []string{filepath.Join(data, "a.png")}}); !isCode(err, CodeBadRequest) {
		t.Fatalf("wrong extension: %v", err)
	}
	res, err := e.c.Import(ctx, ImportArgs{Via: "lister", Paths: []string{filepath.Join(data, "index.csv")}})
	if err != nil || res.Imported != 2 || res.Skipped != 1 {
		t.Fatalf("full import = %+v, %v", res, err)
	}
	got, err := e.c.Get(ctx, "proj:lister/a.png")
	if err != nil || got.Title != "First picture" || !got.Blob.InStore || got.Previews.Thumbnail == "" ||
		got.License.Status != LicenseCleared || got.Provenance.Importer != "plugin:lister" {
		t.Fatalf("full import entry = %+v, %v", got, err)
	}
	// Again: the same keys update.
	res, err = e.c.Import(ctx, ImportArgs{Via: "lister", Paths: []string{filepath.Join(data, "index.csv")}})
	if err != nil || res.Updated != 2 || res.Imported != 0 {
		t.Fatalf("full re-import = %+v, %v", res, err)
	}
}
