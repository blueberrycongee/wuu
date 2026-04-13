package insight

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/jsonl"
)

// memoryRecord mirrors the JSONL schema used by tui/memory.go.
type memoryRecord struct {
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	At           time.Time     `json:"at"`
	ToolCalls    []toolCallRec `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	Name         string        `json:"name,omitempty"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

type toolCallRec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ScanSessions reads all .jsonl session files in sessDir and returns metadata
// sorted by creation time descending (most recent first).
// maxSessions limits how many sessions to return (0 = all).
func ScanSessions(sessDir string, maxSessions int) ([]SessionMeta, error) {
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var metas []SessionMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if entry.Name() == "index.jsonl" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		path := filepath.Join(sessDir, entry.Name())
		meta, err := scanOneSession(path, id)
		if err != nil {
			continue // skip corrupt files
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

// scanOneSession reads a single .jsonl session file and extracts metadata.
func scanOneSession(path string, id string) (SessionMeta, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer file.Close()

	meta := SessionMeta{
		ID:         id,
		ToolCounts: make(map[string]int),
		Languages:  make(map[string]int),
	}

	filesModified := make(map[string]struct{})
	var firstTime, lastTime time.Time
	var firstUserMsg string

	err = jsonl.ForEachLine(file, func(raw []byte) error {
		payload := bytes.TrimSpace(raw)
		if len(payload) == 0 {
			return nil
		}
		var rec memoryRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil
		}

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
			// Token usage records.
			meta.InputTokens += rec.InputTokens
			meta.OutputTokens += rec.OutputTokens
		}
		return nil
	})
	if err != nil {
		return meta, err
	}

	meta.CreatedAt = firstTime
	if !firstTime.IsZero() && !lastTime.IsZero() {
		meta.Duration = lastTime.Sub(firstTime)
	}
	meta.FirstUserMsg = firstUserMsg
	meta.FilesModified = len(filesModified)

	return meta, nil
}

// FormatTranscript builds a condensed text transcript of a session for LLM analysis.
func FormatTranscript(sessDir, sessionID string) (string, error) {
	path := filepath.Join(sessDir, sessionID+".jsonl")
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var b strings.Builder
	b.WriteString("Session: " + sessionID + "\n\n")

	err = jsonl.ForEachLine(file, func(raw []byte) error {
		payload := bytes.TrimSpace(raw)
		if len(payload) == 0 {
			return nil
		}
		var rec memoryRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return nil
		}

		role := strings.ToLower(strings.TrimSpace(rec.Role))
		content := strings.TrimSpace(rec.Content)

		switch role {
		case "user":
			b.WriteString("[User]: " + truncateStr(content, 500) + "\n")
		case "assistant":
			b.WriteString("[Assistant]: " + truncateStr(content, 300) + "\n")
			for _, tc := range rec.ToolCalls {
				b.WriteString("[Tool: " + tc.Name + "]\n")
			}
		case "tool":
			// Skip tool results to keep transcript compact.
		}

		// Cap total transcript size.
		if b.Len() > 30000 {
			b.WriteString("\n... (truncated)\n")
			return jsonl.ErrStop
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	return b.String(), nil
}

// Aggregate combines multiple SessionMeta and Facets into AggregatedData.
func Aggregate(metas []SessionMeta, facets map[string]Facet) AggregatedData {
	agg := AggregatedData{
		TotalSessions:      len(metas),
		SessionsWithFacets: len(facets),
		ToolCounts:         make(map[string]int),
		Languages:          make(map[string]int),
		GoalCategories:     make(map[string]int),
		Outcomes:           make(map[string]int),
		Satisfaction:       make(map[string]int),
		SessionTypes:       make(map[string]int),
		Friction:           make(map[string]int),
		Success:            make(map[string]int),
	}

	daysSet := make(map[string]struct{})

	for _, m := range metas {
		agg.TotalMessages += m.UserMessages + m.AssistantMsgs
		agg.TotalDurationH += m.Duration.Hours()
		agg.TotalEstTokens += m.EstTokens
		agg.TotalInputTokens += m.InputTokens
		agg.TotalOutputTokens += m.OutputTokens
		agg.TotalLinesAdded += m.LinesAdded
		agg.TotalLinesRemoved += m.LinesRemoved
		agg.TotalFilesModified += m.FilesModified
		agg.MessageHours = append(agg.MessageHours, m.MessageHours...)

		for name, cnt := range m.ToolCounts {
			agg.ToolCounts[name] += cnt
		}
		for lang, cnt := range m.Languages {
			agg.Languages[lang] += cnt
		}

		if !m.CreatedAt.IsZero() {
			day := m.CreatedAt.Format("2006-01-02")
			daysSet[day] = struct{}{}
		}
	}

	// Aggregate facets.
	for _, f := range facets {
		for cat, cnt := range f.GoalCategories {
			agg.GoalCategories[cat] += cnt
		}
		if f.Outcome != "" {
			agg.Outcomes[f.Outcome]++
		}
		if f.Satisfaction != "" {
			agg.Satisfaction[f.Satisfaction]++
		}
		if f.SessionType != "" {
			agg.SessionTypes[f.SessionType]++
		}
		for fric, cnt := range f.Friction {
			agg.Friction[fric] += cnt
		}
		if f.PrimarySuccess != "" {
			agg.Success[f.PrimarySuccess]++
		}
	}

	// Summaries from first 50 sessions.
	limit := 50
	if len(metas) < limit {
		limit = len(metas)
	}
	for _, m := range metas[:limit] {
		s := SessionSummary{
			ID:      m.ID,
			Date:    m.CreatedAt.Format("2006-01-02"),
			Summary: m.FirstUserMsg,
		}
		if f, ok := facets[m.ID]; ok {
			s.Goal = f.Goal
		}
		agg.Summaries = append(agg.Summaries, s)
	}

	// Date range.
	if len(metas) > 0 {
		agg.DateRange = [2]string{
			metas[len(metas)-1].CreatedAt.Format("2006-01-02"),
			metas[0].CreatedAt.Format("2006-01-02"),
		}
	}

	agg.DaysActive = len(daysSet)
	if agg.DaysActive > 0 {
		agg.MessagesPerDay = float64(agg.TotalMessages) / float64(agg.DaysActive)
	}

	return agg
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
	if strings.HasPrefix(first, "{") || strings.HasPrefix(first, "RESPOND WITH ONLY") {
		return false
	}
	return true
}

// --- helpers ---

func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
