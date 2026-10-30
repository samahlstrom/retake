package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/assets"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// AssetCatalog opens the asset catalog for one tool call; the call closes it.
type AssetCatalog func() (*assets.Catalog, error)

// AssetsFor returns the catalog of a project root, with the user's library, plug-ins and transcription config from
// the environment.
func AssetsFor(root string) AssetCatalog {
	return func() (*assets.Catalog, error) {
		return assets.Open(assets.Options{Root: root, Transcriber: configuredTranscriber})
	}
}

// configuredTranscriber is the [transcribe] engine of the user's config.
func configuredTranscriber() (assets.Transcriber, error) {
	path, err := config.Path(os.Getenv)
	if err != nil {
		return nil, err
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return transcribe.FromConfig(cfg)
}

const (
	assetSearchDesc = `Search the asset catalog: footage, VO, music, SFX, images and fonts of this project (proj:), ` +
		`its brand kits (brand:) and the user's library (lib:). Filters first (kind, scope, tags, license, span type, ` +
		`and where: a SQL condition on spans), then full-text search over titles, tags, captions, transcripts and span ` +
		`labels; every word must match, as a prefix. Returns assets best first, each with its id, kind, license status, ` +
		`duration, the path of its preview image (contact sheet, waveform, thumbnail or specimen: open it to see the ` +
		`asset), the matching spans (span ids with t0/t1 in seconds into the asset) and, for transcript matches, where ` +
		`the words were said. Every time in a result is stored data: use them as they are, never estimate others. With ` +
		`type or where, results are the matching spans grouped by asset.`
	assetGetDesc = `Get one catalog entry by id (proj:…, brand:<kit>/…, lib:…, or an unprefixed id looked up in the ` +
		`project, then the brand kits, then the library): kind, file (blob.path, whether it exists, its BLAKE3 hash), ` +
		`media (duration, size, fps, audio streams), title, tags, caption, transcript info, preview paths, the number ` +
		`of spans, clocks (how the asset's time maps to a domain such as a game clock), the license (status, whether it ` +
		`may be published, credit line, content-ID risk), provenance and near-duplicate images.`
	assetPreviewDesc = `Look at an asset. Returns preview images (also as image content you can see) and their ` +
		`paths: a video's contact sheet (one tile every every_s seconds, timecodes burned in), a span's 6-frame strip ` +
		`(span: sp:<n>), frames at given times (at: seconds into the asset, up to 24, rendered on demand from the ` +
		`proxy), an audio file's waveform with its loudness, an image's thumbnail or a font's specimen sheet. Missing ` +
		`previews are made now. Use frames to check a moment before cutting to it.`
	spanFindDesc = `Find spans: labeled moments inside assets (t0/t1 in seconds into the asset, a type, a label, ` +
		`data, where the label came from and how sure it is). Filter by type (exact, or LIKE patterns with %), by ` +
		`asset, by plug-in, by where (a SQL condition on spans; plug-in views such as <plugin>_… can be used in ` +
		`subqueries), or near a moment: near is <asset id>@<time> or <clock domain>@<time> (e.g. ` +
		`game:match/42@9:30), within window seconds (default 15). Each span carries its times on the asset's ` +
		`clocks and its strip preview path once made. A span is a timeline source: cut [t0, t1] of its asset.`
	spanAddDesc = `Add a span: label a moment of an asset you found (t0, t1 in seconds into the asset). type ` +
		`defaults to tag (a free label); a plug-in's type must fit its data schema. The span is marked ` +
		`source_of_label: agent so people can tell your labels from theirs. A video span gets its 6-frame strip ` +
		`preview right away; look at it to check you labeled the right moment. Returns the span with its id.`
	assetImportDesc = `Import files or folders into the catalog: each is hashed (BLAKE3), checked for exact ` +
		`duplicates (reported, not imported again) and near-duplicate images (reported), copied into the store or, ` +
		`for big recordings, referenced in place, probed, and given previews (and a transcript with transcribe: true, ` +
		`when transcription is configured). Or run a plug-in's indexer with via (plugin or plugin/indexer) and its ` +
		`args. Everything you import is review-only: fine in drafts, blocked at publish, until a person sets its ` +
		`license with retake assets set. scope is proj (default) or lib.`
	pluginQueryDesc = `Run a plug-in tool, e.g. a game plug-in's find or sql. args go to the tool as they are. ` +
		`Objects in the result that name a span the plug-in indexed (span_key) get its catalog span id and asset ` +
		`added, so you can preview and cut them. The installed plug-ins, their tools and input schemas are also listed ` +
		`as tools named <plugin>_<tool>.`
	licenseReportDesc = `License report of a timeline (timeline.json) or a render manifest (*.manifest.json): ` +
		`every asset used, with the output time ranges where it is seen or heard and the elements using it, its ` +
		`license, the publish blockers (review-only or blocked assets, attribution without a credit line, assets the ` +
		`catalog doesn't know), content-ID warnings, and the credits text for the video description. publishable is ` +
		`true only with no blockers. Run it before asking for a final render.`
)

type assetSearchIn struct {
	Q       string   `json:"q,omitempty" jsonschema:"words to find (all of them, as prefixes); empty: filters only"`
	Kind    []string `json:"kind,omitempty" jsonschema:"only these kinds: video, audio, image, font, other"`
	Scope   []string `json:"scope,omitempty" jsonschema:"only these scopes: proj, brand, lib"`
	Tags    []string `json:"tags,omitempty" jsonschema:"only assets that have all of these tags"`
	License []string `json:"license,omitempty" jsonschema:"only these license statuses: owned, cleared, attribution, review-only, blocked"`
	Type    []string `json:"type,omitempty" jsonschema:"only spans of these types (LIKE patterns with %)"`
	Where   string   `json:"where,omitempty" jsonschema:"a SQL condition on spans: columns id, asset, t0, t1, type, label, data (JSON), source_of_label, confidence, plugin"`
	Limit   int      `json:"limit,omitempty" jsonschema:"at most this many assets (default 20, at most 200)"`
}

type assetIDIn struct {
	ID string `json:"id" jsonschema:"the asset id, e.g. proj:memes/thanos or lib:sfx/whoosh"`
}

type assetPreviewIn struct {
	ID   string    `json:"id" jsonschema:"the asset id"`
	Kind string    `json:"kind,omitempty" jsonschema:"contact_sheet, strip, frames, waveform, thumbnail or specimen (default: the asset's main preview)"`
	At   []float64 `json:"at,omitempty" jsonschema:"frames at these times, seconds into the asset"`
	Span string    `json:"span,omitempty" jsonschema:"a span id (sp:<n>): its 6-frame strip"`
}

type spanFindIn struct {
	Type   []string `json:"type,omitempty" jsonschema:"span types (LIKE patterns with %)"`
	Where  string   `json:"where,omitempty" jsonschema:"a SQL condition on spans"`
	Near   string   `json:"near,omitempty" jsonschema:"<asset id>@<time> or <clock domain>@<time>; time in seconds, m:ss or h:mm:ss"`
	Window float64  `json:"window,omitempty" jsonschema:"seconds around near (default 15)"`
	Asset  string   `json:"asset,omitempty" jsonschema:"only spans of this asset"`
	Plugin string   `json:"plugin,omitempty" jsonschema:"only spans from this plug-in"`
	Limit  int      `json:"limit,omitempty" jsonschema:"at most this many spans (default 50)"`
}

type spanAddIn struct {
	Asset      string         `json:"asset" jsonschema:"the asset id"`
	T0         float64        `json:"t0" jsonschema:"start, seconds into the asset"`
	T1         float64        `json:"t1" jsonschema:"end, seconds into the asset"`
	Type       string         `json:"type,omitempty" jsonschema:"span type (default tag)"`
	Label      string         `json:"label,omitempty" jsonschema:"what happens there, in a few words"`
	Data       map[string]any `json:"data,omitempty" jsonschema:"extra data, as the span type's schema asks"`
	Confidence string         `json:"confidence,omitempty" jsonschema:"how sure you are: high, medium, low"`
}

type assetImportIn struct {
	Paths      []string       `json:"paths,omitempty" jsonschema:"files or folders to import (one path for an indexer that reads a file)"`
	Scope      string         `json:"scope,omitempty" jsonschema:"proj (default) or lib"`
	Tags       []string       `json:"tags,omitempty" jsonschema:"tags for every imported file"`
	Title      string         `json:"title,omitempty" jsonschema:"a title (one file only)"`
	Caption    string         `json:"caption,omitempty" jsonschema:"what the picture shows"`
	Via        string         `json:"via,omitempty" jsonschema:"run this plug-in's indexer instead: plugin or plugin/indexer"`
	Args       map[string]any `json:"args,omitempty" jsonschema:"the indexer's arguments"`
	Transcribe bool           `json:"transcribe,omitempty" jsonschema:"transcribe audio and video (when transcription is configured)"`
	InPlace    bool           `json:"in_place,omitempty" jsonschema:"reference the files where they are instead of copying them"`
}

type pluginQueryIn struct {
	Plugin string         `json:"plugin" jsonschema:"the plug-in name"`
	Tool   string         `json:"tool" jsonschema:"the tool name"`
	Args   map[string]any `json:"args,omitempty" jsonschema:"the tool's arguments"`
}

type licenseReportIn struct {
	Path     string `json:"path" jsonschema:"a timeline.json or a render manifest"`
	Resolved string `json:"resolved,omitempty" jsonschema:"for a timeline: the output of retake-edit resolve --json (it is run when retake-edit is installed)"`
}

// maxInlineImages and maxInlineBytes bound the images asset_preview returns as content.
const (
	maxInlineImages = 4
	maxInlineBytes  = 3 << 20
)

// AddAssetTools registers the asset catalog's tools on s (spec/assets.md, "MCP tools"), plus one tool per plug-in
// tool. open is called for every call, which closes the catalog when done.
func AddAssetTools(s *sdk.Server, open AssetCatalog) { addAssetTools(s, open, "") }

// addAssetTools registers the asset tools. A non-empty within (the remote /mcp route's project root) keeps a remote
// agent inside the project: asset_import and license_report only take paths inside it, plug-in indexers (via) and
// plug-in tools aren't offered, since their arguments can name any file on the machine.
func addAssetTools(s *sdk.Server, open AssetCatalog, within string) {
	with := func(fn func(c *assets.Catalog) error) error {
		c, err := open()
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		return fn(c)
	}
	sdk.AddTool(s, &sdk.Tool{Name: "asset_search", Description: assetSearchDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in assetSearchIn) (*sdk.CallToolResult, assets.SearchResult, error) {
			var res assets.SearchResult
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.Search(ctx, assets.SearchArgs{Query: in.Q, Kind: in.Kind, Scope: in.Scope, Tags: in.Tags,
					License: in.License, Type: in.Type, Where: in.Where, Limit: in.Limit})
				return err
			})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "asset_get", Description: assetGetDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in assetIDIn) (*sdk.CallToolResult, assets.Entry, error) {
			var res assets.Entry
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.Get(ctx, in.ID)
				return err
			})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "asset_preview", Description: assetPreviewDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in assetPreviewIn) (*sdk.CallToolResult, assets.PreviewResult, error) {
			var res assets.PreviewResult
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.Preview(ctx, assets.PreviewArgs{ID: in.ID, Kind: in.Kind, At: in.At, Span: in.Span})
				return err
			})
			if err != nil {
				return nil, res, err
			}
			return previewContent(res), res, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "span_find", Description: spanFindDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in spanFindIn) (*sdk.CallToolResult, assets.SpanFindResult, error) {
			var res assets.SpanFindResult
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.SpanFind(ctx, assets.SpanFindArgs{Type: in.Type, Where: in.Where, Near: in.Near,
					Window: in.Window, Asset: in.Asset, Plugin: in.Plugin, Limit: in.Limit})
				return err
			})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "span_add", Description: spanAddDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in spanAddIn) (*sdk.CallToolResult, assets.Span, error) {
			var res assets.Span
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.SpanAdd(ctx, assets.SpanAddArgs{Asset: in.Asset, T0: in.T0, T1: in.T1, Type: in.Type,
					Label: in.Label, Data: in.Data, Confidence: in.Confidence, SourceOfLabel: "agent"})
				return err
			})
			return nil, res, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "asset_import", Description: assetImportDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in assetImportIn) (*sdk.CallToolResult, assets.ImportResult, error) {
			var res assets.ImportResult
			if within != "" {
				if in.Via != "" {
					return nil, res, &agent.Error{Code: agent.CodeBadRequest,
						Message: "over HTTP, plug-in indexers (via) only run locally"}
				}
				for i, p := range in.Paths {
					var err error
					if in.Paths[i], err = insideFor("imported files", within, p); err != nil {
						return nil, res, err
					}
				}
			}
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.Import(ctx, assets.ImportArgs{Paths: in.Paths, Scope: in.Scope, Tags: in.Tags, Title: in.Title,
					Caption: in.Caption, Via: in.Via, Args: in.Args, Transcribe: in.Transcribe, InPlace: in.InPlace,
					Importer: "mcp"})
				return err
			})
			return nil, res, err
		})
	if within == "" {
		sdk.AddTool(s, &sdk.Tool{Name: "plugin_query", Description: pluginQueryDesc},
			func(ctx context.Context, _ *sdk.CallToolRequest, in pluginQueryIn) (*sdk.CallToolResult, assets.PluginQueryResult, error) {
				var res assets.PluginQueryResult
				err := with(func(c *assets.Catalog) (err error) {
					res, err = c.PluginQuery(ctx, assets.PluginQueryArgs{Plugin: in.Plugin, Tool: in.Tool, Args: in.Args})
					return err
				})
				return nil, res, err
			})
	}
	sdk.AddTool(s, &sdk.Tool{Name: "license_report", Description: licenseReportDesc},
		func(ctx context.Context, _ *sdk.CallToolRequest, in licenseReportIn) (*sdk.CallToolResult, assets.LicenseReport, error) {
			var res assets.LicenseReport
			if within != "" {
				var err error
				if in.Path, err = insideFor("a license report's timeline or manifest", within, in.Path); err != nil {
					return nil, res, err
				}
			}
			err := with(func(c *assets.Catalog) (err error) {
				res, err = c.LicenseReport(ctx, assets.LicenseReportArgs{Path: in.Path, Resolved: in.Resolved})
				return err
			})
			return nil, res, err
		})
	if within == "" {
		addPluginTools(s, open)
	}
}

// ServeAssets serves only the asset tools on in and out (as `retake assets mcp` does on stdio) until the client
// hangs up or ctx is done.
func ServeAssets(ctx context.Context, open AssetCatalog, version string, in io.Reader, out io.Writer) error {
	s := sdk.NewServer(&sdk.Implementation{Name: "retake-assets", Version: version}, &sdk.ServerOptions{
		Instructions: `The asset catalog of a Retake project: search footage, audio, images and fonts, look at ` +
			`previews, find and label moments (spans), import files, run plug-in tools, and check licenses before ` +
			`publishing. Ids carry their scope: proj:, brand:, lib:.`})
	AddAssetTools(s, open)
	return s.Run(ctx, &sdk.IOTransport{Reader: io.NopCloser(in), Writer: nopCloser{out}})
}

// previewContent returns the preview's JSON and up to maxInlineImages of its images as content.
func previewContent(res assets.PreviewResult) *sdk.CallToolResult {
	out := &sdk.CallToolResult{}
	b, err := json.Marshal(res)
	if err == nil {
		out.Content = append(out.Content, &sdk.TextContent{Text: string(b)})
	}
	total := 0
	for i, f := range res.Files {
		if i >= maxInlineImages {
			break
		}
		data, err := os.ReadFile(f.Path)
		if err != nil || total+len(data) > maxInlineBytes {
			continue
		}
		total += len(data)
		mime := "image/jpeg"
		if strings.HasSuffix(f.Path, ".png") {
			mime = "image/png"
		}
		out.Content = append(out.Content, &sdk.ImageContent{Data: data, MIMEType: mime})
	}
	return out
}

// addPluginTools registers each discovered plug-in tool as <plugin>_<tool>, with its own input schema.
func addPluginTools(s *sdk.Server, open AssetCatalog) {
	c, err := open()
	if err != nil {
		return
	}
	ps := c.Plugins()
	_ = c.Close()
	reserved := map[string]bool{"asset_search": true, "asset_get": true, "asset_preview": true, "span_find": true,
		"span_add": true, "asset_import": true, "plugin_query": true, "license_report": true}
	for _, p := range ps.Plugins {
		for _, t := range p.Tools {
			name := strings.ReplaceAll(p.Name, "-", "_") + "_" + t.Name
			if reserved[name] {
				continue
			}
			reserved[name] = true
			schema := map[string]any{"type": "object"}
			for k, v := range t.InputSchema {
				schema[k] = v
			}
			schema["type"] = "object"
			desc := t.Description
			if desc == "" {
				desc = "A tool of the " + p.Name + " plug-in."
			}
			desc += fmt.Sprintf(" (Plug-in %s %s. Objects with a span_key get its catalog span id and asset.)", p.Name,
				p.Version)
			plugin, tool := p.Name, t.Name
			s.AddTool(&sdk.Tool{Name: name, Description: desc, InputSchema: schema},
				func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					args := map[string]any{}
					if raw := req.Params.Arguments; len(raw) > 0 && string(raw) != "null" {
						if err := json.Unmarshal(raw, &args); err != nil {
							res := &sdk.CallToolResult{}
							res.SetError(fmt.Errorf("arguments: %w", err))
							return res, nil
						}
					}
					var out assets.PluginQueryResult
					err := func() error {
						c, err := open()
						if err != nil {
							return err
						}
						defer func() { _ = c.Close() }()
						out, err = c.PluginQuery(ctx, assets.PluginQueryArgs{Plugin: plugin, Tool: tool, Args: args})
						return err
					}()
					res := &sdk.CallToolResult{}
					if err != nil {
						res.SetError(err)
						return res, nil
					}
					b, err := json.Marshal(out.Result)
					if err != nil {
						return nil, err
					}
					res.Content = []sdk.Content{&sdk.TextContent{Text: string(b)}}
					res.StructuredContent = map[string]any{"result": out.Result}
					return res, nil
				})
		}
	}
}
