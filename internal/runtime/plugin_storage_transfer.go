package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Resolve declarations before read-only provider validation during preparation.
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
			if source.StorageSubject() == target.StorageSubject() {
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
			result = append(result, pluginStorageTransfer{targetID: target.ID, transfer: pluginsettings.StateTransfer{ID: declaration.ID, SourceSubjectID: source.StorageSubject(), TargetSubjectID: target.StorageSubject(), KeyPrefix: declaration.KeyPrefix, Scope: pluginsettings.ScopeUser}})
		}
	}
	return result, nil
}

// Storage ownership is transferred by a graph consumer after the selected
// settings provider activates and before its target plugin can start. No
// preparation callback may fence the still-published writer.
type storageTransferConsumer struct {
	*runtimeServiceConsumer
	host     *pluginhost.Host
	transfer pluginStorageTransfer
	settings *pluginsettings.Client
}

func newStorageTransferConsumer(host *pluginhost.Host, registry *pluginhost.ServiceRegistry, transfer pluginStorageTransfer) *storageTransferConsumer {
	raw, _ := json.Marshal(transfer.transfer)
	digest := sha256.Sum256(append([]byte(transfer.targetID+"\x00"), raw...))
	consumer := &storageTransferConsumer{
		runtimeServiceConsumer: &runtimeServiceConsumer{id: "wuu.storage-transfer." + hex.EncodeToString(digest[:16]), state: pluginhost.StatePrepared,
			requirements: []pluginhost.ServiceRequirement{{Name: pluginsettings.ServiceName, MajorVersion: 1, Required: true}}},
		host: host, transfer: transfer,
	}
	consumer.settings = pluginsettings.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, consumer.ID(), pluginhost.ServiceCallParams{Service: pluginsettings.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return consumer
}

func (c *storageTransferConsumer) Activate(ctx context.Context) error {
	// Negotiation may have rejected the target after discovery. Its presence in
	// an activation plan is not authority to fence the source.
	ready := false
	for _, status := range c.host.Statuses() {
		if status.ID == c.transfer.targetID {
			ready = status.State == pluginhost.StatePrepared
		}
	}
	if ready && c.host.DependencyFailures()[c.transfer.targetID] == nil {
		if err := c.settings.MoveStatePrefix(ctx, c.transfer.transfer); err != nil {
			return fmt.Errorf("plugin %q storage transfer %q: %w", c.transfer.targetID, c.transfer.transfer.ID, err)
		}
	}
	return c.runtimeServiceConsumer.Activate(ctx)
}

func (c *storageTransferConsumer) Prepare(ctx context.Context) error {
	for _, status := range c.host.Statuses() {
		// This dependency prepares before its importer. Process runtimes are
		// initialized at this point and become prepared only after this check.
		if status.ID == c.transfer.targetID && (status.State == pluginhost.StateInitialized || status.State == pluginhost.StatePrepared) && c.host.DependencyFailures()[c.transfer.targetID] == nil {
			if err := c.settings.CheckStateTransfer(ctx, c.transfer.transfer); err != nil {
				return fmt.Errorf("plugin %q storage transfer %q preflight: %w", c.transfer.targetID, c.transfer.transfer.ID, err)
			}
		}
	}
	return nil
}
