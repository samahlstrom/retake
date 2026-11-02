package project

import (
	"fmt"
	"regexp"
	"strconv"
)

// Comments on vN become edits in vN+1 (spec/automation.md): a comment's target is the version after the one it was
// made on, made when it doesn't exist and updated in place when it does.

var targetLabelRE = regexp.MustCompile(`^v(\d+)(?:-draft\d+)?$`)

// TargetOf is the version a comment made on version goes in: v7 → v8, and a draft (v8-draft2) → the version it heads
// for (v8). "" for a label that isn't a version's.
func TargetOf(version string) string {
	m := targetLabelRE.FindStringSubmatch(version)
	if m == nil {
		return ""
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return ""
	}
	if len(m[0]) > len("v"+m[1]) { // a draft heads for its own version
		return fmt.Sprintf("v%d", n)
	}
	return fmt.Sprintf("v%d", n+1)
}

// LabelNumber is N of a version label v<N>, 0 for anything else.
func LabelNumber(label string) int {
	m := targetLabelRE.FindStringSubmatch(label)
	if m == nil || len(m[0]) > len("v"+m[1]) {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// Target is the version the note's edit goes in: a typed comment that doesn't want a version is finished in its own
// version (a report alone does it); every other note targets the version after its own.
func (n Note) Target() string {
	if n.IsDoc() && !n.WantsVersion() {
		return n.Version
	}
	return TargetOf(n.Version)
}

// Target is the item's note's target (Note.Target).
func (it Item) Target() string {
	if it.Key != "" && it.Wants != WantsVersion {
		return it.Version
	}
	return TargetOf(it.Version)
}
