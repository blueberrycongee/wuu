package tools

// ToolRisk is a coarse execution risk level used by telemetry and future
// allow/deny guards. It describes the expected blast radius of invoking a tool,
// not whether a specific call is correct.
type ToolRisk string

const (
	ToolRiskLow    ToolRisk = "low"
	ToolRiskMedium ToolRisk = "medium"
	ToolRiskHigh   ToolRisk = "high"
)

// ToolPolicyAction is the runtime authority decision for one tool call.
type ToolPolicyAction string

const (
	ToolPolicyAllow ToolPolicyAction = "allow"
	ToolPolicyDeny  ToolPolicyAction = "deny"
)

// ToolPolicyDecision is recorded before each known tool call executes.
type ToolPolicyDecision struct {
	Action ToolPolicyAction `json:"action"`
	Risk   ToolRisk         `json:"risk"`
	Reason string           `json:"reason,omitempty"`
}

func classifyToolRisk(name string, kind ToolKind, readOnly bool) ToolRisk {
	if name == "list_agent_profiles" {
		return ToolRiskLow
	}
	switch kind {
	case ToolKindShell, ToolKindGit:
		return ToolRiskHigh
	case ToolKindTest:
		return ToolRiskMedium
	case ToolKindFile:
		if readOnly {
			return ToolRiskLow
		}
		return ToolRiskHigh
	case ToolKindProcess, ToolKindSchedule, ToolKindAgent, ToolKindMCP, ToolKindBrowser:
		if readOnly {
			return ToolRiskMedium
		}
		return ToolRiskHigh
	case ToolKindWeb:
		return ToolRiskMedium
	case ToolKindDiscovery, ToolKindSkill, ToolKindSearch:
		return ToolRiskLow
	default:
		if readOnly {
			return ToolRiskLow
		}
		return ToolRiskHigh
	}
}
