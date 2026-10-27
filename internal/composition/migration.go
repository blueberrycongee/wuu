package composition

import "fmt"

// Compatibility is applied only across explicit schema versions, never to
// repair missing providers in a current-version composition. Resolution leaves
// the source unchanged so older executables can still read it.
func projectLegacyProviders(version int, resolved *Resolved, bindings map[string]Binding, excluded map[string]bool) {
	if version < 3 {
		projectLegacyExecutionProviders(version, resolved, bindings, excluded)
	}
	if version < 4 {
		projectLegacyResourceAuthority(version, resolved, bindings, excluded)
	}
}

// Version 3 extracted execution providers from the old host-owned consumers.
func projectLegacyExecutionProviders(version int, resolved *Resolved, bindings map[string]Binding, excluded map[string]bool) {
	source := fmt.Sprintf("migration:v%d-to-v3", version)
	added := false
	add := func(module, service string) {
		if excluded[module] {
			return
		}
		if _, exists := resolved.ModuleSources[module]; exists {
			return
		}
		if _, selected := bindings[bindingKey(Binding{Service: service, Major: 1})]; selected {
			return
		}
		resolved.ModuleSources[module] = source
		added = true
	}
	if _, consumer := resolved.ModuleSources["wuu.tool-execution"]; consumer {
		add("wuu.tools.v1-batch", "agent.tool.batch")
	}
	if _, consumer := resolved.ModuleSources["wuu.session-execution"]; consumer {
		add("wuu.agent.policy.default", "agent.policy")
	}
	_, sessionConsumer := resolved.ModuleSources["wuu.session-execution"]
	_, modelGateway := resolved.ModuleSources["kernel"]
	if sessionConsumer || modelGateway {
		add("wuu.model.native", "model.adapter")
		add("wuu.model.capacity.default", "model.capacity")
		add("wuu.model.capacity.resources", "host.model.capacity")
		if _, exists := resolved.ModuleSources["wuu.model-execution"]; !exists && !excluded["wuu.model-execution"] {
			resolved.ModuleSources["wuu.model-execution"] = source
			added = true
		}
	}
	_, nativeSessions := resolved.ModuleSources["wuu.session.default"]
	_, externalSessions := bindings[bindingKey(Binding{Service: "session.domain", Major: 1})]
	if sessionConsumer || modelGateway && (nativeSessions || externalSessions) {
		for _, module := range []string{"wuu.engines.native", "wuu.engine-execution"} {
			if _, exists := resolved.ModuleSources[module]; !exists && !excluded[module] {
				resolved.ModuleSources[module] = source
				added = true
			}
		}
	}
	for _, consumer := range []string{"wuu.default-shell", "wuu.default-settings", "wuu.default-navigation", "wuu.default-conversation", "wuu.default-composer", "wuu.default-preview"} {
		if _, selected := resolved.ModuleSources[consumer]; selected {
			add("wuu.default-application", "ui.application")
			break
		}
	}
	if added {
		resolved.Layers = append(resolved.Layers, source)
	}
}

// Version 4 makes resource admission a selected service. Preserve the prior
// owner-controlled session boundary for old configurations, but never replace
// an explicitly selected policy or restore an explicitly excluded module.
func projectLegacyResourceAuthority(version int, resolved *Resolved, bindings map[string]Binding, excluded map[string]bool) {
	_, kernel := resolved.ModuleSources["kernel"]
	_, state := resolved.ModuleSources["wuu.session-state"]
	_, session := resolved.ModuleSources["wuu.session-execution"]
	if !kernel && !state && !session {
		return
	}
	source := fmt.Sprintf("migration:v%d-to-v4", version)
	added := false
	for _, choice := range []struct{ module, service string }{
		{"wuu.resource-access", ""},
		{"wuu.resource-policy.default", "resource.authorization"},
	} {
		if excluded[choice.module] {
			continue
		}
		if _, exists := resolved.ModuleSources[choice.module]; exists {
			continue
		}
		if choice.service != "" {
			if _, selected := bindings[bindingKey(Binding{Consumer: "wuu.resource-access", Service: choice.service, Major: 1})]; selected {
				continue
			}
			if _, selected := bindings[bindingKey(Binding{Service: choice.service, Major: 1})]; selected {
				continue
			}
		}
		resolved.ModuleSources[choice.module] = source
		added = true
	}
	if added {
		resolved.Layers = append(resolved.Layers, source)
	}
}
