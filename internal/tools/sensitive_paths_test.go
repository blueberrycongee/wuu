package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// runtimeHome returns the canonical agent home directory for tests, or skips
// the test if the host has no resolvable home (e.g. some CI environments).
func runtimeHome(t *testing.T) string {
	t.Helper()
	home, err := statepath.Home("")
	if err != nil {
		t.Skipf("statepath.Home unavailable: %v", err)
	}
	return filepath.ToSlash(filepath.Clean(home))
}

func TestIsNamedAgentIdentityNotebookPath(t *testing.T) {
	runtimeDir := runtimeHome(t)

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"identity notebook file", runtimeDir + "/channels/agents/agent-1/memory/test.md", true},
		{"identity notebook root", runtimeDir + "/channels/agents/agent-1/memory", true},
		{"user memory", runtimeDir + "/memory/test.md", false},
		{"legacy participant memory", runtimeDir + "/participants/agent-1/memory/test.md", false},
		{"channel database", runtimeDir + "/channels/channels.db", false},
		{"workspace root", "/Users/somebody/work/foo", false},
		{"unrelated dot wuu sibling", "/Users/somebody/.wuuish/foo", false},
		{"partial suffix only", runtimeDir + "ish/foo", false},
		{"empty path", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNamedAgentIdentityNotebookPath(tc.path); got != tc.want {
				t.Fatalf("isNamedAgentIdentityNotebookPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestRejectSensitiveToolPath_AllowsNamedAgentIdentityNotebook(t *testing.T) {
	target := runtimeHome(t) + "/channels/agents/agent-1/memory/test.md"

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.AllowMutations = true

	if err := rejectSensitiveToolPath(kit.env, "write_file", "write", target); err != nil {
		t.Fatalf("named-agent identity notebook should be allowed when mutations are enabled: %v", err)
	}
}

func TestRejectSensitiveToolPath_BlocksUserMemory(t *testing.T) {
	target := runtimeHome(t) + "/memory/test.md"

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.AllowMutations = true

	err = rejectSensitiveToolPath(kit.env, "write_file", "write", target)
	if err == nil {
		t.Fatal("ordinary core file tools must not bypass the Memory plugin")
	}
	if !strings.Contains(err.Error(), "sensitive path") {
		t.Fatalf("expected sensitive-path error, got: %v", err)
	}
}

func TestRejectSensitiveToolPath_NonAgentSensitivePathStillBlocked(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.env.AllowMutations = true

	// A filename containing both "private" and "key" hits the credential
	// gate. The agent-metadata exemption must not let unrelated sensitive
	// paths through just because AllowMutations is on.
	target := filepath.Join(t.TempDir(), "private_key.pem")
	err = rejectSensitiveToolPath(kit.env, "write_file", "write", target)
	if err == nil {
		t.Fatalf("non-agent-runtime sensitive path should remain blocked even with AllowMutations=true")
	}
}

func TestSetBoundaryPropagatesAllowMutations(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	kit.SetBoundary(StandardBoundary())
	if !kit.env.AllowMutations {
		t.Fatalf("StandardBoundary should propagate AllowMutations=true; env=%+v", kit.env)
	}

	kit.SetBoundary(ReadOnlyBoundary())
	if kit.env.AllowMutations {
		t.Fatalf("ReadOnlyBoundary should propagate AllowMutations=false; env=%+v", kit.env)
	}

	kit.SetBoundary(UnconfinedBoundary())
	if !kit.env.AllowMutations {
		t.Fatalf("UnconfinedBoundary should propagate AllowMutations=true; env=%+v", kit.env)
	}
}

func TestResolvePath_BlocksWuuHomeOutsideExplicitFileScope(t *testing.T) {
	target := runtimeHome(t) + "/memory/test.md"

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(StandardBoundary())

	if _, err := kit.env.ResolvePath(target); err == nil {
		t.Fatal("standard boundary must not expose WUU_HOME outside explicit file scope")
	}
}

func TestReadFile_BlocksUserMemoryInStandardMode(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", wuuHome)
	target := filepath.Join(wuuHome, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(target, []byte("- [Preference](preference.md) — durable preference\n"), 0o600); err != nil {
		t.Fatalf("write memory index: %v", err)
	}

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(StandardBoundary())

	result, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: fmt.Sprintf(`{"path":%q}`, target),
	})
	if err == nil {
		t.Fatal("read_file must not bypass the Memory plugin")
	}
	if strings.Contains(result, "durable preference") || strings.Contains(err.Error(), "durable preference") {
		t.Fatalf("read_file leaked user memory: result=%q err=%v", result, err)
	}
}

func TestReadFile_AllowsNamedAgentIdentityNotebookInExplicitScope(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", wuuHome)
	notebook := filepath.Join(wuuHome, "channels", "agents", "agent-1", "memory")
	target := filepath.Join(notebook, "MEMORY.md")
	if err := os.MkdirAll(notebook, 0o755); err != nil {
		t.Fatalf("mkdir identity notebook: %v", err)
	}
	if err := os.WriteFile(target, []byte("durable identity\n"), 0o600); err != nil {
		t.Fatalf("write identity notebook: %v", err)
	}

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(StandardBoundary())
	kit.SetFileScopeRoots([]string{kit.RootDir(), notebook})
	result, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: fmt.Sprintf(`{"path":%q}`, target),
	})
	if err != nil {
		t.Fatalf("read_file identity notebook: %v", err)
	}
	if !strings.Contains(result, "durable identity") {
		t.Fatalf("read_file result missing identity content: %s", result)
	}
}

func TestReadFile_BlocksRuntimeFilesOutsideExplicitScopeInStandardMode(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", wuuHome)
	target := filepath.Join(wuuHome, "auth.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir runtime home: %v", err)
	}
	if err := os.WriteFile(target, []byte(`{"token":"secret-value"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(StandardBoundary())

	result, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: fmt.Sprintf(`{"path":%q}`, target),
	})
	if err == nil {
		t.Fatal("read_file should reject non-memory agent runtime metadata")
	}
	if !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("expected workspace-boundary rejection, got: %v", err)
	}
	if strings.Contains(result, "secret-value") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("read_file leaked auth content: result=%q err=%v", result, err)
	}
}

func TestResolvePath_BlocksAgentRuntimeInReadOnly(t *testing.T) {
	target := runtimeHome(t) + "/memory/test.md"

	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(ReadOnlyBoundary())

	if _, err := kit.env.ResolvePath(target); err == nil {
		t.Fatalf("ReadOnlyBoundary should still block agent runtime metadata path resolution")
	}
}

// TestWuuCredentialFilesFloorAcrossModes pins the credential floor: the
// app's own credential files are never readable or writable through agent
// tools in any permission mode, including unconfined.
func TestWuuCredentialFilesFloorAcrossModes(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", wuuHome)
	target := filepath.Join(wuuHome, "auth.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir runtime home: %v", err)
	}
	if err := os.WriteFile(target, []byte(`{"token":"secret-value"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	boundaries := []struct {
		name     string
		boundary WorkspaceBoundary
	}{
		{"standard", StandardBoundary()},
		{"read_only", ReadOnlyBoundary()},
		{"unconfined", UnconfinedBoundary()},
	}
	for _, tc := range boundaries {
		t.Run(tc.name+"/read", func(t *testing.T) {
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			kit.SetBoundary(tc.boundary)
			result, err := kit.Execute(context.Background(), providers.ToolCall{
				Name:      "read_file",
				Arguments: fmt.Sprintf(`{"path":%q}`, target),
			})
			if err == nil {
				t.Fatalf("read_file should reject wuu credential file in %s mode", tc.name)
			}
			if strings.Contains(result, "secret-value") || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("read_file leaked credential content in %s mode: result=%q err=%v", tc.name, result, err)
			}
		})
		t.Run(tc.name+"/write", func(t *testing.T) {
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			kit.SetBoundary(tc.boundary)
			_, err = kit.Execute(context.Background(), providers.ToolCall{
				Name:      "write_file",
				Arguments: fmt.Sprintf(`{"path":%q,"content":"overwrite"}`, target),
			})
			if err == nil {
				t.Fatalf("write_file should reject wuu credential file in %s mode", tc.name)
			}
			// Standard mode refuses the out-of-workspace path first and
			// read-only mode refuses at the mutation gate. Unconfined mode
			// reaches the host credential floor directly.
			if tc.name == "unconfined" && !strings.Contains(err.Error(), "wuu credential file") {
				t.Fatalf("expected credential-floor rejection in %s mode, got: %v", tc.name, err)
			}
			data, readErr := os.ReadFile(target)
			if readErr != nil || !strings.Contains(string(data), "secret-value") {
				t.Fatalf("credential file should be untouched in %s mode: data=%q err=%v", tc.name, data, readErr)
			}
		})
	}
}

// TestUnconfinedSensitiveWriteBlockedAndReadRedacted pins the unconfined
// floor for ordinary sensitive paths: the path boundary is gone, writes to
// sensitive files stay blocked, and reads reach the model with credential
// values masked.
func TestUnconfinedSensitiveWriteBlockedAndReadRedacted(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("API_KEY=top-secret-value\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(UnconfinedBoundary())

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":".env","content":"API_KEY=overwritten\n"}`,
	})
	if err == nil {
		t.Fatal("write_file should refuse .env in unconfined mode")
	}
	if !strings.Contains(err.Error(), "sensitive path") {
		t.Fatalf("expected sensitive-path refusal in unconfined mode, got: %v", err)
	}
	if data, _ := os.ReadFile(envPath); !strings.Contains(string(data), "top-secret-value") {
		t.Fatalf(".env should be untouched, got %q", data)
	}

	result, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "read_file",
		Arguments: `{"path":".env"}`,
	})
	if err != nil {
		t.Fatalf("unconfined read of .env should be allowed: %v", err)
	}
	if strings.Contains(result, "top-secret-value") {
		t.Fatalf("unconfined read leaked credential value: %s", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("unconfined read of .env should mask credential values: %s", result)
	}
}

func TestSensitivePathReason_SourceFilesAreNotCredentialStores(t *testing.T) {
	allowed := []string{
		"internal/subscriptionquota/credentials.go",
		"internal/example/credentials.go",
		"src/auth/client_secret.ts",
		"pkg/secrets.py",
		"Credentials.GO",
	}
	for _, path := range allowed {
		if reason, ok := sensitivePathReason(path); ok {
			t.Fatalf("source file %q classified as sensitive (%s)", path, reason)
		}
	}

	blocked := []string{
		"credentials.json",
		"secrets.yaml",
		"config/client_secret.json",
		"aws/credentials",
		"secret",
		"credentials.go.json",
		"credentials.go/token.json",
		"secrets.ts/client.go",
		".env",
		"id_rsa",
	}
	for _, path := range blocked {
		if _, ok := sensitivePathReason(path); !ok {
			t.Fatalf("credential store %q should stay sensitive", path)
		}
	}
}

func TestUnconfined_AllowsCredentialSourceButBlocksCredentialStore(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.SetBoundary(UnconfinedBoundary())

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"internal/example/credentials.go","content":"package example\n"}`,
	})
	if err != nil {
		t.Fatalf("write_file should allow credential source: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(root, "internal", "example", "credentials.go"))
	if err != nil || string(written) != "package example\n" {
		t.Fatalf("credential source write = %q, err=%v", written, err)
	}

	for _, path := range []string{"secrets.yaml", "credentials.go/token.json", "secrets.ts/client.go"} {
		t.Run(path, func(t *testing.T) {
			_, err := kit.Execute(context.Background(), providers.ToolCall{
				Name:      "write_file",
				Arguments: `{"path":"` + path + `","content":"placeholder\n"}`,
			})
			if err == nil || !strings.Contains(err.Error(), "credential or secret path") {
				t.Fatalf("expected credential-store refusal, got: %v", err)
			}
			if strings.Contains(err.Error(), "explicit secret handling") || !strings.Contains(err.Error(), "including unconfined") {
				t.Fatalf("refusal should say the guard holds in unconfined and must not ask for secret handling: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, path)); !os.IsNotExist(statErr) {
				t.Fatalf("credential store should not be created, stat err=%v", statErr)
			}
		})
	}
}
