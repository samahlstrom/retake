package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ChooseFolders opens macOS's native multiple-folder chooser. The caller
// decides when macOS is appropriate, which keeps this helper testable on a
// non-macOS host with a fake osascript on PATH.
func ChooseFolders(ctx context.Context) ([]string, error) {
	path, err := exec.LookPath("osascript")
	if err != nil {
		return nil, fmt.Errorf("native folder picker is unavailable: %w", err)
	}
	const script = `ObjC.import('stdlib');
const app = Application.currentApplication();
app.includeStandardAdditions = true;
try {
  const paths = app.chooseFolder({multipleSelectionsAllowed: true}).map(String);
  JSON.stringify(paths);
} catch (error) {
  if (error.number === -128 || error.errorNumber === -128) JSON.stringify([]);
  else throw error;
}`
	out, err := exec.CommandContext(ctx, path, "-l", "JavaScript", "-e", script).Output() //nolint:gosec // Fixed OS tool resolved from the local owner's PATH.
	if err != nil {
		return nil, fmt.Errorf("opening native folder picker: %w", err)
	}
	var paths []string
	if err := json.Unmarshal(out, &paths); err != nil {
		return nil, fmt.Errorf("reading native folder picker: %w", err)
	}
	return paths, nil
}
