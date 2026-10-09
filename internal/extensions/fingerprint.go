package extensions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{[^}]+\}`)

// ExecutableSpec is the fingerprint input for a single executable surface such
// as an MCP server. Secret values are redacted so credential rotation does
// not invalidate a grant.
type ExecutableSpec struct {
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	URL         string            `json:"url,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
}

// Fingerprint returns a deterministic SHA-256 hex digest of a normalized
// executable spec with secret values redacted.
func Fingerprint(spec ExecutableSpec) (string, error) {
	normalized := ExecutableSpec{
		Command:     strings.TrimSpace(spec.Command),
		Args:        append([]string(nil), spec.Args...),
		URL:         strings.TrimSpace(spec.URL),
		Env:         redactSecretMap(spec.Env),
		Headers:     redactSecretMap(spec.Headers),
		Permissions: normalizedStrings(spec.Permissions),
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// SubjectID returns the canonical policy identifier for a package. The format
// is "plugin:<source>:<id>" where source is bundled, user, or project.
func SubjectID(source, id string) string {
	return "plugin:" + strings.TrimSpace(source) + ":" + strings.TrimSpace(id)
}

// RuntimeSpec is a deterministic, secret-free fingerprint input for a plugin
// runtime declaration.
type RuntimeSpec struct {
	Protocol string   `json:"protocol,omitempty"`
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Timeout  int      `json:"timeout,omitempty"`
	EnvNames []string `json:"env_names,omitempty"`
}

// MCPServerSpec is a deterministic, secret-free fingerprint input for an MCP
// server declaration.
type MCPServerSpec struct {
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	URL         string   `json:"url,omitempty"`
	Transport   string   `json:"transport,omitempty"`
	EnvNames    []string `json:"env_names,omitempty"`
	HeaderNames []string `json:"header_names,omitempty"`
}

// HookEntry is a deterministic fingerprint input for a single hook entry.
type HookEntry struct {
	Matcher string `json:"matcher,omitempty"`
	Type    string `json:"type,omitempty"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Model   string `json:"model,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// CommandKind enumerates supported command contribution kinds.
type CommandKind string

const (
	// CommandKindPromptTemplate loads a host-owned prompt template from the
	// plugin root. It is executable only by the host composer.
	CommandKindPromptTemplate CommandKind = "prompt_template"
	// CommandKindRuntimeAction is executable only through an approved desktop
	// command registration with the same plugin and command id.
	CommandKindRuntimeAction CommandKind = "runtime_action"
)

// CommandSpec is a deterministic fingerprint input for a command descriptor.
type CommandSpec struct {
	ID          string      `json:"id"`
	PublicID    string      `json:"public_id,omitempty"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Kind        CommandKind `json:"kind"`
	Prompt      string      `json:"prompt,omitempty"`
	Contexts    []string    `json:"contexts,omitempty"`
	Aliases     []string    `json:"aliases,omitempty"`
	Keywords    []string    `json:"keywords,omitempty"`
}

// PackageSpec is the deterministic fingerprint input for an entire plugin
// package. It intentionally omits secret values and includes only normalized
// manifest fields, surface descriptors, and hashes of referenced entry files.
// PackageDependency is a package relationship, not an installation or trust grant.
type PackageDependency struct {
	ID       string `json:"id"`
	Version  string `json:"version,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type PackageSpec struct {
	ID                   string                   `json:"id"`
	Source               string                   `json:"source"`
	Scope                string                   `json:"scope"`
	Official             bool                     `json:"official,omitempty"`
	Name                 string                   `json:"name,omitempty"`
	Description          string                   `json:"description,omitempty"`
	Version              string                   `json:"version,omitempty"`
	Keywords             []string                 `json:"keywords,omitempty"`
	Skills               []string                 `json:"skills,omitempty"`
	Runtime              *RuntimeSpec             `json:"runtime,omitempty"`
	Hooks                map[string][]HookEntry   `json:"hooks,omitempty"`
	MCPServers           map[string]MCPServerSpec `json:"mcp_servers,omitempty"`
	Commands             []CommandSpec            `json:"commands,omitempty"`
	RequestedPermissions []string                 `json:"requested_permissions,omitempty"`
	ActivityKinds        []string                 `json:"activity_kinds,omitempty"`
	MinimumWuuVersion    string                   `json:"minimum_wuu_version,omitempty"`
	Dependencies         []PackageDependency      `json:"dependencies,omitempty"`
	Requires             []string                 `json:"requires,omitempty"`
	Breaks               []string                 `json:"breaks,omitempty"`
	Conflicts            []string                 `json:"conflicts,omitempty"`
	EntryHashes          map[string]string        `json:"entry_hashes,omitempty"`
}

// ComputeFingerprint returns a deterministic SHA-256 hex digest of a package
// spec. Secret values are never included.
func ComputeFingerprint(spec PackageSpec) (string, error) {
	normalized := normalizePackageSpec(spec)
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// RequestedPermissionUnion returns the union of the manifest's requested
// permissions and the permissions required by each declared runtime/MCP/hook/
// command/skill surface. The result is sorted and deduplicated.
func RequestedPermissionUnion(spec PackageSpec) []string {
	out := append([]string(nil), spec.RequestedPermissions...)
	out = append(out, RequiredPermissionsForPackageSpec(spec)...)
	return normalizedStrings(out)
}

func normalizePackageSpec(spec PackageSpec) PackageSpec {
	normalized := PackageSpec{
		ID:                   strings.TrimSpace(spec.ID),
		Source:               strings.TrimSpace(spec.Source),
		Scope:                strings.TrimSpace(spec.Scope),
		Official:             spec.Official,
		Name:                 strings.TrimSpace(spec.Name),
		Description:          strings.TrimSpace(spec.Description),
		Version:              strings.TrimSpace(spec.Version),
		Keywords:             normalizedStrings(spec.Keywords),
		Skills:               normalizedStrings(spec.Skills),
		RequestedPermissions: normalizedStrings(spec.RequestedPermissions),
		ActivityKinds:        normalizedStrings(spec.ActivityKinds),
		MinimumWuuVersion:    strings.TrimSpace(spec.MinimumWuuVersion),
		Requires:             normalizedStrings(spec.Requires),
		Dependencies:         append([]PackageDependency(nil), spec.Dependencies...),
		Breaks:               normalizedStrings(spec.Breaks),
		Conflicts:            normalizedStrings(spec.Conflicts),
	}
	sort.Slice(normalized.Dependencies, func(i, j int) bool { return normalized.Dependencies[i].ID < normalized.Dependencies[j].ID })
	if spec.Runtime != nil {
		normalized.Runtime = &RuntimeSpec{
			Protocol: strings.TrimSpace(spec.Runtime.Protocol),
			Command:  strings.TrimSpace(spec.Runtime.Command),
			Args:     append([]string(nil), spec.Runtime.Args...),
			Timeout:  spec.Runtime.Timeout,
			EnvNames: normalizedStrings(spec.Runtime.EnvNames),
		}
	}
	if len(spec.Hooks) > 0 {
		normalized.Hooks = make(map[string][]HookEntry, len(spec.Hooks))
		var events []string
		for event := range spec.Hooks {
			events = append(events, event)
		}
		sort.Strings(events)
		for _, event := range events {
			entries := spec.Hooks[event]
			out := make([]HookEntry, len(entries))
			for i, entry := range entries {
				out[i] = HookEntry{
					Matcher: strings.TrimSpace(entry.Matcher),
					Type:    strings.TrimSpace(entry.Type),
					Command: strings.TrimSpace(entry.Command),
					Prompt:  strings.TrimSpace(entry.Prompt),
					Model:   strings.TrimSpace(entry.Model),
					Timeout: entry.Timeout,
				}
			}
			normalized.Hooks[event] = out
		}
	}
	if len(spec.MCPServers) > 0 {
		normalized.MCPServers = make(map[string]MCPServerSpec, len(spec.MCPServers))
		var names []string
		for name := range spec.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			server := spec.MCPServers[name]
			normalized.MCPServers[name] = MCPServerSpec{
				Command:     strings.TrimSpace(server.Command),
				Args:        append([]string(nil), server.Args...),
				URL:         strings.TrimSpace(server.URL),
				Transport:   strings.TrimSpace(server.Transport),
				EnvNames:    normalizedStrings(server.EnvNames),
				HeaderNames: normalizedStrings(server.HeaderNames),
			}
		}
	}
	if len(spec.Commands) > 0 {
		normalized.Commands = make([]CommandSpec, len(spec.Commands))
		copy(normalized.Commands, spec.Commands)
		for i := range normalized.Commands {
			normalized.Commands[i].ID = strings.TrimSpace(normalized.Commands[i].ID)
			normalized.Commands[i].PublicID = strings.TrimSpace(normalized.Commands[i].PublicID)
			normalized.Commands[i].Title = strings.TrimSpace(normalized.Commands[i].Title)
			normalized.Commands[i].Description = strings.TrimSpace(normalized.Commands[i].Description)
			normalized.Commands[i].Kind = CommandKind(strings.TrimSpace(string(normalized.Commands[i].Kind)))
			normalized.Commands[i].Prompt = strings.TrimSpace(normalized.Commands[i].Prompt)
			normalized.Commands[i].Contexts = normalizedStrings(normalized.Commands[i].Contexts)
			normalized.Commands[i].Aliases = normalizedStrings(normalized.Commands[i].Aliases)
			normalized.Commands[i].Keywords = normalizedStrings(normalized.Commands[i].Keywords)
		}
		sort.Slice(normalized.Commands, func(i, j int) bool {
			return normalized.Commands[i].ID < normalized.Commands[j].ID
		})
	}
	if len(spec.EntryHashes) > 0 {
		normalized.EntryHashes = make(map[string]string, len(spec.EntryHashes))
		var keys []string
		for key := range spec.EntryHashes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalized.EntryHashes[key] = spec.EntryHashes[key]
		}
	}
	return normalized
}

func redactSecretMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		placeholders := envPlaceholderPattern.FindAllString(value, -1)
		if len(placeholders) == 0 {
			out[key] = "<redacted>"
			continue
		}
		out[key] = strings.Join(placeholders, ",")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
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
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
