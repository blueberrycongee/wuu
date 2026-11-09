package evalharness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

type TraceEvent struct {
	Type      string    `json:"type"`
	TaskID    string    `json:"task_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Data      any       `json:"data,omitempty"`
}

type TraceTask struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Success              bool                   `json:"success"`
	DurationMS           int64                  `json:"duration_ms"`
	Turns                int                    `json:"turns"`
	ToolCalls            int                    `json:"tool_calls"`
	ToolNames            []string               `json:"tool_names,omitempty"`
	ToolSequence         []string               `json:"tool_sequence,omitempty"`
	MissingTools         []string               `json:"missing_tools,omitempty"`
	ForbiddenToolsUsed   []string               `json:"forbidden_tools,omitempty"`
	MissingToolCalls     []string               `json:"missing_tool_calls,omitempty"`
	MissingToolSeq       []string               `json:"missing_tool_sequence,omitempty"`
	MissingErrors        []string               `json:"missing_errors,omitempty"`
	InputTokens          int                    `json:"input_tokens"`
	OutputTokens         int                    `json:"output_tokens"`
	CacheCreationTokens  int                    `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens      int                    `json:"cache_read_tokens,omitempty"`
	CacheHitRate         float64                `json:"cache_hit_rate,omitempty"`
	VerificationReason   string                 `json:"verification_reason,omitempty"`
	VerificationEvidence []VerificationEvidence `json:"verification_evidence,omitempty"`
	Error                string                 `json:"error,omitempty"`
	Workdir              string                 `json:"workdir,omitempty"`
}

type TraceObservability struct {
	SessionID       string   `json:"session_id,omitempty"`
	StateDir        string   `json:"state_dir,omitempty"`
	SessionDir      string   `json:"session_dir,omitempty"`
	TracePath       string   `json:"trace_path,omitempty"`
	HarnessDir      string   `json:"harness_dir,omitempty"`
	TaskWorkdir     string   `json:"task_workdir,omitempty"`
	TaskWorkdirKept bool     `json:"task_workdir_kept,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

type TraceReplaySummary struct {
	Path                        string                       `json:"path,omitempty"`
	Mode                        string                       `json:"mode"`
	EventCount                  int                          `json:"event_count"`
	EventTypes                  map[string]int               `json:"event_types,omitempty"`
	Task                        *TraceTask                   `json:"task,omitempty"`
	Observability               *TraceObservability          `json:"observability,omitempty"`
	ModelProfile                *ModelProfileObservation     `json:"model_profile,omitempty"`
	ContextBlocks               []ContextBlockObservation    `json:"context_blocks,omitempty"`
	ContextBlockKinds           []string                     `json:"context_block_kinds,omitempty"`
	ToolInventory               []ToolInventoryObservation   `json:"tool_inventory,omitempty"`
	ToolRecords                 []ToolObservation            `json:"tool_records,omitempty"`
	ToolNames                   []string                     `json:"tool_names,omitempty"`
	ToolSummary                 *ToolReplaySummary           `json:"tool_summary,omitempty"`
	ModelProfileRecommendations []ModelProfileRecommendation `json:"model_profile_recommendations,omitempty"`
	Attention                   []AttentionObservation       `json:"attention,omitempty"`
	HarnessTaskIDs              []string                     `json:"harness_task_ids,omitempty"`
	HarnessReportIDs            []string                     `json:"harness_report_ids,omitempty"`
	Validation                  *ValidationReplaySummary     `json:"validation,omitempty"`
	Final                       *TraceReplayFinal            `json:"final,omitempty"`
	Complete                    bool                         `json:"complete"`
	Warnings                    []string                     `json:"warnings,omitempty"`
}

type ToolReplaySummary struct {
	Total             int                           `json:"total"`
	Succeeded         int                           `json:"succeeded"`
	Failed            int                           `json:"failed"`
	ByKind            map[string]int                `json:"by_kind,omitempty"`
	ByRisk            map[string]int                `json:"by_risk,omitempty"`
	ByPolicyAction    map[string]int                `json:"by_policy_action,omitempty"`
	ByResultAction    map[string]int                `json:"by_result_action,omitempty"`
	ByErrorKind       map[string]int                `json:"by_error_kind,omitempty"`
	PolicyBlocks      []ToolPolicyBlockSummary      `json:"policy_blocks,omitempty"`
	RepeatedArguments []ToolRepeatedArgumentSummary `json:"repeated_arguments,omitempty"`
	PatchRisk         *PatchRiskReplaySummary       `json:"patch_risk,omitempty"`
	argumentCounts    map[string]ToolRepeatedArgumentSummary
}

type ToolPolicyBlockSummary struct {
	ToolName        string `json:"tool_name"`
	CallID          string `json:"call_id,omitempty"`
	PolicyAction    string `json:"policy_action"`
	PolicyReason    string `json:"policy_reason,omitempty"`
	Risk            string `json:"risk,omitempty"`
	ErrorKind       string `json:"error_kind,omitempty"`
	ArgumentsSHA256 string `json:"arguments_sha256,omitempty"`
	RevisionBefore  string `json:"revision_before,omitempty"`
	ModelNextAction string `json:"model_next_action,omitempty"`
}

type ToolRepeatedArgumentSummary struct {
	ToolName        string `json:"tool_name"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
	Count           int    `json:"count"`
}

type PatchRiskReplaySummary struct {
	Total          int            `json:"total"`
	ByLevel        map[string]int `json:"by_level,omitempty"`
	MultiFile      int            `json:"multi_file,omitempty"`
	ContainsDelete int            `json:"contains_delete,omitempty"`
	ContainsMove   int            `json:"contains_move,omitempty"`
	FileCount      int            `json:"file_count"`
	HunkCount      int            `json:"hunk_count"`
	AddedLines     int            `json:"added_lines"`
	DeletedLines   int            `json:"deleted_lines"`
}

type ValidationReplaySummary struct {
	Status      string                      `json:"status"`
	ToolCalls   []ValidationToolCallSummary `json:"tool_calls,omitempty"`
	Evidence    []VerificationEvidence      `json:"evidence,omitempty"`
	Missing     []string                    `json:"missing,omitempty"`
	Failures    []string                    `json:"failures,omitempty"`
	NextActions []string                    `json:"next_actions,omitempty"`
}

type ValidationToolCallSummary struct {
	ToolName       string   `json:"tool_name"`
	CallID         string   `json:"call_id,omitempty"`
	ResultAction   string   `json:"result_action,omitempty"`
	Success        bool     `json:"success"`
	ErrorKind      string   `json:"error_kind,omitempty"`
	PolicyAction   string   `json:"policy_action,omitempty"`
	DurationMS     int64    `json:"duration_ms,omitempty"`
	RevisionBefore string   `json:"revision_before,omitempty"`
	RevisionAfter  string   `json:"revision_after,omitempty"`
	ResultRef      string   `json:"result_ref,omitempty"`
	ArtifactRefs   []string `json:"artifact_refs,omitempty"`
}

type TraceReplayFinal struct {
	Success              bool                   `json:"success"`
	VerificationReason   string                 `json:"verification_reason,omitempty"`
	VerificationEvidence []VerificationEvidence `json:"verification_evidence,omitempty"`
	Error                string                 `json:"error,omitempty"`
	FinalAnswerPreview   string                 `json:"final_answer_preview,omitempty"`
	Warnings             []string               `json:"warnings,omitempty"`
}

func TraceEvents(result Result, createdAt time.Time) []TraceEvent {
	taskID := result.TaskID
	events := []TraceEvent{{
		Type:      "task",
		TaskID:    taskID,
		CreatedAt: createdAt,
		Data: TraceTask{
			ID:                   result.TaskID,
			Name:                 result.TaskName,
			Success:              result.Success,
			DurationMS:           result.DurationMS,
			Turns:                result.Turns,
			ToolCalls:            result.ToolCalls,
			ToolNames:            append([]string(nil), result.ToolNames...),
			ToolSequence:         append([]string(nil), result.ToolSequence...),
			MissingTools:         append([]string(nil), result.MissingTools...),
			ForbiddenToolsUsed:   append([]string(nil), result.ForbiddenToolsUsed...),
			MissingToolCalls:     append([]string(nil), result.MissingToolCalls...),
			MissingToolSeq:       append([]string(nil), result.MissingToolSeq...),
			MissingErrors:        append([]string(nil), result.MissingErrors...),
			InputTokens:          result.InputTokens,
			OutputTokens:         result.OutputTokens,
			CacheCreationTokens:  result.CacheCreationTokens,
			CacheReadTokens:      result.CacheReadTokens,
			CacheHitRate:         result.CacheHitRate,
			VerificationReason:   result.VerificationReason,
			VerificationEvidence: append([]VerificationEvidence(nil), result.VerificationEvidence...),
			Error:                result.Error,
			Workdir:              result.Workdir,
		},
	}}
	if result.Observability == nil {
		return events
	}
	obs := result.Observability
	events = append(events, TraceEvent{Type: "observability", TaskID: taskID, CreatedAt: createdAt, Data: TraceObservability{
		SessionID:       obs.SessionID,
		StateDir:        obs.StateDir,
		SessionDir:      obs.SessionDir,
		TracePath:       obs.TracePath,
		HarnessDir:      obs.HarnessDir,
		TaskWorkdir:     obs.TaskWorkdir,
		TaskWorkdirKept: obs.TaskWorkdirKept,
		Warnings:        append([]string(nil), obs.Warnings...),
	}})
	if obs.ModelProfile != nil {
		events = append(events, TraceEvent{Type: "model_profile", TaskID: taskID, CreatedAt: createdAt, Data: obs.ModelProfile})
	}
	if len(obs.ContextBlocks) > 0 {
		events = append(events, TraceEvent{Type: "context_blocks", TaskID: taskID, CreatedAt: createdAt, Data: obs.ContextBlocks})
	}
	if len(obs.ContextRequests) > 0 {
		events = append(events, TraceEvent{Type: "context_requests", TaskID: taskID, CreatedAt: createdAt, Data: obs.ContextRequests})
	}
	if len(obs.ToolInventory) > 0 {
		events = append(events, TraceEvent{Type: "tool_inventory", TaskID: taskID, CreatedAt: createdAt, Data: obs.ToolInventory})
	}
	if len(obs.ToolRecords) > 0 {
		events = append(events, TraceEvent{Type: "tool_records", TaskID: taskID, CreatedAt: createdAt, Data: obs.ToolRecords})
	}
	if len(obs.Attention) > 0 {
		events = append(events, TraceEvent{Type: "attention", TaskID: taskID, CreatedAt: createdAt, Data: obs.Attention})
	}
	if len(obs.HarnessTasks) > 0 {
		events = append(events, TraceEvent{Type: "harness_tasks", TaskID: taskID, CreatedAt: createdAt, Data: obs.HarnessTasks})
	}
	if len(obs.HarnessReports) > 0 {
		events = append(events, TraceEvent{Type: "harness_reports", TaskID: taskID, CreatedAt: createdAt, Data: obs.HarnessReports})
	}
	events = append(events, TraceEvent{Type: "final", TaskID: taskID, CreatedAt: createdAt, Data: map[string]any{
		"success":               result.Success,
		"verification_reason":   result.VerificationReason,
		"verification_evidence": append([]VerificationEvidence(nil), result.VerificationEvidence...),
		"error":                 result.Error,
		"final_answer_preview":  obs.FinalAnswerPreview,
		"warnings":              append([]string(nil), obs.Warnings...),
	}})
	return events
}

func WriteTrace(path string, result Result) error {
	if err := os.MkdirAll(filepath.Dir(path), securefs.DirMode); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, securefs.FileMode)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	createdAt := time.Now().UTC()
	for _, event := range TraceEvents(result, createdAt) {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func BuildValidationSummary(result Result) *ValidationReplaySummary {
	summary := TraceReplaySummary{
		Task: &TraceTask{
			ID:                   result.TaskID,
			Name:                 result.TaskName,
			Success:              result.Success,
			MissingTools:         append([]string(nil), result.MissingTools...),
			ForbiddenToolsUsed:   append([]string(nil), result.ForbiddenToolsUsed...),
			MissingToolCalls:     append([]string(nil), result.MissingToolCalls...),
			MissingToolSeq:       append([]string(nil), result.MissingToolSeq...),
			MissingErrors:        append([]string(nil), result.MissingErrors...),
			VerificationReason:   result.VerificationReason,
			VerificationEvidence: append([]VerificationEvidence(nil), result.VerificationEvidence...),
			Error:                result.Error,
		},
		Final: &TraceReplayFinal{
			Success:              result.Success,
			VerificationReason:   result.VerificationReason,
			VerificationEvidence: append([]VerificationEvidence(nil), result.VerificationEvidence...),
			Error:                result.Error,
		},
	}
	if result.Observability != nil {
		summary.Attention = append(summary.Attention, result.Observability.Attention...)
		for _, record := range result.Observability.ToolRecords {
			summary.addValidationToolObservation(record)
		}
	}
	summary.finalizeValidationSummary()
	return summary.Validation
}

func ReplayTrace(path string) (TraceReplaySummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return TraceReplaySummary{}, err
	}
	defer file.Close()

	summary := TraceReplaySummary{
		Path:       path,
		Mode:       "deterministic_trace_replay",
		EventTypes: make(map[string]int),
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var event struct {
			Type      string          `json:"type"`
			TaskID    string          `json:"task_id,omitempty"`
			CreatedAt time.Time       `json:"created_at"`
			Data      json.RawMessage `json:"data,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return TraceReplaySummary{}, fmt.Errorf("decode trace line %d: %w", line, err)
		}
		summary.EventCount++
		summary.EventTypes[event.Type]++
		if err := replayTraceEvent(&summary, event.Type, event.Data); err != nil {
			return TraceReplaySummary{}, fmt.Errorf("replay trace line %d type %q: %w", line, event.Type, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return TraceReplaySummary{}, err
	}
	if summary.Task == nil {
		return TraceReplaySummary{}, fmt.Errorf("trace %s has no task event", path)
	}
	if summary.Final == nil {
		summary.Warnings = append(summary.Warnings, "trace has no final event; using task success as replay outcome")
		summary.Final = &TraceReplayFinal{Success: summary.Task.Success, VerificationReason: summary.Task.VerificationReason, Error: summary.Task.Error}
	}
	summary.finalizeToolSummary()
	summary.finalizeValidationSummary()
	summary.ModelProfileRecommendations = BuildModelProfileRecommendations(summary)
	summary.Complete = summary.EventTypes["task"] > 0 && summary.EventTypes["final"] > 0
	return summary, nil
}

func replayTraceEvent(summary *TraceReplaySummary, eventType string, data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	switch eventType {
	case "task":
		var task TraceTask
		if err := json.Unmarshal(data, &task); err != nil {
			return err
		}
		summary.Task = &task
	case "observability":
		var obs TraceObservability
		if err := json.Unmarshal(data, &obs); err != nil {
			return err
		}
		summary.Observability = &obs
	case "model_profile":
		var profile ModelProfileObservation
		if err := json.Unmarshal(data, &profile); err != nil {
			return err
		}
		summary.ModelProfile = &profile
	case "context_blocks":
		var blocks []ContextBlockObservation
		if err := json.Unmarshal(data, &blocks); err != nil {
			return err
		}
		summary.ContextBlocks = append(summary.ContextBlocks, blocks...)
		for _, block := range blocks {
			if block.Kind != "" {
				summary.ContextBlockKinds = append(summary.ContextBlockKinds, block.Kind)
			}
		}
	case "tool_records":
		var records []ToolObservation
		if err := json.Unmarshal(data, &records); err != nil {
			return err
		}
		summary.ToolRecords = append(summary.ToolRecords, records...)
		for _, record := range records {
			if record.Name != "" {
				summary.ToolNames = append(summary.ToolNames, record.Name)
			}
			summary.addToolObservation(record)
		}
	case "tool_inventory":
		var inventory []ToolInventoryObservation
		if err := json.Unmarshal(data, &inventory); err != nil {
			return err
		}
		summary.ToolInventory = append(summary.ToolInventory, inventory...)
	case "attention":
		var attention []AttentionObservation
		if err := json.Unmarshal(data, &attention); err != nil {
			return err
		}
		summary.Attention = append(summary.Attention, attention...)
	case "harness_tasks":
		var tasks []HarnessTaskObservation
		if err := json.Unmarshal(data, &tasks); err != nil {
			return err
		}
		for _, task := range tasks {
			if task.ID != "" {
				summary.HarnessTaskIDs = append(summary.HarnessTaskIDs, task.ID)
			}
		}
	case "harness_reports":
		var reports []HarnessReportObservation
		if err := json.Unmarshal(data, &reports); err != nil {
			return err
		}
		for _, report := range reports {
			if report.ID != "" {
				summary.HarnessReportIDs = append(summary.HarnessReportIDs, report.ID)
			}
		}
	case "final":
		var final TraceReplayFinal
		if err := json.Unmarshal(data, &final); err != nil {
			return err
		}
		summary.Final = &final
	}
	return nil
}

func (summary *TraceReplaySummary) addToolObservation(record ToolObservation) {
	summary.ensureToolSummary()
	summary.ToolSummary.Total++
	if record.Success {
		summary.ToolSummary.Succeeded++
	} else {
		summary.ToolSummary.Failed++
	}
	if kind := strings.TrimSpace(record.Kind); kind != "" {
		summary.ToolSummary.ByKind[kind]++
	}
	if risk := strings.TrimSpace(record.Risk); risk != "" {
		summary.ToolSummary.ByRisk[risk]++
	}
	if action := strings.TrimSpace(record.PolicyAction); action != "" {
		summary.ToolSummary.ByPolicyAction[action]++
	}
	if resultAction := toolResultActionKey(record.Name, record.ResultAction); resultAction != "" {
		summary.ToolSummary.ByResultAction[resultAction]++
	}
	if errorKind := strings.TrimSpace(record.ErrorKind); errorKind != "" {
		summary.ToolSummary.ByErrorKind[errorKind]++
	}
	summary.addToolPolicyBlock(record)
	summary.addRepeatedToolArguments(record.Name, record.ArgumentsSHA256)
	if record.PatchRiskSummary != nil {
		summary.addPatchRiskObservation(*record.PatchRiskSummary)
	}
	summary.addValidationToolObservation(record)
}

func (summary *TraceReplaySummary) ensureToolSummary() {
	if summary.ToolSummary != nil {
		return
	}
	summary.ToolSummary = &ToolReplaySummary{
		ByKind:         map[string]int{},
		ByRisk:         map[string]int{},
		ByPolicyAction: map[string]int{},
		ByResultAction: map[string]int{},
		ByErrorKind:    map[string]int{},
	}
}

func toolResultActionKey(toolName, action string) string {
	toolName = strings.TrimSpace(toolName)
	action = strings.TrimSpace(action)
	if toolName == "" || action == "" {
		return ""
	}
	return toolName + ":" + action
}

func (summary *TraceReplaySummary) addToolPolicyBlock(record ToolObservation) {
	action := strings.TrimSpace(record.PolicyAction)
	errorKind := strings.TrimSpace(record.ErrorKind)
	if action != "deny" &&
		errorKind != "boundary_denied" {
		return
	}
	toolName := strings.TrimSpace(record.Name)
	if toolName == "" {
		return
	}
	summary.ensureToolSummary()
	summary.ToolSummary.PolicyBlocks = append(summary.ToolSummary.PolicyBlocks, ToolPolicyBlockSummary{
		ToolName:        toolName,
		CallID:          strings.TrimSpace(record.CallID),
		PolicyAction:    action,
		PolicyReason:    strings.TrimSpace(record.PolicyReason),
		Risk:            strings.TrimSpace(record.Risk),
		ErrorKind:       errorKind,
		ArgumentsSHA256: strings.TrimSpace(record.ArgumentsSHA256),
		RevisionBefore:  strings.TrimSpace(record.RevisionBefore),
		ModelNextAction: modelNextActionForPolicyBlock(action, errorKind),
	})
}

func modelNextActionForPolicyBlock(action, errorKind string) string {
	switch {
	case action == "deny" || errorKind == "boundary_denied":
		return "explain the workspace boundary denial or use a path/action inside the allowed boundary"
	default:
		return ""
	}
}

func (summary *TraceReplaySummary) addRepeatedToolArguments(toolName, argumentsSHA256 string) {
	toolName = strings.TrimSpace(toolName)
	argumentsSHA256 = strings.TrimSpace(argumentsSHA256)
	if toolName == "" || argumentsSHA256 == "" {
		return
	}
	summary.ensureToolSummary()
	if summary.ToolSummary.argumentCounts == nil {
		summary.ToolSummary.argumentCounts = map[string]ToolRepeatedArgumentSummary{}
	}
	key := toolName + "\x00" + argumentsSHA256
	entry := summary.ToolSummary.argumentCounts[key]
	if entry.Count == 0 {
		entry.ToolName = toolName
		entry.ArgumentsSHA256 = argumentsSHA256
	}
	entry.Count++
	summary.ToolSummary.argumentCounts[key] = entry
}

func (summary *TraceReplaySummary) finalizeToolSummary() {
	if summary.ToolSummary == nil || len(summary.ToolSummary.argumentCounts) == 0 {
		return
	}
	summary.ToolSummary.RepeatedArguments = summary.ToolSummary.RepeatedArguments[:0]
	for _, entry := range summary.ToolSummary.argumentCounts {
		if entry.Count > 1 {
			summary.ToolSummary.RepeatedArguments = append(summary.ToolSummary.RepeatedArguments, entry)
		}
	}
	sort.Slice(summary.ToolSummary.RepeatedArguments, func(i, j int) bool {
		left := summary.ToolSummary.RepeatedArguments[i]
		right := summary.ToolSummary.RepeatedArguments[j]
		if left.ToolName != right.ToolName {
			return left.ToolName < right.ToolName
		}
		return left.ArgumentsSHA256 < right.ArgumentsSHA256
	})
	summary.ToolSummary.argumentCounts = nil
}

func (summary *TraceReplaySummary) addValidationToolObservation(record ToolObservation) {
	if !isValidationToolObservation(record) {
		return
	}
	summary.ensureValidationSummary()
	summary.Validation.ToolCalls = append(summary.Validation.ToolCalls, ValidationToolCallSummary{
		ToolName:       record.Name,
		CallID:         record.CallID,
		ResultAction:   record.ResultAction,
		Success:        record.Success,
		ErrorKind:      record.ErrorKind,
		PolicyAction:   record.PolicyAction,
		DurationMS:     record.DurationMS,
		RevisionBefore: record.RevisionBefore,
		RevisionAfter:  record.RevisionAfter,
		ResultRef:      record.ResultRef,
		ArtifactRefs:   append([]string(nil), record.ArtifactRefs...),
	})
	if !record.Success {
		label := strings.TrimSpace(record.Name)
		if record.ErrorKind != "" {
			label += ":" + record.ErrorKind
		}
		if record.CallID != "" {
			label += ":call_id=" + record.CallID
		}
		summary.Validation.Failures = appendUniqueTraceString(summary.Validation.Failures, label)
	}
}

func isValidationToolObservation(record ToolObservation) bool {
	switch strings.TrimSpace(record.Name) {
	case "bash":
		return strings.TrimSpace(record.ClassificationReason) == "local verification command"
	default:
		return false
	}
}

func (summary *TraceReplaySummary) ensureValidationSummary() {
	if summary.Validation != nil {
		return
	}
	summary.Validation = &ValidationReplaySummary{Status: "unverified"}
}

func (summary *TraceReplaySummary) finalizeValidationSummary() {
	var evidence []VerificationEvidence
	if summary.Final != nil {
		evidence = append(evidence, summary.Final.VerificationEvidence...)
	}
	if len(evidence) == 0 && summary.Task != nil {
		evidence = append(evidence, summary.Task.VerificationEvidence...)
	}
	var missing []string
	if summary.Task != nil {
		missing = appendPrefixedTraceStrings(missing, "missing_tool", summary.Task.MissingTools)
		missing = appendPrefixedTraceStrings(missing, "forbidden_tool", summary.Task.ForbiddenToolsUsed)
		missing = appendPrefixedTraceStrings(missing, "missing_tool_call", summary.Task.MissingToolCalls)
		missing = appendPrefixedTraceStrings(missing, "missing_tool_sequence", summary.Task.MissingToolSeq)
		missing = appendPrefixedTraceStrings(missing, "missing_error", summary.Task.MissingErrors)
	}
	missing = appendPrefixedTraceStrings(missing, "attention_issue", AttentionValidationIssues(summary.Attention))
	var failures []string
	for _, item := range evidence {
		if item.Passed {
			continue
		}
		label := strings.TrimSpace(item.Check)
		if label == "" {
			label = "verification_evidence"
		}
		failures = appendUniqueTraceString(failures, label)
	}
	if len(evidence) == 0 && len(missing) == 0 && len(failures) == 0 && summary.Validation == nil {
		return
	}
	summary.ensureValidationSummary()
	summary.Validation.Evidence = append(summary.Validation.Evidence, evidence...)
	summary.Validation.Missing = append(summary.Validation.Missing, missing...)
	summary.Validation.Failures = appendUniqueTraceStrings(summary.Validation.Failures, failures)
	switch {
	case len(summary.Validation.Missing) > 0:
		summary.Validation.Status = "incomplete"
	case summary.Final != nil && !summary.Final.Success:
		summary.Validation.Status = "failed"
	case len(summary.Validation.Failures) > 0:
		summary.Validation.Status = "failed"
	case len(summary.Validation.Evidence) == 0 && len(summary.Validation.ToolCalls) == 0:
		summary.Validation.Status = "unverified"
	default:
		summary.Validation.Status = "passed"
	}
	summary.Validation.NextActions = validationNextActions(summary.Validation)
}

func appendPrefixedTraceStrings(dst []string, prefix string, values []string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		dst = appendUniqueTraceString(dst, prefix+":"+value)
	}
	return dst
}

func appendUniqueTraceStrings(dst []string, values []string) []string {
	for _, value := range values {
		dst = appendUniqueTraceString(dst, value)
	}
	return dst
}

func appendUniqueTraceString(dst []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return dst
	}
	for _, existing := range dst {
		if existing == value {
			return dst
		}
	}
	return append(dst, value)
}

func validationNextActions(validation *ValidationReplaySummary) []string {
	if validation == nil {
		return nil
	}
	switch validation.Status {
	case "passed":
		return []string{"use the verification evidence and validation tool calls as the completion ledger"}
	case "failed":
		return []string{"inspect failed validation tool calls, latest verification evidence, and related artifacts before retrying"}
	case "incomplete":
		return []string{"satisfy missing tool, sequence, policy, or verification requirements before claiming success"}
	default:
		return []string{"run or record relevant validation before claiming success"}
	}
}

func AttentionValidationIssues(items []AttentionObservation) []string {
	var issues []string
	for _, item := range items {
		issues = appendUniqueTraceString(issues, attentionIssueLabel(item))
	}
	sort.Strings(issues)
	return issues
}

func attentionIssueLabel(item AttentionObservation) string {
	parts := []string{firstNonEmptyTraceString(item.Source, "runtime")}
	if id := strings.TrimSpace(item.ID); id != "" {
		parts = append(parts, id)
	}
	if status := strings.TrimSpace(item.Status); status != "" {
		parts = append(parts, "status="+status)
	}
	if path := strings.TrimSpace(item.Path); path != "" {
		parts = append(parts, "path="+path)
	}
	return strings.Join(parts, ":")
}

func firstNonEmptyTraceString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func (summary *TraceReplaySummary) addPatchRiskObservation(risk PatchRiskObservation) {
	summary.ensureToolSummary()
	if summary.ToolSummary.PatchRisk == nil {
		summary.ToolSummary.PatchRisk = &PatchRiskReplaySummary{ByLevel: map[string]int{}}
	}
	patchRisk := summary.ToolSummary.PatchRisk
	patchRisk.Total++
	if level := strings.TrimSpace(risk.RiskLevel); level != "" {
		patchRisk.ByLevel[level]++
	}
	if risk.MultiFile {
		patchRisk.MultiFile++
	}
	if risk.ContainsDelete {
		patchRisk.ContainsDelete++
	}
	if risk.ContainsMove {
		patchRisk.ContainsMove++
	}
	patchRisk.FileCount += risk.FileCount
	patchRisk.HunkCount += risk.HunkCount
	patchRisk.AddedLines += risk.AddedLines
	patchRisk.DeletedLines += risk.DeletedLines
}
