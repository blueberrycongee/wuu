package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type pluginManagementStatus struct {
	ID                   string `json:"id"`
	SubjectID            string `json:"subject_id"`
	Source               string `json:"source"`
	PublishedFingerprint string `json:"published_fingerprint"`
	CurrentFingerprint   string `json:"current_conversation_fingerprint,omitempty"`
	DesiredEnabled       bool   `json:"desired_enabled"`
	Trusted              bool   `json:"trusted"`
	AdoptionPending      bool   `json:"adoption_pending"`
	RuntimeState         string `json:"current_conversation_runtime_state"`
	Error                string `json:"error,omitempty"`
}
type pluginManagementResult struct {
	Action     string                   `json:"action"`
	Plugins    []pluginManagementStatus `json:"plugins"`
	Diagnostic string                   `json:"diagnostic,omitempty"`
	Adoption   string                   `json:"adoption"`
	Experiment json.RawMessage          `json:"experiment,omitempty"`
}
type pluginManagementCommand func(context.Context, string, string, []string) (string, error)

// pluginManager captures the conversation's pinned generation, not the host's
// latest published generation. The tool authorizes execution before calling it.
func (s *Session) pluginManager(generation *PluginGeneration, run pluginManagementCommand) tools.PluginManager {
	return func(ctx context.Context, req tools.PluginManagementRequest) (any, error) {
		switch req.Action {
		case "capture", "candidates", "eval", "select", "rollback":
			return s.pluginExperimentManager(ctx, generation, run, req)
		}
		result := pluginManagementResult{Action: req.Action, Plugins: []pluginManagementStatus{}, Adoption: "published changes are adopted at the next idle turn boundary; this turn keeps its existing tools"}
		configPath := filepath.Join(s.WuuHome, "config.json")
		settings, published, err := s.pluginManagementInventory(req.CWD)
		if err != nil {
			return nil, err
		}
		if req.Action != "status" {
			if s.SafeMode {
				return nil, fmt.Errorf("plugin mutation is unavailable in safe mode")
			}
			var args []string
			switch req.Action {
			case "apply":
				args = []string{"plugin", "dev", "--watch=false", req.Path}
			case "enable", "disable":
				if s.ConfigPath != "" && filepath.Clean(s.ConfigPath) != filepath.Clean(configPath) {
					return nil, fmt.Errorf("plugin policy changes require the canonical user config; this runtime uses a different config")
				}
				var selected *pluginpkg.Plugin
				for i := range published {
					if published[i].ID == req.ID {
						selected = &published[i]
						break
					}
				}
				if selected == nil {
					return nil, fmt.Errorf("plugin %q is not installed", req.ID)
				}
				if req.Action == "enable" && !pluginManagementTrusted(settings, *selected) {
					return nil, fmt.Errorf("plugin %q has no current trusted grant; enable does not approve new or changed source", req.ID)
				}
				args = []string{"plugin", req.Action, "--json", "--workdir", req.CWD, req.ID}
			default:
				return nil, fmt.Errorf("unsupported plugin action %q", req.Action)
			}
			output, err := run(ctx, s.WuuHome, req.CWD, args)
			if err != nil {
				return nil, fmt.Errorf("plugin %s failed: %w: %s", req.Action, err, output)
			}
			result.Diagnostic = output
			// Re-enter read-only status to observe what was actually persisted, while
			// preserving the pinned generation and never claiming activation.
			value, err := s.pluginManager(generation, run)(ctx, tools.PluginManagementRequest{Action: "status", ID: req.ID, CWD: req.CWD})
			if err != nil {
				return nil, fmt.Errorf("plugin %s completed but status could not be read: %w", req.Action, err)
			}
			status := value.(pluginManagementResult)
			result.Plugins = status.Plugins
			if req.Action == "apply" {
				filtered := result.Plugins[:0]
				for _, record := range result.Plugins {
					authorization, err := pluginpkg.ReadDevAuthorization(s.WuuHome, record.ID)
					if err == nil && authorization.Directory == req.Path {
						filtered = append(filtered, record)
					}
				}
				result.Plugins = filtered
			}
			return result, nil
		}
		for _, item := range published {
			if req.ID != "" && item.ID != req.ID {
				continue
			}
			record := pluginManagementStatus{ID: item.ID, SubjectID: item.SubjectID, Source: item.Source, PublishedFingerprint: item.Fingerprint, DesiredEnabled: settings.IsEnabled(item.SubjectID, item.EnabledByDefault()), Trusted: pluginManagementTrusted(settings, item), RuntimeState: "inactive"}
			revoked := false
			if generation != nil {
				for _, active := range generation.active {
					if active.SubjectID == item.SubjectID {
						record.CurrentFingerprint = active.Fingerprint
						record.RuntimeState = "active"
						generation.revokedMu.Lock()
						revoked = generation.revokedPlugins[item.ID]
						if revoked {
							record.RuntimeState = "stopped"
						}
						generation.revokedMu.Unlock()
						break
					}
				}
				if generation.host != nil {
					for _, status := range generation.host.Statuses() {
						if status.ID == item.ID {
							record.RuntimeState = string(status.State)
							record.Error = status.Error
						}
					}
				}
			}
			if s.SafeMode {
				record.RuntimeState = "inactive"
			}
			record.AdoptionPending = !s.SafeMode && record.DesiredEnabled && record.Trusted && (record.PublishedFingerprint != record.CurrentFingerprint || revoked || record.RuntimeState == "stopped")
			if !s.SafeMode && !record.DesiredEnabled && record.CurrentFingerprint != "" && record.RuntimeState != "stopped" {
				record.AdoptionPending = true
			}
			result.Plugins = append(result.Plugins, record)
		}
		return result, nil
	}
}

// Experiments use the same fixed CLI and host-code authorization as ordinary
// development. Capturing and evaluating never change the conversation's tools.
func (s *Session) pluginExperimentManager(ctx context.Context, generation *PluginGeneration, run pluginManagementCommand, req tools.PluginManagementRequest) (any, error) {
	if s.SafeMode && req.Action != "candidates" {
		return nil, fmt.Errorf("plugin mutation is unavailable in safe mode")
	}
	if req.ID != "" {
		if err := pluginpkg.ValidateInstallID(req.ID); err != nil {
			return nil, err
		}
	}
	args := []string{"plugin", "experiment"}
	switch req.Action {
	case "capture":
		args = append(args, "capture", "--json", "--workdir", req.CWD, "--", req.Path)
	case "candidates":
		args = append(args, "list", "--json", "--workdir", req.CWD)
		if req.ID != "" {
			args = append(args, "--", req.ID)
		}
	case "select", "rollback":
		if req.ExpectedCurrent == nil {
			return nil, fmt.Errorf("%s requires expected_current", req.Action)
		}
		args = append(args, req.Action, "--json", "--workdir", req.CWD, "--expect-current="+*req.ExpectedCurrent, "--", req.ID, req.CandidateID)
	case "eval":
		if req.Concurrency < 1 || req.Concurrency > 8 || req.TimeoutSeconds < 1 || req.TimeoutSeconds > 1800 {
			return nil, fmt.Errorf("invalid evaluation concurrency or timeout")
		}
		args = append(args, "eval", "--json", "--workspace", req.CWD, "--concurrency", strconv.Itoa(req.Concurrency), "--timeout", strconv.Itoa(req.TimeoutSeconds)+"s", "--", req.ID)
		args = append(args, req.CandidateIDs...)
		args = append(args, "--")
		args = append(args, req.Command...)
	default:
		return nil, fmt.Errorf("unsupported plugin experiment action %q", req.Action)
	}
	output, err := run(ctx, s.WuuHome, req.CWD, args)
	if err != nil {
		return nil, fmt.Errorf("plugin %s failed: %w: %s", req.Action, err, output)
	}
	if !json.Valid([]byte(output)) {
		return nil, fmt.Errorf("plugin %s completed but its JSON receipt was invalid; inspect candidates/status before retrying", req.Action)
	}
	result := pluginManagementResult{Action: req.Action, Plugins: []pluginManagementStatus{}, Experiment: json.RawMessage(output), Adoption: "published and current-conversation generations are unchanged"}
	if req.Action == "select" || req.Action == "rollback" {
		value, err := s.pluginManager(generation, run)(ctx, tools.PluginManagementRequest{Action: "status", ID: req.ID, CWD: req.CWD})
		if err != nil {
			return nil, fmt.Errorf("plugin %s completed but status could not be read: %w", req.Action, err)
		}
		status := value.(pluginManagementResult)
		result.Plugins = status.Plugins
		result.Adoption = status.Adoption + "; business state and external effects are unchanged"
	}
	return result, nil
}

func pluginManagementTrusted(settings extensions.Settings, item pluginpkg.Plugin) bool {
	if item.Official || item.AuthorizedDev {
		return true
	}
	if settings.IsRejected(item.SubjectID, item.Fingerprint) {
		return false
	}
	grant, ok := settings.FindGrant(item.SubjectID, item.Fingerprint)
	return ok && permissionSetContains(grant.Permissions, item.EffectivePermissions)
}

// The only subprocess is the current trusted Wuu executable, with host-owned
// argv and WUU_HOME. No shell, model-selected executable, or ambient cwd lookup.
func runPluginManagementCommand(ctx context.Context, home, cwd string, args []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	commandTimeout := 2 * time.Minute
	experiment := len(args) > 2 && args[0] == "plugin" && args[1] == "experiment"
	if experiment && args[2] == "eval" {
		commandTimeout = 16*time.Hour + time.Minute // 32 runs at the maximum 30-minute per-run deadline.
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	command := exec.Command(executable, args...)
	command.Dir = cwd
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "WUU_HOME=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "WUU_HOME="+home)
	output := &pluginManagementOutput{}
	stderr := output
	if experiment {
		output.limit = 512 * 1024
		stderr = &pluginManagementOutput{}
	}
	command.Stdout = output
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	handle, err := proc.StartCommand(command)
	if err != nil {
		return "", err
	}
	select {
	case <-handle.Done():
		err = handle.Wait()
	case <-ctx.Done():
		grace := proc.DefaultStopGracePeriod
		if experiment {
			// The evaluator first cancels its child host so owned plugin process
			// groups can retire before the supervisor itself is force-killed.
			grace = 12 * time.Second
		}
		err = errors.Join(ctx.Err(), handle.Stop(grace))
		err = fmt.Errorf("plugin command interrupted; inspect persisted status or experiment evidence before retrying: %w", err)
	}
	result := strings.TrimSpace(output.String())
	if experiment && err != nil && stderr.String() != "" {
		result += "\n" + strings.TrimSpace(stderr.String())
	}
	return result, err
}

type pluginManagementOutput struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *pluginManagementOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	limit := b.limit
	if limit == 0 {
		limit = 32 * 1024
	}
	remaining := limit - len(b.data)
	if len(data) > remaining {
		data = data[:remaining]
	}
	b.data = append(b.data, data...)
	return n, nil
}

// Hold only the short catalog snapshot lease. Never keep it across CLI mutation
// or plugin lifecycle code, which may itself need the catalog writer.
func (s *Session) pluginManagementInventory(cwd string) (extensions.Settings, []pluginpkg.Plugin, error) {
	settings := extensions.Settings{}
	lease, acquired, err := session.TryAcquirePluginCatalogReadLease(s.WuuHome)
	if err != nil {
		return settings, nil, err
	}
	if !acquired {
		return settings, nil, fmt.Errorf("plugin catalog is changing; retry status after publication completes")
	}
	defer lease.Release()
	policyPath := s.ConfigPath
	if policyPath == "" {
		policyPath = filepath.Join(s.WuuHome, "config.json")
	}
	data, err := os.ReadFile(policyPath)
	if err == nil {
		var cfg struct {
			Extensions extensions.Settings `json:"extensions"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return settings, nil, fmt.Errorf("read plugin policy: %w", err)
		}
		settings = cfg.Extensions
	} else if !os.IsNotExist(err) {
		return settings, nil, fmt.Errorf("read plugin policy: %w", err)
	}
	return settings, pluginpkg.Discover(cwd, s.WuuHome), nil
}

func (b *pluginManagementOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
