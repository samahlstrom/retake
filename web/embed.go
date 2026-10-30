//go:build webdist

// Package web holds the built review page. It is compiled only with -tags webdist (what `make build` passes), after
// the page has been built into web/dist, so `go build ./...` and `go test ./...` work on a fresh clone without node.
// internal/ui serves a "page not built" stub when this package isn't compiled in.
package web

import "embed"

// Dist is the built page: web/dist, including files whose names start with "." or "_".
//
//go:embed all:dist
var Dist embed.FS
