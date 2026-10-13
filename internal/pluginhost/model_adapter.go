package pluginhost

import (
	"fmt"
	"strings"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

const (
	CapabilityModelAdapter = pluginapi.CapabilityModelAdapter
	KernelModelEmitService = pluginapi.KernelModelEmitService
)

// ModelAdapterDescriptor names an exact, generation-owned implementation.
// Native is reserved for host protocols; plugin IDs use dotted names.
type ModelAdapterDescriptor = pluginapi.ModelAdapterDescriptor

type RegisteredModelAdapter struct {
	RegisteredCapability
	Adapter ModelAdapterDescriptor
}

type modelAdapterClient interface {
	CapabilityClient
	ModelAdapters() []ModelAdapterDescriptor
}

func (c *ProcessClient) ModelAdapters() []ModelAdapterDescriptor {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ModelAdapterDescriptor(nil), c.modelAdapters...)
}

func validateModelAdapterNegotiation(result CapabilityInitializeResult) error {
	var adapterCapability *CapabilityDescriptor
	for index := range result.Capabilities {
		if result.Capabilities[index].ID == CapabilityModelAdapter {
			adapterCapability = &result.Capabilities[index]
		}
	}
	if len(result.ModelAdapters) == 0 {
		if adapterCapability != nil {
			return fmt.Errorf("model.adapter requires at least one model_adapters registration")
		}
		return nil
	}
	if adapterCapability == nil || EffectiveErrorPolicy(*adapterCapability) != ErrorPolicyPropagate {
		return fmt.Errorf("model adapters require a model.adapter capability with propagate error policy")
	}
	seen := make(map[string]bool, len(result.ModelAdapters))
	for _, descriptor := range result.ModelAdapters {
		if descriptor.ID != strings.TrimSpace(descriptor.ID) || !validModelAdapterID(descriptor.ID) {
			return fmt.Errorf("model adapter id %q must be a dotted identifier", descriptor.ID)
		}
		if descriptor.Mode != "" && descriptor.Mode != "direct" && descriptor.Mode != "delegate" {
			return fmt.Errorf("model adapter %q mode must be direct or delegate", descriptor.ID)
		}
		if descriptor.Version != 1 {
			return fmt.Errorf("model adapter %q requires version 1", descriptor.ID)
		}
		if seen[descriptor.ID] {
			return fmt.Errorf("duplicate model adapter %q", descriptor.ID)
		}
		seen[descriptor.ID] = true
	}
	return nil
}

func validModelAdapterID(id string) bool {
	if !strings.Contains(id, ".") || len(id) > 160 {
		return false
	}
	for _, part := range strings.Split(id, ".") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
				return false
			}
		}
	}
	return true
}

// Duplicate IDs reject a prepared generation rather than routing by discovery
// order. Retained generations keep their own exact registrations during reload.
func (h *Host) validateModelAdapters() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	owners := make(map[string]string)
	for _, client := range h.clients {
		declared, ok := client.(modelAdapterClient)
		if !ok || !capabilityStateReady(client.Status().State) {
			continue
		}
		for _, descriptor := range declared.ModelAdapters() {
			if owner, exists := owners[descriptor.ID]; exists {
				return fmt.Errorf("model adapter %q is declared by both plugin %q and plugin %q", descriptor.ID, owner, client.ID())
			}
			owners[descriptor.ID] = client.ID()
		}
	}
	return nil
}

// ModelAdapter resolves only the selected implementation. Missing, stopped,
// revoked, and ambiguous registrations never fall back to a native provider.
func (h *Host) ModelAdapter(id string) (RegisteredModelAdapter, error) {
	if h == nil {
		return RegisteredModelAdapter{}, fmt.Errorf("model adapter %q is unavailable", id)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var selected *RegisteredModelAdapter
	for _, capability := range h.capabilities {
		if capability.Descriptor.ID != CapabilityModelAdapter || !capabilityStateReady(capability.client.Status().State) {
			continue
		}
		declared, ok := capability.client.(modelAdapterClient)
		if !ok {
			continue
		}
		for _, descriptor := range declared.ModelAdapters() {
			if descriptor.ID != id {
				continue
			}
			if selected != nil {
				return RegisteredModelAdapter{}, fmt.Errorf("model adapter %q has conflicting registrations", id)
			}
			copy := RegisteredModelAdapter{RegisteredCapability: capability, Adapter: descriptor}
			selected = &copy
		}
	}
	if selected == nil {
		return RegisteredModelAdapter{}, fmt.Errorf("model adapter %q is unavailable", id)
	}
	return *selected, nil
}

func KernelModelEmitDescriptor() ServiceDescriptor {
	return ServiceDescriptor{Name: KernelModelEmitService, Version: "1.0.0", Methods: []ServiceMethodDescriptor{{
		Name: KernelServiceMethod, InputSchema: "host.model.emit.input.v1", OutputSchema: "host.model.emit.output.v1",
	}}}
}
