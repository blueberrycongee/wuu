package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// envLookup builds a lookup func from a map, matching os.LookupEnv's signature.
func envLookup(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func diagKinds(diags []mcpJsonDiag) []mcpJsonDiagKind {
	out := make([]mcpJsonDiagKind, len(diags))
	for i, d := range diags {
		out[i] = d.Kind
	}
	return out
}

func diagNames(diags []mcpJsonDiag, kind mcpJsonDiagKind) []string {
	var out []string
	for _, d := range diags {
		if d.Kind == kind {
			out = append(out, d.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestTranslateMCPJson_StdioApproved covers stdio field translation with env.
func TestTranslateMCPJson_StdioApproved(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "local": {
	      "command": "my-server",
	      "args": ["--flag", "value"],
	      "env": {"TOKEN": "abc"}
	    }
	  }
	}`)
	trust := MCPJsonTrust{Enabled: []string{"local"}}
	servers, diags := translateMCPJson(data, nil, trust, envLookup(nil))
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %+v", diags)
	}
	got, ok := servers["local"]
	if !ok {
		t.Fatalf("expected server 'local', got %+v", servers)
	}
	want := MCPServerConfig{
		Command: "my-server",
		Args:    []string{"--flag", "value"},
		Env:     map[string]string{"TOKEN": "abc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdio translation mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestTranslateMCPJson_SSEAndHTTP covers remote (url+headers) translation for
// both sse and http types. The Claude Code `type` value carries through as
// wuu's explicit transport: "sse" connects via legacy SSE, "http" via
// streamable HTTP, in both cases without transport auto-detection fallback.
func TestTranslateMCPJson_SSEAndHTTP(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "remote-sse": {"type": "sse", "url": "https://sse.example/mcp", "headers": {"Authorization": "Bearer x"}},
	    "remote-http": {"type": "http", "url": "https://http.example/mcp", "headers": {"X-Api-Key": "y"}}
	  }
	}`)
	trust := MCPJsonTrust{EnableAll: true}
	servers, diags := translateMCPJson(data, nil, trust, envLookup(nil))
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %+v", diags)
	}
	sse := servers["remote-sse"]
	if sse.URL != "https://sse.example/mcp" || sse.Headers["Authorization"] != "Bearer x" {
		t.Fatalf("sse translation mismatch: %+v", sse)
	}
	if sse.Transport != "sse" {
		t.Fatalf("type sse should map to transport \"sse\": %+v", sse)
	}
	if sse.Command != "" {
		t.Fatalf("sse server should have no command: %+v", sse)
	}
	httpSrv := servers["remote-http"]
	if httpSrv.URL != "https://http.example/mcp" || httpSrv.Headers["X-Api-Key"] != "y" {
		t.Fatalf("http translation mismatch: %+v", httpSrv)
	}
	if httpSrv.Transport != "http" {
		t.Fatalf("type http should map to transport \"http\" (streamable HTTP): %+v", httpSrv)
	}
}

// TestTranslateMCPJson_UnknownFieldsIgnored verifies loose parsing: unknown
// top-level keys and unknown per-server fields (headersHelper, oauth, extra)
// are ignored, not rejected.
func TestTranslateMCPJson_UnknownFieldsIgnored(t *testing.T) {
	data := []byte(`{
	  "someTopLevelExtension": {"whatever": true},
	  "mcpServers": {
	    "local": {
	      "command": "srv",
	      "unknownField": 123,
	      "oauth": {"clientId": "abc"},
	      "headersHelper": "echo hi"
	    }
	  }
	}`)
	trust := MCPJsonTrust{EnableAll: true}
	servers, diags := translateMCPJson(data, nil, trust, envLookup(nil))
	if len(diags) != 0 {
		t.Fatalf("unexpected diags for loose parse: %+v", diags)
	}
	if servers["local"].Command != "srv" {
		t.Fatalf("expected loose parse to keep command, got %+v", servers)
	}
}

// TestTranslateMCPJson_TrustGate covers pending/enabled/enable_all/disabled and
// their precedence (disabled wins over enabled and enable_all).
func TestTranslateMCPJson_TrustGate(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "a": {"command": "a"},
	    "b": {"command": "b"},
	    "c": {"command": "c"},
	    "d": {"command": "d"}
	  }
	}`)

	// No approval: everything pending, nothing loaded.
	servers, diags := translateMCPJson(data, nil, MCPJsonTrust{}, envLookup(nil))
	if len(servers) != 0 {
		t.Fatalf("expected no servers loaded by default, got %+v", servers)
	}
	if got := diagNames(diags, mcpJsonDiagPending); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("expected all pending, got %v", got)
	}

	// enabled allowlist loads only listed names.
	servers, _ = translateMCPJson(data, nil, MCPJsonTrust{Enabled: []string{"a", "c"}}, envLookup(nil))
	if _, ok := servers["a"]; !ok {
		t.Fatalf("expected 'a' loaded")
	}
	if _, ok := servers["c"]; !ok {
		t.Fatalf("expected 'c' loaded")
	}
	if _, ok := servers["b"]; ok {
		t.Fatalf("expected 'b' not loaded")
	}

	// enable_all loads everything except disabled; disabled wins.
	servers, diags = translateMCPJson(data, nil, MCPJsonTrust{EnableAll: true, Disabled: []string{"b"}}, envLookup(nil))
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers (all but disabled), got %+v", servers)
	}
	if _, ok := servers["b"]; ok {
		t.Fatalf("disabled 'b' must not load under enable_all")
	}
	// A rejected (disabled) server produces no diagnostic (silent).
	if names := diagNames(diags, mcpJsonDiagPending); len(names) != 0 {
		t.Fatalf("disabled server must be silent, got pending %v", names)
	}

	// disabled also wins over an explicit enabled entry.
	servers, _ = translateMCPJson(data, nil, MCPJsonTrust{Enabled: []string{"a"}, Disabled: []string{"a"}}, envLookup(nil))
	if _, ok := servers["a"]; ok {
		t.Fatalf("disabled must win over enabled")
	}
}

// TestTranslateMCPJson_NativeConflict verifies native mcp_servers wins on a name
// clash and the .mcp.json entry is skipped with a conflict diagnostic —
// regardless of approval state.
func TestTranslateMCPJson_NativeConflict(t *testing.T) {
	data := []byte(`{"mcpServers": {"shared": {"command": "from-mcp-json"}}}`)
	native := map[string]MCPServerConfig{"shared": {Command: "from-native"}}
	servers, diags := translateMCPJson(data, native, MCPJsonTrust{EnableAll: true}, envLookup(nil))
	if _, ok := servers["shared"]; ok {
		t.Fatalf(".mcp.json must not override native server")
	}
	if got := diagKinds(diags); len(got) != 1 || got[0] != mcpJsonDiagConflict {
		t.Fatalf("expected one conflict diag, got %+v", diags)
	}
}

// TestTranslateMCPJson_UnsupportedTransport verifies unknown transports (ws,
// sdk, etc.) are skipped with a diagnostic, even when approved.
func TestTranslateMCPJson_UnsupportedTransport(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "ws-server": {"type": "ws", "url": "wss://example/mcp"},
	    "sdk-server": {"type": "sdk", "name": "x"}
	  }
	}`)
	servers, diags := translateMCPJson(data, nil, MCPJsonTrust{EnableAll: true}, envLookup(nil))
	if len(servers) != 0 {
		t.Fatalf("expected no servers for unsupported transports, got %+v", servers)
	}
	if got := diagNames(diags, mcpJsonDiagUnsupported); !reflect.DeepEqual(got, []string{"sdk-server", "ws-server"}) {
		t.Fatalf("expected unsupported diags, got %v", got)
	}
}

// TestTranslateMCPJson_InvalidServers covers missing command/url and malformed
// entries.
func TestTranslateMCPJson_InvalidServers(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "no-command": {"type": "stdio"},
	    "no-url": {"type": "http"},
	    "bad": "not-an-object"
	  }
	}`)
	servers, diags := translateMCPJson(data, nil, MCPJsonTrust{EnableAll: true}, envLookup(nil))
	if len(servers) != 0 {
		t.Fatalf("expected no servers, got %+v", servers)
	}
	if got := diagNames(diags, mcpJsonDiagInvalidServer); !reflect.DeepEqual(got, []string{"bad", "no-command", "no-url"}) {
		t.Fatalf("expected invalid-server diags, got %v", got)
	}
}

// TestTranslateMCPJson_InvalidJSON verifies a malformed file yields one JSON
// diagnostic and no servers.
func TestTranslateMCPJson_InvalidJSON(t *testing.T) {
	servers, diags := translateMCPJson([]byte(`{not json`), nil, MCPJsonTrust{EnableAll: true}, envLookup(nil))
	if servers != nil {
		t.Fatalf("expected no servers for invalid JSON, got %+v", servers)
	}
	if got := diagKinds(diags); len(got) != 1 || got[0] != mcpJsonDiagInvalidJSON {
		t.Fatalf("expected one invalid-json diag, got %+v", diags)
	}
}

// TestExpandMCPEnvVars covers ${VAR}, ${VAR:-default}, missing vars, and the
// Claude Code ":-" truncation quirk.
func TestExpandMCPEnvVars(t *testing.T) {
	lookup := envLookup(map[string]string{"SET": "value", "EMPTY": ""})

	cases := []struct {
		in          string
		want        string
		wantMissing []string
	}{
		{"${SET}", "value", nil},
		{"prefix-${SET}-suffix", "prefix-value-suffix", nil},
		{"${EMPTY}", "", nil}, // set-but-empty resolves to empty, not missing
		{"${MISSING}", "${MISSING}", []string{"MISSING"}},
		{"${MISSING:-fallback}", "fallback", nil},
		{"${SET:-fallback}", "value", nil},
		{"${MISSING:-}", "", nil}, // empty default is a defined default
		// Claude Code splits on ":-" and keeps only the first two elements, so
		// a default containing ":-" is truncated at the second separator.
		{"${MISSING:-a:-b}", "a", nil},
		{"no vars here", "no vars here", nil},
	}
	for _, tc := range cases {
		got, missing := expandMCPEnvVars(tc.in, lookup)
		if got != tc.want {
			t.Errorf("expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !reflect.DeepEqual(missing, tc.wantMissing) {
			t.Errorf("expand(%q) missing = %v, want %v", tc.in, missing, tc.wantMissing)
		}
	}
}

// TestTranslateMCPJson_EnvExpansionInFields verifies expansion runs across
// command, args, env values, url, and header values, and reports missing vars.
func TestTranslateMCPJson_EnvExpansionInFields(t *testing.T) {
	data := []byte(`{
	  "mcpServers": {
	    "stdio": {"command": "${BIN}", "args": ["--token", "${TOKEN}"], "env": {"K": "${VAL}"}},
	    "remote": {"type": "sse", "url": "${BASE}/mcp", "headers": {"Authorization": "Bearer ${MISSING}"}}
	  }
	}`)
	lookup := envLookup(map[string]string{
		"BIN": "server-bin", "TOKEN": "t", "VAL": "v", "BASE": "https://host",
	})
	servers, diags := translateMCPJson(data, nil, MCPJsonTrust{EnableAll: true}, lookup)

	stdio := servers["stdio"]
	if stdio.Command != "server-bin" || stdio.Args[1] != "t" || stdio.Env["K"] != "v" {
		t.Fatalf("stdio expansion mismatch: %+v", stdio)
	}
	remote := servers["remote"]
	if remote.URL != "https://host/mcp" {
		t.Fatalf("url expansion mismatch: %+v", remote)
	}
	// The missing var leaves the literal in place but still loads the server.
	if remote.Headers["Authorization"] != "Bearer ${MISSING}" {
		t.Fatalf("expected missing var left literal, got %+v", remote.Headers)
	}
	if got := diagNames(diags, mcpJsonDiagMissingEnv); !reflect.DeepEqual(got, []string{"remote"}) {
		t.Fatalf("expected missing-env diag for 'remote', got %v", got)
	}
}

// --- Integration via LoadFrom ---

const mcpJsonBaseConfig = `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://base.example/v1",
      "api_key_env": "MAIN_KEY",
      "model": "base-model"
    }
  }
}`

func writeMCPJson(t *testing.T, workdir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workdir, ".mcp.json"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}
}

// TestLoadFrom_MCPJson_UnapprovedNotLoaded verifies default trust: .mcp.json
// servers are not loaded without approval.
func TestLoadFrom_MCPJson_UnapprovedNotLoaded(t *testing.T) {
	home := isolatedHome(t)
	workdir := t.TempDir()
	writeBaseConfig(t, home, mcpJsonBaseConfig)
	writeMCPJson(t, workdir, `{"mcpServers": {"local": {"command": "srv"}}}`)

	cfg, _, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if _, ok := cfg.MCPServers["local"]; ok {
		t.Fatalf("unapproved .mcp.json server must not load, got %+v", cfg.MCPServers)
	}
}

// TestLoadFrom_MCPJson_ApprovedViaLocalSettings verifies approval through the
// settings.local.json layer's mcp_json.enabled loads the server.
func TestLoadFrom_MCPJson_ApprovedViaLocalSettings(t *testing.T) {
	home := isolatedHome(t)
	workdir := t.TempDir()
	writeBaseConfig(t, home, mcpJsonBaseConfig)
	writeMCPJson(t, workdir, `{"mcpServers": {"local": {"command": "srv", "args": ["--x"]}}}`)
	writeProjectSettings(t, workdir, localSettingsFile, `{"mcp_json": {"enabled": ["local"]}}`)

	cfg, _, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	got, ok := cfg.MCPServers["local"]
	if !ok {
		t.Fatalf("approved server should load, got %+v", cfg.MCPServers)
	}
	if got.Command != "srv" || len(got.Args) != 1 || got.Args[0] != "--x" {
		t.Fatalf("translated server mismatch: %+v", got)
	}
}

// TestLoadFrom_MCPJson_EnableAllViaLocalSettings verifies enable_all approves
// all .mcp.json servers.
func TestLoadFrom_MCPJson_EnableAllViaLocalSettings(t *testing.T) {
	home := isolatedHome(t)
	workdir := t.TempDir()
	writeBaseConfig(t, home, mcpJsonBaseConfig)
	writeMCPJson(t, workdir, `{"mcpServers": {"a": {"command": "a"}, "b": {"command": "b"}}}`)
	writeProjectSettings(t, workdir, localSettingsFile, `{"mcp_json": {"enable_all": true}}`)

	cfg, _, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if _, ok := cfg.MCPServers["a"]; !ok {
		t.Fatalf("expected 'a' loaded under enable_all")
	}
	if _, ok := cfg.MCPServers["b"]; !ok {
		t.Fatalf("expected 'b' loaded under enable_all")
	}
}

// TestLoadFrom_MCPJson_NativeConflictWins verifies a native mcp_servers entry
// wins over a same-named .mcp.json entry even when the latter is approved.
func TestLoadFrom_MCPJson_NativeConflictWins(t *testing.T) {
	home := isolatedHome(t)
	workdir := t.TempDir()
	base := `{
  "default_provider": "main",
  "providers": {"main": {"type": "openai-compatible", "base_url": "https://b/v1", "api_key_env": "K", "model": "m"}},
  "mcp_servers": {"shared": {"command": "native-cmd"}}
}`
	writeBaseConfig(t, home, base)
	writeMCPJson(t, workdir, `{"mcpServers": {"shared": {"command": "mcpjson-cmd"}}}`)
	writeProjectSettings(t, workdir, localSettingsFile, `{"mcp_json": {"enable_all": true}}`)

	cfg, _, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.MCPServers["shared"].Command != "native-cmd" {
		t.Fatalf("native mcp_servers must win, got %+v", cfg.MCPServers["shared"])
	}
}

// TestEmitMCPJsonDiags_DedupAcrossReloads verifies warnings print once and are
// suppressed on subsequent reloads (anti-spam), and that the pending line names
// the server and points at settings.local.json.
func TestEmitMCPJsonDiags_DedupAcrossReloads(t *testing.T) {
	resetMCPJsonWarnings()
	var buf bytes.Buffer
	prev := mcpJsonWarnWriter
	mcpJsonWarnWriter = &buf
	t.Cleanup(func() {
		mcpJsonWarnWriter = prev
		resetMCPJsonWarnings()
	})

	diags := []mcpJsonDiag{
		{Kind: mcpJsonDiagPending, Name: "svc-a"},
		{Kind: mcpJsonDiagPending, Name: "svc-b"},
	}
	emitMCPJsonDiags("/proj/.mcp.json", diags)
	first := buf.String()
	if !strings.Contains(first, "svc-a") || !strings.Contains(first, "svc-b") {
		t.Fatalf("first emit should name both servers: %q", first)
	}
	if lines := strings.Count(strings.TrimSpace(first), "\n"); lines != 0 {
		t.Fatalf("pending servers should be aggregated into one line, got:\n%s", first)
	}

	// Second reload with the same diags: fully suppressed.
	buf.Reset()
	emitMCPJsonDiags("/proj/.mcp.json", diags)
	if buf.Len() != 0 {
		t.Fatalf("second emit should be suppressed, got %q", buf.String())
	}

	// A brand-new pending name still surfaces.
	buf.Reset()
	emitMCPJsonDiags("/proj/.mcp.json", []mcpJsonDiag{{Kind: mcpJsonDiagPending, Name: "svc-c"}})
	if !strings.Contains(buf.String(), "svc-c") {
		t.Fatalf("new pending name should surface, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "svc-a") {
		t.Fatalf("already-warned names should not repeat, got %q", buf.String())
	}
}
