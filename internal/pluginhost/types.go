package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	maxToolRegistrations   = 64
	maxToolDescriptionLen  = 16 * 1024
	maxToolSchemaLen       = 128 * 1024
	maxToolMetadataLen     = 1024
	maxToolDisplayLabelLen = 80
)

var localToolIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var toolLabelLocalePattern = regexp.MustCompile(`^[a-zA-Z]{2,8}(-[a-zA-Z0-9]{1,8})*$`)

// InitializeParams describes the runtime instance offered to a plugin.
type InitializeParams struct {
	ProtocolVersion   int    `json:"protocol_version"`
	PluginID          string `json:"plugin_id"`
	PluginRoot        string `json:"plugin_root"`
	ProjectRoot       string `json:"project_root"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	WuuHome           string `json:"wuu_home"`
	WorkspaceStateDir string `json:"workspace_state_dir,omitempty"`
}

// InitializeResult declares the interception points and tools implemented by a
// plugin. Tool IDs are local to the declaring plugin; Wuu assigns the public
// model-visible names during registration.
type InitializeResult struct {
	Tools []ToolRegistration `json:"tools,omitempty"`
}

// ToolRegistration is one model-visible tool owned by a plugin process.
type ToolRegistration struct {
	ID              string                     `json:"id"`
	Description     string                     `json:"description"`
	InputSchema     map[string]any             `json:"input_schema"`
	ExecutionScopes []string                   `json:"execution_scopes,omitempty"`
	Activity        *ToolActivityMetadata      `json:"activity,omitempty"`
	Display         *providers.ToolCallDisplay `json:"display,omitempty"`
}

// ToolActivityMetadata controls scheduling and activity classification for a
// registered tool. It is host metadata and is not included in provider tool
// definitions.
type ToolActivityMetadata struct {
	// Orchestrator delegates effects through execution.invoke-tool rather than
	// holding a leaf scheduling slot while waiting for its children.
	Orchestrator    bool   `json:"orchestrator,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	ConcurrencySafe bool   `json:"concurrency_safe,omitempty"`
	Destructive     bool   `json:"destructive,omitempty"`
	Risk            string `json:"risk,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

// ToolExecuteParams carries a registered tool call over the plugin protocol.
// ToolID is the plugin-local registration ID; Tool in the embedded input is
// the public model-visible name.
type ToolExecuteParams struct {
	ToolExecuteInput
	ToolID string `json:"tool_id"`
}

// ToolExecuteResult is the structured result returned by tool.execute.
type ToolExecuteResult struct {
	Result toolresult.Result `json:"result"`
}

func validateToolRegistrations(tools []ToolRegistration) error {
	if len(tools) > maxToolRegistrations {
		return fmt.Errorf("declares %d tools, limit is %d", len(tools), maxToolRegistrations)
	}
	seen := make(map[string]struct{}, len(tools))
	for index, tool := range tools {
		prefix := fmt.Sprintf("tool[%d]", index)
		if !localToolIDPattern.MatchString(tool.ID) {
			return fmt.Errorf("%s id %q must match ^[a-zA-Z0-9_-]{1,64}$", prefix, tool.ID)
		}
		if _, ok := seen[tool.ID]; ok {
			return fmt.Errorf("duplicate tool id %q", tool.ID)
		}
		seen[tool.ID] = struct{}{}
		description := strings.TrimSpace(tool.Description)
		if description == "" {
			return fmt.Errorf("%s description is required", prefix)
		}
		if len(tool.Description) > maxToolDescriptionLen {
			return fmt.Errorf("%s description exceeds %d bytes", prefix, maxToolDescriptionLen)
		}
		if tool.InputSchema == nil {
			return fmt.Errorf("%s input_schema is required", prefix)
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return fmt.Errorf("%s input_schema is not valid JSON: %w", prefix, err)
		}
		if len(raw) > maxToolSchemaLen {
			return fmt.Errorf("%s input_schema exceeds %d bytes", prefix, maxToolSchemaLen)
		}
		if schemaType, ok := tool.InputSchema["type"]; !ok || schemaType != "object" {
			return fmt.Errorf("%s input_schema type must be %q", prefix, "object")
		}
		scopes := make(map[string]struct{}, len(tool.ExecutionScopes))
		for _, scope := range tool.ExecutionScopes {
			scope = strings.TrimSpace(scope)
			if scope != "root" && scope != "child" && scope != "collaboration" {
				return fmt.Errorf("%s execution scope %q must be root, child or collaboration", prefix, scope)
			}
			if _, duplicate := scopes[scope]; duplicate {
				return fmt.Errorf("%s repeats execution scope %q", prefix, scope)
			}
			scopes[scope] = struct{}{}
		}
		if tool.Activity != nil {
			if err := validateBoundedMetadata("activity.risk", tool.Activity.Risk); err != nil {
				return fmt.Errorf("%s: %w", prefix, err)
			}
			if err := validateBoundedMetadata("activity.reason", tool.Activity.Reason); err != nil {
				return fmt.Errorf("%s: %w", prefix, err)
			}
		}
		if tool.Display != nil {
			if strings.TrimSpace(tool.Display.Kind) == "" && strings.TrimSpace(tool.Display.Label) == "" && strings.TrimSpace(tool.Display.Text) == "" && strings.TrimSpace(tool.Display.Capability) == "" {
				return fmt.Errorf("%s display metadata is empty", prefix)
			}
			if len(tool.Display.Label) > maxToolDisplayLabelLen {
				return fmt.Errorf("%s: display.label exceeds %d bytes", prefix, maxToolDisplayLabelLen)
			}
			if len(tool.Display.LabelTranslations) > 0 && strings.TrimSpace(tool.Display.Label) == "" {
				return fmt.Errorf("%s: display.label is required with label_translations", prefix)
			}
			if len(tool.Display.LabelTranslations) > 32 {
				return fmt.Errorf("%s: display.label_translations exceeds 32 locales", prefix)
			}
			for locale, label := range tool.Display.LabelTranslations {
				if len(locale) > 64 || !toolLabelLocalePattern.MatchString(locale) {
					return fmt.Errorf("%s: invalid label locale %q", prefix, locale)
				}
				if strings.TrimSpace(label) == "" || len(label) > maxToolDisplayLabelLen {
					return fmt.Errorf("%s: translated label for %q must contain 1 to %d bytes", prefix, locale, maxToolDisplayLabelLen)
				}
				if err := validateBoundedMetadata("display.label_translations", label); err != nil {
					return fmt.Errorf("%s: %w", prefix, err)
				}
			}
			for field, value := range map[string]string{
				"display.kind":       tool.Display.Kind,
				"display.label":      tool.Display.Label,
				"display.text":       tool.Display.Text,
				"display.capability": tool.Display.Capability,
			} {
				if err := validateBoundedMetadata(field, value); err != nil {
					return fmt.Errorf("%s: %w", prefix, err)
				}
			}
		}
	}
	return nil
}

func validateBoundedMetadata(field, value string) error {
	if len(value) > maxToolMetadataLen {
		return fmt.Errorf("%s exceeds %d bytes", field, maxToolMetadataLen)
	}
	if strings.ContainsRune(value, '\x00') {
		return errors.New(field + " contains a NUL byte")
	}
	return nil
}

type ToolExecuteInput struct {
	SessionID string `json:"session_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	// TurnID identifies the exact model turn that owns this tool invocation.
	// Plugins may use it as a key when deciding how to propagate cancellation.
	TurnID string `json:"turn_id,omitempty"`
	// ExecutionID names this exact dispatch in the execution scope plane. It
	// is the open frame: the host cancels precisely this execution through
	// execution.cancel, and progress rides execution.update with this ID.
	ExecutionID string          `json:"execution_id,omitempty"`
	ActorID     string          `json:"actor_id,omitempty"`
	CWD         string          `json:"cwd"`
	StepIndex   int             `json:"step_index,omitempty"`
	CallID      string          `json:"call_id"`
	Tool        string          `json:"tool"`
	Arguments   json.RawMessage `json:"arguments"`
}

type State string

const (
	StateStarting State = "starting"
	StatePrepared State = "prepared"
	StateActive   State = "active"
	StateFailed   State = "failed"
	StateStopped  State = "stopped"
)

// Status is a user-safe snapshot of one plugin runtime.
type Status struct {
	ID        string    `json:"id"`
	State     State     `json:"state"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}
