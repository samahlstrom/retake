package assets

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// PluginProtocol is the plug-in protocol version (spec/assets.md, "Protocol").
const PluginProtocol = "retake-plugin/0.1"

// Plug-in call time limits.
var (
	IndexTimeout = 30 * time.Minute
	ToolTimeout  = 2 * time.Minute
)

// PluginSpanType is a span type a plug-in adds.
type PluginSpanType struct {
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	DataSchema  map[string]any `json:"data_schema,omitempty"`
}

// PluginClockDomain is a clock domain a plug-in maps times to.
type PluginClockDomain struct {
	Domain      string `json:"domain"`
	Description string `json:"description,omitempty"`
}

// PluginIndexer is a way a plug-in brings assets in.
type PluginIndexer struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Accepts     []string       `json:"accepts,omitempty"`
	ArgsSchema  map[string]any `json:"args_schema,omitempty"`
}

// PluginView is a read-only SQL view a plug-in defines over the catalog.
type PluginView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SQL         string `json:"sql"`
}

// PluginTool is an extra tool a plug-in answers.
type PluginTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

// Plugin is a plug-in's plugin.json, plus where it was found.
type Plugin struct {
	Schema       string              `json:"schema"`
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Description  string              `json:"description,omitempty"`
	Exec         []string            `json:"exec"`
	SpanTypes    []PluginSpanType    `json:"span_types,omitempty"`
	ClockDomains []PluginClockDomain `json:"clock_domains,omitempty"`
	Indexers     []PluginIndexer     `json:"indexers,omitempty"`
	Views        []PluginView        `json:"views,omitempty"`
	Tools        []PluginTool        `json:"tools,omitempty"`
	Dir          string              `json:"dir"`
}

var (
	pluginNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	toolNameRE   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	viewNameRE   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// readPlugin reads and checks a plug-in folder's plugin.json.
func readPlugin(dir string) (*Plugin, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json")) //nolint:gosec // a plug-in folder the user installed
	if err != nil {
		return nil, err
	}
	var p Plugin
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s/plugin.json: %w", dir, err)
	}
	p.Dir = dir
	if p.Schema != "" && p.Schema != PluginProtocol {
		return nil, fmt.Errorf("%s: schema %q, this retake speaks %s", dir, p.Schema, PluginProtocol)
	}
	if !pluginNameRE.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: plug-in name %q must match %s", dir, p.Name, pluginNameRE)
	}
	if len(p.Exec) == 0 || p.Exec[0] == "" {
		return nil, fmt.Errorf("%s: plug-in %s has no exec", dir, p.Name)
	}
	for _, v := range p.Views {
		if err := checkView(p.Name, v); err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
	}
	for _, t := range p.Tools {
		if !toolNameRE.MatchString(t.Name) {
			return nil, fmt.Errorf("%s: tool name %q must match %s", dir, t.Name, toolNameRE)
		}
	}
	return &p, nil
}

func checkView(plugin string, v PluginView) error {
	prefix := strings.ReplaceAll(plugin, "-", "_") + "_"
	if !viewNameRE.MatchString(v.Name) || !strings.HasPrefix(v.Name, prefix) {
		return fmt.Errorf("view %q: names start with %q and use a-z, 0-9 and _", v.Name, prefix)
	}
	return checkSelect(v.SQL)
}

// checkSelect accepts one SELECT or WITH statement.
func checkSelect(q string) error {
	t := strings.ToLower(strings.TrimSpace(q))
	if !strings.HasPrefix(t, "select") && !strings.HasPrefix(t, "with") {
		return errorf(CodeBadRequest, "only SELECT (or WITH … SELECT) is allowed")
	}
	if strings.Contains(strings.TrimRight(strings.TrimSpace(q), ";"), ";") {
		return errorf(CodeBadRequest, "one statement only")
	}
	return nil
}

// pluginSet is the discovered plug-ins.
type pluginSet struct {
	list     []*Plugin
	byName   map[string]*Plugin
	problems []string
}

// pluginDirs lists the folders searched for plug-ins, in order.
func (c *Catalog) pluginDirs() []string {
	var out []string
	if c.o.Root != "" {
		out = append(out, filepath.Join(c.o.Root, ".retake", "plugins"))
	}
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	for _, p := range strings.Split(c.o.Getenv("RETAKE_PLUGIN_PATH"), sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return append(out, filepath.Join(c.o.Library, "plugins"))
}

func (c *Catalog) discover() *pluginSet {
	c.pluginsOnce.Do(func() {
		ps := &pluginSet{byName: map[string]*Plugin{}}
		types := map[string]string{SpanTag: "core", SpanShot: "core", SpanSpeech: "core"}
		add := func(dir string) {
			p, err := readPlugin(dir)
			if err != nil {
				ps.problems = append(ps.problems, err.Error())
				return
			}
			if prev, ok := ps.byName[p.Name]; ok {
				ps.problems = append(ps.problems, fmt.Sprintf("%s: plug-in %s is already loaded from %s", dir, p.Name, prev.Dir))
				return
			}
			var keep []PluginSpanType
			for _, t := range p.SpanTypes {
				if owner, ok := types[t.Type]; ok {
					ps.problems = append(ps.problems, fmt.Sprintf("plug-in %s: span type %s is already declared by %s; "+
						"ignored", p.Name, t.Type, owner))
					continue
				}
				types[t.Type] = p.Name
				keep = append(keep, t)
			}
			p.SpanTypes = keep
			ps.list = append(ps.list, p)
			ps.byName[p.Name] = p
		}
		for _, d := range c.pluginDirs() {
			if _, err := os.Stat(filepath.Join(d, "plugin.json")); err == nil {
				add(d)
				continue
			}
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for _, e := range entries {
				sub := filepath.Join(d, e.Name())
				if _, err := os.Stat(filepath.Join(sub, "plugin.json")); err == nil {
					add(sub)
				}
			}
		}
		c.plugins = ps
	})
	return c.plugins
}

// pluginList returns the discovered plug-ins.
func (c *Catalog) pluginList() []*Plugin { return c.discover().list }

// PluginsResult lists the plug-ins and what went wrong loading others.
type PluginsResult struct {
	Plugins  []*Plugin `json:"plugins"`
	Problems []string  `json:"problems"`
	Searched []string  `json:"searched"`
}

// Plugins lists the discovered plug-ins.
func (c *Catalog) Plugins() PluginsResult {
	ps := c.discover()
	return PluginsResult{Plugins: append([]*Plugin{}, ps.list...), Problems: append([]string{}, ps.problems...),
		Searched: c.pluginDirs()}
}

// plugin finds a plug-in by name.
func (c *Catalog) plugin(name string) (*Plugin, error) {
	ps := c.discover()
	if p, ok := ps.byName[name]; ok {
		return p, nil
	}
	var names []string
	for _, p := range ps.list {
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return nil, errorf(CodeNotFound, "no plug-in %q: none are installed (searched %s)", name,
			strings.Join(c.pluginDirs(), ", "))
	}
	return nil, errorf(CodeNotFound, "no plug-in %q; installed: %s", name, strings.Join(names, ", "))
}

// pluginRequest is the one JSON object a plug-in reads on stdin.
type pluginRequest struct {
	Protocol string         `json:"protocol"`
	Method   string         `json:"method"`
	Indexer  string         `json:"indexer"`
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args"`
	Path     string         `json:"path"`
	Context  pluginContext  `json:"context"`
}

type pluginContext struct {
	ProjectRoot string `json:"project_root"`
	Scope       string `json:"scope"`
	Catalog     string `json:"catalog"`
	PluginDir   string `json:"plugin_dir"`
}

// pluginLine is one line a plug-in writes: its type, and the line itself.
type pluginLine struct {
	Type string
	Raw  json.RawMessage
}

// run runs one plug-in call: the request on stdin, each stdout line to onLine. It fails on a non-zero exit, a line
// that isn't a JSON object, or when final (the type of the line that ends the call) never came.
func (c *Catalog) run(ctx context.Context, p *Plugin, req pluginRequest, timeout time.Duration, final []string,
	onLine func(pluginLine) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req.Protocol = PluginProtocol
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	req.Context.PluginDir = p.Dir
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	argv := append([]string{}, p.Exec...)
	if strings.ContainsAny(argv[0], `/\`) && !filepath.IsAbs(argv[0]) {
		argv[0] = filepath.Join(p.Dir, argv[0])
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the plug-in the user installed, argv only
	cmd.Dir = p.Dir
	cmd.Env = append(os.Environ(), "RETAKE_PLUGIN_DIR="+p.Dir, "RETAKE_PROJECT="+c.o.Root)
	cmd.Stdin = bytes.NewReader(append(body, '\n'))
	var stderr tailBuffer
	cmd.Stderr = io.MultiWriter(&stderr, prefixWriter{w: c.o.Log, prefix: "[" + p.Name + "] "})
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errorf(CodePlugin, "plug-in %s: can't start %s: %v", p.Name, argv[0], err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 1<<16), 64<<20)
	var lineErr error
	ended := false
	n := 0
	for sc.Scan() {
		n++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 || lineErr != nil {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(b, &head); err != nil || head.Type == "" {
			lineErr = errorf(CodePlugin, "plug-in %s: stdout line %d isn't a JSON object with a type: %.120s", p.Name, n, b)
			continue
		}
		for _, f := range final {
			if head.Type == f {
				ended = true
			}
		}
		if err := onLine(pluginLine{Type: head.Type, Raw: append(json.RawMessage{}, b...)}); err != nil {
			lineErr = err
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return errorf(CodePlugin, "plug-in %s: timed out after %s", p.Name, timeout)
	case lineErr != nil:
		return lineErr
	case waitErr != nil:
		return errorf(CodePlugin, "plug-in %s failed (%v): %s", p.Name, waitErr, stderr.tail())
	case scanErr != nil:
		return errorf(CodePlugin, "plug-in %s: reading its output: %v", p.Name, scanErr)
	case !ended:
		return errorf(CodePlugin, "plug-in %s ended without a %s line: %s", p.Name, strings.Join(final, " or "),
			stderr.tail())
	}
	return nil
}

// tailBuffer keeps the last 8 KB written to it.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}

func (t *tailBuffer) tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(t.b))
	if len(s) > 600 {
		s = "…" + s[len(s)-600:]
	}
	if s == "" {
		return "(no output on stderr)"
	}
	return s
}

// prefixWriter prefixes each write (a plug-in's log) for the user's log.
type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (p prefixWriter) Write(b []byte) (int, error) {
	if p.w == nil || p.w == io.Discard {
		return len(b), nil
	}
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			_, _ = io.WriteString(p.w, p.prefix+line)
		}
	}
	return len(b), nil
}

// PluginQueryArgs run a plug-in tool.
type PluginQueryArgs struct {
	Plugin string
	Tool   string
	Args   map[string]any
}

// PluginQueryResult is a plug-in tool's answer.
type PluginQueryResult struct {
	Plugin string `json:"plugin"`
	Tool   string `json:"tool"`
	Result any    `json:"result"`
}

// PluginQuery runs a plug-in tool. Objects in its result with a span_key the plug-in indexed get span and asset.
func (c *Catalog) PluginQuery(ctx context.Context, a PluginQueryArgs) (PluginQueryResult, error) {
	p, err := c.plugin(a.Plugin)
	if err != nil {
		return PluginQueryResult{}, err
	}
	known := false
	var names []string
	for _, t := range p.Tools {
		names = append(names, t.Name)
		if t.Name == a.Tool {
			known = true
		}
	}
	if !known {
		return PluginQueryResult{}, errorf(CodeBadRequest, "plug-in %s has no tool %q (tools: %s)", p.Name, a.Tool,
			strings.Join(names, ", "))
	}
	req := pluginRequest{Method: "tool", Tool: a.Tool, Args: a.Args, Context: c.pluginCtx(ScopeProject)}
	var result any
	var toolErr error
	err = c.run(ctx, p, req, ToolTimeout, []string{"result", "error"}, func(l pluginLine) error {
		switch l.Type {
		case "result":
			var r struct {
				Result any `json:"result"`
			}
			if err := json.Unmarshal(l.Raw, &r); err != nil {
				return errorf(CodePlugin, "plug-in %s: bad result line: %v", p.Name, err)
			}
			result = r.Result
		case "error":
			toolErr = lineError(p.Name, l.Raw)
		case "log":
			c.logLine(p.Name, l.Raw)
		}
		return nil
	})
	if err != nil {
		return PluginQueryResult{}, err
	}
	if toolErr != nil {
		return PluginQueryResult{}, toolErr
	}
	c.linkSpans(ctx, p.Name, result)
	return PluginQueryResult{Plugin: p.Name, Tool: a.Tool, Result: result}, nil
}

// lineError reads an error line.
func lineError(plugin string, raw json.RawMessage) error {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	code := e.Code
	if code != CodeBadRequest && code != CodeNotFound {
		code = CodePlugin
	}
	if e.Message == "" {
		e.Message = "failed without a message"
	}
	return &Error{Code: code, Message: "plug-in " + plugin + ": " + e.Message}
}

func (c *Catalog) logLine(plugin string, raw json.RawMessage) {
	var l struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &l) == nil && l.Message != "" {
		_, _ = fmt.Fprintf(c.o.Log, "[%s] %s\n", plugin, l.Message)
	}
}

// pluginCtx is the context part of a request.
func (c *Catalog) pluginCtx(scope string) pluginContext {
	pc := pluginContext{ProjectRoot: c.o.Root, Scope: scope}
	if scope == ScopeProject && c.o.Root != "" {
		pc.Catalog = filepath.Join(c.o.Root, ".retake", DBName)
	} else if scope != ScopeProject {
		pc.Catalog = filepath.Join(c.o.Library, DBName)
	}
	return pc
}

// linkSpans adds span and asset to every object in v that has a span_key the plug-in indexed.
func (c *Catalog) linkSpans(ctx context.Context, plugin string, v any) {
	cats, err := c.visible()
	if err != nil || len(cats) == 0 {
		return
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if k, ok := x["span_key"].(string); ok {
				for _, d := range cats {
					var id int64
					var asset string
					if d.rw.QueryRowContext(ctx, `SELECT id, asset FROM spans WHERE plugin = ? AND key = ?`, plugin, k).
						Scan(&id, &asset) == nil {
						x["span"] = SpanID(d.scope, id)
						x["asset"] = asset
						break
					}
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
}

// InstallPlugin links a plug-in folder into the library's plugins folder.
func (c *Catalog) InstallPlugin(dir string) (*Plugin, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, wrap(err)
	}
	p, err := readPlugin(abs)
	if err != nil {
		return nil, errorf(CodeBadRequest, "%v", err)
	}
	dst := filepath.Join(c.o.Library, "plugins", p.Name)
	if target, err := os.Readlink(dst); err == nil {
		if target == abs {
			return p, nil
		}
		return nil, errorf(CodeBadRequest, "plug-in %s is already installed from %s: remove %s first", p.Name, target, dst)
	} else if _, err := os.Stat(dst); err == nil {
		return nil, errorf(CodeBadRequest, "%s already exists: remove it first", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a user folder
		return nil, wrap(err)
	}
	if err := os.Symlink(abs, dst); err != nil {
		return nil, wrap(err)
	}
	return p, nil
}

// ensurePlugin (re)creates a plug-in's views in a catalog, and records its span types there.
func (d *db) ensurePlugin(ctx context.Context, p *Plugin) error {
	return d.write(ctx, func(tx *sql.Tx) error {
		for _, t := range p.SpanTypes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO span_types (type, plugin, description, data_schema)
				VALUES (?, ?, ?, ?) ON CONFLICT (type) DO UPDATE SET plugin = excluded.plugin,
				description = excluded.description, data_schema = excluded.data_schema`,
				t.Type, p.Name, t.Description, mustJSON(t.DataSchema)); err != nil {
				return err
			}
		}
		for _, v := range p.Views {
			if _, err := tx.ExecContext(ctx, `DROP VIEW IF EXISTS `+v.Name); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `CREATE VIEW `+v.Name+` AS `+strings.TrimRight(strings.TrimSpace(v.SQL), ";")); err != nil {
				return fmt.Errorf("plug-in %s, view %s: %w", p.Name, v.Name, err)
			}
		}
		return nil
	})
}
