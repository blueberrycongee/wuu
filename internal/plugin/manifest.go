package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	ManifestFilename       = "plugin.json"
	CodexManifestFilename  = ".codex-plugin/plugin.json"
	ClaudeManifestFilename = ".claude-plugin/plugin.json"
	manifestSchemaVersion  = 1
)

// maxPromptFileSize limits prompt_template files to 1 MiB. Larger files are
// rejected during discovery so a package cannot load unbounded host memory.
const maxPromptFileSize = 1 << 20

// maxDesktopEntrySize bounds the browser module read by a Desktop shell.
const maxDesktopEntrySize = 10 << 20

// maxPluginIconSize keeps declarative artwork cheap to inspect and transport.
const maxPluginIconSize = 256 << 10

// CommandKind enumerates the supported command contribution kinds.
type CommandKind string

const (
	// CommandKindPromptTemplate loads a bounded UTF-8 file inside the plugin
	// root and produces a host-owned composer action.
	CommandKindPromptTemplate CommandKind = "prompt_template"
	// CommandKindRuntimeAction is resolved against a command registered by the
	// plugin's approved desktop generation. The manifest alone never executes
	// plugin code.
	CommandKindRuntimeAction CommandKind = "runtime_action"
)

// CommandSpec is the declarative command descriptor contributed by a manifest.
type CommandSpec struct {
	ID          string      `json:"id"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Kind        CommandKind `json:"kind"`
	Prompt      string      `json:"prompt,omitempty"`
	Contexts    []string    `json:"contexts,omitempty"`
	Aliases     []string    `json:"aliases,omitempty"`
	Keywords    []string    `json:"keywords,omitempty"`
}

// ResolvedPrompt holds the loaded, validated prompt content and metadata.
type ResolvedPrompt struct {
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Text    string `json:"text"`
}

// ResolvedCommand is a manifest command with its public id and resolved prompt.
type ResolvedCommand struct {
	CommandSpec
	PublicID       string          `json:"public_id"`
	ResolvedPrompt *ResolvedPrompt `json:"resolved_prompt,omitempty"`
}

type Manifest struct {
	SchemaVersion        int                               `json:"schema_version"`
	ID                   string                            `json:"id"`
	Name                 string                            `json:"name,omitempty"`
	Description          string                            `json:"description,omitempty"`
	Icon                 *IconSpec                         `json:"icon,omitempty"`
	Version              string                            `json:"version,omitempty"`
	DefaultEnabled       *bool                             `json:"default_enabled,omitempty"`
	Author               json.RawMessage                   `json:"author,omitempty"`
	Homepage             string                            `json:"homepage,omitempty"`
	Repository           string                            `json:"repository,omitempty"`
	License              string                            `json:"license,omitempty"`
	Keywords             []string                          `json:"keywords,omitempty"`
	Skills               []string                          `json:"skills,omitempty"`
	Runtime              *RuntimeSpec                      `json:"runtime,omitempty"`
	RuntimePath          string                            `json:"-"`
	Hooks                map[string][]config.HookEntry     `json:"hooks,omitempty"`
	HookPaths            []string                          `json:"-"`
	MCPServers           map[string]config.MCPServerConfig `json:"mcp_servers,omitempty"`
	MCPPaths             []string                          `json:"-"`
	Commands             []ResolvedCommand                 `json:"commands,omitempty"`
	CommandPaths         []string                          `json:"-"`
	Desktop              *DesktopSpec                      `json:"desktop,omitempty"`
	Themes               []ThemeSpec                       `json:"themes,omitempty"`
	Settings             map[string]SettingDefinition      `json:"settings,omitempty"`
	Slots                []SlotContributionSpec            `json:"slots,omitempty"`
	Surfaces             []SurfaceContributionSpec         `json:"surfaces,omitempty"`
	Presenters           []PresenterContributionSpec       `json:"presenters,omitempty"`
	Navigation           []ViewEntryContributionSpec       `json:"navigation,omitempty"`
	WorkspaceTools       []ViewEntryContributionSpec       `json:"workspace_tools,omitempty"`
	SettingsPages        []ViewEntryContributionSpec       `json:"settings_pages,omitempty"`
	Interface            json.RawMessage                   `json:"interface,omitempty"`
	Platforms            []string                          `json:"platforms,omitempty"`
	Dependencies         []Dependency                      `json:"dependencies,omitempty"`
	Requires             []string                          `json:"requires,omitempty"`
	Breaks               []string                          `json:"breaks,omitempty"`
	Conflicts            []string                          `json:"conflicts,omitempty"`
	RequestedPermissions []string                          `json:"requested_permissions,omitempty"`
	ActivityKinds        []string                          `json:"activity_kinds,omitempty"`
	OfficialNativeHelper json.RawMessage                   `json:"official_native_helper,omitempty"`
	MinimumWuuVersion    string                            `json:"minimum_wuu_version,omitempty"`
	UnsupportedFields    []string                          `json:"unsupported_fields,omitempty"`
}

func (m Manifest) EnabledByDefault() bool {
	return m.DefaultEnabled == nil || *m.DefaultEnabled
}

// DesktopSpec identifies the package-relative browser module for Desktop
// contributions. Entry is intentionally never an absolute package path.
type DesktopSpec struct {
	Entry string `json:"entry"`
}

type ThemeSpec struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Base   string            `json:"base"`
	Tokens map[string]string `json:"tokens"`
	Syntax map[string]string `json:"syntax,omitempty"`
}

type ContributionMode string

const (
	ContributionModeReplace ContributionMode = "replace"
	ContributionModeWrap    ContributionMode = "wrap"
)

type SlotContributionSpec struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	Order  int    `json:"order,omitempty"`
	Title  string `json:"title,omitempty"`
}

type SurfaceContributionSpec struct {
	ID     string           `json:"id"`
	Target string           `json:"target"`
	Mode   ContributionMode `json:"mode"`
	Order  int              `json:"order,omitempty"`
	Title  string           `json:"title,omitempty"`
}

type PresenterContributionSpec struct {
	ID       string           `json:"id"`
	Target   string           `json:"target"`
	Mode     ContributionMode `json:"mode"`
	Priority int              `json:"priority,omitempty"`
	Title    string           `json:"title,omitempty"`
}

// ViewEntryContributionSpec exposes one registered desktop View through a
// host-owned discovery surface. The plugin owns the View content; Wuu owns
// navigation, tabs, overflow, lifecycle, and the surrounding chrome.
type ViewEntryContributionSpec struct {
	ID          string    `json:"id"`
	View        string    `json:"view"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Icon        *IconSpec `json:"icon,omitempty"`
	Order       int       `json:"order,omitempty"`
}

// IconSpec is a host-rendered semantic icon or package-contained artwork.
// Exactly one of Name, Path, or the Light/Dark pair is populated after
// manifest normalization.
type IconSpec struct {
	Name  string `json:"name,omitempty"`
	Path  string `json:"path,omitempty"`
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
}

func (i *IconSpec) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &i.Name)
	}
	type iconObject IconSpec
	var value iconObject
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("must be a public icon name or an icon asset object")
	}
	*i = IconSpec(value)
	return nil
}

func (i IconSpec) AssetPaths() []string {
	if i.Path != "" {
		return []string{i.Path}
	}
	paths := make([]string, 0, 2)
	if i.Light != "" {
		paths = append(paths, i.Light)
	}
	if i.Dark != "" && i.Dark != i.Light {
		paths = append(paths, i.Dark)
	}
	return paths
}

func normalizeIcon(root, field string, raw json.RawMessage) (*IconSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var icon IconSpec
	if err := json.Unmarshal(trimmed, &icon); err != nil {
		return nil, fmt.Errorf("%s %w", field, err)
	}
	return normalizeIconValue(root, field, &icon)
}

func normalizeIconValue(root, field string, icon *IconSpec) (*IconSpec, error) {
	if icon == nil {
		return nil, nil
	}
	value := &IconSpec{
		Name: strings.TrimSpace(icon.Name), Path: strings.TrimSpace(icon.Path),
		Light: strings.TrimSpace(icon.Light), Dark: strings.TrimSpace(icon.Dark),
	}
	if value.Name != "" {
		if value.Path != "" || value.Light != "" || value.Dark != "" {
			return nil, fmt.Errorf("%s must declare exactly one public name, path, or light/dark pair", field)
		}
		if _, ok := allowedIconNames[value.Name]; !ok {
			return nil, fmt.Errorf("%s %q is not a public Wuu icon", field, value.Name)
		}
		return value, nil
	}
	if value.Path != "" {
		if value.Light != "" || value.Dark != "" {
			return nil, fmt.Errorf("%s path cannot be combined with light or dark", field)
		}
		path, err := normalizeIconAssetPath(root, field+".path", value.Path)
		if err != nil {
			return nil, err
		}
		value.Path = path
		return value, nil
	}
	if value.Light == "" || value.Dark == "" {
		return nil, fmt.Errorf("%s requires a public icon name, path, or both light and dark", field)
	}
	light, err := normalizeIconAssetPath(root, field+".light", value.Light)
	if err != nil {
		return nil, err
	}
	dark, err := normalizeIconAssetPath(root, field+".dark", value.Dark)
	if err != nil {
		return nil, err
	}
	value.Light, value.Dark = light, dark
	return value, nil
}

func normalizeIconAssetPath(root, field, value string) (string, error) {
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("%s path %q must use package-relative slash separators", field, value)
	}
	rel, err := normalizePluginPath(root, field, value)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".svg", ".png", ".webp":
	default:
		return "", fmt.Errorf("%s %q must be an SVG, PNG, or WebP image", field, value)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", field, rel, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s %s must not be a symbolic link", field, rel)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", field, rel, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %s must be a regular file", field, rel)
	}
	if info.Size() > maxPluginIconSize {
		return "", fmt.Errorf("%s %s exceeds %d bytes", field, rel, maxPluginIconSize)
	}
	if strings.EqualFold(filepath.Ext(rel), ".svg") {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", field, rel, err)
		}
		if err := validatePluginSVG(data); err != nil {
			return "", fmt.Errorf("%s %s: %w", field, rel, err)
		}
	}
	return filepath.ToSlash(rel), nil
}

func validatePluginSVG(data []byte) error {
	if bytes.Contains(bytes.ToLower(data), []byte("<!doctype")) {
		return errors.New("SVG document types are not allowed")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	seenRoot := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid SVG: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		name := strings.ToLower(start.Name.Local)
		if !seenRoot {
			if name != "svg" {
				return errors.New("root element must be svg")
			}
			seenRoot = true
		}
		switch name {
		case "script", "style", "foreignobject", "iframe", "object", "embed", "audio", "video":
			return fmt.Errorf("SVG element %s is not allowed", name)
		}
		for _, attr := range start.Attr {
			attrName := strings.ToLower(attr.Name.Local)
			attrValue := strings.TrimSpace(strings.ToLower(attr.Value))
			if strings.HasPrefix(attrName, "on") {
				return fmt.Errorf("SVG event attribute %s is not allowed", attrName)
			}
			if attrName == "href" && attrValue != "" && !strings.HasPrefix(attrValue, "#") {
				return errors.New("SVG external references are not allowed")
			}
			if (attrName == "style" || attrName == "fill" || attrName == "stroke" || attrName == "filter") && strings.Contains(attrValue, "url(") && !strings.Contains(attrValue, "url(#") {
				return errors.New("SVG external URL references are not allowed")
			}
		}
	}
	if !seenRoot {
		return errors.New("SVG root element is missing")
	}
	return nil
}

type SettingType string

const (
	SettingTypeBoolean SettingType = "boolean"
	SettingTypeString  SettingType = "string"
	SettingTypeNumber  SettingType = "number"
	SettingTypeEnum    SettingType = "enum"
)

type SettingScope string

const (
	SettingScopeUser      SettingScope = "user"
	SettingScopeWorkspace SettingScope = "workspace"
)

type SettingApplyMode string

const (
	SettingApplyLive    SettingApplyMode = "live"
	SettingApplyRestart SettingApplyMode = "restart"
)

// SettingDefinition is a validated generated-control definition. Settings are
// stored in Manifest.Settings under their plugin-qualified public ids.
type SettingDefinition struct {
	Type        SettingType      `json:"type"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Default     any              `json:"default"`
	Enum        []string         `json:"enum,omitempty"`
	Scope       SettingScope     `json:"scope"`
	Apply       SettingApplyMode `json:"apply"`
}

// RuntimeSpec declares a long-lived external plugin process. Installing or
// enabling the plugin grants this process the same user authority as Wuu.
type RuntimeSpec struct {
	Protocol string            `json:"protocol"`
	Command  string            `json:"command"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Timeout  int               `json:"timeout,omitempty"`
}

type LoadOptions struct {
	Source      string
	Official    bool
	WorkspaceID string
}

type rawManifest struct {
	SchemaVersion          json.RawMessage `json:"schemaVersion"`
	SchemaVersionAlias     json.RawMessage `json:"schema_version"`
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Description            string          `json:"description"`
	Icon                   json.RawMessage `json:"icon"`
	Version                string          `json:"version"`
	DefaultEnabled         *bool           `json:"defaultEnabled"`
	DefaultEnabledAlias    *bool           `json:"default_enabled"`
	Author                 json.RawMessage `json:"author"`
	Homepage               string          `json:"homepage"`
	Repository             string          `json:"repository"`
	License                string          `json:"license"`
	Keywords               []string        `json:"keywords"`
	Skills                 json.RawMessage `json:"skills"`
	Runtime                json.RawMessage `json:"runtime"`
	Hooks                  json.RawMessage `json:"hooks"`
	MCPServers             json.RawMessage `json:"mcpServers"`
	MCPServersAlias        json.RawMessage `json:"mcp_servers"`
	Commands               json.RawMessage `json:"commands"`
	Contributes            rawContributes  `json:"contributes"`
	Desktop                json.RawMessage `json:"desktop"`
	Interface              json.RawMessage `json:"interface"`
	Platforms              []string        `json:"platforms"`
	Dependencies           json.RawMessage `json:"dependencies"`
	Requires               []string        `json:"requires"`
	Breaks                 []string        `json:"breaks"`
	Conflicts              []string        `json:"conflicts"`
	RequestedPermissions   []string        `json:"requestedPermissions"`
	RequestedPermsAlias    []string        `json:"requested_permissions"`
	ActivityKinds          []string        `json:"activityKinds"`
	ActivityKindsAlias     []string        `json:"activity_kinds"`
	OfficialNativeHelper   json.RawMessage `json:"officialNativeHelper"`
	OfficialHelperAlias    json.RawMessage `json:"official_native_helper"`
	MinimumWuuVersion      string          `json:"minimumWuuVersion"`
	MinimumWuuVersionAlias string          `json:"minimum_wuu_version"`
}

type rawContributes struct {
	Commands       json.RawMessage `json:"commands"`
	Themes         json.RawMessage `json:"themes"`
	Settings       json.RawMessage `json:"settings"`
	Slots          json.RawMessage `json:"slots"`
	Surfaces       json.RawMessage `json:"surfaces"`
	Presenters     json.RawMessage `json:"presenters"`
	Navigation     json.RawMessage `json:"navigation"`
	WorkspaceTools json.RawMessage `json:"workspaceTools"`
	SettingsPages  json.RawMessage `json:"settingsPages"`
}

var supportedManifestFields = map[string]struct{}{
	"schemaVersion": {}, "schema_version": {},
	"id": {}, "name": {}, "description": {}, "icon": {}, "version": {}, "defaultEnabled": {}, "default_enabled": {}, "author": {},
	"homepage": {}, "repository": {}, "license": {}, "keywords": {},
	"skills": {}, "runtime": {}, "hooks": {}, "mcpServers": {}, "mcp_servers": {},
	"contributes": {}, "desktop": {}, "interface": {}, "platforms": {},
	"dependencies": {}, "requires": {}, "breaks": {}, "conflicts": {},
	"requestedPermissions": {}, "requested_permissions": {},
	"activityKinds": {}, "activity_kinds": {},
	"officialNativeHelper": {}, "official_native_helper": {},
	"minimumWuuVersion": {}, "minimum_wuu_version": {},
}

func LoadManifest(path, source string) (Plugin, error) {
	return LoadManifestWithOptions(path, LoadOptions{Source: source})
}

func LoadManifestWithOptions(path string, options LoadOptions) (Plugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plugin{}, fmt.Errorf("read plugin manifest: %w", err)
	}
	root := manifestRoot(path)
	manifest, err := normalizeManifest(data, root, options.Official)
	if err != nil {
		return Plugin{}, fmt.Errorf("parse plugin manifest %s: %w", path, err)
	}
	item := Plugin{
		Manifest:     manifest,
		Source:       strings.TrimSpace(options.Source),
		Root:         root,
		ManifestPath: path,
		Official:     options.Official,
		WorkspaceID:  strings.TrimSpace(options.WorkspaceID),
	}
	contract, err := item.PackageContract()
	if err != nil {
		return Plugin{}, fmt.Errorf("fingerprint plugin manifest %s: %w", path, err)
	}
	item.SubjectID = contract.SubjectID
	item.Fingerprint = contract.Fingerprint
	item.EffectivePermissions = contract.Permissions
	return item, nil
}

func normalizeManifest(data []byte, root string, official bool) (Manifest, error) {
	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	fields, err := decodeJSONObject("plugin manifest", data)
	if err != nil {
		return Manifest{}, err
	}
	schemaVersion, err := normalizeSchemaVersion(raw.SchemaVersion, raw.SchemaVersionAlias)
	if err != nil {
		return Manifest{}, err
	}
	if contributes, ok := fields["contributes"]; ok {
		if err := validateObjectFields("contributes", contributes, "commands", "themes", "settings", "slots", "surfaces", "presenters", "navigation", "workspaceTools", "settingsPages"); err != nil {
			return Manifest{}, err
		}
	}

	id := strings.TrimSpace(raw.ID)
	if id == "" {
		id = strings.TrimSpace(raw.Name)
	}
	if id == "" {
		return Manifest{}, fmt.Errorf("requires id or name")
	}
	requires, err := normalizePackageRelationships(id, "requires", raw.Requires)
	if err != nil {
		return Manifest{}, err
	}
	dependencies, requires, err := normalizeDependencies(id, raw.Dependencies, requires)
	if err != nil {
		return Manifest{}, err
	}
	breaks, err := normalizePackageRelationships(id, "breaks", raw.Breaks)
	if err != nil {
		return Manifest{}, err
	}
	conflicts, err := normalizePackageRelationships(id, "conflicts", raw.Conflicts)
	if err != nil {
		return Manifest{}, err
	}
	icon, err := normalizeIcon(root, "icon", raw.Icon)
	if err != nil {
		return Manifest{}, err
	}

	skills, err := normalizePathList(root, "skills", raw.Skills)
	if err != nil {
		return Manifest{}, err
	}
	runtimeSpec, runtimePath, err := normalizeRuntime(root, raw.Runtime)
	if err != nil {
		return Manifest{}, err
	}
	hooks, hookPaths, err := normalizeHooks(root, raw.Hooks)
	if err != nil {
		return Manifest{}, err
	}
	mcpServers, mcpPaths, err := normalizeMCPServers(root, firstRaw(raw.MCPServers, raw.MCPServersAlias))
	if err != nil {
		return Manifest{}, err
	}
	commands, commandPaths, err := normalizeCommands(root, id, raw.Contributes.Commands)
	if err != nil {
		return Manifest{}, err
	}
	desktop, err := normalizeDesktop(root, raw.Desktop)
	if err != nil {
		return Manifest{}, err
	}
	themes, err := normalizeThemes(raw.Contributes.Themes)
	if err != nil {
		return Manifest{}, err
	}
	settings, err := normalizeSettings(id, raw.Contributes.Settings)
	if err != nil {
		return Manifest{}, err
	}
	ui, err := normalizeUIContributions(root, raw.Contributes)
	if err != nil {
		return Manifest{}, err
	}
	helper := firstRaw(raw.OfficialNativeHelper, raw.OfficialHelperAlias)
	if hasDeclaredValue(helper) && !official {
		return Manifest{}, fmt.Errorf("official_native_helper is reserved for official bundled plugins")
	}

	requested, err := extensions.NormalizePermissions(append(raw.RequestedPermsAlias, raw.RequestedPermissions...))
	if err != nil {
		return Manifest{}, err
	}

	unsupported := make([]string, 0)
	for field := range fields {
		if _, ok := supportedManifestFields[field]; !ok {
			unsupported = append(unsupported, field)
		}
	}
	sort.Strings(unsupported)

	return Manifest{
		SchemaVersion:        schemaVersion,
		ID:                   id,
		Name:                 strings.TrimSpace(raw.Name),
		Description:          strings.TrimSpace(raw.Description),
		Icon:                 icon,
		Version:              strings.TrimSpace(raw.Version),
		DefaultEnabled:       firstBool(raw.DefaultEnabled, raw.DefaultEnabledAlias),
		Author:               cloneRaw(raw.Author),
		Homepage:             strings.TrimSpace(raw.Homepage),
		Repository:           strings.TrimSpace(raw.Repository),
		License:              strings.TrimSpace(raw.License),
		Keywords:             normalizeStrings(raw.Keywords),
		Skills:               skills,
		Runtime:              runtimeSpec,
		RuntimePath:          runtimePath,
		Hooks:                hooks,
		HookPaths:            hookPaths,
		MCPServers:           mcpServers,
		MCPPaths:             mcpPaths,
		Commands:             commands,
		CommandPaths:         commandPaths,
		Desktop:              desktop,
		Themes:               themes,
		Settings:             settings,
		Slots:                ui.slots,
		Surfaces:             ui.surfaces,
		Presenters:           ui.presenters,
		Navigation:           ui.navigation,
		WorkspaceTools:       ui.workspaceTools,
		SettingsPages:        ui.settingsPages,
		Interface:            cloneRaw(raw.Interface),
		Platforms:            normalizeStrings(raw.Platforms),
		Requires:             requires,
		Dependencies:         dependencies,
		Breaks:               breaks,
		Conflicts:            conflicts,
		RequestedPermissions: requested,
		ActivityKinds:        normalizeStrings(append(raw.ActivityKindsAlias, raw.ActivityKinds...)),
		OfficialNativeHelper: cloneRaw(helper),
		MinimumWuuVersion:    firstString(raw.MinimumWuuVersion, raw.MinimumWuuVersionAlias),
		UnsupportedFields:    unsupported,
	}, nil
}

func normalizePackageRelationships(pluginID, field string, values []string) ([]string, error) {
	normalized := normalizeStrings(values)
	for _, relatedID := range normalized {
		if err := validateInstallID(relatedID); err != nil {
			return nil, fmt.Errorf("%s contains invalid plugin id %q: %w", field, relatedID, err)
		}
		if relatedID == pluginID {
			return nil, fmt.Errorf("plugin %q cannot list itself in %s", pluginID, field)
		}
	}
	return normalized, nil
}

func normalizeSchemaVersion(primary, alias json.RawMessage) (int, error) {
	primary = bytes.TrimSpace(primary)
	alias = bytes.TrimSpace(alias)
	if len(primary) > 0 && len(alias) > 0 {
		return 0, errors.New("plugin manifest must not declare both schemaVersion and schema_version")
	}
	value := primary
	if len(value) == 0 {
		value = alias
	}
	if len(value) == 0 {
		return manifestSchemaVersion, nil
	}
	if !bytes.Equal(value, []byte("1")) {
		return 0, fmt.Errorf("unsupported plugin schema version %s (supported: %d)", value, manifestSchemaVersion)
	}
	return manifestSchemaVersion, nil
}

func normalizeDesktop(root string, raw json.RawMessage) (*DesktopSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if err := validateObjectFields("desktop", trimmed, "entry"); err != nil {
		return nil, err
	}
	var spec DesktopSpec
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return nil, fmt.Errorf("desktop: %w", err)
	}
	if strings.Contains(spec.Entry, `\`) {
		return nil, fmt.Errorf("desktop.entry path %q must use package-relative slash separators", spec.Entry)
	}
	entry, err := normalizePluginPath(root, "desktop.entry", spec.Entry)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(entry)) {
	case ".js", ".mjs":
	default:
		return nil, fmt.Errorf("desktop.entry %q must be a JavaScript module (.js or .mjs)", spec.Entry)
	}
	entryPath := filepath.Join(root, entry)
	linkInfo, err := os.Lstat(entryPath)
	if err != nil {
		return nil, fmt.Errorf("desktop.entry %s: %w", entry, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("desktop.entry %s must not be a symbolic link", entry)
	}
	info, err := os.Stat(entryPath)
	if err != nil {
		return nil, fmt.Errorf("desktop.entry %s: %w", entry, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("desktop.entry %s must be a regular file", entry)
	}
	if info.Size() > maxDesktopEntrySize {
		return nil, fmt.Errorf("desktop.entry %s exceeds %d bytes", entry, maxDesktopEntrySize)
	}
	return &DesktopSpec{Entry: entry}, nil
}

func normalizeThemes(raw json.RawMessage) ([]ThemeSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var rawThemes []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawThemes); err != nil {
		return nil, fmt.Errorf("contributes.themes must be an array: %w", err)
	}
	themes := make([]ThemeSpec, 0, len(rawThemes))
	seen := make(map[string]struct{}, len(rawThemes))
	for index, rawTheme := range rawThemes {
		field := fmt.Sprintf("contributes.themes[%d]", index)
		if err := validateObjectFields(field, rawTheme, "id", "name", "base", "tokens", "syntax"); err != nil {
			return nil, err
		}
		var theme ThemeSpec
		if err := json.Unmarshal(rawTheme, &theme); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		theme.ID = strings.TrimSpace(theme.ID)
		theme.Name = strings.TrimSpace(theme.Name)
		theme.Base = strings.TrimSpace(theme.Base)
		if !validCommandID(theme.ID) {
			return nil, fmt.Errorf("%s has invalid plugin-local id %q", field, theme.ID)
		}
		if _, ok := seen[theme.ID]; ok {
			return nil, fmt.Errorf("contributes.themes: duplicate id %q", theme.ID)
		}
		seen[theme.ID] = struct{}{}
		if theme.Name == "" {
			return nil, fmt.Errorf("%s requires name", field)
		}
		if theme.Base != "light" && theme.Base != "dark" {
			return nil, fmt.Errorf("%s base must be light or dark", field)
		}
		var err error
		theme.Tokens, err = normalizeTokenMap(field+".tokens", theme.Tokens, allowedThemeTokens, true)
		if err != nil {
			return nil, err
		}
		theme.Syntax, err = normalizeTokenMap(field+".syntax", theme.Syntax, allowedSyntaxTokens, false)
		if err != nil {
			return nil, err
		}
		themes = append(themes, theme)
	}
	sort.Slice(themes, func(i, j int) bool { return themes[i].ID < themes[j].ID })
	return themes, nil
}

var knownSlotTargets = map[string]struct{}{
	"sidebar.primary": {}, "sidebar.footer": {}, "workspace.header": {}, "conversation.header": {},
	"conversation.message.before": {}, "conversation.message.after": {}, "composer.above": {},
	"composer.toolbar": {},
}

var knownSurfaceTargets = map[string]struct{}{
	"conversation.timeline": {}, "conversation.message": {},
}

var knownPresenterTargets = map[string]struct{}{
	"conversation.item": {}, "conversation.process": {}, "conversation.tool-activity": {},
	"conversation.composer": {}, "header.conversation": {}, "header.workspace": {},
	"navigation.primary": {}, "app.status": {}, "content.preview": {}, "settings": {},
}

type normalizedUIContributions struct {
	slots          []SlotContributionSpec
	surfaces       []SurfaceContributionSpec
	presenters     []PresenterContributionSpec
	navigation     []ViewEntryContributionSpec
	workspaceTools []ViewEntryContributionSpec
	settingsPages  []ViewEntryContributionSpec
}

func normalizeUIContributions(root string, raw rawContributes) (normalizedUIContributions, error) {
	seen := make(map[string]string)
	slots, err := normalizeSlots(raw.Slots, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	surfaces, err := normalizeSurfaces(raw.Surfaces, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	presenters, err := normalizePresenters(raw.Presenters, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	navigation, err := normalizeViewEntries(root, "contributes.navigation", raw.Navigation, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	workspaceTools, err := normalizeViewEntries(root, "contributes.workspaceTools", raw.WorkspaceTools, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	settingsPages, err := normalizeViewEntries(root, "contributes.settingsPages", raw.SettingsPages, seen)
	if err != nil {
		return normalizedUIContributions{}, err
	}
	return normalizedUIContributions{
		slots: slots, surfaces: surfaces, presenters: presenters,
		navigation: navigation, workspaceTools: workspaceTools, settingsPages: settingsPages,
	}, nil
}

func normalizeViewEntries(root, field string, raw json.RawMessage, seen map[string]string) ([]ViewEntryContributionSpec, error) {
	items, err := decodeContributionArray(field, raw)
	if err != nil {
		return nil, err
	}
	out := make([]ViewEntryContributionSpec, 0, len(items))
	for index, item := range items {
		itemField := fmt.Sprintf("%s[%d]", field, index)
		if err := validateObjectFields(itemField, item, "id", "view", "title", "description", "icon", "order"); err != nil {
			return nil, err
		}
		var spec ViewEntryContributionSpec
		if err := json.Unmarshal(item, &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", itemField, err)
		}
		spec.ID = strings.TrimSpace(spec.ID)
		spec.View = strings.TrimSpace(spec.View)
		spec.Title = strings.TrimSpace(spec.Title)
		spec.Description = strings.TrimSpace(spec.Description)
		icon, err := normalizeIconValue(root, itemField+".icon", spec.Icon)
		if err != nil {
			return nil, err
		}
		spec.Icon = icon
		if spec.ID == "" {
			return nil, fmt.Errorf("%s requires id", itemField)
		}
		if previous, ok := seen[spec.ID]; ok {
			return nil, fmt.Errorf("%s has duplicate plugin-local id %q already declared by %s", itemField, spec.ID, previous)
		}
		if spec.View == "" {
			return nil, fmt.Errorf("%s requires view", itemField)
		}
		if spec.Title == "" {
			return nil, fmt.Errorf("%s requires title", itemField)
		}
		seen[spec.ID] = itemField
		out = append(out, spec)
	}
	return out, nil
}

func normalizeSlots(raw json.RawMessage, seen map[string]string) ([]SlotContributionSpec, error) {
	items, err := decodeContributionArray("contributes.slots", raw)
	if err != nil {
		return nil, err
	}
	out := make([]SlotContributionSpec, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("contributes.slots[%d]", index)
		if err := validateObjectFields(field, item, "id", "target", "order", "title"); err != nil {
			return nil, err
		}
		var spec SlotContributionSpec
		if err := json.Unmarshal(item, &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		if err := normalizeContributionIdentity(field, &spec.ID, &spec.Target, &spec.Title, knownSlotTargets, seen); err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

func normalizeSurfaces(raw json.RawMessage, seen map[string]string) ([]SurfaceContributionSpec, error) {
	items, err := decodeContributionArray("contributes.surfaces", raw)
	if err != nil {
		return nil, err
	}
	out := make([]SurfaceContributionSpec, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("contributes.surfaces[%d]", index)
		if err := validateObjectFields(field, item, "id", "target", "mode", "order", "title"); err != nil {
			return nil, err
		}
		var spec SurfaceContributionSpec
		if err := json.Unmarshal(item, &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		if err := normalizeContributionIdentity(field, &spec.ID, &spec.Target, &spec.Title, knownSurfaceTargets, seen); err != nil {
			return nil, err
		}
		if err := validateContributionMode(field, spec.Mode); err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

func normalizePresenters(raw json.RawMessage, seen map[string]string) ([]PresenterContributionSpec, error) {
	items, err := decodeContributionArray("contributes.presenters", raw)
	if err != nil {
		return nil, err
	}
	out := make([]PresenterContributionSpec, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("contributes.presenters[%d]", index)
		if err := validateObjectFields(field, item, "id", "target", "mode", "priority", "title"); err != nil {
			return nil, err
		}
		var spec PresenterContributionSpec
		if err := json.Unmarshal(item, &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		if err := normalizeContributionIdentity(field, &spec.ID, &spec.Target, &spec.Title, knownPresenterTargets, seen); err != nil {
			return nil, err
		}
		if err := validateContributionMode(field, spec.Mode); err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

func decodeContributionArray(field string, raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("%s must be an array: %w", field, err)
	}
	return items, nil
}

func normalizeContributionIdentity(field string, id, target, title *string, knownTargets map[string]struct{}, seen map[string]string) error {
	*id = strings.TrimSpace(*id)
	*target = strings.TrimSpace(*target)
	*title = strings.TrimSpace(*title)
	if *id == "" {
		return fmt.Errorf("%s requires id", field)
	}
	if previous, ok := seen[*id]; ok {
		return fmt.Errorf("%s has duplicate plugin-local id %q already declared by %s", field, *id, previous)
	}
	if _, ok := knownTargets[*target]; !ok {
		return fmt.Errorf("%s has unknown target %q", field, *target)
	}
	seen[*id] = field
	return nil
}

func validateContributionMode(field string, mode ContributionMode) error {
	if mode != ContributionModeReplace && mode != ContributionModeWrap {
		return fmt.Errorf("%s mode must be replace or wrap", field)
	}
	return nil
}

func normalizeTokenMap(field string, values map[string]string, allowed map[string]struct{}, required bool) (map[string]string, error) {
	if len(values) == 0 {
		if required {
			return nil, fmt.Errorf("%s must be a non-empty semantic token map", field)
		}
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("%s contains unsupported semantic token %q", field, key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s.%s must be a non-empty string", field, key)
		}
		out[key] = value
	}
	return out, nil
}

type rawSettingDefinition struct {
	Type        SettingType      `json:"type"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Default     json.RawMessage  `json:"default"`
	Enum        []string         `json:"enum"`
	Scope       SettingScope     `json:"scope"`
	Apply       SettingApplyMode `json:"apply"`
}

func normalizeSettings(pluginID string, raw json.RawMessage) (map[string]SettingDefinition, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	entries, err := decodeJSONObject("contributes.settings", trimmed)
	if err != nil {
		return nil, err
	}
	settings := make(map[string]SettingDefinition, len(entries))
	for localID, rawDefinition := range entries {
		if !validSettingID(localID) {
			return nil, fmt.Errorf("contributes.settings contains invalid plugin-local id %q", localID)
		}
		field := "contributes.settings." + localID
		if err := validateObjectFields(field, rawDefinition, "type", "title", "description", "default", "enum", "scope", "apply"); err != nil {
			return nil, err
		}
		var rawSetting rawSettingDefinition
		if err := json.Unmarshal(rawDefinition, &rawSetting); err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		definition, err := normalizeSettingDefinition(field, rawSetting)
		if err != nil {
			return nil, err
		}
		settings[pluginID+"."+localID] = definition
	}
	if len(settings) == 0 {
		return nil, nil
	}
	return settings, nil
}

func normalizeSettingDefinition(field string, raw rawSettingDefinition) (SettingDefinition, error) {
	raw.Title = strings.TrimSpace(raw.Title)
	raw.Description = strings.TrimSpace(raw.Description)
	if raw.Title == "" {
		return SettingDefinition{}, fmt.Errorf("%s requires title", field)
	}
	switch raw.Type {
	case SettingTypeBoolean, SettingTypeString, SettingTypeNumber, SettingTypeEnum:
	default:
		return SettingDefinition{}, fmt.Errorf("%s has unsupported type %q", field, raw.Type)
	}
	if raw.Scope != SettingScopeUser && raw.Scope != SettingScopeWorkspace {
		return SettingDefinition{}, fmt.Errorf("%s scope must be user or workspace", field)
	}
	if raw.Apply != SettingApplyLive && raw.Apply != SettingApplyRestart {
		return SettingDefinition{}, fmt.Errorf("%s apply must be live or restart", field)
	}
	defaultValue, err := decodeJSONDefault(raw.Default)
	if err != nil {
		return SettingDefinition{}, fmt.Errorf("%s default: %w", field, err)
	}
	switch raw.Type {
	case SettingTypeBoolean:
		if _, ok := defaultValue.(bool); !ok {
			return SettingDefinition{}, fmt.Errorf("%s default must be boolean", field)
		}
	case SettingTypeString:
		if _, ok := defaultValue.(string); !ok {
			return SettingDefinition{}, fmt.Errorf("%s default must be string", field)
		}
	case SettingTypeNumber:
		if _, ok := defaultValue.(json.Number); !ok {
			return SettingDefinition{}, fmt.Errorf("%s default must be number", field)
		}
	case SettingTypeEnum:
		value, ok := defaultValue.(string)
		if !ok {
			return SettingDefinition{}, fmt.Errorf("%s default must be a string enum value", field)
		}
		choices, err := normalizeEnumChoices(field+".enum", raw.Enum)
		if err != nil {
			return SettingDefinition{}, err
		}
		raw.Enum = choices
		if !containsString(choices, value) {
			return SettingDefinition{}, fmt.Errorf("%s default %q is not an enum choice", field, value)
		}
	}
	if raw.Type != SettingTypeEnum && len(raw.Enum) > 0 {
		return SettingDefinition{}, fmt.Errorf("%s enum choices require type enum", field)
	}
	return SettingDefinition{
		Type: raw.Type, Title: raw.Title, Description: raw.Description, Default: defaultValue,
		Enum: raw.Enum, Scope: raw.Scope, Apply: raw.Apply,
	}, nil
}

func decodeJSONDefault(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if number, ok := value.(json.Number); ok {
		if _, err := number.Float64(); err != nil {
			return nil, errors.New("must be a finite JSON number")
		}
	}
	if value == nil {
		return nil, errors.New("must match the setting type")
	}
	return value, nil
}

func normalizeEnumChoices(field string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must contain at least one choice", field)
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s contains an empty choice", field)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%s contains duplicate choice %q", field, value)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func validSettingID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if !validCommandID(segment) {
			return false
		}
	}
	return true
}

func validateObjectFields(field string, raw json.RawMessage, allowed ...string) error {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	fields, err := decodeJSONObject(field, trimmed)
	if err != nil {
		return err
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name := range fields {
		if _, ok := allowedSet[name]; !ok {
			return fmt.Errorf("%s contains unknown field %q", field, name)
		}
	}
	return nil
}

func decodeJSONObject(field string, raw json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", field, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	out := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%s contains an invalid field name", field)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("%s contains duplicate id or field %q", field, name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", field, name, err)
		}
		out[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return out, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("must contain one JSON value")
		}
		return err
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func normalizeRuntime(root string, raw json.RawMessage) (*RuntimeSpec, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, "", nil
	}
	var spec RuntimeSpec
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return nil, "", fmt.Errorf("runtime: %w", err)
	}
	spec.Protocol = strings.TrimSpace(spec.Protocol)
	spec.Command = strings.TrimSpace(spec.Command)
	if spec.Protocol != pluginhost.ProtocolName {
		return nil, "", fmt.Errorf("runtime protocol must be %q", pluginhost.ProtocolName)
	}
	if spec.Command == "" {
		return nil, "", fmt.Errorf("runtime command is required")
	}
	if spec.Timeout < 0 {
		return nil, "", fmt.Errorf("runtime timeout must be positive")
	}
	var runtimePath string
	if strings.ContainsRune(spec.Command, filepath.Separator) && !filepath.IsAbs(spec.Command) {
		command, err := normalizePluginPath(root, "runtime.command", spec.Command)
		if err != nil {
			return nil, "", err
		}
		runtimePath = command
		spec.Command = filepath.Join(root, command)
	}
	spec.Args = append([]string(nil), spec.Args...)
	if len(spec.Env) > 0 {
		env := make(map[string]string, len(spec.Env))
		for key, value := range spec.Env {
			key = strings.TrimSpace(key)
			if key == "" || strings.Contains(key, "=") {
				return nil, "", fmt.Errorf("runtime env contains invalid name %q", key)
			}
			env[key] = value
		}
		spec.Env = env
	}
	return &spec, runtimePath, nil
}

func normalizePathList(root, field string, raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		path, err := normalizePluginPath(root, field, one)
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("%s must be a string or string array", field)
	}
	out := make([]string, 0, len(many))
	for _, value := range many {
		path, err := normalizePluginPath(root, field, value)
		if err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return normalizeStrings(out), nil
}

func normalizeHooks(root string, raw json.RawMessage) (map[string][]config.HookEntry, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var out map[string][]config.HookEntry
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, nil, fmt.Errorf("hooks: %w", err)
		}
		return out, nil, nil
	}
	paths, err := normalizePathList(root, "hooks", raw)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string][]config.HookEntry)
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, nil, fmt.Errorf("read hooks %s: %w", path, err)
		}
		var wrapper struct {
			Hooks map[string][]config.HookEntry `json:"hooks"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, nil, fmt.Errorf("parse hooks %s: %w", path, err)
		}
		for event, entries := range wrapper.Hooks {
			out[event] = append(out[event], entries...)
		}
	}
	return out, paths, nil
}

func normalizeMCPServers(root string, raw json.RawMessage) (map[string]config.MCPServerConfig, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var out map[string]config.MCPServerConfig
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, nil, fmt.Errorf("mcpServers: %w", err)
		}
		return out, nil, nil
	}
	var ref string
	if err := json.Unmarshal(trimmed, &ref); err != nil {
		return nil, nil, fmt.Errorf("mcpServers must be an object or path")
	}
	path, err := normalizePluginPath(root, "mcpServers", ref)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, nil, fmt.Errorf("read mcpServers %s: %w", path, err)
	}
	var wrapper struct {
		MCPServers map[string]config.MCPServerConfig `json:"mcpServers"`
		Alias      map[string]config.MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, nil, fmt.Errorf("parse mcpServers %s: %w", path, err)
	}
	if len(wrapper.MCPServers) > 0 {
		return wrapper.MCPServers, []string{path}, nil
	}
	return wrapper.Alias, []string{path}, nil
}

func normalizeCommands(root, pluginID string, raw json.RawMessage) ([]ResolvedCommand, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil
	}
	var specs []CommandSpec
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &specs); err != nil {
			return nil, nil, fmt.Errorf("commands: %w", err)
		}
	} else if len(trimmed) > 0 && trimmed[0] == '{' {
		var wrapper struct {
			Commands []CommandSpec `json:"commands"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err == nil && len(wrapper.Commands) > 0 {
			specs = wrapper.Commands
		} else {
			var single CommandSpec
			if err := json.Unmarshal(trimmed, &single); err != nil {
				return nil, nil, fmt.Errorf("commands: %w", err)
			}
			specs = []CommandSpec{single}
		}
	} else {
		var ref string
		if err := json.Unmarshal(trimmed, &ref); err != nil {
			return nil, nil, fmt.Errorf("commands must be an array, object, or path")
		}
		path, err := normalizePluginPath(root, "commands", ref)
		if err != nil {
			return nil, nil, err
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, nil, fmt.Errorf("read commands %s: %w", path, err)
		}
		resolved, nestedPaths, err := normalizeCommands(root, pluginID, data)
		if err != nil {
			return nil, nil, fmt.Errorf("parse commands %s: %w", path, err)
		}
		return resolved, append([]string{path}, nestedPaths...), nil
	}

	var out []ResolvedCommand
	var paths []string
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if err := validateCommandSpec(spec); err != nil {
			return nil, nil, fmt.Errorf("commands: %w", err)
		}
		if _, ok := seen[spec.ID]; ok {
			return nil, nil, fmt.Errorf("commands: duplicate id %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}

		resolved := ResolvedCommand{
			CommandSpec: CommandSpec{
				ID:          spec.ID,
				Title:       strings.TrimSpace(spec.Title),
				Description: strings.TrimSpace(spec.Description),
				Kind:        commandKindFromString(string(spec.Kind)),
				Contexts:    normalizeStrings(spec.Contexts),
				Aliases:     normalizeStrings(spec.Aliases),
				Keywords:    normalizeStrings(spec.Keywords),
			},
			PublicID: pluginID + "." + spec.ID,
		}
		switch resolved.Kind {
		case CommandKindPromptTemplate:
			if strings.TrimSpace(spec.Prompt) == "" {
				return nil, nil, fmt.Errorf("commands: prompt_template %q requires prompt", spec.ID)
			}
			promptPath, err := normalizePluginPath(root, "commands.prompt", spec.Prompt)
			if err != nil {
				return nil, nil, err
			}
			prompt, err := readPromptFile(root, promptPath)
			if err != nil {
				return nil, nil, fmt.Errorf("commands: %w", err)
			}
			resolved.Prompt = promptPath
			resolved.ResolvedPrompt = prompt
			paths = append(paths, promptPath)
		case CommandKindRuntimeAction:
			// Desktop activation resolves this descriptor against a registered
			// command with the same plugin and command id.
		default:
			return nil, nil, fmt.Errorf("commands: unknown kind %q for %q", spec.Kind, spec.ID)
		}
		out = append(out, resolved)
	}
	return out, paths, nil
}

func validateCommandSpec(spec CommandSpec) error {
	if !validCommandID(spec.ID) {
		return fmt.Errorf("invalid command id %q", spec.ID)
	}
	if strings.TrimSpace(spec.Title) == "" {
		return fmt.Errorf("command %q requires title", spec.ID)
	}
	if commandKindFromString(string(spec.Kind)) == "" {
		return fmt.Errorf("command %q requires kind prompt_template or runtime_action", spec.ID)
	}
	return nil
}

func commandKindFromString(value string) CommandKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(CommandKindPromptTemplate):
		return CommandKindPromptTemplate
	case string(CommandKindRuntimeAction):
		return CommandKindRuntimeAction
	}
	return ""
}

func validCommandID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func readPromptFile(root, rel string) (*ResolvedPrompt, error) {
	path := filepath.Join(root, rel)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", rel, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("prompt %s is a directory", rel)
	}
	if info.Size() > maxPromptFileSize {
		return nil, fmt.Errorf("prompt %s exceeds %d bytes", rel, maxPromptFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("prompt %s: %w", rel, err)
	}
	if !utf8.ValidString(string(data)) {
		return nil, fmt.Errorf("prompt %s is not valid UTF-8", rel)
	}
	sum := sha256.Sum256(data)
	return &ResolvedPrompt{
		Path:    path,
		RelPath: rel,
		Size:    info.Size(),
		SHA256:  hex.EncodeToString(sum[:]),
		Text:    string(data),
	}, nil
}

func normalizePluginPath(root, field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return "", fmt.Errorf("%s path %q must remain within plugin root %s", field, value, root)
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path %q must remain within plugin root %s", field, value, root)
	}
	if err := ensureResolvedPathWithinRoot(root, filepath.Join(root, cleaned)); err != nil {
		return "", fmt.Errorf("%s path %q must remain within plugin root %s: %w", field, value, root, err)
	}
	return cleaned, nil
}

func ensureResolvedPathWithinRoot(root, candidate string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return err
	}
	probe := candidate
	var suffix []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(probe)
		if resolveErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(realRoot, resolved)
			if err != nil {
				return err
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return errors.New("resolved path escapes plugin root")
			}
			return nil
		}
		if !os.IsNotExist(resolveErr) {
			return resolveErr
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return resolveErr
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func manifestRoot(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(dir)
	if base == ".codex-plugin" || base == ".claude-plugin" {
		return filepath.Dir(dir)
	}
	return dir
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(bytes.TrimSpace(value)) > 0 {
			return value
		}
	}
	return nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			copy := *value
			return &copy
		}
	}
	return nil
}

func hasDeclaredValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("false"))
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func normalizeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
