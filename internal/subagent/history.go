package subagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

// ResumeSnapshotVersion is the schema version of the persisted run record.
// Snapshots written at this version (or newer) carry every field needed to
// rebuild the runtime for a cross-restart resume. Records with a lower
// version (including 0 / missing, i.e. written before resume support) load
// fine but cannot be resumed — the resume path rejects them explicitly.
const ResumeSnapshotVersion = 1

// historyRecord is the JSON shape we write per sub-agent. It captures the
// metadata and conversation needed to render a worker session and, from
// ResumeSnapshotVersion onward, to rebuild the runtime for a follow-up
// after a process restart.
type historyRecord struct {
	Version            int                     `json:"version"`
	ID                 string                  `json:"id"`
	Type               string                  `json:"type"`
	TaskName           string                  `json:"task_name,omitempty"`
	AgentProfile       string                  `json:"agent_profile,omitempty"`
	AgentPath          string                  `json:"agent_path,omitempty"`
	ParentID           string                  `json:"parent_id,omitempty"`
	ParticipantID      string                  `json:"participant_id,omitempty"`
	Description        string                  `json:"description"`
	Status             string                  `json:"status"`
	StartedAt          time.Time               `json:"started_at"`
	CompletedAt        time.Time               `json:"completed_at"`
	CWD                string                  `json:"cwd,omitempty"`
	Model              string                  `json:"model"`
	ModelPin           string                  `json:"model_pin,omitempty"`
	ModelAlias         string                  `json:"model_alias,omitempty"`
	ModelAliasFallback bool                    `json:"model_alias_fallback,omitempty"`
	Runtime            *workerRuntimeRecord    `json:"runtime,omitempty"`
	Prompt             string                  `json:"prompt"`
	Result             string                  `json:"result,omitempty"`
	Error              string                  `json:"error,omitempty"`
	Messages           []providers.ChatMessage `json:"messages,omitempty"`
}

// workerRuntimeRecord is the serializable form of WorkerRuntime. The Client
// field is intentionally omitted; resume rebuilds it from the persisted Provider.
type workerRuntimeRecord struct {
	Provider                string         `json:"provider,omitempty"`
	Model                   string         `json:"model,omitempty"`
	APIModel                string         `json:"api_model,omitempty"`
	Effort                  string         `json:"effort,omitempty"`
	Variant                 string         `json:"variant,omitempty"`
	ProviderOptions         map[string]any `json:"provider_options,omitempty"`
	Temperature             float64        `json:"temperature,omitempty"`
	ContextWindow           int            `json:"context_window,omitempty"`
	MaxInputTokens          int            `json:"max_input_tokens,omitempty"`
	OutputReserveTokens     int            `json:"output_reserve_tokens,omitempty"`
	CompactThresholdTokens  int            `json:"compact_threshold_tokens,omitempty"`
	CompactThresholdPct     float64        `json:"compact_threshold_pct,omitempty"`
	CompactKeepRecentTokens int            `json:"compact_keep_recent_tokens,omitempty"`
	DisableAutoCompact      bool           `json:"disable_auto_compact,omitempty"`
}

func workerRuntimeRecordFromRuntime(r *WorkerRuntime) *workerRuntimeRecord {
	if r == nil {
		return nil
	}
	return &workerRuntimeRecord{
		Provider:                strings.TrimSpace(r.Provider),
		Model:                   strings.TrimSpace(r.Model),
		APIModel:                strings.TrimSpace(r.APIModel),
		Effort:                  strings.TrimSpace(r.Effort),
		Variant:                 strings.TrimSpace(r.Variant),
		ProviderOptions:         provideroptions.Clone(r.ProviderOptions),
		Temperature:             r.Temperature,
		ContextWindow:           r.ContextWindow,
		MaxInputTokens:          r.MaxInputTokens,
		OutputReserveTokens:     r.OutputReserveTokens,
		CompactThresholdTokens:  r.CompactThresholdTokens,
		CompactThresholdPct:     r.CompactThresholdPct,
		CompactKeepRecentTokens: r.CompactKeepRecentTokens,
		DisableAutoCompact:      r.DisableAutoCompact,
	}
}

func workerRuntimeFromRecord(rec *workerRuntimeRecord) *WorkerRuntime {
	if rec == nil {
		return nil
	}
	return &WorkerRuntime{
		Provider:                strings.TrimSpace(rec.Provider),
		Model:                   strings.TrimSpace(rec.Model),
		APIModel:                strings.TrimSpace(rec.APIModel),
		Effort:                  strings.TrimSpace(rec.Effort),
		Variant:                 strings.TrimSpace(rec.Variant),
		ProviderOptions:         provideroptions.Clone(rec.ProviderOptions),
		Temperature:             rec.Temperature,
		ContextWindow:           rec.ContextWindow,
		MaxInputTokens:          rec.MaxInputTokens,
		OutputReserveTokens:     rec.OutputReserveTokens,
		CompactThresholdTokens:  rec.CompactThresholdTokens,
		CompactThresholdPct:     rec.CompactThresholdPct,
		CompactKeepRecentTokens: rec.CompactKeepRecentTokens,
		DisableAutoCompact:      rec.DisableAutoCompact,
	}
}

// PersistedRun is a decoded sub-agent snapshot. The agentcontrol layer
// loads it to validate and rebuild a resumable runtime; the subagent
// package uses it to seed Manager.Restore.
type PersistedRun struct {
	Version            int
	ID                 string
	Type               string
	TaskName           string
	AgentProfile       string
	AgentPath          string
	ParentID           string
	ParticipantID      string
	Description        string
	Status             Status
	StartedAt          time.Time
	CompletedAt        time.Time
	CWD                string
	Model              string
	ModelPin           string
	ModelAlias         string
	ModelAliasFallback bool
	Runtime            *WorkerRuntime
	Prompt             string
	Result             string
	Error              string
	Messages           []providers.ChatMessage
}

// LoadPersistedRun reads and decodes a sub-agent snapshot from disk. Old
// snapshots that predate the resume fields decode without error, leaving
// the new fields at their zero values (Version 0).
func LoadPersistedRun(path string) (PersistedRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PersistedRun{}, err
	}
	var rec historyRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return PersistedRun{}, err
	}
	return PersistedRun{
		Version:            rec.Version,
		ID:                 rec.ID,
		Type:               rec.Type,
		TaskName:           rec.TaskName,
		AgentProfile:       rec.AgentProfile,
		AgentPath:          rec.AgentPath,
		ParentID:           rec.ParentID,
		ParticipantID:      rec.ParticipantID,
		Description:        rec.Description,
		Status:             Status(rec.Status),
		StartedAt:          rec.StartedAt,
		CompletedAt:        rec.CompletedAt,
		CWD:                rec.CWD,
		Model:              rec.Model,
		ModelPin:           rec.ModelPin,
		ModelAlias:         rec.ModelAlias,
		ModelAliasFallback: rec.ModelAliasFallback,
		Runtime:            workerRuntimeFromRecord(rec.Runtime),
		Prompt:             rec.Prompt,
		Result:             rec.Result,
		Error:              rec.Error,
		Messages:           rec.Messages,
	}, nil
}

// MarkPersistedRunInterrupted rewrites an on-disk run snapshot whose status
// is still non-terminal (the writing process died mid-run) to
// StatusInterrupted so the normal rehydrate/followup path can resume it.
// Every other field is preserved: the original timestamps stay untouched
// (CompletedAt is only filled when it was never set) and an existing error
// message is kept over the reconciliation reason. Snapshots that are already
// terminal are left alone. Returns whether the file was rewritten.
func MarkPersistedRunInterrupted(path, reason string, now time.Time) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var rec historyRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return false, err
	}
	// waiting_children is non-terminal but has no live goroutine by design:
	// the parked state must survive crash repair so child deliveries resume
	// it instead of reconciliation stamping it interrupted.
	if IsTerminal(Status(rec.Status)) || Status(rec.Status) == StatusWaitingChildren {
		return false, nil
	}
	rec.Status = string(StatusInterrupted)
	if strings.TrimSpace(rec.Error) == "" {
		rec.Error = reason
	}
	if rec.CompletedAt.IsZero() {
		rec.CompletedAt = now
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, securefs.FileMode); err != nil {
		return false, err
	}
	return true, nil
}

// persistHistory writes the sub-agent's final state to its configured
// HistoryPath. Callers must surface errors because this snapshot is the
// durable source used to resume a worker after restart.
func persistHistory(sa *SubAgent) error {
	if sa.historyPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(sa.historyPath), securefs.DirMode); err != nil {
		return err
	}

	sa.mu.Lock()
	rec := historyRecord{
		Version:            ResumeSnapshotVersion,
		ID:                 sa.ID,
		Type:               sa.Type,
		TaskName:           sa.TaskName,
		AgentProfile:       sa.AgentProfile,
		AgentPath:          sa.AgentPath,
		ParentID:           sa.ParentID,
		ParticipantID:      sa.ParticipantID,
		Description:        sa.Description,
		Status:             string(sa.Status),
		StartedAt:          sa.StartedAt,
		CompletedAt:        sa.CompletedAt,
		CWD:                sa.workerRoot,
		Model:              sa.model,
		ModelPin:           sa.modelPin,
		ModelAlias:         sa.modelAlias,
		ModelAliasFallback: sa.modelAliasFallback,
		Runtime:            workerRuntimeRecordFromRuntime(sa.runtime),
		Prompt:             sa.prompt,
		Result:             sa.Result,
		Messages:           providers.CloneChatMessages(sa.history),
	}
	if sa.Error != nil {
		rec.Error = sa.Error.Error()
	}
	sa.mu.Unlock()

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sa.historyPath, data, securefs.FileMode)
}
