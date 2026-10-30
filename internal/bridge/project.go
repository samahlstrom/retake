package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ProjectConfig is the file name of a project's own config, in its root.
const ProjectConfig = "retake.toml"

// DefaultTimeline is where a timeline project's timeline is when retake.toml doesn't say: <root>/edit/timeline.json.
var DefaultTimeline = filepath.Join("edit", "timeline.json")

// ErrNoTimeline is returned for a project that isn't a timeline project.
var ErrNoTimeline = errors.New("not a timeline project")

type projectFile struct {
	Edit struct {
		Timeline string `toml:"timeline"`
	} `toml:"edit"`
}

// FindTimeline returns the timeline of the project at root (spec/bridge.md): [edit] timeline in <root>/retake.toml,
// else <root>/edit/timeline.json. ErrNoTimeline when there is none.
func FindTimeline(root string) (string, error) {
	cfg := filepath.Join(root, ProjectConfig)
	if b, err := os.ReadFile(cfg); err == nil { //nolint:gosec // the project's own config
		var pf projectFile
		if _, err := toml.Decode(string(b), &pf); err != nil {
			return "", fmt.Errorf("%s: %w", cfg, err)
		}
		if p := strings.TrimSpace(pf.Edit.Timeline); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("%s names the timeline %s: %w", cfg, p, err)
			}
			return p, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	p := filepath.Join(root, DefaultTimeline)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p, nil
	}
	return "", fmt.Errorf("%w: no %s and no [edit] timeline in %s", ErrNoTimeline, DefaultTimeline, ProjectConfig)
}

// TimelineRev reads a timeline's revision.
func TimelineRev(timeline string) (int64, error) {
	b, err := os.ReadFile(timeline) //nolint:gosec // the project's timeline
	if err != nil {
		return 0, err
	}
	var v struct {
		Rev *int64 `json:"rev"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return 0, fmt.Errorf("%s: %w", timeline, err)
	}
	if v.Rev == nil {
		return 0, fmt.Errorf("%s has no rev", timeline)
	}
	return *v.Rev, nil
}

// Patch is one entry of a timeline's patch log (spec/timeline.md, "Patches").
type Patch struct {
	ID      string   `json:"id"`
	Rev     int64    `json:"rev"`
	Base    int64    `json:"base"`
	Answers []string `json:"answers"`
	Summary string   `json:"summary"`
	Reverts string   `json:"reverts,omitempty"`
	Applied string   `json:"applied,omitempty"`
}

// PatchLogPath is a timeline's patch log: <stem>.patches.jsonl beside it.
func PatchLogPath(timeline string) string {
	return strings.TrimSuffix(timeline, filepath.Ext(timeline)) + ".patches.jsonl"
}

// ReadPatchLog reads a timeline's patch log, oldest first. A timeline without patches has an empty log.
func ReadPatchLog(timeline string) ([]Patch, error) {
	f, err := os.Open(PatchLogPath(timeline))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Patch
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p Patch
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", PatchLogPath(timeline), n, err)
		}
		out = append(out, p)
	}
	return out, sc.Err()
}

var answerRE = regexp.MustCompile(`^(?:(?:[A-Za-z][\w.-]*)?#)?(\d+)$`)

// NoteID parses a patch's answer: "#5", "5" or "v7#5" (the label is informational) all name note 5.
func NoteID(answer string) (int64, bool) {
	m := answerRE.FindStringSubmatch(strings.TrimSpace(answer))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil && n > 0
}
