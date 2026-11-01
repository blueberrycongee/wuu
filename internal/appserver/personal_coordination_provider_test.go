package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestPersonalCoordinatorSelectionControlsConstructionAndEveryIngress(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := "absent"
		if selected {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cfg := config.Default()
			cfg.DefaultProvider = "fixture"
			cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
			const replacement = "fixture.coordinator"
			cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{PersonalCoordinationConsumer: true, personalActionsConsumerID: false, replacement: selected}}}}
			bindings := NewRuntimeBindings()
			peer := &coordinationReplacement{id: replacement}
			factories := append(bindings.NativeProviders(), runtime.NativeProviderFactory{ID: replacement, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) { return peer, nil }})
			plugins := []pluginpkg.Plugin{}
			rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: home, HomeDir: home, UserSpaceID: "coord-space", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NativeProviders: factories, NonInteractive: true})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Cleanup()
			host, err := NewUserHost(rt, personalhost.Options{}, WithRuntimeBindings(bindings))
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close()
			if host.server.personalCoordination.nativeActions() != nil {
				t.Fatal("absent/replacement coordinator constructed native fallback")
			}
			owner, err := clientauthority.AdmitDesktop(personalTestOwner(context.Background()), rt.UserBinding(), strings.NewReader(`{"version":1,"user_space_id":"coord-space","product_domain":"personal"}`))
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"review.list", "memory.operations"} {
				raw, err := host.personal.Call(owner, operation, json.RawMessage(`{}`), "")
				if !selected {
					if err == nil {
						t.Fatal("absent coordinator admitted " + operation)
					}
					continue
				}
				if err != nil || string(raw) != `{"selected":true}` {
					t.Fatalf("%s: %s %v", operation, raw, err)
				}
			}
			_, err = host.server.personalFileGate(owner, rt.Toolkit, providers.ToolCall{ID: "file", Name: "write_file", Arguments: `{"path":"note.txt","content":"x"}`})
			if selected && err != nil {
				t.Fatal(err)
			}
			if !selected && err == nil {
				t.Fatal("absent coordinator bypassed file gate")
			}
			if selected && (!peer.methods["call"] || !peer.methods["file-gate"]) {
				t.Fatalf("replacement not used: %#v", peer.methods)
			}
		})
	}
}

type coordinationReplacement struct {
	personalActionsConsumer
	id      string
	methods map[string]bool
}

func (p *coordinationReplacement) ID() string { return p.id }
func (p *coordinationReplacement) Status() pluginhost.Status {
	s := p.personalActionsConsumer.Status()
	s.ID = p.id
	return s
}
func (*coordinationReplacement) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*coordinationReplacement) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{PersonalCoordinationDescriptor()}
}
func (p *coordinationReplacement) InvokeService(_ context.Context, c pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.methods == nil {
		p.methods = map[string]bool{}
	}
	p.methods[c.Method] = true
	if c.Method == "file-gate" {
		return json.Marshal(toolresult.Result{})
	}
	return json.RawMessage(`{"selected":true}`), nil
}

func TestPersonalCoordinatorRetirementDrainsPendingAdmissions(t *testing.T) {
	f := newPersonalBoundaryFixture(t, nil)
	_, err := f.host.server.personalFileGate(f.owner, f.kit, f.call())
	if err == nil || !strings.Contains(err.Error(), "personal_action_pending") {
		t.Fatalf("pending file: %v", err)
	}
	core := f.host.actionsForTest(t)
	state, err := f.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, found := state.Host.RetirePlugin(ctx, personalActionsConsumerID, nil)
	if !found || outcome.Err != nil {
		t.Fatalf("retire: found=%v err=%v", found, outcome.Err)
	}
	if core.ctx.Err() == nil {
		t.Fatal("retirement left native coordinator running")
	}
	core.admissionsMu.Lock()
	retained := len(core.admissions)
	core.admissionsMu.Unlock()
	if retained != 0 {
		t.Fatalf("retirement retained %d admissions", retained)
	}
	f.host.server.personalEffects.mu.Lock()
	effects := len(f.host.server.personalEffects.resources)
	f.host.server.personalEffects.mu.Unlock()
	if effects != 0 {
		t.Fatalf("retirement retained %d effects", effects)
	}
	if _, err := f.host.server.personalFileGate(f.owner, f.kit, f.call()); err == nil {
		t.Fatal("retired coordinator silently fell back")
	}
}
