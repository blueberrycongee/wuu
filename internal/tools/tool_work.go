package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type ChatWorkTool struct{ env *Env }

func NewChatWorkTool(env *Env) *ChatWorkTool    { return &ChatWorkTool{env: env} }
func (t *ChatWorkTool) Name() string            { return "chat_work" }
func (t *ChatWorkTool) IsReadOnly() bool        { return false }
func (t *ChatWorkTool) IsConcurrencySafe() bool { return false }
func (t *ChatWorkTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "chat_work",
		Description: "Read or update durable room Work lifecycle facts. Use run objects only for independently cancellable or diagnosable producer, verifier, selector, or integration sessions; use artifacts for diffs, snapshots, logs, screenshots, and reports.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":                map[string]any{"type": "string", "enum": []string{"get", "list", "add_artifact", "evidence", "cancel"}},
				"work_id":               map[string]any{"type": "string"},
				"room_id":               map[string]any{"type": "string"},
				"run_id":                map[string]any{"type": "string"},
				"run_kind":              map[string]any{"type": "string", "enum": []string{"producer", "verifier", "selector", "integration"}},
				"run_state":             map[string]any{"type": "string", "enum": []string{"completed", "failed", "cancelled", "interrupted"}},
				"profile":               map[string]any{"type": "string"},
				"session_ref":           map[string]any{"type": "string"},
				"named_agent_id":        map[string]any{"type": "string", "description": "Named identity that owns the run. Required for verifier runs and must differ from the task owner. Work owners and leads can assign runs to another room member."},
				"request_id":            map[string]any{"type": "string", "description": "Stable idempotency key for run and promotion mutations."},
				"deadline_seconds":      map[string]any{"type": "integer", "minimum": 1},
				"round":                 map[string]any{"type": "integer", "minimum": 1},
				"qualified":             map[string]any{"type": "boolean"},
				"workspace_revision":    map[string]any{"type": "string"},
				"provider":              map[string]any{"type": "string"},
				"model":                 map[string]any{"type": "string"},
				"input_tokens":          map[string]any{"type": "integer", "minimum": 0},
				"output_tokens":         map[string]any{"type": "integer", "minimum": 0},
				"cost_usd":              map[string]any{"type": "number", "minimum": 0},
				"checks_rerun":          map[string]any{"type": "integer", "minimum": 0},
				"findings_count":        map[string]any{"type": "integer", "minimum": 0},
				"outcome":               map[string]any{"type": "string"},
				"repair_outcome":        map[string]any{"type": "string"},
				"artifact_kind":         map[string]any{"type": "string", "enum": []string{"candidate", "diff", "snapshot", "check_log", "screenshot", "report", "other"}},
				"uri":                   map[string]any{"type": "string"},
				"label":                 map[string]any{"type": "string"},
				"summary":               map[string]any{"type": "string"},
				"artifact_ref":          map[string]any{"type": "string"},
				"selection_reason":      map[string]any{"type": "string"},
				"checks_summary":        map[string]any{"type": "string"},
				"changed_files_count":   map[string]any{"type": "integer", "minimum": 0},
				"unresolved_items":      map[string]any{"type": "string"},
				"lead_named_agent_id":   map[string]any{"type": "string"},
				"max_verifier_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
				"max_candidates":        map[string]any{"type": "integer", "minimum": 1, "maximum": 4},
				"max_rounds":            map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
				"max_input_tokens":      map[string]any{"type": "integer", "minimum": 0},
				"max_output_tokens":     map[string]any{"type": "integer", "minimum": 0},
				"work_deadline_seconds": map[string]any{"type": "integer", "minimum": 1},
				"fanout_reason":         map[string]any{"type": "string"},
				"reason":                map[string]any{"type": "string"},
			},
			"required": []string{"action"},
		},
	}
}

func (t *ChatWorkTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if t == nil || t.env == nil || t.env.ChatAgent == nil {
		return "", errors.New("chat_work is available only in a named-agent session")
	}
	var args struct {
		Action, WorkID, RoomID, RunID, RunKind, RunState, Profile, SessionRef string
		WorkspaceRevision, Provider, Model, Outcome, RepairOutcome            string
		ArtifactKind, URI, Label, Summary, ChecksSummary, UnresolvedItems     string
		LeadNamedAgentID, FanoutReason, Reason                                string
		NamedAgentID, RequestID, ArtifactRef, SelectionReason                 string
		InputTokens, OutputTokens                                             int64
		MaxInputTokens, MaxOutputTokens                                       int64
		ChecksRerun, FindingsCount, ChangedFilesCount                         int
		MaxVerifierAttempts, MaxCandidates                                    int
		DeadlineSeconds, Round                                                int
		MaxRounds, WorkDeadlineSeconds                                        int
		Qualified                                                             bool
		CostUSD                                                               float64
	}
	if err := json.Unmarshal([]byte(argsJSON), &struct {
		Action              *string  `json:"action"`
		WorkID              *string  `json:"work_id"`
		RoomID              *string  `json:"room_id"`
		RunID               *string  `json:"run_id"`
		RunKind             *string  `json:"run_kind"`
		RunState            *string  `json:"run_state"`
		Profile             *string  `json:"profile"`
		SessionRef          *string  `json:"session_ref"`
		NamedAgentID        *string  `json:"named_agent_id"`
		RequestID           *string  `json:"request_id"`
		ArtifactRef         *string  `json:"artifact_ref"`
		SelectionReason     *string  `json:"selection_reason"`
		DeadlineSeconds     *int     `json:"deadline_seconds"`
		Round               *int     `json:"round"`
		Qualified           *bool    `json:"qualified"`
		CostUSD             *float64 `json:"cost_usd"`
		WorkspaceRevision   *string  `json:"workspace_revision"`
		Provider            *string  `json:"provider"`
		Model               *string  `json:"model"`
		Outcome             *string  `json:"outcome"`
		RepairOutcome       *string  `json:"repair_outcome"`
		ArtifactKind        *string  `json:"artifact_kind"`
		URI                 *string  `json:"uri"`
		Label               *string  `json:"label"`
		Summary             *string  `json:"summary"`
		ChecksSummary       *string  `json:"checks_summary"`
		UnresolvedItems     *string  `json:"unresolved_items"`
		LeadNamedAgentID    *string  `json:"lead_named_agent_id"`
		FanoutReason        *string  `json:"fanout_reason"`
		Reason              *string  `json:"reason"`
		InputTokens         *int64   `json:"input_tokens"`
		OutputTokens        *int64   `json:"output_tokens"`
		ChecksRerun         *int     `json:"checks_rerun"`
		FindingsCount       *int     `json:"findings_count"`
		ChangedFilesCount   *int     `json:"changed_files_count"`
		MaxVerifierAttempts *int     `json:"max_verifier_attempts"`
		MaxCandidates       *int     `json:"max_candidates"`
		MaxRounds           *int     `json:"max_rounds"`
		MaxInputTokens      *int64   `json:"max_input_tokens"`
		MaxOutputTokens     *int64   `json:"max_output_tokens"`
		WorkDeadlineSeconds *int     `json:"work_deadline_seconds"`
	}{
		Action: &args.Action, WorkID: &args.WorkID, RoomID: &args.RoomID, RunID: &args.RunID,
		RunKind: &args.RunKind, RunState: &args.RunState, Profile: &args.Profile, SessionRef: &args.SessionRef,
		NamedAgentID: &args.NamedAgentID, RequestID: &args.RequestID, ArtifactRef: &args.ArtifactRef,
		SelectionReason: &args.SelectionReason, DeadlineSeconds: &args.DeadlineSeconds, Round: &args.Round, Qualified: &args.Qualified,
		CostUSD:           &args.CostUSD,
		WorkspaceRevision: &args.WorkspaceRevision, Provider: &args.Provider, Model: &args.Model,
		Outcome: &args.Outcome, RepairOutcome: &args.RepairOutcome, ArtifactKind: &args.ArtifactKind,
		URI: &args.URI, Label: &args.Label, Summary: &args.Summary, ChecksSummary: &args.ChecksSummary,
		UnresolvedItems: &args.UnresolvedItems, LeadNamedAgentID: &args.LeadNamedAgentID,
		FanoutReason: &args.FanoutReason, Reason: &args.Reason, InputTokens: &args.InputTokens,
		OutputTokens: &args.OutputTokens, ChecksRerun: &args.ChecksRerun, FindingsCount: &args.FindingsCount,
		ChangedFilesCount: &args.ChangedFilesCount, MaxVerifierAttempts: &args.MaxVerifierAttempts,
		MaxCandidates: &args.MaxCandidates,
		MaxRounds:     &args.MaxRounds, MaxInputTokens: &args.MaxInputTokens, MaxOutputTokens: &args.MaxOutputTokens,
		WorkDeadlineSeconds: &args.WorkDeadlineSeconds,
	}); err != nil {
		return "", err
	}
	switch strings.TrimSpace(args.Action) {
	case "get":
		work, err := t.env.ChatAgent.GetWork(ctx, args.WorkID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"work": work})
	case "list":
		works, err := t.env.ChatAgent.ListWorks(ctx, args.RoomID)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"works": works})
	case "start_run":
		run, err := t.env.ChatAgent.StartWorkRun(ctx, channels.WorkRunStartParams{WorkID: args.WorkID, NamedAgentID: args.NamedAgentID, Kind: channels.WorkRunKind(args.RunKind), Profile: args.Profile, SessionRef: args.SessionRef, WorkspaceRevision: args.WorkspaceRevision, RequestID: args.RequestID, Round: args.Round, Deadline: time.Duration(args.DeadlineSeconds) * time.Second})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"run": run})
	case "finish_run":
		run, err := t.env.ChatAgent.FinishWorkRun(ctx, channels.WorkRunFinishParams{WorkID: args.WorkID, RunID: args.RunID, State: channels.WorkRunState(args.RunState), Outcome: args.Outcome, Provider: args.Provider, Model: args.Model, InputTokens: args.InputTokens, OutputTokens: args.OutputTokens, CostUSD: args.CostUSD, ChecksRerun: args.ChecksRerun, FindingsCount: args.FindingsCount, RepairOutcome: args.RepairOutcome, RequestID: args.RequestID, Qualified: args.Qualified})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"run": run})
	case "add_artifact":
		artifact, err := t.env.ChatAgent.AddWorkArtifact(ctx, channels.WorkArtifactAddParams{WorkID: args.WorkID, RunID: args.RunID, Kind: channels.WorkArtifactKind(args.ArtifactKind), URI: args.URI, Label: args.Label, Summary: args.Summary, WorkspaceRevision: args.WorkspaceRevision})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"artifact": artifact})
	case "promote_candidate":
		work, err := t.env.ChatAgent.PromoteWorkCandidate(ctx, channels.WorkCandidatePromoteParams{WorkID: args.WorkID, ArtifactRef: args.ArtifactRef, RunID: args.RunID, RequestID: args.RequestID, SelectionReason: args.SelectionReason, WorkspaceRevision: args.WorkspaceRevision})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"work": work})
	case "evidence":
		work, err := t.env.ChatAgent.UpdateWorkEvidence(ctx, channels.WorkEvidenceUpdateParams{WorkID: args.WorkID, ChecksSummary: args.ChecksSummary, ChangedFilesCount: args.ChangedFilesCount, UnresolvedItems: args.UnresolvedItems})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"work": work})
	case "policy":
		var deadline time.Time
		if args.WorkDeadlineSeconds > 0 {
			deadline = time.Now().UTC().Add(time.Duration(args.WorkDeadlineSeconds) * time.Second)
		}
		work, err := t.env.ChatAgent.UpdateWorkPolicy(ctx, channels.WorkPolicyUpdateParams{WorkID: args.WorkID, LeadNamedAgentID: args.LeadNamedAgentID, MaxVerifierAttempts: args.MaxVerifierAttempts, MaxCandidates: args.MaxCandidates, FanoutReason: args.FanoutReason, MaxRounds: args.MaxRounds, MaxInputTokens: args.MaxInputTokens, MaxOutputTokens: args.MaxOutputTokens, DeadlineAt: deadline})
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"work": work})
	case "cancel":
		work, err := t.env.ChatAgent.CancelWork(ctx, args.WorkID, args.Reason)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"work": work})
	default:
		return "", errors.New("chat_work action is invalid")
	}
}
