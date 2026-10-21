package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type pluginStorageTransfer struct {
	targetID string
	transfer pluginsettings.StateTransfer
}

// Resolve declarations without reading or mutating storage: candidate preparation
// must not fence the currently published owner before its policy is committed.
func resolveStorageTransfers(cfg config.Config, discovered, active []pluginpkg.Plugin, binding userspace.Binding) ([]pluginStorageTransfer, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if !binding.IsUser() {
		return nil, nil
	}
	byID := make(map[string]pluginpkg.Plugin, len(discovered))
	for _, item := range discovered {
		if _, ok := byID[item.ID]; ok {
			return nil, fmt.Errorf("duplicate storage transfer package %q", item.ID)
		}
		byID[item.ID] = item
	}
	trusted := func(item pluginpkg.Plugin) bool {
		if item.SubjectID == "" || item.Fingerprint == "" {
			return false
		}
		if item.Official || item.AuthorizedDev {
			return true
		}
		return cfg.Extensions != nil && pluginManagementTrusted(*cfg.Extensions, item)
	}
	var result []pluginStorageTransfer
	for _, target := range active {
		if target.StorageTransfers == nil {
			continue
		}
		for _, declaration := range target.StorageTransfers.Imports {
			if declaration.Domain != binding.Domain() {
				continue
			}
			if declaration.Scope != "user" {
				return nil, fmt.Errorf("storage transfer %q requires user scope", declaration.ID)
			}
			source, exists := byID[declaration.Plugin]
			// An absent exporter is a fresh installation, not authority to search old
			// identities or other product/user roots for orphaned state.
			if !exists {
				continue
			}
			if !trusted(source) || !trusted(target) {
				return nil, fmt.Errorf("storage transfer %q requires independently trusted packages", declaration.ID)
			}
			if source.SubjectID == target.SubjectID {
				return nil, fmt.Errorf("storage transfer %q has identical owners", declaration.ID)
			}
			matching := false
			if source.StorageTransfers != nil {
				for _, exported := range source.StorageTransfers.Exports {
					if exported.ID == declaration.ID && exported.Plugin == target.ID && exported.Scope == declaration.Scope && exported.Domain == declaration.Domain && exported.KeyPrefix == declaration.KeyPrefix {
						matching = true
						break
					}
				}
			}
			if !matching {
				return nil, fmt.Errorf("storage transfer %q lacks reciprocal source declaration", declaration.ID)
			}
			result = append(result, pluginStorageTransfer{targetID: target.ID, transfer: pluginsettings.StateTransfer{ID: declaration.ID, SourceSubjectID: source.SubjectID, TargetSubjectID: target.SubjectID, KeyPrefix: declaration.KeyPrefix, Scope: pluginsettings.ScopeUser}})
		}
	}
	return result, nil
}

func executeStorageTransfers(home, root string, binding userspace.Binding, transfers []pluginStorageTransfer, host *pluginhost.Host) error {
	dataRoot := binding.State(home)
	var result error
	for _, transfer := range transfers {
		if err := pluginsettings.MoveStatePrefix(dataRoot, root, transfer.transfer); err != nil {
			failure := fmt.Errorf("plugin %q storage transfer %q: %w", transfer.targetID, transfer.transfer.ID, err)
			result = errors.Join(result, failure)
			if host != nil {
				outcome, _ := host.RetirePlugin(context.Background(), transfer.targetID, failure)
				result = errors.Join(result, outcome.Err)
				host.Add(pluginhost.Failed(transfer.targetID, failure))
			}
		}
	}
	if host != nil {
		host.BlockFailedPackageDependencies(context.Background())
	}
	return result
}

func availableStorageTransfers(transfers []pluginStorageTransfer, host *pluginhost.Host) []pluginStorageTransfer {
	ready := make(map[string]bool)
	blocked := host.PackageDependencyFailures()
	for _, status := range host.Statuses() {
		ready[status.ID] = blocked[status.ID] == nil && (status.State == pluginhost.StatePrepared || status.State == pluginhost.StateActive)
	}
	result := transfers[:0]
	for _, transfer := range transfers {
		if ready[transfer.targetID] {
			result = append(result, transfer)
		}
	}
	return result
}
