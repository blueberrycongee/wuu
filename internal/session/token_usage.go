package session

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TokenUsageRow is one persisted token_usage meta row. At is the recorded UTC
// time; legacy rows may have a zero At and still count toward totals.
type TokenUsageRow struct {
	SessionID           string
	At                  time.Time
	Provider            string
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

// UsageTotals counts persisted turn-usage records, not provider requests.
// Cache creation, cache reads and uncached input are separate token categories.
type UsageTotals struct {
	Records             int `json:"records"`
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
}

type SessionUsageEntry struct {
	SessionID string `json:"session_id"`
	ParentID  string `json:"parent_id,omitempty"`
	UsageTotals
}

type SessionModelUsage struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Sessions int    `json:"sessions"`
	UsageTotals
}

// SessionUsageCoverage describes gaps without treating missing usage as free.
// Recorded turns cannot establish complete billing coverage: active requests,
// retries, auxiliary calls and providers without usage receipts may be absent.
type SessionUsageCoverage struct {
	Source                 string   `json:"source"`
	BillingComplete        bool     `json:"billing_complete"`
	SessionsWithoutRecords []string `json:"sessions_without_records"`
	UnattributedRecords    int      `json:"unattributed_records"`
}

type SessionUsageReport struct {
	ThreadID        string               `json:"thread_id"`
	Self            UsageTotals          `json:"self"`
	Delegated       UsageTotals          `json:"delegated"`
	Tree            UsageTotals          `json:"tree"`
	Sessions        []SessionUsageEntry  `json:"sessions"`
	ModelBreakdowns []SessionModelUsage  `json:"model_breakdowns"`
	Coverage        SessionUsageCoverage `json:"coverage"`
}

// ReadSessionUsage returns recorded usage for one session and its durable
// ParentID descendants, including hidden and archived children. Fork ancestry
// alone does not establish delegation. No conversation content is read.
func ReadSessionUsage(sessDir, threadID string) (SessionUsageReport, error) {
	threadID = strings.TrimSpace(threadID)
	report := SessionUsageReport{
		ThreadID: threadID,
		Sessions: []SessionUsageEntry{}, ModelBreakdowns: []SessionModelUsage{},
		Coverage: SessionUsageCoverage{Source: "recorded_turn_usage", SessionsWithoutRecords: []string{}},
	}
	db, ok, err := openStoreForScan(sessDir)
	if err != nil {
		return report, err
	}
	if !ok {
		return report, fmt.Errorf("%w: %q", ErrSessionNotFound, threadID)
	}
	defer db.Close()

	// UNION deduplicates identities, so corrupt cycles cannot inflate totals
	// or recurse forever. Traverse parent_id only; a fork is not another bill
	// for its source session's copied conversation history.
	rows, err := db.Query(`WITH RECURSIVE descendants(id) AS (
		SELECT id FROM sessions WHERE id = ?
		UNION
		SELECT s.id FROM sessions s JOIN descendants d ON s.parent_id = d.id
	)
	SELECT s.id, s.parent_id, COALESCE(m.provider, ''), COALESCE(m.model, ''),
		COUNT(m.seq), COALESCE(SUM(m.input_tokens), 0), COALESCE(SUM(m.output_tokens), 0),
		COALESCE(SUM(m.cache_creation_tokens), 0), COALESCE(SUM(m.cache_read_tokens), 0)
	FROM descendants d JOIN sessions s ON s.id = d.id
	LEFT JOIN session_messages m ON m.session_id = s.id AND m.role = 'meta' AND m.content = ?
	GROUP BY s.id, s.parent_id, m.provider, m.model
	ORDER BY s.id, m.provider, m.model`, threadID, tokenUsageContent)
	if err != nil {
		return report, fmt.Errorf("read session usage: %w", err)
	}
	defer rows.Close()

	sessions := map[string]*SessionUsageEntry{}
	models := map[[2]string]*SessionModelUsage{}
	for rows.Next() {
		var id, parentID, provider, model string
		var usage UsageTotals
		if err := rows.Scan(&id, &parentID, &provider, &model, &usage.Records,
			&usage.InputTokens, &usage.OutputTokens, &usage.CacheCreationTokens, &usage.CacheReadTokens); err != nil {
			return report, fmt.Errorf("scan session usage: %w", err)
		}
		entry := sessions[id]
		if entry == nil {
			entry = &SessionUsageEntry{SessionID: id, ParentID: parentID}
			sessions[id] = entry
		}
		entry.UsageTotals.add(usage)
		if id == threadID {
			report.Self.add(usage)
		} else {
			report.Delegated.add(usage)
		}
		report.Tree.add(usage)
		if usage.Records == 0 {
			continue
		}
		key := [2]string{provider, model}
		bucket := models[key]
		if bucket == nil {
			bucket = &SessionModelUsage{Provider: provider, Model: model}
			models[key] = bucket
		}
		bucket.UsageTotals.add(usage)
		bucket.Sessions++
		if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
			report.Coverage.UnattributedRecords += usage.Records
		}
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("read session usage: %w", err)
	}
	if len(sessions) == 0 {
		return report, fmt.Errorf("%w: %q", ErrSessionNotFound, threadID)
	}
	for _, entry := range sessions {
		report.Sessions = append(report.Sessions, *entry)
		if entry.Records == 0 {
			report.Coverage.SessionsWithoutRecords = append(report.Coverage.SessionsWithoutRecords, entry.SessionID)
		}
	}
	sort.Slice(report.Sessions, func(i, j int) bool {
		leftRoot, rightRoot := report.Sessions[i].SessionID == threadID, report.Sessions[j].SessionID == threadID
		if leftRoot != rightRoot {
			return leftRoot
		}
		return report.Sessions[i].SessionID < report.Sessions[j].SessionID
	})
	sort.Strings(report.Coverage.SessionsWithoutRecords)
	for _, bucket := range models {
		report.ModelBreakdowns = append(report.ModelBreakdowns, *bucket)
	}
	sort.Slice(report.ModelBreakdowns, func(i, j int) bool {
		left, right := report.ModelBreakdowns[i], report.ModelBreakdowns[j]
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.Model < right.Model
	})
	return report, nil
}

func (total *UsageTotals) add(usage UsageTotals) {
	total.Records += usage.Records
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.CacheCreationTokens += usage.CacheCreationTokens
	total.CacheReadTokens += usage.CacheReadTokens
}

// ListTokenUsage returns every persisted token_usage row without reading
// conversation content, so its cost does not grow with message or tool output
// size. Row order is unspecified. A missing store has no rows yet.
func ListTokenUsage(sessDir string) ([]TokenUsageRow, error) {
	db, ok, err := openStoreForScan(sessDir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()

	// CROSS JOIN keeps sessions as the outer loop, so SQLite probes
	// idx_session_messages_role once per session instead of scanning every
	// message row and its content.
	rows, err := db.Query(`SELECT m.session_id, m.at, m.provider, m.model,
		m.input_tokens, m.output_tokens, m.cache_creation_tokens, m.cache_read_tokens
		FROM sessions s CROSS JOIN session_messages m
		ON m.session_id = s.id AND m.role = 'meta' AND m.content = ?`, tokenUsageContent)
	if err != nil {
		return nil, fmt.Errorf("list token usage: %w", err)
	}
	defer rows.Close()

	var out []TokenUsageRow
	for rows.Next() {
		var row TokenUsageRow
		var at sql.NullString
		if err := rows.Scan(&row.SessionID, &at, &row.Provider, &row.Model,
			&row.InputTokens, &row.OutputTokens, &row.CacheCreationTokens, &row.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("scan token usage: %w", err)
		}
		if at.Valid {
			row.At = parseTime(at.String)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list token usage: %w", err)
	}
	return out, nil
}
