// Package extensions holds the extensions built into retake (spec/extensions.md, "Where extensions are found"):
// Ignition (spec/ignition.md). They are found last, so a user's copy of one wins, and they are off until turned on.
// Only what runs is built in: the manifest, the agent's Markdown and ui/ (the web build's output, with the committed
// icon). In a fresh clone ui/ignition.js isn't there yet, and Ignition has a "missing file" problem until
// `make build`. Its Svelte source (ignition/src/) stays out of the binary.
package extensions

import "embed"

// FS is the bundled extensions, one folder per id: ignition/extension.toml, ignition/agent/…, ignition/ui/….
//
//go:embed ignition/extension.toml ignition/agent ignition/ui
var FS embed.FS
