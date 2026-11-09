package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/compact"
)

const threadAnchorBudgetDivisor = 3

type projectedThreadTurn struct {
	StartSeq  int              `json:"start_seq"`
	EndSeq    int              `json:"end_seq"`
	AnchorSeq int              `json:"anchor_seq,omitempty"`
	Records   []map[string]any `json:"records"`
}

func projectThreadGetResult(rawText string, pc projectorContext) (string, projectionOmission, bool) {
	raw, ok := parseToolEnvelope(rawText)
	if !ok {
		return "", projectionOmission{}, false
	}
	history, ok := raw["history"].([]any)
	if !ok {
		return "", projectionOmission{}, false
	}
	beforeSeq := intJSONNumber(raw["before_seq"])
	snapshotSeq := intJSONNumber(raw["snapshot_seq"])
	snapshotToken, _ := raw["snapshot_token"].(string)
	if beforeSeq > 0 && (snapshotSeq <= 0 || snapshotToken == "") {
		return "", projectionOmission{}, false
	}

	anchorIndex, anchorKind := findThreadAnchor(history)
	var anchor map[string]any
	var anchorSource string
	anchorOmittedRunes := 0
	if beforeSeq == 0 && anchorIndex >= 0 {
		record, _ := history[anchorIndex].(map[string]any)
		anchor = map[string]any{"kind": anchorKind}
		copyThreadFields(anchor, record, "seq", "role", "content", "at")
		if content, _ := anchor["content"].(string); content != "" {
			anchorSource = content
			// Recent turns are the primary continuation surface. Capping the anchor
			// at one third of the shared result budget prevents a large compact
			// summary from consuming every byte while avoiding a second byte limit.
			clipped, omitted := clipThreadText(content, pc.BudgetTokens/threadAnchorBudgetDivisor)
			anchor["content"] = clipped
			anchorOmittedRunes = omitted
			if omitted > 0 {
				anchor["content_omitted_runes"] = omitted
			}
		}
	}

	turns, semanticRecords, filteredRecords := buildProjectedThreadTurns(history, anchorIndex, pc.BudgetTokens)
	eligible := make([]projectedThreadTurn, 0, len(turns))
	for _, turn := range turns {
		if beforeSeq == 0 || turn.EndSeq < beforeSeq {
			eligible = append(eligible, turn)
		}
	}
	recover := fmt.Sprintf("use page.next for the next older non-overlapping turns; the full snapshot is saved at %s", pc.ArtifactRef)

	build := func(selected []projectedThreadTurn) (string, bool) {
		candidate := map[string]any{
			"thread_id":             raw["thread_id"],
			"session":               projectThreadSession(raw["session"]),
			"history_record_count":  len(history),
			"semantic_record_count": semanticRecords,
			"turns":                 selected,
			"snapshot_seq":          snapshotSeq,
			"snapshot_token":        snapshotToken,
		}
		if anchor != nil {
			candidate["context_anchor"] = anchor
		}
		hasMore := len(selected) < len(eligible)
		page := map[string]any{
			"direction":      "older",
			"before_seq":     beforeSeq,
			"returned_turns": len(selected),
			"has_more":       hasMore,
		}
		if hasMore && len(selected) > 0 {
			page["next"] = map[string]any{
				"thread_id":         raw["thread_id"],
				"before_seq":        selected[0].StartSeq,
				"snapshot_seq":      snapshotSeq,
				"expected_snapshot": snapshotToken,
			}
		}
		candidate["page"] = page
		setProjectionMeta(candidate, pc.BudgetTokens, pc.ArtifactRef, recover, map[string]any{
			"anchor_content_omitted_runes": anchorOmittedRunes,
			"filtered_record_count":        filteredRecords,
			"omitted_history_records":      len(history) - projectedThreadRecordCount(anchor, selected),
			"omitted_turns":                len(eligible) - len(selected),
		})
		return marshalEnvelope(candidate)
	}
	if anchor != nil {
		anchorBaseline := []projectedThreadTurn(nil)
		if len(eligible) > 0 {
			anchorBaseline = []projectedThreadTurn{clipProjectedThreadTurn(eligible[len(eligible)-1], 0)}
		}
		if baseline, ok := build(anchorBaseline); !ok || estimateResultTokens(baseline) > pc.BudgetTokens {
			maxAnchorTokens := pc.BudgetTokens / threadAnchorBudgetDivisor
			fit := largestFitting(maxAnchorTokens, pc.BudgetTokens, func(anchorTokens int) int {
				clipped, omitted := clipThreadText(anchorSource, anchorTokens)
				anchor["content"] = clipped
				anchorOmittedRunes = omitted
				if omitted > 0 {
					anchor["content_omitted_runes"] = omitted
				} else {
					delete(anchor, "content_omitted_runes")
				}
				out, ok := build(anchorBaseline)
				if !ok {
					return pc.BudgetTokens + 1
				}
				return estimateResultTokens(out)
			})
			clipped, omitted := clipThreadText(anchorSource, fit)
			anchor["content"] = clipped
			anchorOmittedRunes = omitted
			if omitted > 0 {
				anchor["content_omitted_runes"] = omitted
			} else {
				delete(anchor, "content_omitted_runes")
			}
		}
	}

	keep := largestFitting(len(eligible), pc.BudgetTokens, func(keep int) int {
		out, ok := build(threadTurnSuffix(eligible, keep))
		if !ok {
			return pc.BudgetTokens + 1
		}
		return estimateResultTokens(out)
	})
	selected := threadTurnSuffix(eligible, keep)
	if keep == 0 && len(eligible) > 0 {
		latest := eligible[len(eligible)-1]
		textBudget := largestFitting(pc.BudgetTokens, pc.BudgetTokens, func(textBudget int) int {
			out, ok := build([]projectedThreadTurn{clipProjectedThreadTurn(latest, textBudget)})
			if !ok {
				return pc.BudgetTokens + 1
			}
			return estimateResultTokens(out)
		})
		selected = []projectedThreadTurn{clipProjectedThreadTurn(latest, textBudget)}
	}

	out, ok := build(selected)
	if !ok || estimateResultTokens(out) > pc.BudgetTokens {
		return "", projectionOmission{}, false
	}
	omittedRecords := len(history) - projectedThreadRecordCount(anchor, selected)
	return out, projectionOmission{Records: omittedRecords}, true
}

func findThreadAnchor(history []any) (int, string) {
	firstUser := -1
	latestSummary := -1
	for i, value := range history {
		record, ok := value.(map[string]any)
		if !ok || boolField(record, "hidden") {
			continue
		}
		role, _ := record["role"].(string)
		content, _ := record["content"].(string)
		if role == "system" && strings.HasPrefix(strings.TrimSpace(content), compact.ConversationSummaryPrefix) {
			latestSummary = i
		}
		if firstUser < 0 && role == "user" && strings.TrimSpace(content) != "" {
			firstUser = i
		}
	}
	if latestSummary >= 0 {
		return latestSummary, "compact_summary"
	}
	if firstUser >= 0 {
		return firstUser, "first_user_message"
	}
	return -1, ""
}

func buildProjectedThreadTurns(history []any, anchorIndex, budgetTokens int) ([]projectedThreadTurn, int, int) {
	start := anchorIndex + 1
	if start < 0 {
		start = 0
	}
	turns := make([]projectedThreadTurn, 0)
	anchorSeq := 0
	anchorRole := ""
	if anchorIndex >= 0 {
		if anchor, ok := history[anchorIndex].(map[string]any); ok {
			anchorSeq = intJSONNumber(anchor["seq"])
			anchorRole, _ = anchor["role"].(string)
		}
	}
	semanticRecords := 0
	filteredRecords := start
	for _, value := range history[start:] {
		raw, ok := value.(map[string]any)
		if !ok {
			filteredRecords++
			continue
		}
		record, keep := semanticThreadRecord(raw, budgetTokens)
		if !keep {
			filteredRecords++
			continue
		}
		semanticRecords++
		seq := intJSONNumber(record["seq"])
		role, _ := record["role"].(string)
		if len(turns) == 0 || role == "user" {
			turn := projectedThreadTurn{StartSeq: seq}
			if len(turns) == 0 && role != "user" && anchorRole == "user" {
				turn.StartSeq = anchorSeq
				turn.AnchorSeq = anchorSeq
			}
			turns = append(turns, turn)
		}
		turn := &turns[len(turns)-1]
		turn.Records = append(turn.Records, record)
		turn.EndSeq = seq
	}
	return turns, semanticRecords, filteredRecords
}

func semanticThreadRecord(raw map[string]any, budgetTokens int) (map[string]any, bool) {
	if boolField(raw, "hidden") {
		return nil, false
	}
	role, _ := raw["role"].(string)
	if role != "user" && role != "assistant" && role != "tool" {
		return nil, false
	}
	record := map[string]any{}
	copyThreadFields(record, raw,
		"seq", "role", "content", "display_content", "phase", "steered", "name",
		"tool_call_id", "tool_result_kind", "finish_reason", "stop_reason", "truncated", "at")

	if role == "assistant" {
		if calls, ok := raw["tool_calls"].([]any); ok {
			projected := make([]any, 0, len(calls))
			for _, value := range calls {
				call, ok := value.(map[string]any)
				if !ok {
					continue
				}
				item := map[string]any{}
				copyThreadFields(item, call, "name", "arguments", "kind", "display")
				if len(item) > 0 {
					projected = append(projected, item)
				}
			}
			if len(projected) > 0 {
				record["tool_calls"] = projected
			}
		}
	}
	for _, key := range []string{"images", "files"} {
		if values, ok := raw[key].([]any); ok && len(values) > 0 {
			record[key+"_count"] = len(values)
		}
	}
	if role == "tool" {
		content, _ := record["content"].(string)
		if content != "" && estimateResultTokens(content) > budgetTokens && !threadToolResultFailed(raw, content) {
			delete(record, "content")
			record["content_omitted_runes"] = utf8.RuneCountInString(content)
			if summary := summarizeThreadToolResult(content); len(summary) > 0 {
				record["result_summary"] = summary
			}
		}
	}
	return record, true
}

func threadToolResultFailed(record map[string]any, content string) bool {
	if kind, _ := record["tool_result_kind"].(string); strings.Contains(strings.ToLower(kind), "error") {
		return true
	}
	envelope, ok := parseToolEnvelope(content)
	if !ok {
		return false
	}
	if boolField(envelope, "is_error") || boolField(envelope, "timed_out") {
		return true
	}
	if code := intJSONNumber(envelope["exit_code"]); code != 0 {
		return true
	}
	if verification, ok := envelope["verification"].(map[string]any); ok {
		if passed, exists := verification["passed"].(bool); exists && !passed {
			return true
		}
	}
	return false
}

func summarizeThreadToolResult(content string) map[string]any {
	envelope, ok := parseToolEnvelope(content)
	if !ok {
		return nil
	}
	summary := map[string]any{}
	copyThreadFields(summary, envelope,
		"action", "path", "pattern", "exit_code", "timed_out", "duration_ms",
		"workspace_revision", "full_log_ref", "total", "truncated")
	return summary
}

func clipProjectedThreadTurn(turn projectedThreadTurn, totalTextBudget int) projectedThreadTurn {
	clipped := projectedThreadTurn{StartSeq: turn.StartSeq, EndSeq: turn.EndSeq, AnchorSeq: turn.AnchorSeq, Records: make([]map[string]any, len(turn.Records))}
	textFields := 0
	for _, record := range turn.Records {
		for _, key := range []string{"content", "display_content"} {
			if text, _ := record[key].(string); text != "" {
				textFields++
			}
		}
		if calls, ok := record["tool_calls"].([]any); ok {
			for _, value := range calls {
				if call, ok := value.(map[string]any); ok {
					if args, _ := call["arguments"].(string); args != "" {
						textFields++
					}
				}
			}
		}
	}
	perFieldBudget := 0
	if textFields > 0 {
		perFieldBudget = totalTextBudget / textFields
	}
	for i, record := range turn.Records {
		copy := cloneShallow(record)
		clipThreadMapText(copy, "content", perFieldBudget)
		clipThreadMapText(copy, "display_content", perFieldBudget)
		if calls, ok := record["tool_calls"].([]any); ok {
			projected := make([]any, 0, len(calls))
			for _, value := range calls {
				call, ok := value.(map[string]any)
				if !ok {
					continue
				}
				item := cloneShallow(call)
				clipThreadMapText(item, "arguments", perFieldBudget)
				projected = append(projected, item)
			}
			copy["tool_calls"] = projected
		}
		clipped.Records[i] = copy
	}
	return clipped
}

func clipThreadMapText(m map[string]any, key string, budgetTokens int) {
	text, _ := m[key].(string)
	if text == "" {
		return
	}
	clipped, omitted := clipThreadText(text, budgetTokens)
	m[key] = clipped
	if omitted > 0 {
		m[key+"_omitted_runes"] = omitted
	}
}

func clipThreadText(text string, budgetTokens int) (string, int) {
	runes := []rune(text)
	if estimateResultTokens(text) <= budgetTokens {
		return text, 0
	}
	if budgetTokens <= 0 {
		return "", len(runes)
	}
	build := func(keep int) string {
		if keep >= len(runes) {
			return text
		}
		head := (keep + 1) / 2
		tail := keep - head
		return fmt.Sprintf("%s\n… %d runes omitted …\n%s", string(runes[:head]), len(runes)-keep, string(runes[len(runes)-tail:]))
	}
	keep := largestFitting(len(runes), budgetTokens, func(keep int) int {
		return estimateResultTokens(build(keep))
	})
	return build(keep), len(runes) - keep
}

func projectThreadSession(value any) map[string]any {
	raw, _ := value.(map[string]any)
	session := map[string]any{}
	copyThreadFields(session, raw,
		"id", "created_at", "updated_at", "title", "entries", "cwd", "source", "provider", "model",
		"variant", "effort", "permission_mode", "workspace_id", "forked_from_id", "pinned_at", "archived_at")
	return session
}

func threadTurnSuffix(turns []projectedThreadTurn, keep int) []projectedThreadTurn {
	if keep <= 0 {
		return []projectedThreadTurn{}
	}
	return turns[len(turns)-keep:]
}

func projectedThreadRecordCount(anchor map[string]any, turns []projectedThreadTurn) int {
	count := 0
	if anchor != nil {
		count++
	}
	for _, turn := range turns {
		count += len(turn.Records)
	}
	return count
}

func copyThreadFields(dst, src map[string]any, keys ...string) {
	for _, key := range keys {
		if value, ok := src[key]; ok && value != nil {
			dst[key] = value
		}
	}
}

func boolField(m map[string]any, key string) bool {
	value, _ := m[key].(bool)
	return value
}
