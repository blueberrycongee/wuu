// Package composition defines the bootstrap configuration shared by provider hosts.
package composition

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const Version = 2

// Config selects implementations independently of discovery order. Profiles
// compose ordered bundles from an empty module set. Inline choices and patches
// then override that result. Inclusion never grants trust or enables a package.
type Config struct {
	Version  int                `json:"version"`
	Profile  string             `json:"profile,omitempty"`
	Profiles map[string]Profile `json:"profiles,omitempty"`
	Bundles  map[string]Bundle  `json:"bundles,omitempty"`
	Modules  *[]string          `json:"modules,omitempty"`
	Services []Binding          `json:"services,omitempty"`
	Patches  []Patch            `json:"patches,omitzero"`
}

// Profile applies bundles in order, then inline choices, then its own patches.
// Dependencies must be selected explicitly. Modules, when present, replaces the
// complete module set produced by the bundles, including when it is empty.
type Profile struct {
	Bundles  []string  `json:"bundles,omitzero"`
	Modules  []string  `json:"modules,omitzero"`
	Services []Binding `json:"services,omitempty"`
	Patches  []Patch   `json:"patches,omitzero"`
}

// Bundle is reusable static configuration. It neither executes code nor grants
// authority. Profiles may share bundles without sharing mutable resolved state.
type Bundle struct {
	Patches []Patch `json:"patches"`
}

// Patch edits an explicit module set and service selection. A false module
// entry excludes it; it does not remove bindings that still refer to it.
// Removing a binding restores unique-provider resolution, not a fallback order.
type Patch struct {
	Modules        map[string]bool `json:"modules,omitempty"`
	Services       []Binding       `json:"services,omitempty"`
	RemoveServices []ServiceKey    `json:"remove_services,omitempty"`
}

// ServiceKey addresses one major version independently of its provider.
type ServiceKey struct {
	Service string `json:"service"`
	Major   int    `json:"major"`
}

// Binding selects one provider for a service major in the owning composition.
// Removing or failing that provider never selects another implementation.
type Binding struct {
	Service  string `json:"service"`
	Major    int    `json:"major"`
	Provider string `json:"provider"`
	// Source is computed during resolution; configuration cannot claim it.
	Source string `json:"-"`
}

// Resolved is an immutable, owned selection used for one generation. Nil
// Modules means discovery defaults; a non-nil empty set disables every module.
type Resolved struct {
	Profile       string
	Modules       *[]string
	ModuleSource  string
	ModuleSources map[string]string
	Layers        []string
	Services      []Binding
}

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)

func (c Config) Validate() error {
	_, err := c.Resolve()
	return err
}

func (c *Config) Resolve() (Resolved, error) {
	resolved := Resolved{ModuleSource: "discovery-default"}
	if c == nil {
		return resolved, nil
	}
	if c.Version != 1 && c.Version != Version {
		return Resolved{}, fmt.Errorf("composition version %d is unsupported; expected 1 or %d", c.Version, Version)
	}
	if c.Version == 1 && (c.Bundles != nil || c.Patches != nil) {
		return Resolved{}, fmt.Errorf("composition bundles and patches require version %d", Version)
	}
	for _, name := range slices.Sorted(maps.Keys(c.Bundles)) {
		if name == "" || name != strings.TrimSpace(name) {
			return Resolved{}, fmt.Errorf("invalid composition bundle name %q", name)
		}
		if c.Bundles[name].Patches == nil {
			return Resolved{}, fmt.Errorf("composition bundle %q requires a patches array", name)
		}
		if err := validatePatches(c.Bundles[name].Patches, "bundle:"+name); err != nil {
			return Resolved{}, err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Profiles)) {
		profile := c.Profiles[name]
		if c.Version == 1 && (profile.Bundles != nil || profile.Patches != nil) {
			return Resolved{}, fmt.Errorf("composition profile %q bundles and patches require version %d", name, Version)
		}
		if name == "" || name != strings.TrimSpace(name) {
			return Resolved{}, fmt.Errorf("invalid composition profile name %q", name)
		}
		if profile.Modules == nil && profile.Bundles == nil && profile.Patches == nil {
			return Resolved{}, fmt.Errorf("composition profile %q requires modules, bundles or patches", name)
		}
		if err := validateModules(profile.Modules); err != nil {
			return Resolved{}, fmt.Errorf("composition profile %q: %w", name, err)
		}
		if err := validateBindings(profile.Services); err != nil {
			return Resolved{}, fmt.Errorf("composition profile %q: %w", name, err)
		}
		seen := make(map[string]bool)
		for _, bundle := range profile.Bundles {
			if _, ok := c.Bundles[bundle]; !ok {
				return Resolved{}, fmt.Errorf("composition profile %q selects unknown bundle %q", name, bundle)
			}
			if seen[bundle] {
				return Resolved{}, fmt.Errorf("composition profile %q repeats bundle %q", name, bundle)
			}
			seen[bundle] = true
		}
		if err := validatePatches(profile.Patches, "profile:"+name); err != nil {
			return Resolved{}, err
		}
	}
	if c.Modules != nil {
		if err := validateModules(*c.Modules); err != nil {
			return Resolved{}, err
		}
	}
	if err := validateBindings(c.Services); err != nil {
		return Resolved{}, err
	}
	if err := validatePatches(c.Patches, "composition"); err != nil {
		return Resolved{}, err
	}

	bindings := make(map[string]Binding)
	applyChoices := func(modules []string, services []Binding, source string) {
		if modules != nil {
			resolved.ModuleSources = make(map[string]string, len(modules))
			for _, id := range modules {
				resolved.ModuleSources[id] = source
			}
			resolved.ModuleSource = source
		}
		for _, binding := range services {
			binding.Source = source
			bindings[bindingKey(binding)] = binding
		}
		if modules != nil || len(services) > 0 {
			resolved.Layers = append(resolved.Layers, source)
		}
	}
	applyPatches := func(patches []Patch, owner string) {
		for index, patch := range patches {
			source := fmt.Sprintf("%s:patch:%d", owner, index)
			resolved.Layers = append(resolved.Layers, source)
			for id, enabled := range patch.Modules {
				if enabled {
					resolved.ModuleSources[id] = source
				} else {
					delete(resolved.ModuleSources, id)
				}
			}
			if len(patch.Modules) > 0 {
				resolved.ModuleSource = source
			}
			for _, key := range patch.RemoveServices {
				delete(bindings, bindingKey(Binding{Service: key.Service, Major: key.Major}))
			}
			for _, binding := range patch.Services {
				binding.Source = source
				bindings[bindingKey(binding)] = binding
			}
		}
	}
	// Patches never inherit a discovery-dependent module set. This also lets a
	// patches-only composition describe an empty recovery configuration.
	if c.Profile != "" || c.Patches != nil {
		resolved.ModuleSources = make(map[string]string)
		resolved.ModuleSource = "composition"
	}
	if c.Profile != "" {
		profile, ok := c.Profiles[c.Profile]
		if !ok {
			return Resolved{}, fmt.Errorf("unknown composition profile %q", c.Profile)
		}
		resolved.Profile = c.Profile
		resolved.ModuleSource = "profile:" + c.Profile
		for _, bundle := range profile.Bundles {
			applyPatches(c.Bundles[bundle].Patches, "bundle:"+bundle)
		}
		applyChoices(profile.Modules, profile.Services, "profile:"+c.Profile)
		applyPatches(profile.Patches, "profile:"+c.Profile)
	}
	var modules []string
	if c.Modules != nil {
		modules = *c.Modules
		if modules == nil {
			modules = []string{}
		}
	}
	applyChoices(modules, c.Services, "composition")
	applyPatches(c.Patches, "composition")
	if resolved.ModuleSources != nil {
		modules := make([]string, 0, len(resolved.ModuleSources))
		for id := range resolved.ModuleSources {
			modules = append(modules, id)
		}
		sort.Strings(modules)
		resolved.Modules = &modules
	}
	for _, key := range slices.Sorted(maps.Keys(bindings)) {
		binding := bindings[key]
		if !resolved.Includes(binding.Provider) {
			return Resolved{}, fmt.Errorf("composition provider %q for %s is excluded from modules", binding.Provider, bindingKey(binding))
		}
		resolved.Services = append(resolved.Services, binding)
	}
	return resolved, nil
}

// SourceForModule identifies the layer that last selected an included module.
func (r Resolved) SourceForModule(id string) string {
	if r.ModuleSources == nil {
		return r.ModuleSource
	}
	return r.ModuleSources[id]
}

func (r Resolved) Includes(id string) bool {
	return r.Modules == nil || slices.Contains(*r.Modules, id)
}

// ValidateAvailable rejects unavailable explicit choices before any code starts.
// The caller supplies only trusted, enabled and compatible modules.
func (r Resolved) ValidateAvailable(available []string) error {
	if r.Modules == nil {
		return nil
	}
	for _, id := range *r.Modules {
		if !slices.Contains(available, id) {
			return fmt.Errorf("composition selects unavailable module %q; it must be discovered, enabled, trusted and compatible", id)
		}
	}
	return nil
}

func validatePatches(patches []Patch, owner string) error {
	for index, patch := range patches {
		source := fmt.Sprintf("%s:patch:%d", owner, index)
		if err := validateModules(slices.Sorted(maps.Keys(patch.Modules))); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if err := validateBindings(patch.Services); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		seen := make(map[string]bool)
		for _, binding := range patch.Services {
			seen[bindingKey(binding)] = true
		}
		for _, key := range patch.RemoveServices {
			if !serviceName.MatchString(key.Service) || key.Major < 0 {
				return fmt.Errorf("%s: invalid removed service %s@%d", source, key.Service, key.Major)
			}
			id := bindingKey(Binding{Service: key.Service, Major: key.Major})
			if seen[id] {
				return fmt.Errorf("%s: duplicate or conflicting binding operation for %s", source, id)
			}
			seen[id] = true
		}
	}
	return nil
}

func validateModules(modules []string) error {
	seen := make(map[string]bool, len(modules))
	for _, id := range modules {
		if id == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("invalid composition module %q", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate composition module %q", id)
		}
		seen[id] = true
	}
	return nil
}

func bindingKey(binding Binding) string {
	return fmt.Sprintf("%s@%d", binding.Service, binding.Major)
}

func validateBindings(bindings []Binding) error {
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		if !serviceName.MatchString(binding.Service) || binding.Major < 0 || strings.TrimSpace(binding.Provider) == "" || binding.Provider != strings.TrimSpace(binding.Provider) {
			return fmt.Errorf("invalid composition service binding: %s@%d -> %q", binding.Service, binding.Major, binding.Provider)
		}
		key := bindingKey(binding)
		if seen[key] {
			return fmt.Errorf("duplicate composition binding for %s", key)
		}
		seen[key] = true
	}
	return nil
}
