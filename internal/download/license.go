package download

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/samahlstrom/retake/internal/assets"
)

// License is the quiet line a download shows when the version's license report (spec/assets.md, "The gate") has
// review-only or blocked assets ("Has 2 assets marked review only"), else "". Assets with no license on record
// aren't counted: most projects never import theirs. It never blocks the download. Cached per manifest and mtime.
func (m *Manager) License(ctx context.Context, s Source) string {
	if s.Manifest == "" || s.Unavailable != "" {
		return ""
	}
	fi, err := os.Stat(s.Manifest)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	l, ok := m.licenses[s.Manifest]
	m.mu.Unlock()
	if ok && l.modified.Equal(fi.ModTime()) {
		return l.line
	}
	line := licenseLine(ctx, s.Root, s.Manifest)
	m.mu.Lock()
	if m.licenses == nil {
		m.licenses = map[string]license{}
	}
	m.licenses[s.Manifest] = license{modified: fi.ModTime(), line: line}
	m.mu.Unlock()
	return line
}

func licenseLine(ctx context.Context, root, manifest string) string {
	c, err := assets.Open(assets.Options{Root: root})
	if err != nil {
		return ""
	}
	rep, err := c.LicenseReport(ctx, assets.LicenseReportArgs{Path: manifest})
	if err != nil {
		return ""
	}
	var reviewOnly, blocked int
	for _, b := range rep.Blockers {
		switch {
		case strings.HasPrefix(b.Reason, "review-only"):
			reviewOnly++
		case strings.HasPrefix(b.Reason, "blocked"):
			blocked++
		}
	}
	return LicenseLine(reviewOnly, blocked)
}

// LicenseLine says how many assets are marked review only or blocked, in a few words.
func LicenseLine(reviewOnly, blocked int) string {
	n := reviewOnly + blocked
	assetWord := "assets"
	if n == 1 {
		assetWord = "asset"
	}
	switch {
	case n == 0:
		return ""
	case blocked == 0:
		return fmt.Sprintf("Has %d %s marked review only", n, assetWord)
	case reviewOnly == 0:
		return fmt.Sprintf("Has %d %s marked blocked", n, assetWord)
	}
	return fmt.Sprintf("Has %d %s marked review only or blocked", n, assetWord)
}
