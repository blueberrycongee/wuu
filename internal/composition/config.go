// Package composition defines the bootstrap configuration shared by provider hosts.
package composition

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const Version = 1

// Config selects implementations independently of discovery order. A profile
// supplies a complete module set and service choices. Top-level modules replace
// that set; top-level services replace individual choices by service and major.
// Without a module set, enabled, trusted discoveries remain eligible.
type Config struct {
	Version  int                `json:"version"`
	Profile  string             `json:"profile,omitempty"`
	Profiles map[string]Profile `json:"profiles,omitempty"`
	Modules  *[]string          `json:"modules,omitempty"`
	Services []Binding          `json:"services,omitempty"`
}

// Profile is a named composition, not a grant of package trust. Dependencies
// must be included explicitly. An empty module list starts no modules.
type Profile struct {
	Modules  []string  `json:"modules"`
	Services []Binding `json:"services,omitempty"`
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
	Profile      string
	Modules      *[]string
	ModuleSource string
	Services     []Binding
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
	if c.Version != Version {
		return Resolved{}, fmt.Errorf("composition version %d is unsupported; expected %d", c.Version, Version)
	}
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		profile := c.Profiles[name]
		if name == "" || name != strings.TrimSpace(name) {
			return Resolved{}, fmt.Errorf("invalid composition profile name %q", name)
		}
		if profile.Modules == nil {
			return Resolved{}, fmt.Errorf("composition profile %q requires a modules array", name)
		}
		if err := validateModules(profile.Modules); err != nil {
			return Resolved{}, fmt.Errorf("composition profile %q: %w", name, err)
		}
		if err := validateBindings(profile.Services); err != nil {
			return Resolved{}, fmt.Errorf("composition profile %q: %w", name, err)
		}
	}
	bindings := make(map[string]Binding)
	if c.Profile != "" {
		profile, ok := c.Profiles[c.Profile]
		if !ok {
			return Resolved{}, fmt.Errorf("unknown composition profile %q", c.Profile)
		}
		resolved.Profile = c.Profile
		modules := slices.Clone(profile.Modules)
		resolved.Modules = &modules
		resolved.ModuleSource = "profile:" + c.Profile
		for _, binding := range profile.Services {
			binding.Source = resolved.ModuleSource
			bindings[bindingKey(binding)] = binding
		}
	}
	if c.Modules != nil {
		if err := validateModules(*c.Modules); err != nil {
			return Resolved{}, err
		}
		modules := slices.Clone(*c.Modules)
		resolved.Modules = &modules
		resolved.ModuleSource = "composition"
	}
	if err := validateBindings(c.Services); err != nil {
		return Resolved{}, err
	}
	for _, binding := range c.Services {
		binding.Source = "composition"
		bindings[bindingKey(binding)] = binding
	}
	for _, binding := range bindings {
		if !resolved.Includes(binding.Provider) {
			return Resolved{}, fmt.Errorf("composition provider %q for %s is excluded from modules", binding.Provider, bindingKey(binding))
		}
		resolved.Services = append(resolved.Services, binding)
	}
	sort.Slice(resolved.Services, func(i, j int) bool {
		return bindingKey(resolved.Services[i]) < bindingKey(resolved.Services[j])
	})
	return resolved, nil
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
