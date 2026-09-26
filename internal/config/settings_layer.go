package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Project-scoped settings layering. Normal startup uses the user config as its
// trusted base, then deep-merges project sources in ascending priority:
//
//  1. <projectRoot>/.wuu.json (or wuu.json)
//  2. <projectRoot>/.wuu/settings.json       — team-shared, checked into git
//  3. <projectRoot>/.wuu/settings.local.json — machine-local, gitignored
//
// Every project source is schema-checked, but normal startup removes settings
// that must remain user-owned: the entire provider selection/connection map,
// role-specific model selection, instruction discovery, and permission mode.
// Explicit LoadProjectConfig keeps the historical standalone-project behavior
// for callers that deliberately trust the files.
const (
	// projectSettingsDir is the project-scoped directory that holds the
	// layered settings files, mirroring Claude Code's .claude directory.
	projectSettingsDir = ".wuu"
	// sharedSettingsFile is the team-shared, checked-in settings layer.
	sharedSettingsFile = "settings.json"
	// localSettingsFile is the machine-local, personal settings layer. It has
	// the highest priority and is expected to be gitignored.
	localSettingsFile = "settings.local.json"
)

// sharedLayerCredentialFields are provider secret fields that must never be
// honored from the shared, checked-in settings layer. Their env-indirection
// twins (api_key_env / auth_token_env) are safe and are preserved.
var sharedLayerCredentialFields = []string{"api_key", "auth_token"}

var projectSettingsWarningSeen sync.Map

// settingsLayer describes one project-scoped overlay file.
type settingsLayer struct {
	path string
	// stripCredentials removes inline provider secrets before merging. It is
	// true only for the shared layer, which may be authored by someone else in
	// the repo; the machine-local layer is fully trusted.
	stripCredentials     bool
	stripExtensionGrants bool
	protectUserSettings  bool
	label                string
}

// projectSettingsLayers returns the ordered overlay files for projectRoot,
// lowest priority first.
func projectSettingsLayers(projectRoot string) []settingsLayer {
	dir := filepath.Join(projectRoot, projectSettingsDir)
	return []settingsLayer{
		{path: filepath.Join(dir, sharedSettingsFile), stripCredentials: true, stripExtensionGrants: true, label: "shared settings"},
		{path: filepath.Join(dir, localSettingsFile), stripCredentials: false, stripExtensionGrants: false, label: "local settings"},
	}
}

func restrictedProjectLayers(projectRoot string) ([]settingsLayer, error) {
	var layers []settingsLayer
	for _, name := range []string{localPrimaryConfig, localFallbackConfig} {
		path := filepath.Join(projectRoot, name)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat project config %s: %w", path, err)
		}
		layers = append(layers, settingsLayer{
			path:                 path,
			stripExtensionGrants: true,
			protectUserSettings:  true,
			label:                "project config",
		})
		break
	}
	for _, layer := range projectSettingsLayers(projectRoot) {
		layer.protectUserSettings = true
		layers = append(layers, layer)
	}
	return layers, nil
}

// applyProjectSettingsLayers deep-merges the project-scoped settings layers on
// top of the already-parsed base config selected by LoadFrom.
//
// basePath is the on-disk path of the selected base config; it is re-read to
// obtain the raw base object for merging and to label decode errors. base is
// the parsed base config, returned untouched when no layer files are present so
// the non-layered path stays byte-for-byte identical to before. projectRoot is
// LoadFrom's rootDir (workdir).
//
// It returns the merged config and the ordered list of layer paths that were
// actually applied (empty when none existed).
func applyProjectSettingsLayers(basePath, projectRoot string, base Config) (Config, []string, error) {
	return applySettingsLayers(basePath, base, projectSettingsLayers(projectRoot))
}

// applyRestrictedProjectLayers overlays every project config source on a
// user-owned base while preserving the user-only security boundary.
func applyRestrictedProjectLayers(basePath, projectRoot string, base Config) (Config, []string, error) {
	layers, err := restrictedProjectLayers(projectRoot)
	if err != nil {
		return Config{}, nil, err
	}
	return applySettingsLayers(basePath, base, layers)
}

func applySettingsLayers(basePath string, base Config, layers []settingsLayer) (Config, []string, error) {
	type pendingLayer struct {
		spec settingsLayer
		data []byte
	}

	var pending []pendingLayer
	for _, layer := range layers {
		data, err := os.ReadFile(layer.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Config{}, nil, fmt.Errorf("read %s %s: %w", layer.label, layer.path, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			continue // empty file is a no-op, same as a missing layer
		}
		pending = append(pending, pendingLayer{spec: layer, data: data})
	}

	// Fast path: no usable layer files. Return the base config exactly as the
	// pre-layering loader would have.
	if len(pending) == 0 {
		return base, nil, nil
	}

	baseData, err := os.ReadFile(basePath)
	if err != nil {
		return Config{}, nil, fmt.Errorf("read config %s: %w", basePath, err)
	}
	merged, err := decodeRawObject(baseData)
	if err != nil {
		return Config{}, nil, fmt.Errorf("parse config %s: %w", basePath, err)
	}

	var applied []string
	for _, p := range pending {
		// Enforce the same strict schema as config.json: unknown fields are
		// rejected and the overlay must decode into the Config shape. The
		// resulting Config is discarded — the merge itself operates on the raw
		// object so it can distinguish "set" keys from absent ones.
		if _, err := decodeConfig(p.data, p.spec.path); err != nil {
			return Config{}, nil, err
		}
		overlay, err := decodeRawObject(p.data)
		if err != nil {
			return Config{}, nil, fmt.Errorf("parse %s %s: %w", p.spec.label, p.spec.path, err)
		}
		if p.spec.protectUserSettings {
			stripProjectUserSettings(overlay, p.spec.path)
		} else if p.spec.stripCredentials {
			stripSharedLayerCredentials(overlay, p.spec.path)
		}
		if p.spec.stripExtensionGrants {
			stripSharedLayerExtensionPolicy(overlay, p.spec.path)
		}
		deepMergeObject(merged, overlay)
		applied = append(applied, p.spec.path)
	}

	mergedData, err := json.Marshal(merged)
	if err != nil {
		return Config{}, nil, fmt.Errorf("merge settings layers: %w", err)
	}
	cfg, err := decodeConfig(mergedData, basePath)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, applied, nil
}

// decodeRawObject decodes JSON bytes into a generic object for deep-merging.
// UseNumber preserves numeric literals so re-marshaling the merged object does
// not distort integers (for example turning a token count into 1e+06).
func decodeRawObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

// deepMergeObject merges overlay into base in place. JSON objects merge
// recursively; every other value — scalars and arrays — replaces the base
// value wholesale, so the highest-priority layer wins for leaves and arrays are
// never element-wise combined.
func deepMergeObject(base, overlay map[string]any) {
	for key, overlayVal := range overlay {
		baseVal, ok := base[key]
		if !ok {
			base[key] = overlayVal
			continue
		}
		baseObj, baseIsObj := baseVal.(map[string]any)
		overlayObj, overlayIsObj := overlayVal.(map[string]any)
		if baseIsObj && overlayIsObj {
			deepMergeObject(baseObj, overlayObj)
			continue
		}
		base[key] = overlayVal
	}
}

// stripProjectUserSettings enforces the trust boundary for every file under a
// project root, including settings.local.json. A repository must not be able
// to choose a provider, model, endpoint, credential source, request headers,
// external memory inputs, the optional execution runtime, or a broader permission mode. Removing the whole
// providers and memory objects is an allowlist-by-omission: newly added fields
// remain protected until they are deliberately classified as project-safe.
func stripProjectUserSettings(overlay map[string]any, path string) {
	var ignored []string
	for _, field := range []string{"default_provider", "providers", "instructions", "memory", "ptc"} {
		if deleteKeysEqualFold(overlay, field) {
			ignored = append(ignored, field)
		}
	}
	for key, rawAgent := range overlay {
		if !strings.EqualFold(key, "agent") {
			continue
		}
		agent, ok := rawAgent.(map[string]any)
		if !ok {
			// JSON null would replace the complete user agent object and reset
			// a restrictive permission mode to the standard default.
			delete(overlay, key)
			ignored = append(ignored, "agent")
			continue
		}
		if deleteKeysEqualFold(agent, "permission_mode") {
			ignored = append(ignored, "agent.permission_mode")
		}
		if deleteKeysEqualFold(agent, "model_roles") {
			ignored = append(ignored, "agent.model_roles")
		}
		if deleteKeysEqualFold(agent, "model_aliases") {
			ignored = append(ignored, "agent.model_aliases")
		}
	}
	if len(ignored) == 0 {
		return
	}
	sort.Strings(ignored)
	warningKey := path + "\x00" + strings.Join(ignored, "\x00")
	if _, loaded := projectSettingsWarningSeen.LoadOrStore(warningKey, struct{}{}); loaded {
		return
	}
	fmt.Fprintf(os.Stderr,
		"wuu: ignoring user-owned settings from project source %s: %s\n",
		path,
		strings.Join(ignored, ", "),
	)
}

// deleteKeysEqualFold mirrors encoding/json's case-insensitive struct-field
// matching. Project JSON is decoded into a raw map before merging, so deleting
// only the canonical spelling would let fields such as "Providers" or
// "Permission_Mode" survive this boundary and bind to the same Config field
// during the final decode. Remove every matching spelling, including duplicate
// case variants in one object.
func deleteKeysEqualFold(object map[string]any, field string) bool {
	deleted := false
	for key := range object {
		if strings.EqualFold(key, field) {
			delete(object, key)
			deleted = true
		}
	}
	return deleted
}

// stripSharedLayerCredentials removes inline provider secrets from a shared
// settings overlay before it is merged. The shared layer lives in the repo and
// may be authored by someone else, so honoring inline credentials from it would
// be a credential-injection vector. Each stripped field emits one warning line
// to stderr. Environment-indirection fields (api_key_env / auth_token_env) are
// left intact, matching Claude Code's rule that secrets are only trusted from
// user-level sources.
func stripSharedLayerCredentials(overlay map[string]any, path string) {
	providers, ok := overlay["providers"].(map[string]any)
	if !ok {
		return
	}
	// Iterate provider names in sorted order so the warning output is stable
	// and deterministic across runs.
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider, ok := providers[name].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range sharedLayerCredentialFields {
			if _, present := provider[field]; !present {
				continue
			}
			delete(provider, field)
			fmt.Fprintf(os.Stderr,
				"wuu: ignoring providers.%s.%s from shared settings layer %s; inline credentials are only trusted from user config or settings.local.json (use %s_env instead)\n",
				name, field, path, field)
		}
	}
}

func stripSharedLayerExtensionPolicy(overlay map[string]any, path string) {
	if _, ok := overlay["extensions"]; !ok {
		return
	}
	delete(overlay, "extensions")
	fmt.Fprintf(os.Stderr,
		"wuu: ignoring extensions policy from shared settings layer %s; package decisions are only trusted from machine-local settings\n",
		path,
	)
}

// logSettingsLayerDebug records which project settings layers overlaid the base
// config. It is gated behind WUU_DEBUG so normal runs stay quiet while the
// layering stays observable when debugging. The base configPath returned by
// LoadFrom is intentionally left pointing at the writable base file; layer
// provenance is surfaced here rather than by mutating that path (which callers
// use as a write target).
func logSettingsLayerDebug(basePath string, applied []string) {
	if len(applied) == 0 {
		return
	}
	if strings.TrimSpace(os.Getenv("WUU_DEBUG")) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "wuu: config %s layered with %s\n", basePath, strings.Join(applied, ", "))
}
