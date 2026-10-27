package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Dependency is a resolved edge in a generation's lifecycle graph. Optional
// services participate in ordering when present, but do not propagate failure.
type Dependency struct {
	Provider string `json:"provider"`
	Required bool   `json:"required"`
	Service  string `json:"service,omitempty"`
	Major    int    `json:"major,omitempty"`
}

// SetPackageRequirements seeds the same graph used by resolved service edges.
// Declarative-only packages remain nodes so required chains cannot escape
// through a package without a runtime. Version/trust selection precedes this.
func (h *Host) SetPackageRequirements(requirements map[string][]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dependencies = make(map[string][]Dependency, len(requirements))
	for id, required := range requirements {
		h.dependencies[id] = nil
		for _, provider := range required {
			h.dependencies[id] = append(h.dependencies[id], Dependency{Provider: provider, Required: true})
		}
	}
}

// resolveServiceDependencies captures the selected graph before activation.
// Keep its edges after revocation: a removed provider must not make its former
// consumers appear independent in a retained generation.
func (h *Host) resolveServiceDependencies(registry *ServiceRegistry) {
	if registry == nil {
		return
	}
	dependencies := make(map[string][]Dependency)
	failures := make(map[string]error)
	registry.mu.RLock()
	for id, requirements := range registry.requirements {
		for _, requirement := range requirements {
			declaration := serviceKey{name: strings.TrimSpace(requirement.Name), major: requirement.MajorVersion}
			key := registry.providerKeyLocked(id, declaration)
			provider, found := registry.providers[key]
			if !found {
				if requirement.Required {
					var majors []int
					for candidate := range registry.providers {
						if candidate.name == key.name {
							majors = append(majors, candidate.major)
						}
					}
					sort.Ints(majors)
					failures[id] = errors.Join(failures[id], fmt.Errorf("plugin %q requires service %s@%d with no provider (provided majors: %v)", id, key.name, key.major, majors))
				}
				continue
			}
			// Renderer -> own runtime is internal to one package lifecycle node.
			// A runtime consuming its own selected service is a true self-delegate
			// and must retain the edge so cycle validation rejects it.
			if provider.pluginID == id && !registry.consumers[id][declaration].runtime {
				continue
			}
			dependencies[id] = append(dependencies[id], Dependency{Provider: provider.pluginID, Required: requirement.Required, Service: key.name, Major: key.major})
		}
	}
	registry.mu.RUnlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dependencies == nil {
		h.dependencies = make(map[string][]Dependency)
	}
	for id, edges := range dependencies {
		h.dependencies[id] = append(h.dependencies[id], edges...)
	}
	h.dependencyErrors = failures
}

// Dependencies returns the selected generation's graph, including service and
// package edges. Callers must not infer dependencies from a newer generation.
func (h *Host) Dependencies() map[string][]Dependency {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string][]Dependency, len(h.dependencies))
	for id, dependencies := range h.dependencies {
		result[id] = append([]Dependency(nil), dependencies...)
	}
	return result
}

// DependencyOrder returns providers before consumers. Cycles fail preparation
// before effects; the complete deterministic order still permits cleanup of a
// rejected candidate. Independent clients preserve their registration order.
func (h *Host) DependencyOrder() ([]string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.dependencyOrderLocked()
}

func (h *Host) dependencyOrderLocked() ([]string, error) {
	var order, path []string
	visited := make(map[string]uint8)
	var cycle error
	var visit func(string)
	visit = func(id string) {
		if visited[id] == 2 {
			return
		}
		if visited[id] == 1 {
			if cycle == nil {
				cycle = fmt.Errorf("plugin dependency cycle: %s", strings.Join(append(append([]string(nil), path...), id), " -> "))
			}
			return
		}
		visited[id] = 1
		path = append(path, id)
		for _, dependency := range h.dependencies[id] {
			visit(dependency.Provider)
		}
		path = path[:len(path)-1]
		visited[id] = 2
		order = append(order, id)
	}
	for _, client := range h.clients {
		visit(client.ID())
	}
	var remaining []string
	for id := range h.dependencies {
		remaining = append(remaining, id)
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		visit(id)
	}
	return order, cycle
}

func (h *Host) orderedClients() []Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.orderedClientsLocked()
}

func (h *Host) orderedClientsLocked() []Client {
	order, _ := h.dependencyOrderLocked()
	byID := make(map[string]Client, len(h.clients))
	for _, client := range h.clients {
		byID[client.ID()] = client
	}
	clients := make([]Client, 0, len(h.clients))
	for _, id := range order {
		if client := byID[id]; client != nil {
			clients = append(clients, client)
		}
	}
	return clients
}

// DependencyFailures includes missing services and transitive unavailable
// providers. Optional dependencies never block an otherwise usable consumer.
func (h *Host) DependencyFailures() map[string]error {
	h.mu.RLock()
	requirements := make(map[string][]Dependency, len(h.dependencies))
	for id, edges := range h.dependencies {
		requirements[id] = append([]Dependency(nil), edges...)
	}
	clients := append([]Client(nil), h.clients...)
	failures := make(map[string]error, len(h.dependencyErrors))
	unavailable := make(map[string]bool)
	for id, err := range h.dependencyErrors {
		failures[id], unavailable[id] = err, true
	}
	h.mu.RUnlock()
	for _, client := range clients {
		state := client.Status().State
		if state == StateFailed || state == StateStopped {
			unavailable[client.ID()] = true
		}
	}
	ids := make([]string, 0, len(requirements))
	for id := range requirements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			if failures[id] != nil {
				continue
			}
			for _, dependency := range requirements[id] {
				if dependency.Required && unavailable[dependency.Provider] {
					failures[id] = fmt.Errorf("plugin %q requires unavailable plugin %q", id, dependency.Provider)
					unavailable[id], changed = true, true
					break
				}
			}
		}
	}
	return failures
}

// BlockFailedDependencies closes consumers before providers and detaches every
// executable registration, retaining attributable failure records.
func (h *Host) BlockFailedDependencies(ctx context.Context) {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	h.blockFailedDependencies(ctx)
}

func (h *Host) blockFailedDependencies(ctx context.Context) {
	failures := h.DependencyFailures()
	clients := h.orderedClients()
	for i := len(clients) - 1; i >= 0; i-- {
		client := clients[i]
		failure := failures[client.ID()]
		if failure == nil {
			continue
		}
		delete(failures, client.ID())
		if _, failed := client.(*failedClient); failed {
			continue
		}
		outcome, _ := h.retirePlugin(ctx, client.ID(), failure)
		if outcome.Err != nil {
			failure = fmt.Errorf("%w (close consumer: %v)", failure, outcome.Err)
		}
		h.Add(Failed(client.ID(), failure))
	}
	ids := make([]string, 0, len(failures))
	for id := range failures {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		h.Add(Failed(id, failures[id]))
	}
}
