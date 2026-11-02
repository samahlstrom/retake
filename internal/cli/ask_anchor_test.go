package cli

import (
	"path/filepath"
	"testing"
)

// retake ask --file: a question about a place in a document (spec/extensions.md, "Questions with a text anchor").
func TestTaskAskAboutADocument(t *testing.T) {
	getenv := func(string) string { return "" }
	in := taskAsk{note: "12", file: "./v0/script.md", line: "7", heading: "## Cold open", quote: " So the idea ",
		opts: []string{"Start on the jump", "Start on the shop"}}
	q, err := in.request("Which opening?", getenv)
	if err != nil {
		t.Fatal(err)
	}
	if q.Anchor == nil || q.Anchor.File != "v0/script.md" || q.Anchor.Line != 7 || q.Anchor.Heading != "Cold open" ||
		q.Anchor.Quote != "So the idea" || len(q.Options) != 2 || q.At != nil {
		t.Fatalf("%+v %+v", q, q.Anchor)
	}
	if q, err = (taskAsk{file: "v0/idea.md"}).request("q", getenv); err != nil || q.Anchor.Line != 1 {
		t.Fatalf("no --line is the first line: %+v %v", q.Anchor, err)
	}
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "a.png"))
	for name, bad := range map[string]taskAsk{
		"no file":      {line: "3"},
		"with a time":  {file: "v0/script.md", at: "1:23"},
		"with a range": {file: "v0/script.md", from: "1", to: "2"},
		"with a box":   {file: "v0/script.md", boxes: []string{"0,0,1,1"}},
		"with px":      {file: "v0/script.md", px: true},
		"placed image": {file: "v0/script.md", images: []string{filepath.Join(dir, "a.png") + "@0,0,1,1"}},
		"bad line":     {file: "v0/script.md", line: "seven"},
		"traversal":    {file: "../secrets.md"},
		"hidden":       {file: "v0/.history/x.md"},
	} {
		if _, err := bad.request("q", getenv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// An image beside the question (not placed on a frame) is fine.
	if _, err := (taskAsk{file: "v0/script.md", images: []string{filepath.Join(dir, "a.png")}}).request("q", getenv); err != nil {
		t.Fatal(err)
	}
}
