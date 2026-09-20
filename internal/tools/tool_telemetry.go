package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	workspaceRevisionTimeout       = 500 * time.Millisecond
	workspaceDigestMaxFiles        = 5000
	workspaceDigestMaxBytes        = 32 * 1024 * 1024
	workspaceDigestMaxBytesPerFile = 1024 * 1024
	repeatedToolInputPriorLimit    = 2
)

// ToolExecutionRecord captures benchmark-oriented facts about one tool
// execution. It deliberately excludes arguments and output content.
type ToolExecutionRecord struct {
	Name                       string                 `json:"name"`
	StepIndex                  *int                   `json:"step_index,omitempty"`
	CallID                     string                 `json:"call_id,omitempty"`
	ArgumentsSHA256            string                 `json:"arguments_sha256,omitempty"`
	ResultAction               string                 `json:"result_action,omitempty"`
	Kind                       ToolKind               `json:"kind"`
	Exposure                   ToolExposure           `json:"exposure"`
	Risk                       ToolRisk               `json:"risk"`
	ClassificationReason       string                 `json:"classification_reason,omitempty"`
	PolicyAction               ToolPolicyAction       `json:"policy_action"`
	PolicyReason               string                 `json:"policy_reason,omitempty"`
	ReadOnly                   bool                   `json:"read_only"`
	ConcurrencySafe            bool                   `json:"concurrency_safe"`
	StartedAt                  time.Time              `json:"started_at"`
	DurationMS                 int64                  `json:"duration_ms"`
	RevisionBefore             string                 `json:"revision_before,omitempty"`
	RevisionAfter              string                 `json:"revision_after,omitempty"`
	Success                    bool                   `json:"success"`
	Error                      string                 `json:"error,omitempty"`
	ErrorKind                  string                 `json:"error_kind,omitempty"`
	RawOutputBytes             int                    `json:"raw_output_bytes"`
	ReturnedOutputBytes        int                    `json:"returned_output_bytes"`
	ResultBudgeted             bool                   `json:"result_budgeted"`
	ResultRef                  string                 `json:"result_ref,omitempty"`
	ArtifactRefs               []string               `json:"artifact_refs,omitempty"`
	LoadedDeferredTools        []string               `json:"loaded_deferred_tools,omitempty"`
	NewlyLoadedDeferredTools   []string               `json:"newly_loaded_deferred_tools,omitempty"`
	AlreadyLoadedDeferredTools []string               `json:"already_loaded_deferred_tools,omitempty"`
	ToolSurfaceChanged         bool                   `json:"tool_surface_changed,omitempty"`
	PatchRiskSummary           *ToolPatchRisk         `json:"patch_risk_summary,omitempty"`
	Projection                 *ProjectionDiagnostics `json:"projection,omitempty"`
}

type ToolPatchRisk struct {
	FileCount      int            `json:"file_count"`
	HunkCount      int            `json:"hunk_count"`
	AddedLines     int            `json:"added_lines"`
	DeletedLines   int            `json:"deleted_lines"`
	Actions        map[string]int `json:"actions,omitempty"`
	MultiFile      bool           `json:"multi_file,omitempty"`
	ContainsDelete bool           `json:"contains_delete,omitempty"`
	ContainsMove   bool           `json:"contains_move,omitempty"`
	RiskLevel      string         `json:"risk_level"`
	ReviewHint     string         `json:"review_hint,omitempty"`
}

type toolTelemetry struct {
	mu      sync.RWMutex
	records []ToolExecutionRecord
}

type InputValidatingTool interface {
	ValidateInput(argsJSON string) error
}

func (t *toolTelemetry) record(record ToolExecutionRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records = append(t.records, record)
}

func (t *toolTelemetry) snapshot() []ToolExecutionRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]ToolExecutionRecord, len(t.records))
	copy(out, t.records)
	return out
}

// ToolTelemetry returns a snapshot of tool execution records for this toolkit.
func (t *Toolkit) ToolTelemetry() []ToolExecutionRecord {
	return t.env.toolTelemetry.snapshot()
}

func (t *Toolkit) executeKnownToolResult(ctx context.Context, call providers.ToolCall, tool Tool) (toolresult.Result, error) {
	return t.executeKnownToolResultWithRepeatPolicy(ctx, call, tool, false)
}

func (t *Toolkit) executeKnownToolResultAllowRepeated(ctx context.Context, call providers.ToolCall, tool Tool) (toolresult.Result, error) {
	return t.executeKnownToolResultWithRepeatPolicy(ctx, call, tool, true)
}

func (t *Toolkit) executeKnownToolResultWithRepeatPolicy(ctx context.Context, call providers.ToolCall, tool Tool, allowRepeated bool) (toolresult.Result, error) {
	info := buildToolInfoForArgs(tool, t.toolExposure(call.Name), call.Arguments)
	startedAt := time.Now()
	revisionBefore := workspaceRevision(ctx, t.env.RootDir)
	// Hand the freshly computed revision to the tool so read-only tools can
	// reuse it instead of re-running git for the same root.
	ctx = toolctx.WithWorkspaceRevision(ctx, t.env.RootDir, revisionBefore)
	decision := ToolPolicyDecision{Action: ToolPolicyAllow, Reason: "workspace boundary"}

	if err := validateToolArgumentsJSON(call.Arguments); err != nil {
		t.recordToolExecution(ctx, call, info, decision, startedAt, revisionBefore, revisionBefore, "", "", "", false, err, nil)
		return toolresult.Result{}, err
	}
	if validator, ok := tool.(InputValidatingTool); ok {
		if err := validator.ValidateInput(call.Arguments); err != nil {
			t.recordToolExecution(ctx, call, info, decision, startedAt, revisionBefore, revisionBefore, "", "", "", false, err, nil)
			return toolresult.Result{}, err
		}
	}

	if err := t.checkPermission(ctx, info, call); err != nil {
		decision.Action = ToolPolicyDeny
		decision.Reason = "workspace boundary"
		t.recordToolExecution(ctx, call, info, decision, startedAt, revisionBefore, revisionBefore, "", "", "", false, err, nil)
		return toolresult.Result{}, err
	}

	if priorRepeats := t.repeatedToolInputCount(call, revisionBefore); !allowRepeated && priorRepeats >= repeatedToolInputPriorLimit {
		err := repeatedToolInputError{
			ToolName:        call.Name,
			ArgumentsSHA256: toolArgumentsSHA256(call.Arguments),
			Revision:        revisionBefore,
			PriorRepeats:    priorRepeats,
			MaxPriorRepeats: repeatedToolInputPriorLimit,
		}
		t.recordToolExecution(ctx, call, info, decision, startedAt, revisionBefore, revisionBefore, "", "", "", false, err, nil)
		return toolresult.Result{}, err
	}

	var result toolresult.Result
	var err error
	if callAware, ok := tool.(CallAwareRichTool); ok {
		result, err = callAware.ExecuteResultCall(ctx, call)
	} else if richTool, ok := tool.(RichTool); ok {
		result, err = richTool.ExecuteResult(ctx, call.Arguments)
	} else {
		var text string
		text, err = tool.Execute(ctx, call.Arguments)
		result = toolresult.FromText(text)
	}
	if err != nil {
		result.IsError = true
	}
	if validationErr := result.Validate(); validationErr != nil {
		if err == nil {
			err = fmt.Errorf("tool %q returned invalid rich result: %w", call.Name, validationErr)
		} else {
			err = fmt.Errorf("%w; tool %q also returned invalid rich result: %v", err, call.Name, validationErr)
		}
		result.IsError = true
	}
	if info.Kind == ToolKindMCP {
		result = redactRichToolText(result)
	}
	rawProjection := result.TextProjection()
	returned := result.Clone()
	returnedProjection := rawProjection
	resultRef := ""
	resultBudgeted := false
	var projectionDiag *ProjectionDiagnostics
	if err == nil {
		returned, resultRef, resultBudgeted, projectionDiag = t.finalizeToolResult(call, result)
		returnedProjection = returned.TextProjection()
	}

	revisionAfter := revisionBefore
	if !info.ReadOnly {
		// A mutating tool may have changed the workspace; recompute so the
		// telemetry record shows the post-execution state. Read-only tools
		// cannot mutate, so their before/after revisions are identical and
		// the second git round-trip is pure overhead.
		revisionAfter = workspaceRevision(ctx, t.env.RootDir)
	}
	t.recordToolExecution(ctx, call, info, decision, startedAt, revisionBefore, revisionAfter, rawProjection, returnedProjection, resultRef, resultBudgeted, err, projectionDiag)
	return returned, err
}

func redactRichToolText(result toolresult.Result) toolresult.Result {
	redacted := result.Clone()
	for index := range redacted.Content {
		if redacted.Content[index].Type == toolresult.ContentTypeText {
			redacted.Content[index].Text = redactToolOutput(redacted.Content[index].Text)
		}
	}
	return redacted
}

func validateToolArgumentsJSON(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	if _, ok := payload.(map[string]any); !ok {
		return errors.New("tool arguments must be a JSON object")
	}
	return nil
}

type repeatedToolInputError struct {
	ToolName        string
	ArgumentsSHA256 string
	Revision        string
	PriorRepeats    int
	MaxPriorRepeats int
}

func (e repeatedToolInputError) Error() string {
	return fmt.Sprintf(
		"tool %q blocked repeated identical input: error_kind=repeated_tool_input args_sha256=%s prior_repeats=%d max_prior_repeats=%d workspace_revision=%s safe_retry=%q model_next_action=%q",
		e.ToolName,
		e.ArgumentsSHA256,
		e.PriorRepeats,
		e.MaxPriorRepeats,
		e.Revision,
		"inspect prior tool evidence, change the input, wait for new evidence, or change the workspace before retrying",
		"stop repeating the same call; use existing observations or choose a different next action",
	)
}

func (t *Toolkit) repeatedToolInputCount(call providers.ToolCall, revision string) int {
	// Context transitions depend on the active window, not filesystem changes.
	// The agent owns admission at the completed tool-batch boundary.
	if call.Name == newContextToolName {
		return 0
	}
	if t == nil || t.env == nil || isRepeatablePollingTool(call) {
		return 0
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return 0
	}
	argumentsSHA256 := toolArgumentsSHA256(call.Arguments)
	var count int
	for _, record := range t.env.toolTelemetry.snapshot() {
		if record.Name != call.Name ||
			record.ArgumentsSHA256 != argumentsSHA256 ||
			strings.TrimSpace(record.RevisionBefore) != revision {
			continue
		}
		count++
	}
	return count
}

func isRepeatablePollingTool(call providers.ToolCall) bool {
	name := strings.TrimSpace(call.Name)
	// Channel inbox and room contents can change independently of the file
	// workspace revision used by the repeated-input guard. Identical reads are
	// therefore normal polling, while chat mutation tools remain protected.
	if name == "chat_check" || name == "chat_read" {
		return true
	}
	if strings.HasPrefix(name, "mcp_plugin_cua_mac_computer_computer_") {
		var args struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &args); err == nil && args.Action == "observe" {
			return true
		}
	}
	if name == browserToolName {
		// Re-observing/re-screenshotting the same tab is the browser's polling
		// idiom (a page settles between identical calls), so exempt it from the
		// repeated-input guard the way CUA observe is exempt.
		var args struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &args); err == nil {
			switch args.Action {
			case "observe", "screenshot", "wait_for", "tabs":
				return true
			}
		}
	}
	if name == "bash" {
		var args bashArgs
		if err := decodeArgs(call.Arguments, &args); err == nil {
			switch normalizeBashAction(args) {
			case bashActionListBackground, bashActionReadBackground:
				return true
			case bashActionRun:
				return bashCommandLooksLikeVerification(args.Command)
			}
		}
	}
	return false
}

func (t *Toolkit) recordToolExecution(
	ctx context.Context,
	call providers.ToolCall,
	info ToolInfo,
	decision ToolPolicyDecision,
	startedAt time.Time,
	revisionBefore string,
	revisionAfter string,
	result string,
	returned string,
	resultRef string,
	resultBudgeted bool,
	err error,
	projection *ProjectionDiagnostics,
) {
	artifactRefs := extractToolArtifactRefs(result, resultRef)
	loadedDeferredTools, newlyLoadedDeferredTools, alreadyLoadedDeferredTools, toolSurfaceChanged := extractToolSearchLoadMetadata(call.Name, result)
	var stepIndexPtr *int
	if stepIndex, ok := toolctx.StepIndex(ctx); ok {
		value := stepIndex
		stepIndexPtr = &value
	}
	record := ToolExecutionRecord{
		Name:                       call.Name,
		StepIndex:                  stepIndexPtr,
		CallID:                     call.ID,
		ArgumentsSHA256:            toolArgumentsSHA256(call.Arguments),
		ResultAction:               extractToolResultAction(result),
		Kind:                       info.Kind,
		Exposure:                   info.Exposure,
		Risk:                       info.Risk,
		ClassificationReason:       info.Reason,
		PolicyAction:               decision.Action,
		PolicyReason:               decision.Reason,
		ReadOnly:                   info.ReadOnly,
		ConcurrencySafe:            info.ConcurrencySafe,
		StartedAt:                  startedAt,
		DurationMS:                 time.Since(startedAt).Milliseconds(),
		RevisionBefore:             revisionBefore,
		RevisionAfter:              revisionAfter,
		Success:                    err == nil,
		RawOutputBytes:             len(result),
		ReturnedOutputBytes:        len(returned),
		ResultBudgeted:             resultBudgeted,
		ResultRef:                  resultRef,
		ArtifactRefs:               artifactRefs,
		LoadedDeferredTools:        loadedDeferredTools,
		NewlyLoadedDeferredTools:   newlyLoadedDeferredTools,
		AlreadyLoadedDeferredTools: alreadyLoadedDeferredTools,
		ToolSurfaceChanged:         toolSurfaceChanged,
		PatchRiskSummary:           extractToolPatchRisk(call.Name, result),
		Projection:                 projection,
	}
	if err != nil {
		record.Error = err.Error()
		record.ErrorKind = extractToolErrorKind(record.Error)
	}
	t.env.toolTelemetry.record(record)
}

func toolArgumentsSHA256(arguments string) string {
	sum := sha256.Sum256([]byte(arguments))
	return hex.EncodeToString(sum[:])
}

func extractToolResultAction(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return ""
	}
	action, _ := payload["action"].(string)
	return sanitizeShortToolValue(action, 80)
}

func extractToolSearchLoadMetadata(toolName, result string) ([]string, []string, []string, bool) {
	if toolName != "tool_search" {
		return nil, nil, nil, false
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return nil, nil, nil, false
	}
	var payload struct {
		LoadedTools    []string `json:"loaded_tools"`
		NewlyLoaded    []string `json:"newly_loaded"`
		AlreadyLoaded  []string `json:"already_loaded"`
		SurfaceChanged bool     `json:"surface_changed"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return nil, nil, nil, false
	}
	return sanitizeToolNameList(payload.LoadedTools), sanitizeToolNameList(payload.NewlyLoaded), sanitizeToolNameList(payload.AlreadyLoaded), payload.SurfaceChanged
}

func sanitizeToolNameList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = sanitizeShortToolValue(value, 120)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sanitizeShortToolValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= limit {
			break
		}
	}
	return strings.Trim(b.String(), "-._")
}

func extractToolErrorKind(message string) string {
	const marker = "error_kind="
	idx := strings.Index(message, marker)
	if idx < 0 {
		return ""
	}
	rest := message[idx+len(marker):]
	if rest == "" {
		return ""
	}
	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		rest = rest[1:]
		end := strings.IndexByte(rest, quote)
		if end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	end := 0
	for end < len(rest) {
		ch := rest[end]
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '_' || ch == '-' {
			end++
			continue
		}
		break
	}
	return strings.TrimSpace(rest[:end])
}

func extractToolArtifactRefs(result, resultRef string) []string {
	seen := map[string]bool{}
	out := []string(nil)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	add(resultRef)

	var payload any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return out
	}
	collectToolArtifactRefs(payload, add)
	return out
}

func extractToolPatchRisk(toolName, result string) *ToolPatchRisk {
	if toolName != "apply_patch" || strings.TrimSpace(result) == "" {
		return nil
	}
	var payload struct {
		RiskSummary ToolPatchRisk `json:"risk_summary"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(payload.RiskSummary.RiskLevel) == "" &&
		payload.RiskSummary.FileCount == 0 &&
		payload.RiskSummary.HunkCount == 0 {
		return nil
	}
	if payload.RiskSummary.Actions == nil {
		payload.RiskSummary.Actions = map[string]int{}
	}
	return &payload.RiskSummary
}

func collectToolArtifactRefs(value any, add func(string)) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if toolArtifactRefKey(key) {
				collectArtifactValue(item, add)
				continue
			}
			collectToolArtifactRefs(item, add)
		}
	case []any:
		for _, item := range v {
			collectToolArtifactRefs(item, add)
		}
	}
}

func collectArtifactValue(value any, add func(string)) {
	switch v := value.(type) {
	case string:
		add(v)
	case []any:
		for _, item := range v {
			collectArtifactValue(item, add)
		}
	case map[string]any:
		if path, ok := v["path"].(string); ok {
			add(path)
		}
		if path, ok := v["report_path"].(string); ok {
			add(path)
		}
	}
}

func toolArtifactRefKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "full_log_ref", "result_ref", "artifact_ref", "artifact_refs",
		"artifact_path", "artifact_paths", "artifacts",
		"report_path", "final_report_path", "script_path",
		"trace_path", "patch_path", "manifest_path", "archive_path":
		return true
	default:
		return false
	}
}

func workspaceRevision(ctx context.Context, rootDir string) string {
	if rootDir == "" {
		return ""
	}
	revCtx, cancel := context.WithTimeout(ctx, workspaceRevisionTimeout)
	defer cancel()

	// One subprocess yields both the HEAD commit and the worktree status:
	// porcelain v2 --branch opens with a "# branch.oid <sha>" record and the
	// remaining NUL-terminated records are the status entries. Unborn repos
	// print "# branch.oid (initial)", which is treated the same as a missing
	// HEAD below (the filesystem digest fallback).
	statusCmd := exec.CommandContext(revCtx, "git", "status", "--porcelain=v2", "-z", "--branch")
	statusCmd.Dir = rootDir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return filesystemRevisionFallback(revCtx, rootDir)
	}

	head := ""
	records := make([]byte, 0, len(statusOut))
	for _, record := range bytes.Split(statusOut, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		if bytes.HasPrefix(record, []byte("# branch.oid ")) {
			head = strings.TrimSpace(string(bytes.TrimPrefix(record, []byte("# branch.oid "))))
			continue
		}
		// Header records (branch.head/upstream/ab) describe repo position, not
		// worktree content; file records alone keep the digest stable across
		// fetch/push of unrelated refs.
		if bytes.HasPrefix(record, []byte("# ")) {
			continue
		}
		records = append(records, record...)
		records = append(records, 0)
	}

	head = trimRevisionToken(head)
	if head == "" {
		return filesystemRevisionFallback(revCtx, rootDir)
	}
	if len(head) >= 12 {
		head = head[:12]
	}
	sum := sha256.Sum256(records)
	return "git:" + head + ":worktree:" + hex.EncodeToString(sum[:])[:16]
}

func filesystemRevisionFallback(ctx context.Context, rootDir string) string {
	digest, ok := filesystemWorkspaceRevision(ctx, rootDir)
	if !ok {
		return ""
	}
	return "fs:worktree:" + digest[:16]
}

func filesystemWorkspaceRevision(ctx context.Context, rootDir string) (string, bool) {
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}

	hash := sha256.New()
	fileCount := 0
	var byteCount int64
	truncated := false

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if isWorkspaceRevisionSkippedDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		mode := info.Mode()
		if mode.Type() != 0 {
			recordSpecialWorkspaceEntry(hash, rel, mode, path)
			return nil
		}
		if fileCount >= workspaceDigestMaxFiles || byteCount >= workspaceDigestMaxBytes {
			truncated = true
			return filepath.SkipAll
		}
		fileCount++
		fileDigest, copied, err := digestWorkspaceFile(path, workspaceDigestMaxBytesPerFile, workspaceDigestMaxBytes-byteCount)
		if err != nil {
			fmt.Fprintf(hash, "file\t%s\tunreadable\t%d\n", rel, info.Size())
			return nil
		}
		byteCount += copied
		if copied < info.Size() {
			truncated = true
		}
		fmt.Fprintf(hash, "file\t%s\t%d\t%x\n", rel, info.Size(), fileDigest)
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return "", false
	}
	fmt.Fprintf(hash, "summary\tfiles=%d\tbytes=%d\ttruncated=%t\n", fileCount, byteCount, truncated)
	return hex.EncodeToString(hash.Sum(nil)), true
}

func isWorkspaceRevisionSkippedDir(name string) bool {
	switch name {
	case ".wuu-home":
		return true
	default:
		return isSkippedDir(name)
	}
}

func recordSpecialWorkspaceEntry(hash io.Writer, rel string, mode os.FileMode, path string) {
	if mode&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err == nil {
			fmt.Fprintf(hash, "symlink\t%s\t%s\n", rel, target)
			return
		}
	}
	fmt.Fprintf(hash, "special\t%s\t%s\n", rel, mode.Type().String())
}

func digestWorkspaceFile(path string, perFileLimit, remainingLimit int64) ([]byte, int64, error) {
	if remainingLimit <= 0 {
		return nil, 0, nil
	}
	limit := perFileLimit
	if remainingLimit < limit {
		limit = remainingLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	hash := sha256.New()
	copied, err := io.CopyN(hash, file, limit)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, copied, err
	}
	return hash.Sum(nil), copied, nil
}

func trimRevisionToken(value string) string {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'f':
			out = append(out, c)
		case c >= 'A' && c <= 'F':
			out = append(out, c+'a'-'A')
		case c >= '0' && c <= '9':
			out = append(out, c)
		}
	}
	return string(out)
}
