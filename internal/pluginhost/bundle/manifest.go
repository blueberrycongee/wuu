package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ManifestSchemaVersion is the bundle manifest version this contract accepts.
// Version 1 manifests are rejected: the cross-runtime contract starts at v2 and
// does not carry a compatibility path.
const ManifestSchemaVersion = 2

// Manifest is the typed view of a v2 bundle manifest. Generation is computed
// from the raw authored tree (ParseManifestValue), not from this re-serialized
// struct, so unknown fields still participate in identity.
type Manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	ID            string              `json:"id"`
	Version       string              `json:"version"`
	Name          string              `json:"name,omitempty"`
	Description   string              `json:"description,omitempty"`
	Agent         *AgentDeclaration   `json:"agent,omitempty"`
	Desktop       *DesktopDeclaration `json:"desktop,omitempty"`
}

// AgentDeclaration describes the long-lived plugin process that contributes the
// agent surface. There is no wasm entry: agents are external processes.
type AgentDeclaration struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// DesktopDeclaration describes the package-relative browser module that
// contributes the desktop surface.
type DesktopDeclaration struct {
	Entry string `json:"entry"`
}

// Parse decodes and validates a v2 bundle manifest. It returns both the typed
// manifest and the raw value tree suitable for Generation.
func Parse(raw []byte) (*Manifest, any, error) {
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, nil, fmt.Errorf("bundle: decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, nil, err
	}
	value, err := ParseManifestValue(raw)
	if err != nil {
		return nil, nil, err
	}
	return &manifest, value, nil
}

// Validate checks the invariant fields of a v2 manifest. It is intentionally
// narrow: it does not interpret contribution payloads, only the bundle shape.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("bundle: manifest is nil")
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("bundle: schema_version must be %d, got %d", ManifestSchemaVersion, m.SchemaVersion)
	}
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("bundle: manifest.id is required")
	}
	if err := validateASCII(m.ID, "manifest.id"); err != nil {
		return err
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("bundle: manifest.version is required")
	}
	if m.Agent == nil && m.Desktop == nil {
		return errors.New("bundle: manifest must declare at least one of agent or desktop")
	}
	if m.Agent != nil {
		if strings.TrimSpace(m.Agent.Command) == "" {
			return errors.New("bundle: manifest.agent.command is required when agent is declared")
		}
		if err := validateASCII(m.Agent.Command, "manifest.agent.command"); err != nil {
			return err
		}
		for _, arg := range m.Agent.Args {
			if err := validateASCII(arg, "manifest.agent.args"); err != nil {
				return err
			}
		}
		for key, value := range m.Agent.Env {
			if err := validateASCII(key, "manifest.agent.env key"); err != nil {
				return err
			}
			if err := validateASCII(value, "manifest.agent.env value"); err != nil {
				return err
			}
		}
	}
	if m.Desktop != nil {
		if strings.TrimSpace(m.Desktop.Entry) == "" {
			return errors.New("bundle: manifest.desktop.entry is required when desktop is declared")
		}
		if err := validateRelativePath(m.Desktop.Entry, "manifest.desktop.entry"); err != nil {
			return err
		}
	}
	return nil
}

func validateASCII(value, field string) error {
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("bundle: %s contains a NUL byte", field)
	}
	for _, r := range value {
		if r > 0x7f {
			return fmt.Errorf("bundle: %s must be ASCII", field)
		}
	}
	return nil
}

func validateRelativePath(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("bundle: %s is required", field)
	}
	if path.IsAbs(value) {
		return fmt.Errorf("bundle: %s must be package-relative", field)
	}
	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("bundle: %s must not escape the package root", field)
	}
	return nil
}

// HasAgent reports whether the manifest declares an agent surface.
func (m *Manifest) HasAgent() bool {
	return m != nil && m.Agent != nil
}

// HasDesktop reports whether the manifest declares a desktop surface.
func (m *Manifest) HasDesktop() bool {
	return m != nil && m.Desktop != nil
}
