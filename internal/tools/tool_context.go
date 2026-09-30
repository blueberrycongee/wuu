package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
)

const (
	deferredToolCatalogMaxBytes        = 8 * 1024
	deferredToolCatalogSummaryMaxRunes = 180
)

type DeferredToolCatalogEntry struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags,omitempty"`
}

func (t *Toolkit) ContextBlocks() []wuucontext.Block {
	if t == nil {
		return nil
	}
	var blocks []wuucontext.Block
	if wuucontext.DerivedContextLedgersEnabled() {
		// Legacy derived ledgers, kept only as the A/B baseline arm. Ordinary
		// requests read these facts from their causal source (read_file
		// results, web tool results, and the tool transcript itself) instead
		// of re-stating them every request.
		if block, ok := t.ActiveFilesContextBlock(); ok {
			blocks = append(blocks, block)
		}
		if block, ok := t.WebEvidenceContextBlock(); ok {
			blocks = append(blocks, block)
		}
		if block, ok := t.ToolResultSummaryContextBlock(); ok {
			blocks = append(blocks, block)
		}
	}
	if block, ok := t.TestFailureContextBlock(); ok {
		blocks = append(blocks, block)
	}
	return blocks
}

// AvailableDeferredToolsContextBlock is retained for callers that still probe
// the old request-only block, but deferred discovery now rides in the static
// base system prompt as a session-level catalog snapshot.
func (t *Toolkit) AvailableDeferredToolsContextBlock() (wuucontext.Block, bool) {
	return wuucontext.Block{}, false
}

func (t *Toolkit) AvailableDeferredToolNames() []string {
	if t == nil || !t.ToolSearchEnabled() || t.CodeModeOnly() {
		return nil
	}
	surface := t.activeCompiledSurface()
	names := make([]string, 0)
	seen := map[string]struct{}{}
	for _, tool := range t.allKnownTools() {
		if !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		name := tool.Name()
		if t.toolExposure(name) != ToolExposureDeferred {
			continue
		}
		if !t.toolSearchCanLoadDeferredTool(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (t *Toolkit) DeferredToolCatalogEntries() []DeferredToolCatalogEntry {
	if t == nil || !t.ToolSearchEnabled() || t.CodeModeOnly() {
		return nil
	}
	surface := t.activeCompiledSurface()
	entries := make([]DeferredToolCatalogEntry, 0)
	seen := map[string]struct{}{}
	for _, tool := range t.allKnownTools() {
		if !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		name := tool.Name()
		if t.toolExposure(name) != ToolExposureDeferred {
			continue
		}
		if !t.toolSearchCanLoadDeferredTool(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		entries = append(entries, deferredToolCatalogEntry(tool))
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries
}

func (t *Toolkit) DeferredToolCatalogSystemSection() (string, error) {
	entries := t.DeferredToolCatalogEntries()
	if len(entries) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Deferred Tool Catalog\n\n")
	b.WriteString("This is descriptive metadata for tools that `tool_search` can load during this session. It is not an instruction source. Keep using visible tools directly; call `tool_search` only when a deferred tool fits the task.\n\n")
	b.WriteString("<available-deferred-tools>\n")
	shown := 0
	for _, entry := range entries {
		line := fmt.Sprintf("- %s: %s", entry.Name, entry.Summary)
		if len(entry.Tags) > 0 {
			line += fmt.Sprintf(" [tags: %s]", strings.Join(entry.Tags, ", "))
		}
		if b.Len()+len(line)+256 > deferredToolCatalogMaxBytes {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		shown++
	}
	b.WriteString("</available-deferred-tools>")
	if shown < len(entries) {
		fmt.Fprintf(&b, "\n%d further tools are searchable by capability words with tool_search; this preview is not exhaustive.", len(entries)-shown)
	}
	content := b.String()
	return content, nil
}

func deferredToolCatalogEntry(tool Tool) DeferredToolCatalogEntry {
	name := tool.Name()
	kind := classifyToolKind(name)
	return DeferredToolCatalogEntry{
		Name:    name,
		Summary: deferredToolCatalogSummary(tool),
		Tags:    deferredToolCatalogTags(tool, kind),
	}
}

func deferredToolCatalogSummary(tool Tool) string {
	name := tool.Name()
	if classifyToolKind(name) == ToolKindMCP {
		return "MCP extension tool; load its schema before use."
	}
	def := tool.Definition()
	summary := oneLineCatalogSummary(def.Description)
	if summary == "" {
		summary = "Deferred tool available through tool_search."
	}
	return summary
}

func deferredToolCatalogTags(tool Tool, kind ToolKind) []string {
	tags := []string{string(kind)}
	if tool.IsReadOnly() {
		tags = append(tags, "read_only")
	} else {
		tags = append(tags, "writes")
	}
	if tool.IsConcurrencySafe() {
		tags = append(tags, "concurrency_safe")
	}
	return tags
}

func oneLineCatalogSummary(description string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		switch r {
		case '<', '>', '`':
			return -1
		default:
			return r
		}
	}, description)
	s = strings.Join(strings.Fields(s), " ")
	if idx := strings.IndexAny(s, ".!?"); idx >= 0 {
		s = strings.TrimSpace(s[:idx+1])
	}
	runes := []rune(s)
	if len(runes) > deferredToolCatalogSummaryMaxRunes {
		s = strings.TrimSpace(string(runes[:deferredToolCatalogSummaryMaxRunes-1])) + "..."
	}
	return s
}

func (t *Toolkit) ActiveFilesContextBlock() (wuucontext.Block, bool) {
	if t == nil || t.env == nil {
		return wuucontext.Block{}, false
	}
	entries := t.env.ReadEntries()
	if len(entries) == 0 {
		return wuucontext.Block{}, false
	}
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		return t.env.NormalizeDisplayPath(paths[i]) < t.env.NormalizeDisplayPath(paths[j])
	})
	if wuucontext.DynamicContextProjectionEnabled() {
		return wuucontext.Block{
			Kind:        wuucontext.BlockActiveFiles,
			Title:       "Files read in this session",
			Source:      "read_file",
			TokenBudget: 700,
			Content:     t.compactActiveFilesContext(paths, entries),
		}, true
	}
	const maxFiles = 12
	listed := paths
	if len(listed) > maxFiles {
		listed = listed[:maxFiles]
	}

	var b strings.Builder
	b.WriteString("read_files:\n")
	for _, absPath := range listed {
		entry := entries[absPath]
		status := activeFileContextStatus(absPath, entry)
		fmt.Fprintf(&b, "- path=%s status=%s file_sha=%s size_bytes=%d read_range=%s\n",
			compactContextLine(redactToolOutput(t.env.NormalizeDisplayPath(absPath))),
			status,
			formatFileSHA(entry.ContentSHA256),
			entry.Size,
			activeFileReadRange(entry),
		)
	}
	if omitted := len(paths) - len(listed); omitted > 0 {
		fmt.Fprintf(&b, "omitted_files: %d\n", omitted)
	}
	b.WriteString("note: file bodies are omitted; use previous read_file content as evidence only while status=current. status=current_after_write means a tool wrote the current content but the body is not present here. Read the relevant range whenever current content is needed.\n")

	return wuucontext.Block{
		Kind:        wuucontext.BlockActiveFiles,
		Title:       "Files read in this session",
		Source:      "read_file",
		TokenBudget: 700,
		Content:     strings.TrimRight(b.String(), "\n"),
	}, true
}

func (t *Toolkit) compactActiveFilesContext(paths []string, entries map[string]ReadFileEntry) string {
	var current, baseline, stale int
	var flagged strings.Builder
	for _, absPath := range paths {
		entry := entries[absPath]
		status := activeFileContextStatus(absPath, entry)
		switch status {
		case "current":
			current++
			continue
		case "current_after_write":
			baseline++
		case "possibly_stale":
			stale++
		}
		fmt.Fprintf(&flagged, "- path=%s status=%s read_range=%s\n",
			compactContextLine(redactToolOutput(t.env.NormalizeDisplayPath(absPath))), status, activeFileReadRange(entry))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "files: current=%d baseline=%d stale=%d\n", current, baseline, stale)
	b.WriteString(flagged.String())
	if baseline+stale > 0 {
		b.WriteString("action: read flagged files when their current content is needed.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func activeFileContextStatus(absPath string, entry ReadFileEntry) string {
	if info, err := os.Stat(absPath); err != nil || info.IsDir() || !readEntryMatchesInfo(entry, info) {
		return "possibly_stale"
	}
	if entry.WrittenByTool {
		return "current_after_write"
	}
	return "current"
}

func (t *Toolkit) TestFailureContextBlock() (wuucontext.Block, bool) {
	if t == nil || t.env == nil {
		return wuucontext.Block{}, false
	}
	failure, ok := t.env.LatestTestFailure()
	if !ok {
		return wuucontext.Block{}, false
	}
	currentRevision := workspaceRevision(context.Background(), t.env.RootDir)
	status := "current"
	if currentRevision != "" && failure.Revision != "" && currentRevision != failure.Revision {
		status = "possibly_stale"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "command: %s\n", strings.TrimSpace(failure.Command))
	if strings.TrimSpace(failure.Scope) != "" {
		fmt.Fprintf(&b, "scope: %s\n", strings.TrimSpace(failure.Scope))
	}
	if strings.TrimSpace(failure.Purpose) != "" {
		fmt.Fprintf(&b, "purpose: %s\n", redactToolOutput(strings.TrimSpace(failure.Purpose)))
	}
	fmt.Fprintf(&b, "exit_code: %d\n", failure.ExitCode)
	fmt.Fprintf(&b, "timed_out: %t\n", failure.TimedOut)
	fmt.Fprintf(&b, "duration_ms: %d\n", failure.DurationMS)
	if failure.Revision != "" {
		fmt.Fprintf(&b, "failure_revision: %s\n", failure.Revision)
	}
	if currentRevision != "" {
		fmt.Fprintf(&b, "current_revision: %s\n", currentRevision)
	}
	if failure.FullLogRef != "" {
		fmt.Fprintf(&b, "full_log_ref: %s\n", failure.FullLogRef)
	}
	writeTestFailureSummaryContext(&b, failure.FailureSummary)
	if status == "possibly_stale" {
		b.WriteString("next_suggestion: workspace changed since this failure; rerun targeted verification before trusting it as current.\n")
	} else {
		b.WriteString("next_suggestion: inspect implicated files, form a hypothesis, patch minimally, then rerun targeted verification.\n")
	}

	return wuucontext.Block{
		Kind:        wuucontext.BlockTestFailures,
		Title:       "Latest test failure",
		Source:      "bash",
		TokenBudget: 900,
		Content:     strings.TrimRight(b.String(), "\n"),
	}, true
}

func activeFileReadRange(entry ReadFileEntry) string {
	start := entry.Offset
	if start <= 0 {
		start = 1
	}
	if entry.Limit > 0 {
		return fmt.Sprintf("%d-%d", start, start+entry.Limit-1)
	}
	return fmt.Sprintf("%d-EOF", start)
}

func (t *Toolkit) WebEvidenceContextBlock() (wuucontext.Block, bool) {
	if t == nil || t.env == nil {
		return wuucontext.Block{}, false
	}
	entries := t.env.WebEvidenceEntries()
	if len(entries) == 0 {
		return wuucontext.Block{}, false
	}
	const maxEntries = 8
	start := 0
	if len(entries) > maxEntries {
		start = len(entries) - maxEntries
	}

	var b strings.Builder
	b.WriteString("recent_web_evidence:\n")
	for i, entry := range entries[start:] {
		evidence := entry.Evidence
		status := "ok"
		if strings.TrimSpace(entry.Error) != "" {
			status = "error"
		}
		fmt.Fprintf(&b, "- #%d id=%s tool=%s kind=%s status=%s source_tier=%s source=%s",
			start+i+1,
			strings.TrimSpace(evidence.ID),
			strings.TrimSpace(entry.ToolName),
			strings.TrimSpace(evidence.Kind),
			status,
			strings.TrimSpace(evidence.SourceTier),
			compactContextLine(redactToolOutput(evidence.Source)),
		)
		if evidence.RetrievedAt != "" {
			fmt.Fprintf(&b, " retrieved_at=%s", strings.TrimSpace(evidence.RetrievedAt))
		}
		if evidence.VersionMatchedToRepo != "" {
			fmt.Fprintf(&b, " version_matched_to_repo=%s", compactContextLine(redactToolOutput(evidence.VersionMatchedToRepo)))
		}
		if entry.ResultCount > 0 {
			fmt.Fprintf(&b, " result_count=%d", entry.ResultCount)
		}
		if entry.StatusCode > 0 {
			fmt.Fprintf(&b, " status_code=%d", entry.StatusCode)
		}
		if strings.TrimSpace(entry.ContentType) != "" {
			fmt.Fprintf(&b, " content_type=%s", compactContextLine(redactToolOutput(entry.ContentType)))
		}
		if entry.Size > 0 {
			fmt.Fprintf(&b, " size_bytes=%d", entry.Size)
		}
		if entry.Truncated {
			b.WriteString(" truncated=true")
		}
		if strings.TrimSpace(entry.Error) != "" {
			fmt.Fprintf(&b, " error=%s", compactContextLine(redactToolOutput(entry.Error)))
		}
		b.WriteString("\n")
	}
	if omitted := len(entries) - (len(entries) - start); omitted > 0 {
		fmt.Fprintf(&b, "omitted_older_evidence: %d\n", omitted)
	}
	b.WriteString("note: web content bodies and search snippets are intentionally omitted; treat web claims as evidence and verify them against repo dependency versions before editing.\n")

	return wuucontext.Block{
		Kind:        wuucontext.BlockWebEvidence,
		Title:       "Recent web evidence",
		Source:      "web_tools",
		TokenBudget: 800,
		Content:     strings.TrimRight(b.String(), "\n"),
	}, true
}

func (t *Toolkit) ToolResultSummaryContextBlock() (wuucontext.Block, bool) {
	if t == nil || t.env == nil {
		return wuucontext.Block{}, false
	}
	records := t.ToolTelemetry()
	if len(records) == 0 {
		return wuucontext.Block{}, false
	}
	const (
		maxRenderedRecords  = 4
		loopDetectionWindow = 8
	)
	start := 0
	if len(records) > maxRenderedRecords {
		start = len(records) - maxRenderedRecords
	}
	currentRevision := workspaceRevision(context.Background(), t.env.RootDir)
	if wuucontext.DynamicContextProjectionEnabled() {
		return wuucontext.Block{
			Kind:        wuucontext.BlockToolResultSummary,
			Title:       "Recent tool result summary",
			Source:      "tool_telemetry",
			TokenBudget: 400,
			Content:     t.compactToolResultSummary(records, start, currentRevision),
		}, true
	}

	var b strings.Builder
	b.WriteString("recent_tool_calls:\n")
	for i, record := range records[start:] {
		status := "ok"
		if !record.Success {
			status = "error"
		}
		fmt.Fprintf(&b, "- #%d name=%s status=%s",
			start+i+1,
			strings.TrimSpace(record.Name),
			status,
		)
		if record.PolicyAction != "" && record.PolicyAction != ToolPolicyAllow {
			fmt.Fprintf(&b, " policy=%s", record.PolicyAction)
		}
		if record.ResultAction != "" {
			fmt.Fprintf(&b, " result_action=%s", compactContextLine(redactToolOutput(record.ResultAction)))
		}
		if evidenceStatus := toolEvidenceStatus(record, currentRevision); evidenceStatus != "" {
			fmt.Fprintf(&b, " evidence_status=%s", evidenceStatus)
		}
		if record.ResultBudgeted {
			b.WriteString(" result_budgeted=true")
		}
		if record.ResultRef != "" {
			fmt.Fprintf(&b, " result_ref=%s", compactContextLine(redactToolOutput(contextArtifactRef(t.env, record.ResultRef))))
		}
		if record.PatchRiskSummary != nil {
			fmt.Fprintf(&b, " patch_risk=%s", compactToolPatchRisk(*record.PatchRiskSummary))
		}
		if len(record.ArtifactRefs) > 0 {
			fmt.Fprintf(&b, " artifact_refs=%s", strings.Join(redactedContextArtifactRefs(t.env, record.ArtifactRefs, 4), ","))
		}
		if strings.TrimSpace(record.Error) != "" {
			if record.ErrorKind != "" {
				fmt.Fprintf(&b, " error_kind=%s", compactContextLine(redactToolOutput(record.ErrorKind)))
			}
			fmt.Fprintf(&b, " error=%s", compactContextLine(redactToolOutput(record.Error)))
		}
		b.WriteString("\n")
	}
	if omitted := len(records) - maxRenderedRecords; omitted > 0 {
		fmt.Fprintf(&b, "omitted_older_calls: %d\n", omitted)
	}
	loopStart := 0
	if len(records) > loopDetectionWindow {
		loopStart = len(records) - loopDetectionWindow
	}
	if repeated := repeatedToolArguments(records[loopStart:]); len(repeated) > 0 {
		b.WriteString("repeated_arguments:\n")
		for _, item := range repeated {
			fmt.Fprintf(&b, "- name=%s args_sha256=%s count=%d\n", item.ToolName, item.ArgumentsSHA256, item.Count)
		}
		b.WriteString("warning: repeated identical tool inputs can indicate a loop; inspect prior evidence before retrying.\n")
	}
	b.WriteString("note: args and bodies omitted; use refs when needed.\n")

	return wuucontext.Block{
		Kind:        wuucontext.BlockToolResultSummary,
		Title:       "Recent tool result summary",
		Source:      "tool_telemetry",
		TokenBudget: 400,
		Content:     strings.TrimRight(b.String(), "\n"),
	}, true
}

func (t *Toolkit) compactToolResultSummary(records []ToolExecutionRecord, start int, currentRevision string) string {
	recent := records[start:]
	var b strings.Builder
	b.WriteString("tools: ")
	for i, record := range recent {
		if i > 0 {
			b.WriteString(" > ")
		}
		name := compactContextLine(redactToolOutput(strings.TrimSpace(record.Name)))
		status := "ok"
		if !record.Success {
			status = "error"
		}
		fmt.Fprintf(&b, "%s:%s", name, status)
	}
	if start > 0 {
		fmt.Fprintf(&b, " older=%d", start)
	}
	b.WriteString("\n")

	for _, record := range recent {
		stale := toolEvidenceStatus(record, currentRevision) == "possibly_stale"
		nonAllow := record.PolicyAction != "" && record.PolicyAction != ToolPolicyAllow
		patchRisk := record.PatchRiskSummary != nil && record.PatchRiskSummary.RiskLevel != "low"
		if record.Success && !record.ResultBudgeted && record.ResultRef == "" && !stale && !nonAllow && !patchRisk {
			continue
		}
		fmt.Fprintf(&b, "- tool=%s", compactContextLine(redactToolOutput(strings.TrimSpace(record.Name))))
		if !record.Success {
			b.WriteString(" status=error")
			if record.ErrorKind != "" {
				fmt.Fprintf(&b, " error_kind=%s", compactContextLine(redactToolOutput(record.ErrorKind)))
			}
			if strings.TrimSpace(record.Error) != "" {
				fmt.Fprintf(&b, " error=%s", compactContextLine(redactToolOutput(record.Error)))
			}
		}
		if nonAllow {
			fmt.Fprintf(&b, " policy=%s", record.PolicyAction)
		}
		if stale {
			b.WriteString(" evidence=stale")
		}
		if record.ResultBudgeted {
			b.WriteString(" result=projected")
		}
		if record.ResultRef != "" {
			fmt.Fprintf(&b, " ref=%s", compactContextLine(redactToolOutput(contextArtifactRef(t.env, record.ResultRef))))
		} else if record.ResultBudgeted && len(record.ArtifactRefs) > 0 {
			fmt.Fprintf(&b, " ref=%s", redactedContextArtifactRefs(t.env, record.ArtifactRefs, 1)[0])
		}
		if patchRisk {
			fmt.Fprintf(&b, " patch_risk=%s", compactToolPatchRisk(*record.PatchRiskSummary))
		}
		b.WriteString("\n")
	}

	loopStart := 0
	if len(records) > 8 {
		loopStart = len(records) - 8
	}
	for _, item := range repeatedToolArguments(records[loopStart:]) {
		fmt.Fprintf(&b, "loop_warning: tool=%s repeated=%d; inspect prior evidence before retrying.\n", item.ToolName, item.Count)
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolEvidenceStatus(record ToolExecutionRecord, currentRevision string) string {
	currentRevision = strings.TrimSpace(currentRevision)
	revisionAfter := strings.TrimSpace(record.RevisionAfter)
	if currentRevision == "" || revisionAfter == "" {
		return ""
	}
	if revisionAfter == currentRevision {
		return "current"
	}
	return "possibly_stale"
}

type repeatedToolArgument struct {
	ToolName        string
	ArgumentsSHA256 string
	Count           int
}

func repeatedToolArguments(records []ToolExecutionRecord) []repeatedToolArgument {
	counts := map[string]repeatedToolArgument{}
	for _, record := range records {
		toolName := strings.TrimSpace(record.Name)
		argumentsSHA256 := strings.TrimSpace(record.ArgumentsSHA256)
		if toolName == "" || argumentsSHA256 == "" {
			continue
		}
		key := toolName + "\x00" + argumentsSHA256
		item := counts[key]
		if item.Count == 0 {
			item.ToolName = toolName
			item.ArgumentsSHA256 = argumentsSHA256
		}
		item.Count++
		counts[key] = item
	}
	repeated := make([]repeatedToolArgument, 0, len(counts))
	for _, item := range counts {
		if item.Count > 1 {
			repeated = append(repeated, item)
		}
	}
	sort.Slice(repeated, func(i, j int) bool {
		if repeated[i].ToolName != repeated[j].ToolName {
			return repeated[i].ToolName < repeated[j].ToolName
		}
		return repeated[i].ArgumentsSHA256 < repeated[j].ArgumentsSHA256
	})
	return repeated
}

func compactToolPatchRisk(risk ToolPatchRisk) string {
	parts := []string(nil)
	if level := strings.TrimSpace(risk.RiskLevel); level != "" {
		parts = append(parts, "level="+level)
	}
	if risk.FileCount > 0 {
		parts = append(parts, fmt.Sprintf("files=%d", risk.FileCount))
	}
	if risk.HunkCount > 0 {
		parts = append(parts, fmt.Sprintf("hunks=%d", risk.HunkCount))
	}
	if risk.AddedLines > 0 || risk.DeletedLines > 0 {
		parts = append(parts, fmt.Sprintf("+%d/-%d", risk.AddedLines, risk.DeletedLines))
	}
	if risk.MultiFile {
		parts = append(parts, "multi_file=true")
	}
	if risk.ContainsDelete {
		parts = append(parts, "contains_delete=true")
	}
	if risk.ContainsMove {
		parts = append(parts, "contains_move=true")
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ",")
}

func writeTestFailureSummaryContext(b *strings.Builder, summary testFailureSummary) {
	if len(summary.FailingTests) > 0 {
		b.WriteString("failing_tests:\n")
		for _, test := range limitStrings(summary.FailingTests, 8) {
			fmt.Fprintf(b, "- %s\n", test)
		}
	}
	if len(summary.Locations) > 0 {
		b.WriteString("locations:\n")
		for i, loc := range summary.Locations {
			if i >= 8 {
				break
			}
			fmt.Fprintf(b, "- %s", loc.Path)
			if loc.Line > 0 {
				fmt.Fprintf(b, ":%d", loc.Line)
				if loc.Column > 0 {
					fmt.Fprintf(b, ":%d", loc.Column)
				}
			}
			if strings.TrimSpace(loc.Text) != "" {
				fmt.Fprintf(b, " %s", strings.TrimSpace(loc.Text))
			}
			b.WriteString("\n")
		}
	}
	if len(summary.Indicators) > 0 {
		b.WriteString("indicators:\n")
		for _, indicator := range limitStrings(summary.Indicators, 8) {
			fmt.Fprintf(b, "- %s\n", indicator)
		}
	}
	if len(summary.Snippets) > 0 {
		b.WriteString("snippets:\n")
		for _, snippet := range limitStrings(summary.Snippets, 3) {
			fmt.Fprintf(b, "- %s\n", compactContextLine(snippet))
		}
	}
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func compactContextLine(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > 240 {
		return value[:240] + "...[truncated]"
	}
	return value
}

func redactedContextArtifactRefs(env *Env, values []string, limit int) []string {
	values = limitStrings(values, limit)
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, compactContextLine(redactToolOutput(contextArtifactRef(env, value))))
	}
	return out
}

func contextArtifactRef(env *Env, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || env == nil {
		return value
	}
	if rel, ok := relativeContextRef("$SESSION_DIR", env.SessionDir, value); ok {
		return rel
	}
	if rel, ok := relativeContextRef("$STATE_DIR", env.StateDir, value); ok {
		return rel
	}
	if rel, ok := relativeContextRef("$WORKSPACE", env.RootDir, value); ok {
		return rel
	}
	return value
}

func relativeContextRef(prefix, base, value string) (string, bool) {
	base = strings.TrimSpace(base)
	if prefix == "" || base == "" || value == "" {
		return "", false
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	absValue, err := filepath.Abs(value)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absValue)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return prefix + "/" + filepath.ToSlash(rel), true
}
