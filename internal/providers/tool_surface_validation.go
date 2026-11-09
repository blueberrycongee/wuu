package providers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	openAIToolSchemaMaxBytes    = 128 * 1024
	anthropicToolSchemaMaxBytes = 256 * 1024
	localToolSchemaMaxBytes     = 512 * 1024
)

var providerToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type ToolSurfaceValidationTarget struct {
	ProviderKind      string
	ProviderName      string
	Model             string
	ReservedToolNames []string
}

type ToolSurfaceValidationProblem struct {
	ToolName string `json:"tool_name,omitempty"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Bytes    int    `json:"bytes,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type ToolSurfaceValidationError struct {
	Target   ToolSurfaceValidationTarget    `json:"target"`
	Problems []ToolSurfaceValidationProblem `json:"problems"`
}

func (e *ToolSurfaceValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "tool surface validation failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tool surface is invalid for provider %q model %q", strings.TrimSpace(e.Target.ProviderName), strings.TrimSpace(e.Target.Model))
	maxProblems := len(e.Problems)
	if maxProblems > 4 {
		maxProblems = 4
	}
	for i := 0; i < maxProblems; i++ {
		p := e.Problems[i]
		if p.ToolName != "" {
			fmt.Fprintf(&b, "; tool %q %s", p.ToolName, p.Message)
		} else {
			fmt.Fprintf(&b, "; %s", p.Message)
		}
	}
	if omitted := len(e.Problems) - maxProblems; omitted > 0 {
		fmt.Fprintf(&b, "; %d more problem(s)", omitted)
	}
	return b.String()
}

func ValidateToolDefinitionsForProvider(target ToolSurfaceValidationTarget, defs []ToolDefinition) error {
	var problems []ToolSurfaceValidationProblem
	seen := make(map[string]struct{}, len(defs))
	reservedNames := make(map[string]struct{}, len(target.ReservedToolNames))
	for _, reserved := range target.ReservedToolNames {
		if reserved = strings.ToLower(strings.TrimSpace(reserved)); reserved != "" {
			reservedNames[reserved] = struct{}{}
		}
	}
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			problems = append(problems, ToolSurfaceValidationProblem{
				Field:   "name",
				Code:    "empty_tool_name",
				Message: "has empty tool name",
			})
			continue
		}
		if _, exists := seen[name]; exists {
			problems = append(problems, ToolSurfaceValidationProblem{
				ToolName: name,
				Field:    "name",
				Code:     "duplicate_tool_name",
				Message:  "duplicates another exposed tool name",
			})
		}
		seen[name] = struct{}{}
		if _, reserved := reservedNames[strings.ToLower(name)]; reserved {
			problems = append(problems, ToolSurfaceValidationProblem{
				ToolName: name,
				Field:    "name",
				Code:     "reserved_provider_tool_name",
				Message:  "uses a tool name reserved by this provider",
			})
		}
		if requiresProviderToolNamePattern(target) && !providerToolNamePattern.MatchString(name) {
			problems = append(problems, ToolSurfaceValidationProblem{
				ToolName: name,
				Field:    "name",
				Code:     "invalid_provider_tool_name",
				Message:  "name must match ^[a-zA-Z0-9_-]{1,64}$ for this provider",
			})
		}
		schema := ToolInputSchemaForModel(target.Model, def.InputSchema)
		raw, err := json.Marshal(schema)
		if err != nil {
			problems = append(problems, ToolSurfaceValidationProblem{
				ToolName: name,
				Field:    "input_schema",
				Code:     "schema_not_serializable",
				Message:  "input schema is not JSON serializable",
			})
			continue
		}
		if limit := providerToolSchemaMaxBytes(target); limit > 0 && len(raw) > limit {
			problems = append(problems, ToolSurfaceValidationProblem{
				ToolName: name,
				Field:    "input_schema",
				Code:     "schema_too_large",
				Message:  fmt.Sprintf("input schema is %d bytes after provider normalization, over provider surface budget %d bytes", len(raw), limit),
				Bytes:    len(raw),
				Limit:    limit,
			})
		}
	}
	if len(problems) > 0 {
		return &ToolSurfaceValidationError{Target: target, Problems: problems}
	}
	return nil
}

func requiresProviderToolNamePattern(target ToolSurfaceValidationTarget) bool {
	switch normalizedProviderKind(target) {
	case "openai", "openai-compatible", "anthropic":
		return true
	default:
		return false
	}
}

func providerToolSchemaMaxBytes(target ToolSurfaceValidationTarget) int {
	switch normalizedProviderKind(target) {
	case "openai", "openai-compatible":
		return openAIToolSchemaMaxBytes
	case "anthropic":
		return anthropicToolSchemaMaxBytes
	case "local", "ollama", "lmstudio":
		return localToolSchemaMaxBytes
	default:
		return localToolSchemaMaxBytes
	}
}

func normalizedProviderKind(target ToolSurfaceValidationTarget) string {
	kind := strings.ToLower(strings.TrimSpace(target.ProviderKind))
	if kind != "" {
		return kind
	}
	return strings.ToLower(strings.TrimSpace(target.ProviderName))
}
