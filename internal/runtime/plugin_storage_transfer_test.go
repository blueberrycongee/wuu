package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/userspace"
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
	dev := target
	dev.Official, dev.AuthorizedDev = false, true
	dev.SubjectID, dev.StorageSubjectID = "plugin:dev:receiver", target.SubjectID
	devPlan, err := resolveStorageTransfers(cfg, []pluginpkg.Plugin{source, dev}, []pluginpkg.Plugin{dev}, binding)
	if err != nil || len(devPlan) != 1 || devPlan[0].transfer != plan[0].transfer {
		t.Fatalf("development override changed an existing transfer: %+v %v", devPlan, err)
	}
	home := t.TempDir()
	if err := pluginsettings.MoveStatePrefix(home, "", plan[0].transfer); err != nil {
		t.Fatal(err)
	}
	if err := pluginsettings.MoveStatePrefix(home, "", devPlan[0].transfer); err != nil {
		t.Fatalf("development activation conflicts with committed ownership: %v", err)
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
			s := testGenerationSession(t, old)
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
			transfer := pluginStorageTransfer{targetID: "target", transfer: move}
			settings := pluginsettings.NewServiceWithStateRoot(pluginsettings.Module, s.WuuHome, home, s.RootDir)
			registry := pluginhost.NewServiceRegistry()
			migration := newStorageTransferConsumer(candidate.host, registry, transfer)
			candidate.host.Add(settings)
			candidate.host.Add(migration)
			candidate.host.SetPackageRequirements(map[string][]string{"target": {migration.ID()}})
			if failures := registry.RegisterClients(settings, migration); len(failures) != 0 {
				t.Fatal(failures)
			}
			candidate.host.AttachServiceRegistry(registry, nil)
			if err := candidate.host.PrepareAvailable(context.Background(), map[string]bool{"target": true}); err != nil {
				t.Fatal(err)
			}
			if failure == "collision" {
				if _, err := pluginsettings.UpdateState(home, "", move.TargetSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "newer"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
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
	host, _, buildErr := buildPluginHostForBinding(context.Background(), []pluginpkg.Plugin{target}, t.TempDir(), "workspace", home, t.TempDir(), nil, startPluginClient, nil, nil, nil, false, "", binding, []pluginStorageTransfer{{targetID: "target", transfer: move}})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	_ = host.Activate(context.Background())
	defer host.Close(context.Background())
	source, err := pluginsettings.ReadState(binding.State(home), "", move.SourceSubjectID, move.Scope)
	if err != nil || source.Values["records.one"] != "original" {
		t.Fatalf("failed initial runtime fenced source: %+v %v", source, err)
	}
}

func TestStorageTransferPreparationRejectsChangedOwner(t *testing.T) {
	home := t.TempDir()
	move := pluginsettings.StateTransfer{ID: "records-v1", SourceSubjectID: "plugin:bundled:source", TargetSubjectID: "plugin:bundled:target", Scope: pluginsettings.ScopeUser, KeyPrefix: "records."}
	if _, err := pluginsettings.UpdateState(home, "", move.SourceSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "original"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := pluginsettings.MoveStatePrefix(home, "", move); err != nil {
		t.Fatal(err)
	}
	move.TargetSubjectID = "plugin:dev:target"
	client := &preparedGenerationClient{generationClient: &generationClient{id: "target"}}
	host := pluginhost.New(client)
	defer host.Close(context.Background())
	registry := pluginhost.NewServiceRegistry()
	settings := pluginsettings.NewService(pluginsettings.Module, home, "")
	migration := newStorageTransferConsumer(host, registry, pluginStorageTransfer{targetID: "target", transfer: move})
	host.Add(settings)
	host.Add(migration)
	host.SetPackageRequirements(map[string][]string{"target": {migration.ID()}})
	registry.RegisterClients(settings, migration)
	host.AttachServiceRegistry(registry, nil)
	if err := host.PrepareAvailable(context.Background(), map[string]bool{"target": true}); err == nil {
		t.Fatal("preparation accepted a conflicting durable owner")
	}
	doc, err := pluginsettings.ReadState(home, "", "plugin:bundled:target", move.Scope)
	if err != nil || doc.Values["records.one"] != "original" || client.activateCall != 0 {
		t.Fatalf("preparation changed live state: %+v %v", doc, err)
	}
}

// A historical owner conflict must not make unrelated product capabilities
// unavailable at cold start; live replacement still preserves the old graph.
func TestStorageTransferStartupRecoveryPreservesOwnership(t *testing.T) {
	for _, mode := range []string{"initial conflict", "completed retry", "live conflict", "explicit conflict"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			binding := userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
			stateRoot := binding.State(home)
			move := pluginsettings.StateTransfer{ID: "records-v1", SourceSubjectID: "plugin:bundled:source", TargetSubjectID: "plugin:bundled:target", Scope: pluginsettings.ScopeUser, KeyPrefix: "records."}
			if _, err := pluginsettings.UpdateState(stateRoot, "", move.SourceSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "original"; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := pluginsettings.MoveStatePrefix(stateRoot, "", move); err != nil {
				t.Fatal(err)
			}
			if _, err := pluginsettings.UpdateState(stateRoot, "", move.TargetSubjectID, move.Scope, func(values map[string]string) error { values["records.one"] = "updated by current owner"; return nil }); err != nil {
				t.Fatal(err)
			}
			originalOwner := move.TargetSubjectID
			if mode != "completed retry" {
				move.TargetSubjectID = "plugin:dev:target"
			}
			target := &preparedGenerationClient{generationClient: &generationClient{id: "target"}}
			healthy := &preparedGenerationClient{generationClient: &generationClient{id: "healthy"}}
			clients := map[string]pluginhost.Client{"target": target, "healthy": healthy}
			items := []pluginpkg.Plugin{
				{Manifest: pluginpkg.Manifest{ID: "target", Runtime: &pluginpkg.RuntimeSpec{Command: "fixture"}}, SubjectID: move.TargetSubjectID},
				{Manifest: pluginpkg.Manifest{ID: "healthy", Runtime: &pluginpkg.RuntimeSpec{Command: "fixture"}}, SubjectID: "plugin:bundled:healthy"},
			}
			var required map[string]bool
			if mode == "live conflict" {
				required = map[string]bool{}
			}
			if mode == "explicit conflict" {
				required = map[string]bool{"target": true}
			}
			host, _, err := buildPluginHostForBinding(context.Background(), items, t.TempDir(), "workspace", home, t.TempDir(), required,
				func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
					return clients[cfg.ID], nil
				},
				nil, nil, nil, false, "", binding, []pluginStorageTransfer{{targetID: "target", transfer: move}})
			if mode == "live conflict" || mode == "explicit conflict" {
				if err == nil {
					host.Close(context.Background())
					t.Fatal("conflicting replacement was admitted")
				}
				if target.activateCall != 0 || healthy.activateCall != 0 {
					t.Fatal("rejected graph activated")
				}
			} else {
				if err != nil {
					t.Fatalf("cold start rejected independent capabilities: %v", err)
				}
				defer host.Close(context.Background())
				_ = host.Activate(context.Background())
				if healthy.activateCall != 1 {
					t.Fatal("unrelated plugin did not activate")
				}
				if mode == "completed retry" {
					if target.activateCall != 1 {
						t.Fatal("matching completed migration did not activate")
					}
				} else {
					if target.activateCall != 0 {
						t.Fatal("conflicting importer activated")
					}
					diagnosed := false
					for _, status := range host.Statuses() {
						if status.ID == "target" && status.State == pluginhost.StateFailed && strings.Contains(status.Error, "ownership") {
							diagnosed = true
						}
					}
					if !diagnosed {
						t.Fatalf("importer conflict hidden: %+v", host.Statuses())
					}
				}
			}
			owner, err := pluginsettings.ReadState(stateRoot, "", originalOwner, move.Scope)
			if err != nil || owner.Values["records.one"] != "updated by current owner" {
				t.Fatalf("existing owner data changed: %+v %v", owner, err)
			}
			source, err := pluginsettings.ReadState(stateRoot, "", move.SourceSubjectID, move.Scope)
			if err != nil || len(source.Values) != 0 {
				t.Fatalf("source fence lost: %+v %v", source, err)
			}
			if mode != "completed retry" {
				replacement, err := pluginsettings.ReadState(stateRoot, "", move.TargetSubjectID, move.Scope)
				if err != nil || len(replacement.Values) != 0 {
					t.Fatalf("conflicting importer took ownership: %+v %v", replacement, err)
				}
			}
		})
	}
}
