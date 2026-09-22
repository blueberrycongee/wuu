package insight

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sessionstore "github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/stringutil"
)

// memoryRecord mirrors the session history JSONL schema.
type memoryRecord struct {
	Role                string        `json:"role"`
	Content             string        `json:"content"`
	At                  time.Time     `json:"at"`
	ToolCalls           []toolCallRec `json:"tool_calls,omitempty"`
	ToolCallID          string        `json:"tool_call_id,omitempty"`
	Name                string        `json:"name,omitempty"`
	InputTokens         int           `json:"input_tokens,omitempty"`
	OutputTokens        int           `json:"output_tokens,omitempty"`
	CacheCreationTokens int           `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int           `json:"cache_read_tokens,omitempty"`
	// Provider and Model carry which provider/model produced this token_usage
	// meta row. Empty for non-meta records and for legacy rows written before
	// these fields were added.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type toolCallRec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ScanSessions reads all SQLite-backed sessions in sessDir and returns metadata
// sorted by creation time descending (most recent first).
// maxSessions limits how many sessions to return (0 = all).
func ScanSessions(sessDir string, maxSessions int) ([]SessionMeta, error) {
	sessions, err := sessionstore.List(sessDir, 0)
	if err != nil {
		return nil, err
	}
	return scanSessions(sessDir, sessions, maxSessions)
}

// CollectTokenUsageRows walks every session in sessDir and returns one
// TokenUsageRow per persisted token_usage meta record, in the order they
// appear in the session history. Corrupt or missing session files are
// skipped silently; an empty slice with a nil error means "no usage
// rows yet". Callers are responsible for time-windowing and per-day
// bucketing — this function never aggregates, so range filters and
// "最近 N 条" lists stay exact instead of inheriting whatever rollup
// the SessionMeta cache happens to hold.
func CollectTokenUsageRows(sessDir string) ([]TokenUsageRow, error) {
	sessions, err := sessionstore.List(sessDir, 0)
	if err != nil {
		return nil, err
	}
	var rows []TokenUsageRow
	for _, sess := range sessions {
		records, err := sessionstore.LoadHistoryRecords(sessDir, sess.ID, true)
		if err != nil {
			continue
		}
		for _, rec := range records {
			if !strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
				continue
			}
			if strings.TrimSpace(rec.Content) != "token_usage" && !strings.HasPrefix(rec.Content, "auto_model:") {
				continue
			}
			rows = append(rows, TokenUsageRow{
				SessionID:           sess.ID,
				At:                  rec.At,
				Provider:            rec.Provider,
				Model:               rec.Model,
				InputTokens:         rec.InputTokens,
				OutputTokens:        rec.OutputTokens,
				CacheCreationTokens: rec.CacheCreationTokens,
				CacheReadTokens:     rec.CacheReadTokens,
			})
		}
	}
	return rows, nil
}

// CollectSkillUsage scans persisted assistant tool calls and counts each
// load_skill invocation. Invalid history records are skipped like token usage.
func CollectSkillUsage(sessDir string) ([]SkillUsage, error) {
	sessions, err := sessionstore.List(sessDir, 0)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, sess := range sessions {
		records, err := sessionstore.LoadHistoryRecords(sessDir, sess.ID, true)
		if err != nil {
			continue
		}
		collectSkillUsage(records, counts)
	}
	out := make([]SkillUsage, 0, len(counts))
	for name, count := range counts {
		out = append(out, SkillUsage{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
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
			if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") && (strings.TrimSpace(rec.Content) == "token_usage" || strings.HasPrefix(rec.Content, "auto_model:")) {
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

func scanSessions(sessDir string, sessions []sessionstore.Session, maxSessions int) ([]SessionMeta, error) {
	metas := make([]SessionMeta, 0, len(sessions))
	for _, sess := range sessions {
		records, err := sessionstore.LoadHistoryRecords(sessDir, sess.ID, true)
		if err != nil {
			continue // skip corrupt or partially migrated sessions
		}
		meta := scanSessionRecords(records, sess.ID)
		if meta.CreatedAt.IsZero() {
			meta.CreatedAt = sess.CreatedAt
		}
		if meta.Duration == 0 && !sess.CreatedAt.IsZero() && !sess.UpdatedAt.IsZero() && sess.UpdatedAt.After(sess.CreatedAt) {
			meta.Duration = sess.UpdatedAt.Sub(sess.CreatedAt)
		}
		if !isSubstantiveSession(meta) {
			continue
		}
		metas = append(metas, meta)
	}

	// Deduplicate fork/branch sessions: group by base ID, keep the one
	// with the most user messages (tie-break by duration).
	metas = deduplicateSessions(metas)

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].CreatedAt.After(metas[j].CreatedAt)
	})

	if maxSessions > 0 && len(metas) > maxSessions {
		metas = metas[:maxSessions]
	}
	return metas, nil
}

// deduplicateSessions groups sessions by their base ID (stripping .fork-*
// suffixes) and keeps only the session with the most user messages per group.
func deduplicateSessions(metas []SessionMeta) []SessionMeta {
	groups := make(map[string]SessionMeta)
	for _, m := range metas {
		baseID := baseSessionID(m.ID)
		existing, ok := groups[baseID]
		if !ok {
			groups[baseID] = m
			continue
		}
		// Keep the one with more user messages; tie-break by duration.
		if m.UserMessages > existing.UserMessages ||
			(m.UserMessages == existing.UserMessages && m.Duration > existing.Duration) {
			groups[baseID] = m
		}
	}
	result := make([]SessionMeta, 0, len(groups))
	for _, m := range groups {
		result = append(result, m)
	}
	return result
}

// baseSessionID strips fork suffixes from session IDs.
// "20260409-084018-b774.fork-20260409-150000" → "20260409-084018-b774"
func baseSessionID(id string) string {
	if idx := strings.Index(id, ".fork-"); idx >= 0 {
		return id[:idx]
	}
	return id
}

func scanSessionRecords(records []sessionstore.HistoryRecord, id string) SessionMeta {
	meta := SessionMeta{
		ID:         id,
		ToolCounts: make(map[string]int),
		Languages:  make(map[string]int),
	}

	filesModified := make(map[string]struct{})
	var firstTime, lastTime time.Time
	var firstUserMsg string

	for _, historyRec := range records {
		rec := memoryRecordFromHistoryRecord(historyRec)

		// Track time range.
		if !rec.At.IsZero() {
			if firstTime.IsZero() || rec.At.Before(firstTime) {
				firstTime = rec.At
			}
			if rec.At.After(lastTime) {
				lastTime = rec.At
			}
		}

		role := strings.ToLower(strings.TrimSpace(rec.Role))
		meta.EstTokens += len(rec.Content) / 4

		switch role {
		case "user":
			meta.UserMessages++
			if firstUserMsg == "" {
				firstUserMsg = truncateStr(rec.Content, 120)
			}
			if !rec.At.IsZero() {
				meta.MessageHours = append(meta.MessageHours, rec.At.Hour())
				meta.UserTimestamps = append(meta.UserTimestamps, rec.At)
			}
		case "assistant":
			meta.AssistantMsgs++
			// Count tool calls.
			for _, tc := range rec.ToolCalls {
				name := strings.TrimSpace(tc.Name)
				if name == "" {
					continue
				}
				meta.ToolCounts[name]++
				// Detect language from file paths in arguments.
				detectLanguageFromArgs(tc.Arguments, meta.Languages)
				// Track file modifications.
				if isFileModifyTool(name) {
					if fp := extractFilePath(tc.Arguments); fp != "" {
						filesModified[fp] = struct{}{}
					}
				}
			}
		case "tool":
			// Parse diff results from edit_file/write_file tool outputs.
			added, removed := parseDiffFromToolResult(rec.Content)
			meta.LinesAdded += added
			meta.LinesRemoved += removed
		case "meta":
			// Token usage records. Accumulate into per-session totals and
			// the per-provider/model breakdown so usage consumers can roll
			// up across sessions without re-reading history.
			meta.InputTokens += rec.InputTokens
			meta.OutputTokens += rec.OutputTokens
			meta.CacheCreationTokens += rec.CacheCreationTokens
			meta.CacheReadTokens += rec.CacheReadTokens
			if meta.ModelBreakdowns == nil {
				meta.ModelBreakdowns = make(map[string]*ModelUsage)
			}
			key := rec.Provider + "|" + rec.Model
			bucket, ok := meta.ModelBreakdowns[key]
			if !ok {
				bucket = &ModelUsage{Provider: rec.Provider, Model: rec.Model}
				meta.ModelBreakdowns[key] = bucket
			}
			bucket.InputTokens += rec.InputTokens
			bucket.OutputTokens += rec.OutputTokens
			bucket.CacheCreationTokens += rec.CacheCreationTokens
			bucket.CacheReadTokens += rec.CacheReadTokens
		}
	}

	meta.CreatedAt = firstTime
	if !firstTime.IsZero() && !lastTime.IsZero() {
		meta.Duration = lastTime.Sub(firstTime)
	}
	meta.FirstUserMsg = firstUserMsg
	meta.FilesModified = len(filesModified)

	return meta
}

func memoryRecordFromHistoryRecord(rec sessionstore.HistoryRecord) memoryRecord {
	out := memoryRecord{
		Role:                rec.Role,
		Content:             rec.Content,
		At:                  rec.At,
		ToolCallID:          rec.ToolCallID,
		Name:                rec.Name,
		InputTokens:         rec.InputTokens,
		OutputTokens:        rec.OutputTokens,
		CacheCreationTokens: rec.CacheCreationTokens,
		CacheReadTokens:     rec.CacheReadTokens,
		Provider:            rec.Provider,
		Model:               rec.Model,
	}
	if len(rec.ToolCalls) > 0 {
		_ = json.Unmarshal(rec.ToolCalls, &out.ToolCalls)
	}
	return out
}

// --- filtering ---

// isSubstantiveSession returns true if the session is worth analyzing.
// Filters out: empty sessions, trivial sessions (<2 user messages or <1 min),
// and meta-sessions (e.g. facet extraction API calls with JSON-only prompts).
func isSubstantiveSession(meta SessionMeta) bool {
	if meta.UserMessages < 2 {
		return false
	}
	if meta.Duration > 0 && meta.Duration < time.Minute {
		return false
	}
	// Meta-session detection: if the first user message looks like a JSON
	// object or contains facet extraction markers, skip it.
	first := strings.TrimSpace(meta.FirstUserMsg)
	if strings.HasPrefix(first, "{") || strings.HasPrefix(strings.ToLower(first), "respond with only") {
		return false
	}
	return true
}

// --- helpers ---

func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	return stringutil.Truncate(s, maxLen, "...")
}

var langExtMap = map[string]string{
	".go":     "Go",
	".ts":     "TypeScript",
	".tsx":    "TypeScript",
	".js":     "JavaScript",
	".jsx":    "JavaScript",
	".py":     "Python",
	".rs":     "Rust",
	".java":   "Java",
	".rb":     "Ruby",
	".c":      "C",
	".cpp":    "C++",
	".h":      "C",
	".cs":     "C#",
	".swift":  "Swift",
	".kt":     "Kotlin",
	".php":    "PHP",
	".sh":     "Shell",
	".bash":   "Shell",
	".zsh":    "Shell",
	".sql":    "SQL",
	".html":   "HTML",
	".css":    "CSS",
	".scss":   "CSS",
	".json":   "JSON",
	".yaml":   "YAML",
	".yml":    "YAML",
	".toml":   "TOML",
	".md":     "Markdown",
	".lua":    "Lua",
	".zig":    "Zig",
	".dart":   "Dart",
	".vue":    "Vue",
	".svelte": "Svelte",
}

func detectLanguageFromArgs(args string, langs map[string]int) {
	// Look for file_path or path in tool arguments JSON.
	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		return
	}
	for _, key := range []string{"file_path", "path", "filename"} {
		if v, ok := parsed[key]; ok {
			if s, ok := v.(string); ok {
				ext := strings.ToLower(filepath.Ext(s))
				if lang, ok := langExtMap[ext]; ok {
					langs[lang]++
				}
			}
		}
	}
}

// parseDiffFromToolResult extracts line add/remove counts from a tool result
// that contains a DiffResult JSON structure (as produced by wuu's edit_file/write_file).
func parseDiffFromToolResult(content string) (added, removed int) {
	// Try parsing as a JSON object containing "hunks" or "diff" fields.
	var wrapper map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &wrapper) != nil {
		return 0, 0
	}

	// Look for a "diff" field containing the DiffResult.
	raw, ok := wrapper["diff"]
	if !ok {
		// Maybe the content IS a DiffResult directly.
		raw = []byte(content)
	}

	var diff struct {
		Hunks []struct {
			Lines []struct {
				Op string `json:"op"`
			} `json:"lines"`
		} `json:"hunks"`
		NewFile bool `json:"new_file"`
		Lines   int  `json:"lines"`
	}
	if json.Unmarshal(raw, &diff) != nil {
		return 0, 0
	}

	// New file: all lines are additions.
	if diff.NewFile && diff.Lines > 0 {
		return diff.Lines, 0
	}

	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			switch line.Op {
			case "insert":
				added++
			case "delete":
				removed++
			}
		}
	}
	return added, removed
}

func isFileModifyTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "Write", "Edit":
		return true
	}
	return false
}

func extractFilePath(args string) string {
	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path"} {
		if v, ok := parsed[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}
