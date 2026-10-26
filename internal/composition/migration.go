package composition

import "fmt"

// projectLegacyProviders preserves the behavior formerly implemented inside
// consumers when reading version 1/2 configuration. It changes only the resolved
// view: the original configuration remains readable by the previous executable.
// Provenance makes the compatibility choice observable, and version 3 never
// repairs an explicitly incomplete composition. This is not a runtime fallback.
func projectLegacyProviders(version int, resolved *Resolved, bindings map[string]Binding, excluded map[string]bool) {
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
