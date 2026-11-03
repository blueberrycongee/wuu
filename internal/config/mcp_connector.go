package config

import (
	"errors"
	"net/url"
	"strings"
)

// MCPConnectorDescriptor describes a server's onboarding without credentials.
// Onboarding gates apply to plugin-owned servers; standalone metadata is informational.
// SetupRequired is an explanation of configuration still required from the
// package or its administrator; it is not a request to start authentication.
type MCPConnectorDescriptor struct {
	Title            string            `json:"title"`
	Description      string            `json:"description,omitempty"`
	DocumentationURL string            `json:"documentation_url,omitempty"`
	SetupRequired    string            `json:"setup_required,omitempty"`
	IdentityProbe    *MCPIdentityProbe `json:"identity_probe,omitempty"`
	// Icon is a package-relative brand image. Plugin manifests validate and
	// serve it; standalone configuration has no package and never renders it.
	Icon *MCPConnectorIcon `json:"icon,omitempty"`
}

// MCPConnectorIcon declares one image or a light/dark pair.
type MCPConnectorIcon struct {
	Path  string `json:"path,omitempty"`
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
}

// AssetPaths returns the declared package-relative image paths.
func (i *MCPConnectorIcon) AssetPaths() []string {
	if i == nil {
		return nil
	}
	if i.Path != "" {
		return []string{i.Path}
	}
	return []string{i.Light, i.Dark}
}

// Validate applies the same metadata contract to configuration and packages.
func (c *MCPConnectorDescriptor) Validate() error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.Title) == "" {
		return errors.New("title must not be empty")
	}
	if c.DocumentationURL != "" {
		u, err := url.Parse(c.DocumentationURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return errors.New("documentation_url must be an absolute HTTPS URL without credentials")
		}
	}
	return c.IdentityProbe.Validate()
}

// ConnectorEnabled reports a plugin-owned connector's effective startup
// preference. A saved override cannot bypass a package setup requirement.
func (c MCPServerConfig) ConnectorEnabled() bool {
	return (c.Enabled == nil || *c.Enabled) && (c.Connector == nil || strings.TrimSpace(c.Connector.SetupRequired) == "")
}
