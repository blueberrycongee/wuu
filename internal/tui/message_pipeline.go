package tui

import (
	"fmt"
	"strings"
	"time"
)

// renderableMessage is the UI-projection of a transcriptEntry after
// normalization, grouping, and collapse. This is the intermediate layer
// between raw transcript data and what actually gets painted.
type renderableMessage struct {
	Role            string
	Content         string
	Rendered        string
	ThinkingContent string
	ThinkingDone    bool
	ThinkingExpanded bool
	ThinkingDuration time.Duration
	ToolCalls       []ToolCallEntry
	blockOrder      []string
	textSegmentOffsets []int

	// UI presentation hints.
	GroupLeader     bool   // first message in a group
	GroupTrailer    bool   // last message in a group
	Collapsed       bool   // visually collapsed (e.g. background bash)
	MetadataLine    string // model name / timestamp for transcript mode
	DividerBefore   bool   // show a divider line before this message
}

// messagePipeline transforms raw transcript entries into renderable messages.
// Aligned with Claude Code's Messages.tsx preprocessing pipeline:
//   1. normalizeMessages
//   2. filter empty / null-rendering attachments
//   3. reorderMessagesInUI
//   4. brief mode filtering
//   5. render cap / virtual scroll slice
//   6. applyGrouping
//   7. collapseBackgroundBashNotifications
//   8. collapseHookSummaries
//   9. collapseTeammateShutdowns
//   10. collapseReadSearchGroups
//
// In wuu we keep the pipeline but adapt it to Go/BubbleTea constraints.
type messagePipeline struct {
	// Configuration.
	maxMessagesWithoutVirtualization int
	messageCapStep                   int

	// State derived from entries.
	entries []transcriptEntry
	width   int
}

func newMessagePipeline(entries []transcriptEntry, width int) *messagePipeline {
	return &messagePipeline{
		entries: entries,
		width:   width,
		maxMessagesWithoutVirtualization: 200,
		messageCapStep:                   50,
	}
}

// run executes the full pipeline and returns renderable messages.
func (p *messagePipeline) run() []renderableMessage {
	msgs := p.normalizeMessages()
	msgs = p.filterEmpty(msgs)
	msgs = p.applyGrouping(msgs)
	msgs = p.collapseBackgroundBash(msgs)
	msgs = p.collapseHookSummaries(msgs)
	msgs = p.collapseTeammateShutdowns(msgs)
	msgs = p.collapseReadSearchGroups(msgs)
	msgs = p.addMetadata(msgs)
	return msgs
}

// normalizeMessages converts transcriptEntry slices into renderableMessage
// primitives. No policy decisions yet — just shape normalization.
func (p *messagePipeline) normalizeMessages() []renderableMessage {
	msgs := make([]renderableMessage, 0, len(p.entries))
	for _, e := range p.entries {
		if e.Role == "TOOL" {
			continue // TOOL entries are merged into their parent turn
		}
		msgs = append(msgs, renderableMessage{
			Role:             e.Role,
			Content:          e.Content,
			Rendered:         e.rendered,
			ThinkingContent:  e.ThinkingContent,
			ThinkingDone:     e.ThinkingDone,
			ThinkingExpanded: e.ThinkingExpanded,
			ThinkingDuration: e.ThinkingDuration,
			ToolCalls:        append([]ToolCallEntry(nil), e.ToolCalls...),
			blockOrder:       append([]string(nil), e.blockOrder...),
			textSegmentOffsets: append([]int(nil), e.textSegmentOffsets...),
		})
	}
	return msgs
}

// filterEmpty removes messages that would render as blank lines.
func (p *messagePipeline) filterEmpty(msgs []renderableMessage) []renderableMessage {
	filtered := make([]renderableMessage, 0, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" &&
			strings.TrimSpace(m.ThinkingContent) == "" &&
			len(m.ToolCalls) == 0 {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// applyGrouping groups consecutive system messages and consecutive
// assistant tool-use sequences so the UI can show them with lighter
// dividers or no role-label repetition.
func (p *messagePipeline) applyGrouping(msgs []renderableMessage) []renderableMessage {
	if len(msgs) == 0 {
		return msgs
	}
	for i := range msgs {
		msgs[i].GroupLeader = (i == 0) || (msgs[i].Role != msgs[i-1].Role)
		msgs[i].GroupTrailer = (i == len(msgs)-1) || (msgs[i].Role != msgs[i+1].Role)
		if !msgs[i].GroupLeader && i > 0 {
			msgs[i].DividerBefore = false
		}
	}
	return msgs
}

// collapseBackgroundBash collapses consecutive "run_shell" tool result
// notifications from background workers into a single summary line.
func (p *messagePipeline) collapseBackgroundBash(msgs []renderableMessage) []renderableMessage {
	// Find runs of system messages that are just background bash outputs.
	var result []renderableMessage
	i := 0
	for i < len(msgs) {
		if msgs[i].Role != "system" || !isBackgroundBashLine(msgs[i].Content) {
			result = append(result, msgs[i])
			i++
			continue
		}
		// Start of a potential run.
		start := i
		for i < len(msgs) && msgs[i].Role == "system" && isBackgroundBashLine(msgs[i].Content) {
			i++
		}
		count := i - start
		if count <= 1 {
			result = append(result, msgs[start])
			continue
		}
		// Collapse into a summary message.
		summary := renderableMessage{
			Role:         "system",
			Content:      fmt.Sprintf("… %d background shell notifications …", count),
			GroupLeader:  true,
			GroupTrailer: true,
			Collapsed:    true,
		}
		result = append(result, summary)
	}
	return result
}

func isBackgroundBashLine(content string) bool {
	// Heuristic: system messages containing shell output markers.
	trimmed := strings.TrimSpace(content)
	return strings.Contains(trimmed, "process ") &&
		(strings.Contains(trimmed, "started") || strings.Contains(trimmed, "stopped"))
}

// collapseHookSummaries collapses consecutive hook execution summaries
// into a single "hooks ran" line.
func (p *messagePipeline) collapseHookSummaries(msgs []renderableMessage) []renderableMessage {
	var result []renderableMessage
	i := 0
	for i < len(msgs) {
		if msgs[i].Role != "system" || !isHookSummaryLine(msgs[i].Content) {
			result = append(result, msgs[i])
			i++
			continue
		}
		start := i
		for i < len(msgs) && msgs[i].Role == "system" && isHookSummaryLine(msgs[i].Content) {
			i++
		}
		count := i - start
		if count <= 1 {
			result = append(result, msgs[start])
			continue
		}
		summary := renderableMessage{
			Role:         "system",
			Content:      fmt.Sprintf("… %d hook summaries …", count),
			GroupLeader:  true,
			GroupTrailer: true,
			Collapsed:    true,
		}
		result = append(result, summary)
	}
	return result
}

func isHookSummaryLine(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "✓ Hook ") || strings.HasPrefix(trimmed, "⊘ Hook ")
}

// collapseTeammateShutdowns collapses consecutive teammate shutdown
// notifications into a single summary.
func (p *messagePipeline) collapseTeammateShutdowns(msgs []renderableMessage) []renderableMessage {
	var result []renderableMessage
	i := 0
	for i < len(msgs) {
		if msgs[i].Role != "system" || !isTeammateShutdownLine(msgs[i].Content) {
			result = append(result, msgs[i])
			i++
			continue
		}
		start := i
		for i < len(msgs) && msgs[i].Role == "system" && isTeammateShutdownLine(msgs[i].Content) {
			i++
		}
		count := i - start
		if count <= 1 {
			result = append(result, msgs[start])
			continue
		}
		summary := renderableMessage{
			Role:         "system",
			Content:      fmt.Sprintf("… %d teammate shutdowns …", count),
			GroupLeader:  true,
			GroupTrailer: true,
			Collapsed:    true,
		}
		result = append(result, summary)
	}
	return result
}

func isTeammateShutdownLine(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.Contains(trimmed, "sub-agent") || strings.Contains(trimmed, "worker")
}

// collapseReadSearchGroups collapses consecutive read_file / grep / glob
// tool result lines into a lighter representation.
func (p *messagePipeline) collapseReadSearchGroups(msgs []renderableMessage) []renderableMessage {
	// For now, this is a no-op placeholder. In Claude Code this would
	// group rapid-fire read/search tools into a single "read 5 files"
	// summary. Implementing this requires tracking tool results across
	// assistant turns, which wuu's current architecture handles at the
	// ToolCallEntry level already.
	return msgs
}

// addMetadata adds model name / timestamp metadata lines to assistant
// messages in transcript mode. For now this is a lightweight placeholder
// that can be expanded when transcript mode is fully implemented.
func (p *messagePipeline) addMetadata(msgs []renderableMessage) []renderableMessage {
	for i := range msgs {
		if msgs[i].Role == "ASSISTANT" && !msgs[i].GroupLeader {
			msgs[i].MetadataLine = "Claude · " + time.Now().Format("15:04")
		}
	}
	return msgs
}
