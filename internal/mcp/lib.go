package mcp

import (
	"context"
	"encoding/json"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/lib"
)

const (
	componentListDesc = `List the components (reusable motion graphics: titles, pops, lower thirds, captions, ` +
		`picture-in-picture, zooms...) and brand kits this project can use: its own packages (components/ next to the ` +
		`timeline), $RETAKE_COMPONENT_PATH and the user library. Each has its ref (name@version), title, category, where ` +
		`it can be used (use_as: overlay, or picture for a shot's or an insert's picture), the path of its storybook ` +
		`sheet (open it to see the component), which timeline elements use it and whether the lockfile pins it. Use ` +
		`component_describe before using one; lib_search finds more in a registry.`
	componentDescribeDesc = `Describe a component (or a brand kit) before you use it: the props JSON Schema with every ` +
		`prop's description and default, required props, props that take an asset id (inputs), the examples, the ` +
		`storybook sheet (also returned as an image: each example at five moments), the changelog, other versions, and ` +
		`"use": a ready-to-patch use with the first example's props. A component goes where layers would: an overlay ` +
		`{"id", "use": "<name@version>", "from", "to", "props"}, or a picture {"use", "props"}; props are in pixels of ` +
		`its 1920x1080 design canvas and it fits the timeline's canvas by itself. Colors can be kit tokens such as ` +
		`kit:color.accent. Patch props to change one; never patch its layers. A kit's description has its tokens, ` +
		`components and house rules.`
	libSearchDesc = `Search a registry (a static index.json: a path or URL, default $RETAKE_REGISTRY) and the user ` +
		`library for packages: components, brand kits and edit recipes. Every word must appear in the name, title, ` +
		`description, category or tags. Returns the newest version of each, with its license, source, the sheet's ` +
		`path or URL, and whether the project already has it. Add one with lib_add.`
	libAddDesc = `Add a registry package to the project: it is copied into components/<name>@<version> next to the ` +
		`timeline (the project owns the copy), every file is checked against its hash, and the lockfile pins it with ` +
		`its source. A kit brings the components it lists. Only packages with an open license (CC0, MIT, Apache, ` +
		`BSD, ISC, OFL, CC BY) are added. Returns what was added and how to use it; then patch the timeline to use it ` +
		`(a kit goes in "kits").`
	lintDesc = `Check the project's timeline against its brand kits' house rules (retake-edit lint): one pop at a ` +
		`time, room between pops, a budget of punch-ins, graphics inside the safe area, names never over captions, ` +
		`and the kit's own rules. Returns typed violations, each with its rule id, severity (error or warning), a ` +
		`message with the times ("pop overlaps pop at 0:12.3–0:12.5 (pop/a, pop/b); one pop at a time"), the ` +
		`elements and their JSON pointers, and a hint saying how to fix it. clean is true when there are none. Run ` +
		`it after your patches and before a final render; fix errors, weigh warnings.`
)

type componentListIn struct {
	Q string `json:"q,omitempty" jsonschema:"words that must all appear in the name, title, description or category"`
}

type componentDescribeIn struct {
	Name string `json:"name" jsonschema:"the component or kit: name (newest version) or name@version, e.g. pop-text@1.0.0"`
}

type libSearchIn struct {
	Q        string `json:"q,omitempty" jsonschema:"words to find (all of them); empty: everything"`
	Registry string `json:"registry,omitempty" jsonschema:"a registry index.json path or http(s) URL (default: $RETAKE_REGISTRY)"`
	Type     string `json:"type,omitempty" jsonschema:"only this type: component, kit or recipe"`
}

type libAddIn struct {
	Name     string `json:"name" jsonschema:"the package: name (newest version) or name@version"`
	Registry string `json:"registry,omitempty" jsonschema:"a registry index.json path or http(s) URL (default: $RETAKE_REGISTRY)"`
}

type lintIn struct {
	Timeline string `json:"timeline,omitempty" jsonschema:"the timeline (default: the project's)"`
}

// LibOptions configure the library tools.
type LibOptions struct {
	Root   string        // the project root (its timeline is found as in spec/bridge.md)
	Editor bridge.Editor // finds retake-edit, for lint
	Getenv func(string) string
	// Within keeps a remote agent inside the project: no lib_add, the configured registry only, timelines inside.
	Within string
}

// AddLibTools registers the shared library's tools (spec/components.md, "Agent tools"): component_list,
// component_describe, lib_search, lib_add (local agents only) and lint.
func AddLibTools(s *sdk.Server, o LibOptions) {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	timeline := func(given string) (string, error) {
		if given != "" {
			if o.Within != "" {
				return insideFor("a timeline", o.Within, given)
			}
			return given, nil
		}
		t, err := bridge.FindTimeline(o.Root)
		if err != nil {
			return "", &agent.Error{Code: agent.CodeNotFound, Message: err.Error()}
		}
		return t, nil
	}
	registry := func(given string) string {
		if given != "" && o.Within == "" {
			return given
		}
		return o.Getenv("RETAKE_REGISTRY")
	}
	sdk.AddTool(s, &sdk.Tool{Name: "component_list", Description: componentListDesc},
		func(_ context.Context, _ *sdk.CallToolRequest, in componentListIn) (*sdk.CallToolResult, lib.ListResult, error) {
			tl, _ := timeline("")
			res, err := lib.List(lib.ListArgs{Timeline: tl, Query: in.Q, Getenv: o.Getenv})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "component_describe", Description: componentDescribeDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in componentDescribeIn) (*sdk.CallToolResult, lib.Description, error) {
			tl, _ := timeline("")
			d, err := lib.Describe(ctx, lib.DescribeArgs{Ref: in.Name, Timeline: tl, Registry: registry(""),
				Getenv: o.Getenv})
			if err != nil {
				return nil, d, err
			}
			return describeContent(d), d, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "lib_search", Description: libSearchDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in libSearchIn) (*sdk.CallToolResult, lib.SearchResult, error) {
			tl, _ := timeline("")
			res, err := lib.Search(ctx, lib.SearchArgs{Query: in.Q, Registry: registry(in.Registry), Type: in.Type,
				Timeline: tl, Getenv: o.Getenv})
			return nil, res, err
		})
	if o.Within == "" {
		sdk.AddTool(s, &sdk.Tool{Name: "lib_add", Description: libAddDesc},
			func(ctx context.Context, _ *sdk.CallToolRequest, in libAddIn) (*sdk.CallToolResult, lib.AddResult, error) {
				tl, err := timeline("")
				if err != nil {
					return nil, lib.AddResult{}, err
				}
				res, err := lib.Add(ctx, lib.AddArgs{Ref: in.Name, Registry: registry(in.Registry), Timeline: tl})
				return nil, res, err
			})
	}
	sdk.AddTool(s, &sdk.Tool{Name: "lint", Description: lintDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in lintIn) (*sdk.CallToolResult, lib.LintResult, error) {
			tl, err := timeline(in.Timeline)
			if err != nil {
				return nil, lib.LintResult{}, err
			}
			res, err := lib.Lint(ctx, o.Editor, tl)
			return nil, res, err
		})
}

// describeContent is a description as JSON plus its storybook sheet as an image the agent can see.
func describeContent(d lib.Description) *sdk.CallToolResult {
	out := &sdk.CallToolResult{}
	if b, err := json.Marshal(d); err == nil {
		out.Content = append(out.Content, &sdk.TextContent{Text: string(b)})
	}
	if p := d.Previews["sheet"]; p != "" {
		if data, err := os.ReadFile(p); err == nil && len(data) <= maxInlineBytes { //nolint:gosec // a package's sheet
			out.Content = append(out.Content, &sdk.ImageContent{Data: data, MIMEType: "image/png"})
		}
	}
	return out
}
