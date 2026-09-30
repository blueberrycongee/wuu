package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Claude Code project-level MCP compatibility (`.mcp.json`).
//
// Claude Code repositories commonly ship a `<repoRoot>/.mcp.json`:
//
//	{"mcpServers": {"<name>": {"command": "...", "args": [...], "env": {...}}}}
//
// with remote servers expressed as {"type": "sse"|"http", "url": "...",
// "headers": {...}}. Wuu reads this file (after LoadFrom finishes layering) and
// translates approved entries into cfg.MCPServers so the existing MCP startup
// path connects to them.
//
// The file is authored by Claude Code, so parsing is deliberately loose:
// unknown fields are ignored (the opposite of wuu's own DisallowUnknownFields
// config), and only the fields wuu understands are mapped. Entries wuu cannot
// translate (unknown transports, missing command/url, malformed JSON) are
// skipped with a one-line stderr warning.
//
// Trust gate (the core of this feature): `.mcp.json` servers are NOT enabled by
// default. A server only reaches cfg.MCPServers when the user approves it via
// the `mcp_json` config section (Config.MCPJson) — recommended in the machine-
// local `.wuu/settings.local.json` layer. The three knobs mirror Claude Code's
// enableAllProjectMcpServers / enabledMcpjsonServers / disabledMcpjsonServers.

const projectMCPJsonFile = ".mcp.json"

// MCPJsonTrust gates which Claude Code `.mcp.json` servers are approved to load.
// It aligns with Claude Code's approval fields (stored there in
// settings.local.json): enable_all == enableAllProjectMcpServers,
// enabled == enabledMcpjsonServers, disabled == disabledMcpjsonServers.
//
// Resolution precedence per server name: disabled wins over everything, then
// enable_all, then enabled; a name in none of them stays pending (not loaded).
// Put this section in `.wuu/settings.local.json` (personal, gitignored) to
// approve servers only on your own machine, or in any config layer to approve
// them more broadly.
type MCPJsonTrust struct {
	// EnableAll approves every server in `.mcp.json` (except names listed in
	// Disabled). Mirrors Claude Code's enableAllProjectMcpServers.
	EnableAll bool `json:"enable_all,omitempty"`
	// Enabled approves the named servers. Mirrors enabledMcpjsonServers.
	Enabled []string `json:"enabled,omitempty"`
	// Disabled rejects the named servers. Takes precedence over Enabled and
	// EnableAll. Mirrors disabledMcpjsonServers.
	Disabled []string `json:"disabled,omitempty"`
}

type mcpJsonStatus int

const (
	mcpJsonStatusPending mcpJsonStatus = iota
	mcpJsonStatusApproved
	mcpJsonStatusRejected
)

// status resolves the trust decision for one server name. It mirrors Claude
// Code's getProjectMcpServerStatus ordering: disabled is checked first (and
// wins), then enable_all, then the enabled allowlist.
func (t MCPJsonTrust) status(name string) mcpJsonStatus {
	name = strings.TrimSpace(name)
	for _, d := range t.Disabled {
		if strings.TrimSpace(d) == name {
			return mcpJsonStatusRejected
		}
	}
	if t.EnableAll {
		return mcpJsonStatusApproved
	}
	for _, e := range t.Enabled {
		if strings.TrimSpace(e) == name {
			return mcpJsonStatusApproved
		}
	}
	return mcpJsonStatusPending
}

// applyMCPJsonServers reads <projectRoot>/.mcp.json (if present), translates the
// approved servers, and merges them into cfg.MCPServers. It is a no-op when the
// file is absent, so a project without `.mcp.json` behaves exactly as before.
//
// projectRoot is LoadFrom's workdir — the same directory used for the project
// config and the settings layers.
func applyMCPJsonServers(cfg *Config, projectRoot string) {
	if cfg == nil {
		return
	}
	path := filepath.Join(projectRoot, projectMCPJsonFile)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return // missing (or a directory) — zero behavior change
	}
	data, err := os.ReadFile(path)
	if err != nil {
		warnMCPJsonOnce("read|"+path, fmt.Sprintf("wuu: cannot read %s: %v", path, err))
		return
	}

	var trust MCPJsonTrust
	if cfg.MCPJson != nil {
		trust = *cfg.MCPJson
	}

	servers, diags := translateMCPJson(data, cfg.MCPServers, trust, os.LookupEnv)
	for name, sc := range servers {
		if cfg.MCPServers == nil {
			cfg.MCPServers = make(map[string]MCPServerConfig)
		}
		cfg.MCPServers[name] = sc
	}
	emitMCPJsonDiags(path, diags)
}

type mcpJsonDiagKind int

const (
	mcpJsonDiagPending mcpJsonDiagKind = iota
	mcpJsonDiagConflict
	mcpJsonDiagUnsupported
	mcpJsonDiagInvalidServer
	mcpJsonDiagMissingEnv
	mcpJsonDiagInvalidJSON
)

// mcpJsonDiag is one advisory about a `.mcp.json` entry. Diagnostics are pure
// data so translateMCPJson stays testable without touching stderr or globals;
// emitMCPJsonDiags renders and de-duplicates them.
type mcpJsonDiag struct {
	Kind    mcpJsonDiagKind
	Name    string
	Message string
}

// translateMCPJson parses `.mcp.json` bytes loosely and returns the approved,
// translatable servers plus diagnostics for everything skipped. native is the
// current cfg.MCPServers map (pre-merge) so name conflicts can be detected;
// native entries that define a server always win and are never overwritten. lookup resolves
// environment variables for ${VAR} expansion (os.LookupEnv in production).
func translateMCPJson(data []byte, native map[string]MCPServerConfig, trust MCPJsonTrust, lookup func(string) (string, bool)) (map[string]MCPServerConfig, []mcpJsonDiag) {
	// Loose parse: only mcpServers is read; unknown top-level keys are ignored.
	var file struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, []mcpJsonDiag{{Kind: mcpJsonDiagInvalidJSON, Message: fmt.Sprintf("not valid JSON: %v", err)}}
	}
	if len(file.MCPServers) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(file.MCPServers))
	for name := range file.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic diagnostic order

	out := make(map[string]MCPServerConfig)
	var diags []mcpJsonDiag
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue // malformed empty name — ignore silently
		}
		// A native entry that defines a server wins on name conflict, regardless
		// of approval. One that only carries startup preferences (such as the
		// settings on/off switch) does not: it applies on top of the project
		// definition instead of hiding it.
		preference, exists := native[name]
		if exists && preference.definesServer() {
			diags = append(diags, mcpJsonDiag{
				Kind:    mcpJsonDiagConflict,
				Name:    name,
				Message: fmt.Sprintf("mcp server %q from .mcp.json ignored; a native mcp_servers entry with the same name takes precedence", name),
			})
			continue
		}
		// Trust gate.
		switch trust.status(name) {
		case mcpJsonStatusRejected:
			continue // explicitly rejected — stay silent
		case mcpJsonStatusPending:
			diags = append(diags, mcpJsonDiag{Kind: mcpJsonDiagPending, Name: name})
			continue
		}
		// Approved — translate the transport.
		sc, transDiags, ok := translateMCPJsonServer(name, file.MCPServers[rawName], lookup)
		diags = append(diags, transDiags...)
		if !ok {
			continue
		}
		if exists {
			sc.Enabled = preference.Enabled
			sc.ToolOverrides = preference.ToolOverrides
		}
		out[name] = sc
	}
	if len(out) == 0 {
		out = nil
	}
	return out, diags
}

// translateMCPJsonServer maps one Claude Code server entry to wuu's
// MCPServerConfig. Wuu's transport names deliberately match Claude Code's
// `type` values, so remote entries translate one-to-one:
//
//   - type "stdio" (or omitted) -> command/args/env
//   - type "sse"                -> url/headers, transport "sse" (legacy HTTP+SSE)
//   - type "http"               -> url/headers, transport "http" (streamable HTTP)
//   - anything else (ws, sdk, ...) -> skipped as unsupported
//
// The transport is set explicitly (rather than left to wuu's auto-detection)
// because `.mcp.json` already states which protocol the server speaks; an
// explicit transport connects directly and never silently falls back.
func translateMCPJsonServer(name string, raw json.RawMessage, lookup func(string) (string, bool)) (MCPServerConfig, []mcpJsonDiag, bool) {
	var s struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return MCPServerConfig{}, []mcpJsonDiag{{
			Kind:    mcpJsonDiagInvalidServer,
			Name:    name,
			Message: fmt.Sprintf("mcp server %q from .mcp.json skipped: malformed config: %v", name, err),
		}}, false
	}

	var missing []string
	expand := func(v string) string {
		exp, miss := expandMCPEnvVars(v, lookup)
		missing = append(missing, miss...)
		return exp
	}
	expandMap := func(in map[string]string) map[string]string {
		if len(in) == 0 {
			return nil
		}
		out := make(map[string]string, len(in))
		for k, v := range in {
			out[k] = expand(v)
		}
		return out
	}

	var sc MCPServerConfig
	typ := strings.ToLower(strings.TrimSpace(s.Type))
	switch typ {
	case "", "stdio":
		command := expand(s.Command)
		if strings.TrimSpace(command) == "" {
			return MCPServerConfig{}, []mcpJsonDiag{{
				Kind:    mcpJsonDiagInvalidServer,
				Name:    name,
				Message: fmt.Sprintf("mcp server %q from .mcp.json skipped: stdio transport requires a command", name),
			}}, false
		}
		var args []string
		for _, a := range s.Args {
			args = append(args, expand(a))
		}
		sc = MCPServerConfig{Command: command, Args: args, Env: expandMap(s.Env)}
	case "sse", "http":
		url := expand(s.URL)
		if strings.TrimSpace(url) == "" {
			return MCPServerConfig{}, []mcpJsonDiag{{
				Kind:    mcpJsonDiagInvalidServer,
				Name:    name,
				Message: fmt.Sprintf("mcp server %q from .mcp.json skipped: remote transport requires a url", name),
			}}, false
		}
		// typ is already one of wuu's transport names ("sse" = legacy
		// HTTP+SSE, "http" = streamable HTTP); pass it through explicitly.
		sc = MCPServerConfig{URL: url, Headers: expandMap(s.Headers), Transport: typ}
	default:
		return MCPServerConfig{}, []mcpJsonDiag{{
			Kind:    mcpJsonDiagUnsupported,
			Name:    name,
			Message: fmt.Sprintf("mcp server %q from .mcp.json skipped: unsupported transport %q", name, strings.TrimSpace(s.Type)),
		}}, false
	}

	// Missing env vars are a warning, not a skip — Claude Code leaves the
	// unexpanded ${VAR} literal in place and still loads the server.
	var diags []mcpJsonDiag
	if len(missing) > 0 {
		diags = append(diags, mcpJsonDiag{
			Kind:    mcpJsonDiagMissingEnv,
			Name:    name,
			Message: fmt.Sprintf("mcp server %q from .mcp.json references unset environment variables: %s (left unexpanded)", name, strings.Join(dedupeStrings(missing), ", ")),
		})
	}
	return sc, diags, true
}

// mcpEnvVarPattern matches ${...} references, mirroring Claude Code's
// /\$\{([^}]+)\}/g. Content runs up to the first '}', so nested braces are not
// supported (same as Claude Code).
var mcpEnvVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandMCPEnvVars expands ${VAR} and ${VAR:-default} references, replicating
// Claude Code's expandEnvVarsInString (services/mcp/envExpansion.ts). It returns
// the expanded string and the list of variable names that were referenced but
// unset (and had no default). Missing references are left as the literal ${VAR}
// text, exactly as Claude Code does.
func expandMCPEnvVars(value string, lookup func(string) (string, bool)) (string, []string) {
	var missing []string
	expanded := mcpEnvVarPattern.ReplaceAllStringFunc(value, func(match string) string {
		content := match[2 : len(match)-1] // strip "${" and "}"
		// Claude Code does `varContent.split(':-', 2)`: it keeps only the first
		// two elements, so a default value that itself contains ":-" is
		// truncated. Replicate that faithfully rather than using strings.SplitN
		// (which would retain the remainder in the second element).
		parts := strings.Split(content, ":-")
		varName := parts[0]
		hasDefault := len(parts) > 1
		if v, ok := lookup(varName); ok {
			return v
		}
		if hasDefault {
			return parts[1]
		}
		missing = append(missing, varName)
		return match
	})
	return expanded, missing
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// mcpJsonWarned de-duplicates diagnostics across the process lifetime. The
// app-server reloads config repeatedly; without this each reload would re-print
// the same lines. Keys include the file path so distinct projects don't
// suppress each other. Tests reset it via resetMCPJsonWarnings.
var mcpJsonWarned sync.Map

// mcpJsonWarnWriter is the sink for diagnostics. It defaults to stderr and is a
// test seam so warning output can be asserted without pipe plumbing.
var mcpJsonWarnWriter io.Writer = os.Stderr

func warnMCPJsonOnce(key, msg string) {
	if _, seen := mcpJsonWarned.LoadOrStore(key, struct{}{}); seen {
		return
	}
	fmt.Fprintln(mcpJsonWarnWriter, msg)
}

// resetMCPJsonWarnings clears the process-level dedupe set. Test-only.
func resetMCPJsonWarnings() {
	mcpJsonWarned.Range(func(k, _ any) bool {
		mcpJsonWarned.Delete(k)
		return true
	})
}

// emitMCPJsonDiags prints diagnostics to stderr, de-duplicated per process. The
// pending advisories (the actionable "not approved" case) are aggregated into a
// single line that names every never-before-warned server and points the user
// at mcp_json.enabled in .wuu/settings.local.json. Rejected servers produce no
// diagnostic at all — the user explicitly declined them.
func emitMCPJsonDiags(path string, diags []mcpJsonDiag) {
	if len(diags) == 0 {
		return
	}
	var pending []string
	for _, d := range diags {
		switch d.Kind {
		case mcpJsonDiagPending:
			key := "pending|" + path + "|" + d.Name
			if _, seen := mcpJsonWarned.LoadOrStore(key, struct{}{}); !seen {
				pending = append(pending, d.Name)
			}
		case mcpJsonDiagInvalidJSON:
			warnMCPJsonOnce("json|"+path, fmt.Sprintf("wuu: %s: %s", path, d.Message))
		default:
			warnMCPJsonOnce(fmt.Sprintf("%d|%s|%s", d.Kind, path, d.Name), "wuu: "+d.Message)
		}
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		quoted := make([]string, len(pending))
		for i, n := range pending {
			quoted[i] = strconv.Quote(n)
		}
		noun := "MCP server"
		if len(pending) > 1 {
			noun = "MCP servers"
		}
		fmt.Fprintf(mcpJsonWarnWriter,
			"wuu: .mcp.json %s not approved: %s; add the name(s) to mcp_json.enabled in .wuu/settings.local.json (or set mcp_json.enable_all: true) to load them\n",
			noun, strings.Join(quoted, ", "))
	}
}
