// Package ui picks the files of the review page: a folder given with --ui-dir, else the build embedded in the
// binary (web/dist, compiled in with -tags webdist), else nothing, in which case the server shows NotBuiltHTML.
package ui

import (
	"fmt"
	"io/fs"
	"os"
)

// NotBuiltHTML is served at / when the binary was built without the page.
const NotBuiltHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Retake: page not built</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem}</style></head>
<body>
<h1>Page not built</h1>
<p>This <code>retake</code> binary was built without the review page. Run <code>make build</code> and use
<code>bin/retake</code>, or pass <code>--ui-dir</code> with a built page folder.</p>
<p>The API is up: <a href="/api/info">/api/info</a>.</p>
</body>
</html>
`

// Files returns the page's files: dir if it isn't empty, else the embedded build. It returns nil, nil when there's
// no dir and no embedded build.
func Files(dir string) (fs.FS, error) {
	if dir == "" {
		return Embedded(), nil
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return os.DirFS(dir), nil
}

// Embedded returns the page embedded in the binary, or nil if it was built without one.
func Embedded() fs.FS {
	return embedded()
}
