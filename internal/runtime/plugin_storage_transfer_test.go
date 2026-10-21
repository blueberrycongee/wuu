package runtime

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"testing"
)

func TestStorageTransferPlanTrustReciprocityAndScope(t *testing.T) {
	disabled := false
	declaration := extensions.StorageTransferDeclaration{ID: "records-v1", Plugin: "sender", Scope: "user", Domain: "personal", KeyPrefix: "records."}
	target := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "receiver", StorageTransfers: &extensions.StorageTransfers{Imports: []extensions.StorageTransferDeclaration{declaration}}}, SubjectID: "plugin:bundled:receiver", Fingerprint: "target", Official: true}
	declaration.Plugin = "receiver"
	source := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "sender", DefaultEnabled: &disabled, StorageTransfers: &extensions.StorageTransfers{Exports: []extensions.StorageTransferDeclaration{declaration}}}, SubjectID: "plugin:user:sender", Fingerprint: "source", Source: "user"}
	binding := userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	cfg := config.Config{Extensions: &extensions.Settings{}}
	if _, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, binding); err == nil {
		t.Fatal("untrusted source accepted")
	}
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: source.SubjectID, Fingerprint: source.Fingerprint, Scope: extensions.GrantScopeUser}); err != nil {
		t.Fatal(err)
	}
	plan, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, binding)
	if err != nil || len(plan) != 1 {
		t.Fatalf("disabled trusted exporter: %+v %v", plan, err)
	}
	if plan[0].transfer.SourceSubjectID != source.SubjectID {
		t.Fatalf("wrong source subject: %+v", plan)
	}
	if got, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, userspace.Binding{}); err != nil || len(got) != 0 {
		t.Fatalf("cross domain transfer: %+v %v", got, err)
	}
	target.Official = false
	if _, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, binding); err == nil {
		t.Fatal("source trust authorized untrusted target")
	}
	target.Official = true
	source.Fingerprint = "changed-source"
	if _, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, binding); err == nil {
		t.Fatal("stale source grant authorized transfer")
	}
	source.Fingerprint = "source"
	source.StorageTransfers.Exports[0].KeyPrefix = "different."
	if _, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, target}, []pluginpkg.Plugin{target}, binding); err == nil {
		t.Fatal("nonreciprocal declaration accepted")
	}
}

func TestStorageTransferRunsAfterCommitBeforeActivation(t *testing.T) {
	for _, failure := range []string{"", "commit", "collision"} {
		t.Run(failure, func(t *testing.T) {
			old := testPluginGeneration("old", &generationClient{id: "old"})
			s := testGenerationSession(old)
			s.WuuHome = t.TempDir()
			s.RootDir = t.TempDir()
			s.UserSpaceID = "alice"
			s.ProductDomain = "personal"
			defer s.Cleanup()
			binding := s.UserBinding()
			home := binding.State(s.WuuHome)
			move := pluginsettings.StateTransfer{ID: "records-v1", SourceSubjectID: "plugin:user:source", TargetSubjectID: "plugin:user:target", Scope: pluginsettings.ScopeUser, KeyPrefix: "records."}
			if _, err := pluginsettings.UpdateState(home, "", move.SourceSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "original"; return nil }); err != nil {
				t.Fatal(err)
			}
			if failure == "collision" {
				if _, err := pluginsettings.UpdateState(home, "", move.TargetSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "newer"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			committed := false
			client := &preparedGenerationClient{generationClient: &generationClient{id: "target"}, onActivate: func() {
				if !committed {
					t.Error("target activation preceded commit")
				}
				target, err := pluginsettings.ReadState(home, "", move.TargetSubjectID, move.Scope)
				if err != nil || target.Values["records.one"] != "original" {
					t.Errorf("target activated before transfer: %+v %v", target, err)
				}
			}}
			candidate := testPluginGeneration("target", client)
			candidate.active[0].Runtime = &pluginpkg.RuntimeSpec{Command: "fixture"}
			healthy := &preparedGenerationClient{generationClient: &generationClient{id: "healthy"}}
			candidate.host.Add(healthy)
			candidate.active = append(candidate.active, pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "healthy", Runtime: &pluginpkg.RuntimeSpec{Command: "fixture"}}})
			candidate.storageTransfers = []pluginStorageTransfer{{targetID: "target", transfer: move}}
			err := s.ActivatePluginGeneration(candidate, func() error {
				if failure == "commit" {
					return errors.New("commit rejected")
				}
				committed = true
				return nil
			})
			source, readErr := pluginsettings.ReadState(home, "", move.SourceSubjectID, move.Scope)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if failure == "" {
				if err != nil || client.activateCall != 1 || len(source.Values) != 0 {
					t.Fatalf("activation transfer failed: %v calls=%d source=%+v", err, client.activateCall, source)
				}
			} else {
				if err == nil || client.activateCall != 0 || source.Values["records.one"] != "original" {
					t.Fatalf("failed activation changed source or ran target: %v calls=%d source=%+v", err, client.activateCall, source)
				}
				if failure == "collision" && healthy.activateCall != 1 {
					t.Fatal("transfer failure blocked unrelated plugin")
				}
				if failure == "collision" && !PluginGenerationWasCommitted(err) {
					t.Fatalf("lost committed failure: %v", err)
				}
			}
		})
	}
}

func TestInitialStorageTransferDoesNotFenceFailedRuntime(t *testing.T) {
	home := t.TempDir()
	binding := userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	move := pluginsettings.StateTransfer{ID: "records-v1", SourceSubjectID: "plugin:user:source", TargetSubjectID: "plugin:user:target", Scope: pluginsettings.ScopeUser, KeyPrefix: "records."}
	if _, err := pluginsettings.UpdateState(binding.State(home), "", move.SourceSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "original"; return nil }); err != nil {
		t.Fatal(err)
	}
	target := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "target", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: "/nonexistent/storage-transfer-runtime"}}, SubjectID: move.TargetSubjectID}
	host, _ := startPluginHostWithTransfers([]pluginpkg.Plugin{target}, t.TempDir(), "workspace", home, t.TempDir(), nil, nil, binding, []pluginStorageTransfer{{targetID: "target", transfer: move}})
	defer host.Close(context.Background())
	source, err := pluginsettings.ReadState(binding.State(home), "", move.SourceSubjectID, move.Scope)
	if err != nil || source.Values["records.one"] != "original" {
		t.Fatalf("failed initial runtime fenced source: %+v %v", source, err)
	}
}
