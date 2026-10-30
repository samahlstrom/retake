package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// call runs one request through run and returns the JSON lines it printed.
func call(t *testing.T, req map[string]any) []map[string]any {
	t.Helper()
	in, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(bytes.NewReader(in), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var lines []map[string]any
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("not a JSON line: %q", sc.Text())
		}
		lines = append(lines, m)
	}
	return lines
}

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "INDEX.csv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const sample = "\xef\xbb\xbffilename,type,tags_when_to_use,duration_s,source_url,license\n" +
	`sfx/whoosh-fast__transition__cc0.wav,sfx,transition / whoosh,0.5,https://example.org/whoosh,CC0 1.0 - no attribution required` + "\n" +
	`music/calm-loop__bed.mp3,music,"bed; calm, loop",60,https://example.org/calm,"CC-BY 4.0 (Example Artist) - credit required"` + "\n" +
	`memes/big-reaction__meme.png,meme-image,reaction|meme,,https://example.org/meme,"Unknown/copyrighted meme template; fair-use risk, not CC"` + "\n" +
	`,sfx,orphan,,,CC0` + "\n" +
	`clips/my-take.mp4,meme-video,take,3,,my own recording (owned)` + "\n"

func TestIndexCSV(t *testing.T) {
	csvPath := writeCSV(t, sample)
	lines := call(t, map[string]any{"protocol": protocol, "method": "index", "indexer": "csv", "path": csvPath,
		"args": map[string]any{}})
	var assets []map[string]any
	var logs, done int
	for _, l := range lines {
		switch l["type"] {
		case "asset":
			assets = append(assets, l)
		case "log":
			logs++
		case "done":
			done++
			st := l["stats"].(map[string]any)
			if st["rows"] != 5.0 || st["assets"] != 4.0 || st["skipped"] != 1.0 {
				t.Errorf("stats = %v", st)
			}
		default:
			t.Errorf("unexpected line %v", l)
		}
	}
	if len(assets) != 4 || logs != 1 || done != 1 || lines[len(lines)-1]["type"] != "done" {
		t.Fatalf("lines = %v", lines)
	}

	a := assets[0]
	dir := filepath.Dir(csvPath)
	if a["key"] != "sfx/whoosh-fast__transition__cc0.wav" || a["path"] != filepath.Join(dir, "sfx", "whoosh-fast__transition__cc0.wav") ||
		a["import"] != "full" || a["kind"] != "audio" || a["title"] != "whoosh fast" {
		t.Errorf("asset 0 = %v", a)
	}
	if tags := toStrings(a["tags"]); !slices.Equal(tags, []string{"transition", "whoosh", "sfx"}) {
		t.Errorf("tags = %v", tags)
	}
	if lic := a["license"].(map[string]any); lic["status"] != "cleared" || lic["source"] != "https://example.org/whoosh" ||
		!strings.HasPrefix(lic["note"].(string), "CC0") || lic["content_id_risk"] != nil {
		t.Errorf("license 0 = %v", lic)
	}
	if row := a["meta"].(map[string]any)["row"].(map[string]any); row["duration_s"] != "0.5" {
		t.Errorf("meta.row = %v", row)
	}

	lic := assets[1]["license"].(map[string]any)
	if lic["status"] != "attribution" || !strings.Contains(lic["credit"].(string), "Example Artist") {
		t.Errorf("attribution = %v", lic)
	}
	if tags := toStrings(assets[1]["tags"]); !slices.Equal(tags, []string{"bed", "calm", "loop", "music"}) {
		t.Errorf("tags = %v", tags)
	}
	lic = assets[2]["license"].(map[string]any)
	if lic["status"] != "review-only" || lic["content_id_risk"] != "high" || assets[2]["kind"] != "image" {
		t.Errorf("meme = %v", assets[2])
	}
	if lic := assets[3]["license"].(map[string]any); lic["status"] != "owned" || assets[3]["kind"] != "video" {
		t.Errorf("own clip = %v", assets[3])
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestIndexArgs(t *testing.T) {
	csvPath := writeCSV(t, "Path,Name,Licence\n/abs/a.png,Alpha,\nrel/b.png,,owned\nc.png,Gamma,whatever\n")
	root := t.TempDir()
	lines := call(t, map[string]any{"method": "index", "path": csvPath,
		"args": map[string]any{"import": "metadata", "limit": 2, "root": root, "license_default": "blocked"}})
	if len(lines) != 3 || lines[2]["type"] != "done" {
		t.Fatalf("lines = %v", lines)
	}
	a, b := lines[0], lines[1]
	if a["path"] != filepath.FromSlash("/abs/a.png") || a["import"] != "metadata" || a["title"] != "Alpha" ||
		a["license"].(map[string]any)["status"] != "blocked" {
		t.Errorf("a = %v", a)
	}
	if b["path"] != filepath.Join(root, "rel", "b.png") || b["title"] != "b" || b["license"].(map[string]any)["status"] != "owned" {
		t.Errorf("b = %v", b)
	}
	if _, ok := a["kind"]; ok {
		t.Errorf("no type column, yet a kind: %v", a)
	}

	// Named columns.
	csvPath = writeCSV(t, "asset,label\nx.wav,Ex\n")
	lines = call(t, map[string]any{"method": "index", "path": csvPath,
		"args": map[string]any{"path_column": "ASSET", "title_column": "label"}})
	if len(lines) != 2 || lines[0]["key"] != "x.wav" || lines[0]["title"] != "Ex" {
		t.Errorf("named columns: %v", lines)
	}
}

func TestErrors(t *testing.T) {
	csvPath := writeCSV(t, "name,tags\nx,y\n")
	for _, c := range []struct {
		req  map[string]any
		want string
	}{
		{map[string]any{"method": "index", "path": csvPath}, "no path column"},
		{map[string]any{"method": "index"}, "give the CSV"},
		{map[string]any{"method": "index", "indexer": "xml", "path": csvPath}, "unknown indexer"},
		{map[string]any{"method": "tool", "tool": "nope"}, "unknown tool"},
		{map[string]any{"method": "dance"}, "unknown method"},
		{map[string]any{"protocol": "retake-plugin/9", "method": "index"}, "this plug-in speaks"},
		{map[string]any{"method": "index", "path": csvPath, "args": map[string]any{"import": "copy"}}, "arg import"},
		{map[string]any{"method": "index", "path": csvPath, "args": map[string]any{"license_default": "maybe"}}, "not a status"},
		{map[string]any{"method": "index", "path": filepath.Join(t.TempDir(), "missing.csv")}, "missing.csv"},
	} {
		lines := call(t, c.req)
		if len(lines) != 1 || lines[0]["type"] != "error" || !strings.Contains(lines[0]["message"].(string), c.want) {
			t.Errorf("%v: %v", c.req, lines)
		}
	}
	var out bytes.Buffer
	if code := run(strings.NewReader("not json"), &out, &bytes.Buffer{}); code != 0 || !strings.Contains(out.String(), `"error"`) {
		t.Errorf("garbage request: %d %s", code, out.String())
	}
}

func TestColumnsTool(t *testing.T) {
	csvPath := writeCSV(t, sample)
	lines := call(t, map[string]any{"method": "tool", "tool": "columns", "args": map[string]any{"path": csvPath}})
	if len(lines) != 1 || lines[0]["type"] != "result" {
		t.Fatalf("lines = %v", lines)
	}
	res := lines[0]["result"].(map[string]any)
	m := res["mapping"].(map[string]any)
	if res["rows"] != 5.0 || m["path"] != "filename" || m["tags"] != "tags_when_to_use" || m["license"] != "license" ||
		m["source"] != "source_url" || m["type"] != "type" || m["title"] != nil {
		t.Errorf("columns = %v", res)
	}
	if h := toStrings(res["header"]); h[0] != "filename" {
		t.Errorf("BOM not stripped: %q", h[0])
	}
	lines = call(t, map[string]any{"method": "tool", "tool": "columns", "args": map[string]any{"path": writeCSV(t, "a,b\n1,2\n")}})
	if res := lines[0]["result"].(map[string]any); !strings.Contains(res["problem"].(string), "no path column") {
		t.Errorf("problem = %v", res)
	}
}

func TestLicenseMapping(t *testing.T) {
	for _, c := range []struct{ text, status, risk string }{
		{"owned", "owned", ""},
		{"review-only", "review-only", ""},
		{"", "review-only", ""},
		{"CC0", "cleared", ""},
		{"Public Domain", "cleared", ""},
		{"CC BY 2.0 - by someone", "attribution", ""},
		{"SIL OFL 1.1", "cleared", ""},
		{"Ubuntu Font Licence 1.0", "cleared", ""},
		{"Epidemic Sound subscription", "cleared", ""},
		{"Copyrighted: Some Band - Some Song. Not licensed", "review-only", "high"},
		{"Unknown - user-uploaded meme audio (no license stated); copyrighted-meme risk, not CC", "review-only", "high"},
		{"Studio art (c) Someone; Copyright Someone", "review-only", "high"},
		{"do not use", "blocked", ""},
		{"our own mascot", "owned", ""},
		{"no idea", "review-only", ""},
	} {
		l := mapLicense(c.text, "", "", "T", "review-only")
		if l.Status != c.status || l.ContentIDRisk != c.risk || l.Note != c.text {
			t.Errorf("%q -> %+v, want %s/%s", c.text, l, c.status, c.risk)
		}
	}
	if l := mapLicense("CC-BY 4.0", "Art by X", "", "T", "review-only"); l.Credit != "Art by X" {
		t.Errorf("credit column ignored: %+v", l)
	}
	if l := mapLicense("CC-BY 4.0", "", "", "Song", "review-only"); l.Credit != "Song, CC-BY 4.0" {
		t.Errorf("built credit = %q", l.Credit)
	}
}

func TestHelpers(t *testing.T) {
	if got := titleFromFile("memes/big-toe__censor-gag__cc0.png"); got != "big toe" {
		t.Errorf("title = %q", got)
	}
	if got := titleFromFile("plain_name.wav"); got != "plain name" {
		t.Errorf("title = %q", got)
	}
	if got := splitTags(" A / b;B , c|", "Sfx"); !slices.Equal(got, []string{"a", "b", "c", "sfx"}) {
		t.Errorf("tags = %v", got)
	}
	for in, want := range map[string]string{"sfx": "audio", "music": "audio", "meme-video": "video", "meme-image": "image",
		"edu-card": "image", "edu-map": "image", "game-art": "image", "brand-icon": "image", "font": "font", "edu-data": "", "": ""} {
		if got := kindHint(in); got != want {
			t.Errorf("kindHint(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := parseTable(strings.NewReader("")); err == nil {
		t.Error("empty CSV parsed")
	}
	tb, err := parseTable(strings.NewReader("a,b\n1\n1,2,3\n\"q\"\"x\",y\n"))
	if err != nil || len(tb.rows) != 3 || tb.rows[0]["a"] != "1" || tb.rows[2]["a"] != `q"x` {
		t.Errorf("ragged/quoted: %v %v", tb, err)
	}
}
