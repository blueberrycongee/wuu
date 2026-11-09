package claudeengine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

// ModelInfo preserves the CLI's selection value separately from its resolved
// model. Aliases and context suffixes must survive a round trip through --model.
type ModelInfo struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	SupportsEffort        *bool    `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
	SupportsFastMode      bool     `json:"supportsFastMode"`
}

// ModelCatalog caches successful CLI discovery in memory. A failed refresh may
// return the last successful list and an error, but never across login, binary,
// workspace, or configuration changes. The zero value is ready to use.
type ModelCatalog struct {
	once      sync.Once
	gate      chan struct{}
	key       [32]byte
	models    []ModelInfo
	completed time.Time
	err       error
}

// List coalesces concurrent reads, including forced refreshes. The caller owns
// the discovery deadline; cancellation also closes the temporary CLI process.
func (c *ModelCatalog) List(ctx context.Context, binary, root string, force bool) ([]ModelInfo, error) {
	requested := time.Now()
	c.once.Do(func() { c.gate = make(chan struct{}, 1) })
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	key, err := modelContext(ctx, binary, root)
	if err != nil {
		// Without an identity check, an old account's list is not a safe fallback.
		c.models, c.completed = nil, time.Time{}
		return nil, err
	}
	if key != c.key {
		c.key, c.models, c.completed, c.err = key, nil, time.Time{}, nil
	}
	freshFor := 2 * time.Minute
	if c.err != nil {
		freshFor = 5 * time.Second
	}
	if !c.completed.IsZero() && (c.completed.After(requested) || (!force && time.Since(c.completed) < freshFor)) {
		return cloneModels(c.models), c.err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	models, discoverErr := DiscoverModels(probeCtx, binary, root)
	cancel()
	current, err := modelContext(ctx, binary, root)
	if err != nil || current != key {
		c.models, c.completed = nil, time.Time{}
		if err != nil {
			return nil, err
		}
		return nil, errors.New("Claude login or configuration changed during model discovery; refresh to retry")
	}
	c.completed, c.err = time.Now(), discoverErr
	if discoverErr == nil || errors.Is(discoverErr, enginecatalog.ErrCatalogUnsupported) {
		c.models = models
	}
	return cloneModels(c.models), c.err
}

func cloneModels(models []ModelInfo) []ModelInfo {
	out := slices.Clone(models)
	for i := range out {
		out[i].SupportedEffortLevels = slices.Clone(out[i].SupportedEffortLevels)
		if out[i].SupportsEffort != nil {
			value := *out[i].SupportsEffort
			out[i].SupportsEffort = &value
		}
	}
	return out
}

// modelContext uses the CLI's public login status, not its credential store.
// Only a digest is retained; account details and environment values are never
// returned to clients or written to logs.
func modelContext(ctx context.Context, binary, root string) ([32]byte, error) {
	var zero [32]byte
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return zero, err
	}
	authCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(authCtx, binary, "auth", "status", "--json")
	cmd.Dir = root
	cmd.WaitDelay = time.Second
	output, runErr := cmd.Output()
	if authCtx.Err() != nil {
		return zero, fmt.Errorf("Claude login status: %w", authCtx.Err())
	}
	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		if runErr != nil {
			return zero, fmt.Errorf("Claude login status: %w", runErr)
		}
		return zero, errors.New("Claude returned invalid login status")
	}
	if _, ok := status["loggedIn"].(bool); !ok {
		return zero, errors.New("Claude login status is missing loggedIn")
	}
	// A logged-out CLI may return exit status 1 with valid status JSON.
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(status)
	_ = encoder.Encode([]string{binary, root})
	env := os.Environ()
	slices.Sort(env)
	_ = encoder.Encode(env)
	home, err := os.UserHomeDir()
	if err != nil {
		return zero, err
	}
	configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if configDir == "" {
		configDir = filepath.Join(home, ".claude")
	} else if !filepath.IsAbs(configDir) {
		configDir = filepath.Join(root, configDir)
	}
	paths := []string{binary, filepath.Join(configDir, "settings.json"), filepath.Join(configDir, "managed-settings.json")}
	for dir := root; ; dir = filepath.Dir(dir) {
		paths = append(paths, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// Machine policy and drop-ins can change model routing independently of the
	// workspace. Metadata is enough to invalidate; do not read their contents.
	policyDir := "/etc/claude-code"
	switch runtime.GOOS {
	case "darwin":
		policyDir = "/Library/Application Support/ClaudeCode"
	case "windows":
		policyDir = filepath.Join(os.Getenv("ProgramFiles"), "ClaudeCode")
	}
	paths = append(paths, filepath.Join(policyDir, "managed-settings.json"))
	files, err := filepath.Glob(filepath.Join(policyDir, "managed-settings.d", "*.json"))
	if err != nil {
		return zero, err
	}
	paths = append(paths, files...)
	for _, path := range paths {
		_ = encoder.Encode(path)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			_ = encoder.Encode(nil)
			continue
		}
		if err != nil {
			return zero, fmt.Errorf("Claude model configuration: %w", err)
		}
		_ = encoder.Encode([]int64{info.Size(), info.ModTime().UnixNano(), int64(info.Mode())})
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key, nil
}

// DiscoverModels reads the initialize control response without sending a user
// message or making a model turn. It uses the same executable, environment and
// workspace as the engine. Hooks and MCP startup are unnecessary for discovery.
func DiscoverModels(ctx context.Context, binary, root string) ([]ModelInfo, error) {
	raw, err := queryNativeControl(ctx, binary, root, "initialize")
	if err != nil {
		return nil, err
	}
	var response struct {
		Models []ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Models == nil {
		return nil, enginecatalog.ErrCatalogUnsupported
	}
	models := make([]ModelInfo, 0, len(response.Models))
	seen := make(map[string]bool)
	for _, model := range response.Models {
		model.Value = strings.TrimSpace(model.Value)
		if model.Value == "" || seen[model.Value] {
			continue
		}
		seen[model.Value] = true
		if model.SupportsEffort != nil && !*model.SupportsEffort {
			model.SupportedEffortLevels = nil
		}
		models = append(models, model)
	}
	return models, nil
}

// queryNativeControl never prompts or persists a session. get_settings exposes
// the CLI's applied defaults, including native settings/environment precedence.
func queryNativeControl(ctx context.Context, binary, root, method string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transport, err := NewTransport(TransportOptions{
		BinaryPath: binary, CWD: root,
		Args: []string{
			"--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
			"--settings", `{"disableAllHooks":true}`,
		},
	})
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	const requestID = "wuu-model-discovery"
	type outcome struct {
		response json.RawMessage
		err      error
	}
	done := make(chan outcome, 1)
	var once sync.Once
	finish := func(out outcome) { once.Do(func() { done <- out }) }
	transport.OnLine(func(line string) {
		var frame struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string          `json:"request_id"`
				Subtype   string          `json:"subtype"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(line), &frame) != nil || frame.Type != "control_response" || frame.Response.RequestID != requestID {
			return
		}
		if frame.Response.Subtype != "success" {
			finish(outcome{err: fmt.Errorf("Claude rejected %s", method)})
			return
		}
		finish(outcome{response: frame.Response.Response})
	})
	transport.OnClose(func(reason string) { finish(outcome{err: fmt.Errorf("Claude model discovery: %s", reason)}) })
	request := map[string]any{
		"type": "control_request", "request_id": requestID,
		"request": map[string]string{"subtype": method},
	}
	if err := transport.WriteLine(ctx, marshalLine(request)); err != nil {
		return nil, fmt.Errorf("Claude %s: %w", method, err)
	}
	select {
	case out := <-done:
		return out.response, out.err
	case <-ctx.Done():
		return nil, fmt.Errorf("Claude %s: %w", method, ctx.Err())
	}
}
