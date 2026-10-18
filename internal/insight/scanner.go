package insight

import (
	"encoding/json"
	"sort"
	"strings"

	sessionstore "github.com/blueberrycongee/wuu/internal/session"
)

type toolCallRec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CollectUsageScan reads each session history once and extracts both token
// usage rows and load_skill calls for the settings usage view.
func CollectUsageScan(sessDir string) (UsageScan, error) {
	sessions, err := sessionstore.List(sessDir, 0)
	if err != nil {
		return UsageScan{}, err
	}
	var rows []TokenUsageRow
	counts := make(map[string]int)
	for _, sess := range sessions {
		records, err := sessionstore.LoadHistoryRecords(sessDir, sess.ID, true)
		if err != nil {
			continue
		}
		collectSkillUsage(records, counts)
		for _, rec := range records {
			if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") && strings.TrimSpace(rec.Content) == "token_usage" {
				rows = append(rows, TokenUsageRow{
					SessionID: sess.ID, At: rec.At, Provider: rec.Provider, Model: rec.Model,
					InputTokens: rec.InputTokens, OutputTokens: rec.OutputTokens,
					CacheCreationTokens: rec.CacheCreationTokens, CacheReadTokens: rec.CacheReadTokens,
				})
			}
		}
	}
	skills := make([]SkillUsage, 0, len(counts))
	for name, count := range counts {
		skills = append(skills, SkillUsage{Name: name, Count: count})
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Count != skills[j].Count {
			return skills[i].Count > skills[j].Count
		}
		return skills[i].Name < skills[j].Name
	})
	return UsageScan{TokenRows: rows, Skills: skills}, nil
}

// collectSkillUsage prefers assistant tool-call arguments, which preserve the
// requested skill name even when execution fails. Provider checkpoints and
// history compaction can retain a tool result after dropping its assistant
// call, so unmatched load_skill results are also decoded as a fallback.
func collectSkillUsage(records []sessionstore.HistoryRecord, counts map[string]int) {
	seenCallIDs := make(map[string]struct{})
	for _, rec := range records {
		switch strings.ToLower(strings.TrimSpace(rec.Role)) {
		case "assistant":
			var calls []toolCallRec
			if err := json.Unmarshal(rec.ToolCalls, &calls); err != nil {
				continue
			}
			for _, call := range calls {
				if !isLoadSkillTool(call.Name) {
					continue
				}
				name := skillNameFromArguments(call.Arguments)
				if name == "" {
					continue
				}
				callID := strings.TrimSpace(call.ID)
				if callID != "" {
					if _, seen := seenCallIDs[callID]; seen {
						continue
					}
					seenCallIDs[callID] = struct{}{}
				}
				counts[name]++
			}
		case "tool":
			if !isLoadSkillTool(rec.Name) {
				continue
			}
			callID := strings.TrimSpace(rec.ToolCallID)
			if callID != "" {
				if _, seen := seenCallIDs[callID]; seen {
					continue
				}
				seenCallIDs[callID] = struct{}{}
			}
			if name := skillNameFromResult(rec.Content); name != "" {
				counts[name]++
			}
		}
	}
}

func isLoadSkillTool(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, separator := range []string{".", "/", ":"} {
		if index := strings.LastIndex(normalized, separator); index >= 0 {
			normalized = normalized[index+len(separator):]
		}
	}
	return normalized == "load_skill"
}

func skillNameFromArguments(arguments string) string {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Name)
}

func skillNameFromResult(content string) string {
	var result struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.Name)
}
