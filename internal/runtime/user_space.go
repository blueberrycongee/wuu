package runtime

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"github.com/blueberrycongee/wuu/internal/workspaces"
	"path/filepath"
)

func (s *Session) UserBinding() userspace.Binding {
	return userspace.Binding{UserSpaceID: s.UserSpaceID, ProductDomain: s.ProductDomain}
}

// This one-way tool restriction is defense in depth, not proof of two-way host
// isolation. Legacy Coding tools keep their existing permissions. User hosts
// require a separate trusted isolation verifier before production activation.
func memoryProtectedPaths(home string, binding userspace.Binding) []string {
	if !binding.IsUser() {
		return nil
	}
	return []string{binding.State(home), filepath.Join(home, "memory"), filepath.Join(home, "working-notes"), filepath.Join(home, "sessions"), filepath.Join(home, "workspaces"), filepath.Join(home, "log"), filepath.Join(home, "plugin-storage"), filepath.Join(home, "agents"), filepath.Join(home, "scratch")}
}

func runtimeFileScopeRoots(root, home string, binding userspace.Binding, extras ...string) []string {
	if binding.IsUser() {
		return []string{root}
	}
	return workspaces.BoundaryRoots(root, home, extras...)
}

// UserHostIsolationVerifier is supplied only by a trusted embedding host. It
// must verify that other product hosts cannot read this user's data, including
// through unrestricted subprocesses, browser bridges, extensions and old
// sessions. A configuration flag or caller assertion is not sufficient.
// The desktop/CLI ship without a verifier and therefore cannot activate a
// production user host yet. Synthetic tests may inject an explicit fake.
const UserHostIsolationRequiredCode = "user_host_isolation_required"

type UserHostIsolationVerifier interface {
	VerifyUserHostIsolation(context.Context, userspace.Binding, string) error
}

func VerifyUserHostIsolation(ctx context.Context, binding userspace.Binding, home string, verifier UserHostIsolationVerifier) error {
	if !binding.IsUser() {
		return nil
	}
	if verifier == nil {
		return fmt.Errorf(UserHostIsolationRequiredCode + ": Personal user host requires verified host isolation; unrestricted Coding processes sharing this OS identity can read Personal data. Configure a verified separate host identity before activation")
	}
	if err := verifier.VerifyUserHostIsolation(ctx, binding, home); err != nil {
		return fmt.Errorf(UserHostIsolationRequiredCode+": verified host isolation unavailable: %w", err)
	}
	return nil
}

func userHostConfig(cfg config.Config, binding userspace.Binding) config.Config {
	if !binding.IsUser() {
		return cfg
	}
	cfg.Hooks = nil
	cfg.MCPServers = nil
	cfg.MCPJson = nil
	cfg.ExecutionEnvironments = executionenv.Config{}
	return cfg
}

// ProductDataRoot owns histories, summaries, records, artifacts and caches.
// WuuHome remains the shared settings/plugin installation root only.
func (s *Session) ProductDataRoot() string {
	if s.DataRoot != "" {
		return s.DataRoot
	}
	return s.WuuHome
}

func userHostWorkspaceStateDir(home, root string, binding userspace.Binding) (string, error) {
	return resolveWorkspaceStateDir(binding.State(home), "", root)
}

func runtimeBasePrompt(binding userspace.Binding) string {
	if !binding.IsUser() {
		return config.DefaultSystemPrompt()
	}
	return "You are Wuu, an assistant helping the user. Follow the active product's instructions and the user's requests. Use only authorized tools, preserve source provenance, and distinguish verified outcomes from assumptions. Do not infer a project or a coding role from the host environment."
}
