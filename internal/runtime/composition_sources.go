package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginprogram"
)

// ActivePackages captures the non-revoked packages of a retained generation.
// The caller must retain that generation until consumers have finished preparing
// their own snapshots. It does not discover or select a newer catalog.
func (g *PluginGeneration) ActivePackages() []pluginpkg.Plugin {
	g.revokedMu.Lock()
	defer g.revokedMu.Unlock()
	catalog := make([]pluginpkg.Plugin, 0, len(g.active))
	for _, item := range g.active {
		if !g.revokedPlugins[item.ID] {
			catalog = append(catalog, item)
		}
	}
	return catalog
}

// PrepareCompositionSources snapshots explicitly requested local sources into a
// caller-owned state namespace. Configuration and catalog are copied; neither
// the parent generation nor its persisted grants are changed. The caller owns
// cleanup of home, including after a partial preparation failure.
func PrepareCompositionSources(ctx context.Context, cfg config.Config, catalog []pluginpkg.Plugin, home, root string, paths []string) (config.Config, []pluginpkg.Plugin, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return cfg, nil, err
	}
	var prepared config.Config
	if err := json.Unmarshal(encoded, &prepared); err != nil {
		return cfg, nil, err
	}
	catalog = append([]pluginpkg.Plugin{}, catalog...)
	if prepared.Extensions == nil {
		prepared.Extensions = &extensions.Settings{}
	}
	for _, source := range paths {
		if err := ctx.Err(); err != nil {
			return prepared, catalog, err
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(root, source)
		}
		cleanup := func() {}
		if filepath.Ext(source) == ".ts" {
			source, cleanup, err = pluginprogram.PrepareSingleFile(ctx, source)
			if err != nil {
				return prepared, catalog, err
			}
		}
		installed, installErr := pluginpkg.InstallPackage(home, source)
		cleanup()
		if installErr != nil {
			return prepared, catalog, installErr
		}
		item := installed.Plugin
		if err := prepared.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint,
			Scope: extensions.GrantScopeSession, Permissions: item.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
			return prepared, catalog, err
		}
		prepared.Extensions.SetEnabled(item.SubjectID, true)
		replaced := false
		for i := range catalog {
			if catalog[i].ID == item.ID {
				catalog[i], replaced = item, true
				break
			}
		}
		if !replaced {
			catalog = append(catalog, item)
		}
	}
	return prepared, catalog, nil
}
