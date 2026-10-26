package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type AwaitAgentsResult struct {
	Action    string             `json:"action"`
	TimedOut  bool               `json:"timed_out"`
	Warnings  []string           `json:"warnings,omitempty"`
	Results   []AwaitAgentResult `json:"results"`
	NextSteps []string           `json:"next_steps,omitempty"`
}

type AwaitAgentResult struct {
	ResultID        string   `json:"result_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	TaskName        string   `json:"task_name,omitempty"`
	AgentProfile    string   `json:"agent_profile,omitempty"`
	AgentPath       string   `json:"agent_path,omitempty"`
	Status          string   `json:"status"`
	Result          string   `json:"result,omitempty"`
	ResultPath      string   `json:"result_path,omitempty"`
	ResultBytes     int      `json:"result_bytes,omitempty"`
	ResultTruncated bool     `json:"result_truncated,omitempty"`
	Error           string   `json:"error,omitempty"`
	ChangedFiles    []string `json:"changed_files,omitempty"`
	ReportPath      string   `json:"report_path,omitempty"`
	ReportKind      string   `json:"report_kind,omitempty"`
	ReportMissing   bool     `json:"report_missing,omitempty"`
	Artifacts       []string `json:"artifacts,omitempty"`
	WorktreePath    string   `json:"worktree_path,omitempty"`
	InputTokens     int      `json:"input_tokens,omitempty"`
	OutputTokens    int      `json:"output_tokens,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	ResultConsumed  bool     `json:"result_consumed,omitempty"`
	ConsumedBy      string   `json:"consumed_by,omitempty"`
}

type awaitTarget struct {
	Input string
	Meta  agentthread.Metadata
	Found bool
}

func (c *AgentControl) AwaitFrom(currentPath string, ctx context.Context, targets []string) (AwaitAgentsResult, error) {
	if c == nil || c.manager == nil {
		return AwaitAgentsResult{}, errors.New("agent control not configured")
	}
	resolved := c.resolveAwaitTargets(currentPath, targets)
	if len(resolved) == 0 {
		result := AwaitAgentsResult{Action: "await_agents", Results: []AwaitAgentResult{}}
		result.NextSteps = awaitAgentsNextSteps(result)
		return result, nil
	}

	ch := make(chan subagent.Notification, 16)
	c.manager.Subscribe(ch)
	defer c.manager.Unsubscribe(ch)

	for {
		result := c.awaitSnapshot(resolved)
		if c.awaitComplete(result.Results) {
			c.claimAwaitedAgentResults(result.Results)
			result.NextSteps = awaitAgentsNextSteps(result)
			return result, nil
		}
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			result.TimedOut = true
			c.claimAwaitedAgentResults(result.Results)
			result.NextSteps = awaitAgentsNextSteps(result)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return result, nil
			}
			return result, ctx.Err()
		}
	}
}

func (c *AgentControl) claimAwaitedAgentResults(results []AwaitAgentResult) {
	if c == nil || len(results) == 0 {
		return
	}
	for i := range results {
		result := &results[i]
		if !awaitResultSuppressesCompletion(*result) {
			continue
		}
		resultID := strings.TrimSpace(result.ResultID)
		if resultID == "" {
			continue
		}
		claimed, consumedBy, claimErr := c.ClaimAgentResultDeliveryID(resultID, agentResultConsumerAwaitAgents)
		if claimErr != nil {
			providers.DebugLogf("agentcontrol: persist await result claim for %s: %v", resultID, claimErr)
			continue
		}
		if claimed {
			continue
		}
		// The delivery ledger dedupes automatic injection into the parent's
		// context, not availability: an explicit await always returns the
		// result text. Annotate that a prior path already consumed it, but
		// never blank the text a parent asked for by name.
		result.ResultConsumed = true
		result.ConsumedBy = consumedBy
	}
}

func awaitResultSuppressesCompletion(result AwaitAgentResult) bool {
	switch strings.TrimSpace(result.Status) {
	case string(harness.TaskStatusCompleted), string(harness.TaskStatusFailed), string(harness.TaskStatusCancelled):
		return true
	default:
		return false
	}
}

// ActiveTaskReminder renders the per-turn <subagent_status> block injected into
// the model request (never appended to durable history). Since the list_agents
// tool was retired, this reminder is the sole model-facing child-agent roster:
// it enumerates every descendant agent — running and terminal — with its type,
// description, status, timing, and a missing-structured-report flag, replacing
// what list_agents used to return without adding a tool to the schema.
func (c *AgentControl) ActiveTaskReminder(currentPath string) string {
	list := c.ListFrom(currentPath, "")
	if len(list) == 0 {
		return ""
	}
	const maxReminderTasks = 12
	var b strings.Builder
	b.WriteString("<subagent_status>\n")
	b.WriteString("Child agents in this session (running and finished). Read this instead of polling; finished agents resume you automatically:\n")
	limit := len(list)
	if limit > maxReminderTasks {
		limit = maxReminderTasks
	}
	now := time.Now().UTC()
	for i := 0; i < limit; i++ {
		snap := list[i]
		label := snap.AgentPath
		if label == "" {
			label = snap.ID
		}
		b.WriteString("- ")
		b.WriteString(label)
		if snap.TaskName != "" {
			b.WriteString(" (")
			b.WriteString(snap.TaskName)
			b.WriteString(")")
		}
		if snap.Type != "" {
			b.WriteString(" [")
			b.WriteString(snap.Type)
			b.WriteString("]")
		}
		b.WriteString(": ")
		b.WriteString(string(snap.Status))
		if snap.Status == subagent.StatusCompleted && c.reportKindForTask(snap.ID) != harness.ReportKindStructured {
			b.WriteString(", missing agent_report")
		}
		if desc := strings.TrimSpace(snap.Description); desc != "" {
			b.WriteString(" - ")
			b.WriteString(desc)
		}
		if elapsed := activeTaskElapsed(now, snap); elapsed != "" {
			b.WriteString(" (")
			b.WriteString(elapsed)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(list) > limit {
		b.WriteString("- ... ")
		b.WriteString(strconv.Itoa(len(list) - limit))
		b.WriteString(" more\n")
	}
	b.WriteString("\nA terminal agent's result is yours to use once - do not re-trigger it. Reconcile any changed_file_overlap warnings before merge.\n")
	b.WriteString("</subagent_status>")
	return b.String()
}

func activeTaskElapsed(now time.Time, snap subagent.SubAgentSnapshot) string {
	if snap.StartedAt.IsZero() {
		return ""
	}
	end := now
	if !snap.CompletedAt.IsZero() {
		end = snap.CompletedAt
	}
	d := end.Sub(snap.StartedAt)
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	default:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
}

func (c *AgentControl) resolveAwaitTargets(currentPath string, targets []string) []awaitTarget {
	cleaned := trimStringSlice(targets)
	if len(cleaned) == 0 {
		return c.activeDescendantTargets(currentPath)
	}
	out := make([]awaitTarget, 0, len(cleaned))
	seen := map[string]struct{}{}
	for _, target := range cleaned {
		meta, ok := c.threads.ResolveFrom(currentPath, target)
		if !ok {
			if snap := c.snapshotByID(target); snap != nil {
				meta = metadataFromSnapshot(*snap)
				ok = true
			}
		}
		if !ok {
			// Cross-restart parity with send_message/followup_task: an
			// explicitly addressed run the live manager no longer tracks
			// may still have a persisted snapshot. Rehydrate it lazily so
			// await reports its terminal state instead of not_found.
			// Rehydration failures (no snapshot, pre-resume version,
			// missing working directory, ...) keep the not_found path.
			// Only explicit targets rehydrate; targetless awaits never
			// scan history (see activeDescendantTargets).
			if sa, err := c.rehydrateAgent(target); err == nil && sa != nil {
				snap := sa.Snapshot()
				meta = metadataFromSnapshot(snap)
				ok = true
			}
		}
		key := strings.TrimSpace(target)
		if ok {
			key = meta.ID
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, awaitTarget{Input: target, Meta: meta, Found: ok})
	}
	return out
}

func (c *AgentControl) activeDescendantTargets(currentPath string) []awaitTarget {
	if c == nil || c.threads == nil {
		return nil
	}
	current := strings.TrimSpace(currentPath)
	if current == "" {
		current = agentthread.RootPath
	}
	out := make([]awaitTarget, 0)
	for _, meta := range c.threads.List() {
		if meta.Path == agentthread.RootPath || !isDescendantAgentPath(current, meta.Path) {
			continue
		}
		result := c.awaitResultForTarget(awaitTarget{Meta: meta, Found: true})
		if isAwaitActiveStatus(result.Status) {
			out = append(out, awaitTarget{Meta: meta, Found: true})
			continue
		}
		// Surface a completed worker that has not yet filed a structured
		// report until its raw result has been delivered once. After that,
		// re-joining it from a no-target await only produces an empty,
		// already-consumed row and traps parents in a polling loop: the
		// child is terminal, so its status will never change on its own.
		if result.Status == string(harness.TaskStatusCompleted) && result.ReportMissing && !c.agentResultDeliveryConsumed(result.ResultID) {
			out = append(out, awaitTarget{Meta: meta, Found: true})
		}
	}
	return out
}

func isDescendantAgentPath(parent, child string) bool {
	parent = strings.TrimRight(strings.TrimSpace(parent), "/")
	child = strings.TrimRight(strings.TrimSpace(child), "/")
	if parent == "" {
		parent = agentthread.RootPath
	}
	if parent == agentthread.RootPath {
		return child != "" && child != agentthread.RootPath
	}
	return strings.HasPrefix(child, parent+"/")
}

func (c *AgentControl) awaitSnapshot(targets []awaitTarget) AwaitAgentsResult {
	results := make([]AwaitAgentResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, c.awaitResultForTarget(target))
	}
	return AwaitAgentsResult{Action: "await_agents", Warnings: changedFileOverlapWarnings(results), Results: results}
}

func awaitAgentsNextSteps(result AwaitAgentsResult) []string {
	if len(result.Results) == 0 {
		return []string{"No active or matching child agents were found; continue local work or spawn_agent only if delegation is still needed."}
	}
	if result.TimedOut {
		return []string{
			"Some child agents are still active; continue non-overlapping local work when possible.",
			"Let their completion notifications resume you when synthesis or integration is blocked on their output.",
		}
	}

	steps := make([]string, 0, 4)
	if awaitResultsHaveStatus(result.Results, string(harness.TaskStatusFailed)) || awaitResultsHaveStatus(result.Results, string(subagent.StatusFailed)) {
		steps = append(steps, "Inspect failed agent errors and artifacts, then decide whether to retry with a narrower brief, rollback, or ask the user.")
	}
	if len(result.Warnings) > 0 {
		steps = append(steps, "Resolve changed-file overlap warnings before synthesis or merge decisions.")
	}
	if len(steps) == 0 {
		steps = append(steps, "Use agent reports, changed_files, artifacts, and results to synthesize the parent answer.")
	}
	return steps
}

func awaitResultsHaveStatus(results []AwaitAgentResult, status string) bool {
	for _, result := range results {
		if result.Status == status {
			return true
		}
	}
	return false
}

func (c *AgentControl) awaitResultForTarget(target awaitTarget) AwaitAgentResult {
	if !target.Found {
		return AwaitAgentResult{
			AgentID: strings.TrimSpace(target.Input),
			Status:  "not_found",
			Error:   fmt.Sprintf("agent %q not found", strings.TrimSpace(target.Input)),
		}
	}

	meta := target.Meta
	out := AwaitAgentResult{
		AgentID:   meta.ID,
		TaskName:  meta.TaskName,
		AgentPath: meta.Path,
		Status:    string(meta.Status),
	}

	snap := c.snapshotByID(meta.ID)
	if snap != nil {
		out = awaitResultFromSnapshot(*snap)
		if delivery, err := c.ensureAgentResultDelivery(*snap); err == nil && delivery.ResultID != "" {
			out.ResultID = delivery.ResultID
		} else if err != nil {
			providers.DebugLogf("agentcontrol: persist result-ready for %s: %v", snap.ID, err)
		}
		ref := c.AgentResultReference(*snap)
		out.Result = ref.Preview
		out.ResultPath = ref.Path
		out.ResultBytes = ref.Bytes
		out.ResultTruncated = ref.Truncated
	}
	if task, ok := c.harnessTask(meta.ID); ok {
		applyHarnessTaskToAwaitResult(&out, task)
		if snap != nil {
			// The harness can publish completion after the live snapshot was
			// captured. Keep lifecycle and result from the same generation;
			// otherwise await can return completed with the earlier empty result.
			out.Status = string(snap.Status)
		}
	}
	reportPath, artifacts := c.harnessReportForTask(meta.ID)
	out.ReportPath = c.sessionArtifactRef(reportPath)
	out.Artifacts = c.sessionArtifactRefs(artifacts)
	if report, ok := c.harnessReportDetailsForTask(meta.ID); ok {
		out.ChangedFiles = trimStringSlice(report.ChangedFiles)
	}
	// A completed worker's report is typed metadata, not a lifecycle status:
	// structured when it filed agent_report, otherwise final_text (a
	// synthesized handoff). report_missing is the derived alias for
	// kind != structured, kept for wire compatibility this release.
	if out.Status == string(subagent.StatusCompleted) {
		kind := c.reportKindForTask(meta.ID)
		out.ReportKind = string(kind)
		out.ReportMissing = kind != harness.ReportKindStructured
	}
	return out
}

func (c *AgentControl) harnessReportDetailsForTask(taskID string) (harness.Report, bool) {
	if c == nil || c.harnessStore == nil {
		return harness.Report{}, false
	}
	report, ok, err := c.harnessStore.ReportForTask(taskID)
	if err != nil || !ok {
		return harness.Report{}, false
	}
	return report, true
}

func (c *AgentControl) snapshotByID(id string) *subagent.SubAgentSnapshot {
	if c == nil || c.manager == nil {
		return nil
	}
	sa := c.manager.Get(strings.TrimSpace(id))
	if sa == nil {
		return nil
	}
	snap := sa.Snapshot()
	return &snap
}

func metadataFromSnapshot(snap subagent.SubAgentSnapshot) agentthread.Metadata {
	return agentthread.Metadata{
		ID:           snap.ID,
		Path:         snap.AgentPath,
		TaskName:     snap.TaskName,
		AgentProfile: snap.AgentProfile,
		Role:         snap.Type,
		ParentID:     snap.ParentID,
		CWD:          snap.WorkerRoot,
		Model:        snap.Model,
		Status:       threadStatusFromSubAgent(snap.Status),
	}
}

func awaitResultFromSnapshot(snap subagent.SubAgentSnapshot) AwaitAgentResult {
	out := AwaitAgentResult{
		AgentID:      snap.ID,
		TaskName:     snap.TaskName,
		AgentProfile: snap.AgentProfile,
		AgentPath:    snap.AgentPath,
		Status:       string(snap.Status),
		Result:       snap.Result,
		InputTokens:  snap.InputTokens,
		OutputTokens: snap.OutputTokens,
	}
	if snap.Error != nil {
		out.Error = snap.Error.Error()
	}
	if !snap.CompletedAt.IsZero() && !snap.StartedAt.IsZero() {
		out.DurationMS = snap.CompletedAt.Sub(snap.StartedAt).Milliseconds()
	}
	return out
}

func applyHarnessTaskToAwaitResult(out *AwaitAgentResult, task harness.Task) {
	if out.AgentID == "" {
		out.AgentID = task.ID
	}
	if out.TaskName == "" {
		out.TaskName = task.Name
	}
	if out.AgentPath == "" {
		out.AgentPath = task.Path
	}
	if shouldPreferHarnessStatus(task.Status) {
		out.Status = string(task.Status)
	}
	if out.Error == "" {
		out.Error = task.Error
	}
	if task.Workspace.Mode == harness.WorkspaceWorktree {
		out.WorktreePath = task.Workspace.Root
	}
	if out.InputTokens == 0 {
		out.InputTokens = task.InputTokens
	}
	if out.OutputTokens == 0 {
		out.OutputTokens = task.OutputTokens
	}
	if out.DurationMS == 0 && !task.CompletedAt.IsZero() && !task.StartedAt.IsZero() {
		out.DurationMS = task.CompletedAt.Sub(task.StartedAt).Milliseconds()
	}
}

func shouldPreferHarnessStatus(status harness.TaskStatus) bool {
	switch status {
	case harness.TaskStatusQueued, harness.TaskStatusCompleted, harness.TaskStatusFailed, harness.TaskStatusCancelled, harness.TaskStatusInterrupted:
		return true
	default:
		return false
	}
}

// awaitComplete reports whether every awaited row is consumable. A row is
// still in flight when its status is active, and also when a requires_report
// run this process started shows a completed snapshot whose terminal
// notification the consumer has not recorded yet: in that window the runtime
// has not decided between accepting a structured report, launching the one
// closing-turn nudge, and synthesizing a final_text report, so handing the
// row to the parent would leak a report-less completion (and a one-shot exec
// parent would exit and kill the closing turn mid-flight). Runs from
// previous processes are never in that window, so rehydrated dormant results
// settle on the persisted facts immediately.
func (c *AgentControl) awaitComplete(results []AwaitAgentResult) bool {
	for _, result := range results {
		if isAwaitActiveStatus(result.Status) {
			return false
		}
		if result.Status == string(harness.TaskStatusCompleted) && c.reportSettlementPending(result.AgentID) {
			return false
		}
	}
	return true
}

// CompletionOverlapWarnings surfaces the changed-file conflict signal that the
// deleted await_agents tool used to compute in awaitSnapshot. When a child
// agent finishes, it compares that agent's changed_files against every sibling
// descendant agent's changed_files and returns "changed_file_overlap:" lines
// for any file both touched. The subagent-completion wakeup path appends these
// to the handoff so the resumed parent still sees overlapping writes it must
// reconcile before synthesis or merge — the value formerly delivered only when
// a parent explicitly awaited multiple agents.
func (c *AgentControl) CompletionOverlapWarnings(snap subagent.SubAgentSnapshot) []string {
	if c == nil || c.threads == nil {
		return nil
	}
	completing := c.awaitResultForTarget(awaitTarget{Meta: metadataFromSnapshot(snap), Found: true})
	completingFiles := trimStringSlice(completing.ChangedFiles)
	if len(completingFiles) == 0 {
		return nil
	}
	completingLabel := completing.AgentPath
	if completingLabel == "" {
		completingLabel = completing.AgentID
	}
	completingSet := make(map[string]struct{}, len(completingFiles))
	for _, file := range completingFiles {
		completingSet[file] = struct{}{}
	}

	// file -> other-agent labels that also touched it
	overlaps := map[string][]string{}
	for _, meta := range c.threads.List() {
		if meta.Path == agentthread.RootPath || meta.ID == snap.ID {
			continue
		}
		report, ok := c.harnessReportDetailsForTask(meta.ID)
		if !ok {
			continue
		}
		label := meta.Path
		if label == "" {
			label = meta.ID
		}
		for _, file := range trimStringSlice(report.ChangedFiles) {
			if _, shared := completingSet[file]; shared {
				overlaps[file] = append(overlaps[file], label)
			}
		}
	}
	if len(overlaps) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(overlaps))
	for file, labels := range overlaps {
		warnings = append(warnings, "changed_file_overlap: "+file+" touched by "+completingLabel+", "+strings.Join(labels, ", "))
	}
	sort.Strings(warnings)
	return warnings
}

func changedFileOverlapWarnings(results []AwaitAgentResult) []string {
	owners := map[string][]string{}
	for _, result := range results {
		label := result.AgentPath
		if label == "" {
			label = result.AgentID
		}
		for _, file := range trimStringSlice(result.ChangedFiles) {
			owners[file] = append(owners[file], label)
		}
	}
	warnings := make([]string, 0)
	for file, labels := range owners {
		if len(labels) < 2 {
			continue
		}
		warnings = append(warnings, "changed_file_overlap: "+file+" touched by "+strings.Join(labels, ", "))
	}
	return warnings
}

func isAwaitActiveStatus(status string) bool {
	switch status {
	case string(harness.TaskStatusPending), string(harness.TaskStatusQueued), string(harness.TaskStatusRunning):
		return true
	default:
		return false
	}
}
