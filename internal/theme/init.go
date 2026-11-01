package theme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrExists is Init's error when a starter file is there already and force wasn't given.
var ErrExists = errors.New("already there")

// Init writes a starter theme into dir (spec/theme.md, "retake theme init"): theme.css with every token at its
// current value and every part, all commented out, and theme.toml with its four keys commented out. It makes dir, and
// never overwrites a file unless force. It returns the files it wrote.
func Init(dir string, force bool) ([]string, error) {
	spec, err := Current()
	if err != nil {
		return nil, err
	}
	files := map[string]string{CSSFile: StarterCSS(spec), TOMLFile: StarterTOML()}
	order := []string{CSSFile, TOMLFile}
	if !force {
		var there []string
		for _, name := range order {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				there = append(there, filepath.Join(dir, name))
			}
		}
		if len(there) > 0 {
			return nil, fmt.Errorf("%s %w (--force replaces it)", strings.Join(there, " and "), ErrExists)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a theme folder others may read, like ~/.config
		return nil, err
	}
	var wrote []string
	for _, name := range order {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(files[name]), 0o644); err != nil { //nolint:gosec // CSS and settings, not secrets
			return wrote, err
		}
		wrote = append(wrote, p)
	}
	return wrote, nil
}

// StarterCSS is the starter theme.css: every token of spec with its value, then every part, commented out.
func StarterCSS(spec *Spec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `/* A Retake theme (Theme API v%d, spec/theme.md). This folder is yours: it lives outside the install, so
   updating retake never changes it. Uncomment a line and change its value, then reload the page.
   After an update, run `+"`retake theme check`"+`: it says if something here isn't used any more. */

/* Tokens: every colour, font and key size of the page, at this version's built-in value. */
:root {
`, spec.API)
	group := ""
	var wide []Token
	for _, t := range spec.Tokens {
		if t.Group != group {
			if group != "" {
				b.WriteString("\n")
			}
			group = t.Group
			fmt.Fprintf(&b, "  /* %s */\n", group)
		}
		fmt.Fprintf(&b, "  /* %s: %s; */\n", t.Name, t.Default)
		if t.Wide != "" {
			wide = append(wide, t)
		}
	}
	b.WriteString("}\n")
	if len(wide) > 0 {
		b.WriteString("\n/* On screens 2200 px wide and up, these have their own built-in values. A value set above is used at\n" +
			"   every width; to change only wide screens, uncomment this rule.\n@media (min-width: 2200px) {\n  :root {\n")
		for _, t := range wide {
			fmt.Fprintf(&b, "    %s: %s;\n", t.Name, t.Wide)
		}
		b.WriteString("  }\n} */\n")
	}
	b.WriteString("\n/* Parts: every major element has a data-part. Select one with [data-part~='name'], for example\n" +
		"   [data-part~='bar'] { border-top-color: var(--accent); }\n" +
		"   Comments and markers also have data-state (open, working, question, done); video cards data-status.\n\n")
	width := 0
	for _, p := range spec.Parts {
		width = max(width, len(p.Name))
	}
	for _, p := range spec.Parts {
		sel := "[data-part~='" + p.Name + "'] {}"
		fmt.Fprintf(&b, "   %-*s  %s\n", width+22, sel, p.What)
	}
	b.WriteString("*/\n")
	return b.String()
}

// StarterTOML is the starter theme.toml.
func StarterTOML() string {
	return `# A Retake theme's settings (spec/theme.md). Every key is optional; uncomment one to use it.

# name = "My Review"       # shown in the page titles and at the top of the home, instead of "Retake"
# logo = "logo.svg"        # shown before the name at the top of the home: a file in this folder
# favicon = "favicon.svg"  # the browser tab's icon: a file in this folder
# mode = "dark"            # dark, light or auto (follow the computer's setting)
`
}
