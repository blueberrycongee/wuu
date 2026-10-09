package pluginhost

import (
	"context"
	"fmt"
	"sort"
)

// SetPackageRequirements records the already resolved package graph. It includes
// declarative-only packages so a required chain cannot escape through a package
// without a native client. Version and trust selection belong to the planner.
func (h *Host) SetPackageRequirements(requirements map[string][]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.packageRequirements = make(map[string][]string, len(requirements))
	for id, required := range requirements {
		h.packageRequirements[id] = append([]string(nil), required...)
	}
}

// PackageDependencyFailures describes consumers blocked by unavailable native
// runtimes, including transitive consumers. A runtime's own failure does not
// remove its independent declarative contributions.
func (h *Host) PackageDependencyFailures() map[string]error {
	h.mu.RLock()
	requirements := h.packageRequirements
	clients := append([]Client(nil), h.clients...)
	h.mu.RUnlock()
	unavailable := make(map[string]bool)
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
	failures := make(map[string]error)
	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			if failures[id] != nil {
				continue
			}
			for _, required := range requirements[id] {
				if unavailable[required] {
					failures[id] = fmt.Errorf("plugin %q requires unavailable plugin %q", id, required)
					unavailable[id] = true
					changed = true
					break
				}
			}
		}
	}
	return failures
}

// BlockFailedPackageDependencies closes consumers before providers and retains
// failure records after detaching their executable registrations.
func (h *Host) BlockFailedPackageDependencies(ctx context.Context) {
	failures := h.PackageDependencyFailures()
	h.mu.RLock()
	clients := append([]Client(nil), h.clients...)
	h.mu.RUnlock()
	for i := len(clients) - 1; i >= 0; i-- {
		client := clients[i]
		failure := failures[client.ID()]
		if failure == nil {
			continue
		}
		delete(failures, client.ID())
		if client.Status().State == StateFailed {
			continue
		}
		outcome, _ := h.RetirePlugin(ctx, client.ID(), failure)
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
