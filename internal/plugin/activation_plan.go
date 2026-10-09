package plugin

import (
	"fmt"
	"sort"
	"strings"
)

type ActivationIssueKind string

const (
	ActivationIssueMissingRequirement ActivationIssueKind = "missing_requirement"
	ActivationIssueConflict           ActivationIssueKind = "conflict"
	ActivationIssueVersionMismatch    ActivationIssueKind = "version_mismatch"
	ActivationIssueOptionalCycle      ActivationIssueKind = "optional_cycle"
)

type ActivationIssue struct {
	Kind            ActivationIssueKind `json:"kind"`
	RelatedPluginID string              `json:"related_plugin_id"`
}

type ActivationPlan struct {
	Dependencies map[string][]string
	Plugins      []Plugin
	Issues       map[string][]ActivationIssue
}

// BuildActivationPlan resolves installed package relationships without granting
// trust or installing packages. Hard incompatibilities and required dependency
// cycles reject the generation. Missing or incompatible requirements deactivate
// their dependents. Optional edges never block activation.
func BuildActivationPlan(candidates []Plugin) (ActivationPlan, error) {
	byID := make(map[string]Plugin, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; exists {
			return ActivationPlan{}, fmt.Errorf("plugin activation contains duplicate id %q", id)
		}
		candidate.Requires = candidate.RequiredPluginIDs()
		byID[id] = candidate
		ids = append(ids, id)
	}
	sort.Strings(ids)

	available := make(map[string]bool, len(byID))
	for _, id := range ids {
		available[id] = true
	}
	issues := make(map[string][]ActivationIssue)
	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			if !available[id] {
				continue
			}
			for _, dependency := range byID[id].Dependencies {
				matches, err := matchesDependencyVersion(byID[dependency.ID].Version, dependency.Version)
				if err != nil {
					return ActivationPlan{}, fmt.Errorf("plugin %q dependency %q: %w", id, dependency.ID, err)
				}
				if !dependency.Optional && available[dependency.ID] && !matches {
					available[id] = false
					issues[id] = append(issues[id], ActivationIssue{Kind: ActivationIssueVersionMismatch, RelatedPluginID: dependency.ID})
					changed = true
					break
				}
			}
			if !available[id] {
				continue
			}
			for _, requiredID := range byID[id].Requires {
				if available[requiredID] {
					continue
				}
				available[id] = false
				issues[id] = append(issues[id], ActivationIssue{
					Kind:            ActivationIssueMissingRequirement,
					RelatedPluginID: requiredID,
				})
				changed = true
				break
			}
		}
	}
	for _, id := range ids {
		if !available[id] {
			continue
		}
		for _, brokenID := range byID[id].Breaks {
			if available[brokenID] {
				return ActivationPlan{}, fmt.Errorf("plugin %q breaks plugin %q; disable one before activation", id, brokenID)
			}
		}
	}

	for _, id := range ids {
		if !available[id] {
			continue
		}
		for _, conflictID := range byID[id].Conflicts {
			if available[conflictID] {
				issues[id] = append(issues[id], ActivationIssue{
					Kind:            ActivationIssueConflict,
					RelatedPluginID: conflictID,
				})
			}
		}
	}

	dependencies := make(map[string][]string)
	for _, id := range ids {
		if available[id] {
			dependencies[id] = append([]string(nil), byID[id].Requires...)
		}
	}
	if _, err := topologicalActivationOrder(byID, available, ids, dependencies); err != nil {
		return ActivationPlan{}, err
	}
	// Optional edges improve order only. Add them deterministically when they
	// cannot turn a valid required graph into an unusable generation.
	for _, id := range ids {
		if !available[id] {
			continue
		}
		optional := append([]Dependency(nil), byID[id].Dependencies...)
		sort.Slice(optional, func(i, j int) bool { return optional[i].ID < optional[j].ID })
		for _, dependency := range optional {
			if !dependency.Optional || !available[dependency.ID] {
				continue
			}
			matches, _ := matchesDependencyVersion(byID[dependency.ID].Version, dependency.Version)
			if !matches {
				continue
			}
			if dependencyReachable(dependencies, dependency.ID, id, map[string]bool{}) {
				issues[id] = append(issues[id], ActivationIssue{Kind: ActivationIssueOptionalCycle, RelatedPluginID: dependency.ID})
				continue
			}
			dependencies[id] = normalizeStrings(append(dependencies[id], dependency.ID))
		}
	}
	ordered, err := topologicalActivationOrder(byID, available, ids, dependencies)
	if err != nil {
		return ActivationPlan{}, err
	}
	return ActivationPlan{Plugins: ordered, Issues: issues, Dependencies: dependencies}, nil
}

func dependencyReachable(graph map[string][]string, from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, next := range graph[from] {
		if dependencyReachable(graph, next, target, seen) {
			return true
		}
	}
	return false
}

func topologicalActivationOrder(byID map[string]Plugin, available map[string]bool, ids []string, dependencies map[string][]string) ([]Plugin, error) {
	remaining := make(map[string]int)
	dependents := make(map[string][]string)
	for _, id := range ids {
		if !available[id] {
			continue
		}
		remaining[id] = 0
		for _, requiredID := range dependencies[id] {
			if !available[requiredID] {
				continue
			}
			remaining[id]++
			dependents[requiredID] = append(dependents[requiredID], id)
		}
	}
	ready := make([]string, 0, len(remaining))
	for _, id := range ids {
		if available[id] && remaining[id] == 0 {
			ready = append(ready, id)
		}
	}
	ordered := make([]Plugin, 0, len(remaining))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[id])
		for _, dependentID := range dependents[id] {
			remaining[dependentID]--
			if remaining[dependentID] == 0 {
				ready = append(ready, dependentID)
				sort.Strings(ready)
			}
		}
	}
	if len(ordered) != len(remaining) {
		cycle := make([]string, 0)
		for _, id := range ids {
			if available[id] && remaining[id] > 0 {
				cycle = append(cycle, id)
			}
		}
		return nil, fmt.Errorf("plugin requires cycle: %s", strings.Join(cycle, ", "))
	}
	return ordered, nil
}
