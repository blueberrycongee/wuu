package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/blueberrycongee/wuu/internal/extensions"
)

// Dependency identifies an installed package used by another package. It grants
// no trust or permissions and never causes installation.
type Dependency = extensions.PackageDependency

func normalizeDependencies(id string, raw json.RawMessage, requires []string) ([]Dependency, []string, error) {
	var dependencies []Dependency
	if len(raw) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&dependencies); err != nil {
			return nil, nil, fmt.Errorf("dependencies: %w", err)
		}
	}
	seen := map[string]bool{}
	for i := range dependencies {
		d := &dependencies[i]
		d.ID = strings.TrimSpace(d.ID)
		d.Version = strings.TrimSpace(d.Version)
		if d.ID == "" {
			return nil, nil, fmt.Errorf("dependencies[%d] requires id", i)
		}
		if _, err := normalizePackageRelationships(id, "dependencies", []string{d.ID}); err != nil {
			return nil, nil, err
		}
		if seen[d.ID] {
			return nil, nil, fmt.Errorf("duplicate dependency %q", d.ID)
		}
		seen[d.ID] = true
		if d.Version != "" {
			if _, err := semver.NewConstraint(d.Version); err != nil {
				return nil, nil, fmt.Errorf("dependency %q version: %w", d.ID, err)
			}
		}
		for _, required := range requires {
			if required == d.ID && d.Optional {
				return nil, nil, fmt.Errorf("dependency %q cannot be optional and required", d.ID)
			}
		}
		if !d.Optional {
			requires = append(requires, d.ID)
		}
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	return dependencies, normalizeStrings(requires), nil
}

func matchesDependencyVersion(version, constraint string) (bool, error) {
	if constraint == "" {
		return true, nil
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, err
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return false, nil
	}
	return c.Check(v), nil
}

// RequiredPluginIDs includes legacy requirements and non-optional dependencies.
// It also supports programmatic manifests that did not pass through the loader.
func (p Plugin) RequiredPluginIDs() []string {
	ids := append([]string(nil), p.Requires...)
	for _, d := range p.Dependencies {
		if !d.Optional {
			ids = append(ids, d.ID)
		}
	}
	return normalizeStrings(ids)
}
