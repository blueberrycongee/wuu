package session

import (
	"database/sql"
	"strings"
	"time"
)

const (
	turnTerminalContent = "turn_terminal"
	tokenUsageContent   = "token_usage"
)

// SubscriptionActivityKey selects one dashboard source. EngineID matches an
// external engine binding. Provider matches the built-in model service, which
// keeps its own credentials and is not an engine binding.
type SubscriptionActivityKey struct {
	EngineID string
	Provider string
}

// SubscriptionActivity is the newest settled request this store can prove for
// one source. Status and Error come from the latest turn_terminal row. Usage
// is present only when that source also has a token_usage row; a source with
// neither row is simply absent from the result.
type SubscriptionActivity struct {
	Key                 SubscriptionActivityKey
	Status              string
	Error               string
	At                  time.Time
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	UsageReported       bool
	LocalUsage          SubscriptionUsage
}

// SubscriptionUsage totals retained Wuu token records, not account billing.
type SubscriptionUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	ReportedTurns       int `json:"reported_turns"`
}

// LatestSubscriptionActivity reads one newest request per requested source.
// It also aggregates reported token usage across retained sessions. A missing
// store returns an empty map; absent usage is not evidence of zero billing.
func LatestSubscriptionActivity(sessDir string, keys []SubscriptionActivityKey) (map[SubscriptionActivityKey]SubscriptionActivity, error) {
	out := make(map[SubscriptionActivityKey]SubscriptionActivity)
	wanted := make([]SubscriptionActivityKey, 0, len(keys))
	seen := make(map[SubscriptionActivityKey]struct{}, len(keys))
	for _, key := range keys {
		key.EngineID = strings.TrimSpace(key.EngineID)
		key.Provider = strings.TrimSpace(key.Provider)
		if key.EngineID == "" && key.Provider == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		wanted = append(wanted, key)
	}
	if len(wanted) == 0 {
		return out, nil
	}
	db, ok, err := openStoreForScan(sessDir)
	if err != nil || !ok {
		return out, err
	}
	defer db.Close()

	for _, key := range wanted {
		activity, found, err := latestSubscriptionActivity(db, key)
		if err != nil {
			return nil, err
		}
		usage, err := subscriptionUsage(db, key)
		if err != nil {
			return nil, err
		}
		activity.LocalUsage = usage
		if found || usage.ReportedTurns > 0 {
			out[key] = activity
		}
	}
	return out, nil
}

func subscriptionUsage(db *sql.DB, key SubscriptionActivityKey) (SubscriptionUsage, error) {
	where, value := "s.engine_id = ?", key.EngineID
	if key.Provider != "" {
		// Attribute by the recorded provider, never the session's current
		// selection: users can change providers between turns.
		where, value = "(s.engine_id = '' OR s.engine_id = 'wuu') AND m.provider = ?", key.Provider
	}
	var usage SubscriptionUsage
	err := db.QueryRow(`SELECT COALESCE(SUM(m.input_tokens),0), COALESCE(SUM(m.output_tokens),0),
		COALESCE(SUM(m.cache_creation_tokens),0), COALESCE(SUM(m.cache_read_tokens),0), COUNT(*)
		FROM session_messages m JOIN sessions s ON s.id = m.session_id
		WHERE m.role = 'meta' AND m.content = 'token_usage'
		AND (m.input_tokens > 0 OR m.output_tokens > 0 OR m.cache_creation_tokens > 0 OR m.cache_read_tokens > 0)
		AND `+where, value).Scan(&usage.InputTokens, &usage.OutputTokens, &usage.CacheCreationTokens, &usage.CacheReadTokens, &usage.ReportedTurns)
	return usage, err
}

func latestSubscriptionActivity(db *sql.DB, key SubscriptionActivityKey) (SubscriptionActivity, bool, error) {
	sessionID, err := newestSubscriptionSession(db, key)
	if err != nil || sessionID == "" {
		return SubscriptionActivity{}, false, err
	}
	activity := SubscriptionActivity{Key: key}
	found := false
	terminal, ok, err := latestMeta(db, sessionID, turnTerminalContent)
	if err != nil {
		return SubscriptionActivity{}, false, err
	}
	if ok {
		activity.Status = terminal.StopReason
		activity.Error = strings.TrimSpace(terminal.DisplayContent)
		activity.At = terminal.At
		found = true
	}
	usage, ok, err := latestMeta(db, sessionID, tokenUsageContent)
	if err != nil {
		return SubscriptionActivity{}, false, err
	}
	if ok && (usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheCreationTokens > 0 || usage.CacheReadTokens > 0) {
		activity.Model = usage.Model
		activity.InputTokens = usage.InputTokens
		activity.OutputTokens = usage.OutputTokens
		activity.CacheCreationTokens = usage.CacheCreationTokens
		activity.CacheReadTokens = usage.CacheReadTokens
		activity.UsageReported = true
		if activity.At.IsZero() {
			activity.At = usage.At
		}
		found = true
	}
	return activity, found, nil
}

func newestSubscriptionSession(db *sql.DB, key SubscriptionActivityKey) (string, error) {
	column := "engine_id"
	value := key.EngineID
	if key.Provider != "" {
		column = "provider"
		value = key.Provider
	}
	var id string
	err := db.QueryRow(
		`SELECT id FROM sessions WHERE `+column+` = ? ORDER BY updated_at DESC, id DESC LIMIT 1`,
		value,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func latestMeta(db *sql.DB, sessionID, content string) (HistoryRecord, bool, error) {
	var record HistoryRecord
	var at sql.NullString
	err := db.QueryRow(`
		SELECT stop_reason, display_content, at, model,
		       input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens
		FROM session_messages
		WHERE session_id = ? AND role = 'meta' AND content = ?
		ORDER BY seq DESC
		LIMIT 1`, sessionID, content).Scan(
		&record.StopReason,
		&record.DisplayContent,
		&at,
		&record.Model,
		&record.InputTokens,
		&record.OutputTokens,
		&record.CacheCreationTokens,
		&record.CacheReadTokens,
	)
	if err == sql.ErrNoRows {
		return HistoryRecord{}, false, nil
	}
	if err != nil {
		return HistoryRecord{}, false, err
	}
	if at.Valid {
		record.At = parseTime(at.String)
	}
	return record, true, nil
}
