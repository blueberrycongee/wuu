// Package composition defines the bootstrap configuration shared by provider hosts.
package composition

import (
	"fmt"
	"regexp"
	"strings"
)

const Version = 1

// Config selects implementations independently of package discovery order.
// An omitted configuration preserves unique-provider discovery. Ambiguous
// services always require a binding, including when configuration is omitted.
type Config struct {
	Version  int       `json:"version"`
	Services []Binding `json:"services,omitempty"`
}

// Binding selects one provider for a service major in the owning composition.
// Removing or failing that provider never selects another implementation.
type Binding struct {
	Service  string `json:"service"`
	Major    int    `json:"major"`
	Provider string `json:"provider"`
}

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)

func (c Config) Validate() error {
	if c.Version != Version {
		return fmt.Errorf("composition version %d is unsupported; expected %d", c.Version, Version)
	}
	seen := make(map[string]bool, len(c.Services))
	for _, binding := range c.Services {
		if !serviceName.MatchString(binding.Service) || binding.Major < 0 || strings.TrimSpace(binding.Provider) == "" || binding.Provider != strings.TrimSpace(binding.Provider) {
			return fmt.Errorf("invalid composition service binding: %s@%d -> %q", binding.Service, binding.Major, binding.Provider)
		}
		key := fmt.Sprintf("%s@%d", binding.Service, binding.Major)
		if seen[key] {
			return fmt.Errorf("duplicate composition binding for %s", key)
		}
		seen[key] = true
	}
	return nil
}
