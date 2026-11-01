// Package web holds the review page's source files the Go side needs: the built page (embed.go, only with -tags
// webdist) and the Theme API's lists (this file, always), so `retake theme init` and `retake theme check` work in a
// binary built without the page too.
package web

import _ "embed" // the Theme API's files

// ThemeTokens is src/theme/tokens.css: every token of the look with its built-in value (spec/theme.md, "Tokens").
//
//go:embed src/theme/tokens.css
var ThemeTokens string

// ThemeAPI is src/theme/api.json: the parts (data-part) and the deprecated names (spec/theme.md, "Parts").
//
//go:embed src/theme/api.json
var ThemeAPI []byte
