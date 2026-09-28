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
// one source. Status and Error come from its turn_terminal row. Usage is
// attached only from the same request boundary and recorded provider. Legacy
// usage without a terminal has unknown status; missing usage is not zero usage.
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
// It also aggregates reported token usage in the same scan across retained
// sessions. A missing store returns an empty map; absent usage is not evidence
// of zero billing.
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

	engineIDs := make(map[string]bool)
	for _, key := range wanted {
		if key.Provider != "" {
			engineIDs[""] = true
			engineIDs["wuu"] = true
		} else {
			engineIDs[key.EngineID] = true
		}
	}
	args := make([]any, 0, len(engineIDs))
	for id := range engineIDs {
		args = append(args, id)
	}
	where := "s.engine_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	// Read durable request boundaries, not the mutable session selection or
	// updated_at (which also changes when a user renames or pins a session).
	// Do not filter rows by provider: legacy terminals and user boundaries may
	// be untagged. All requested sources share this one ordered scan.
	rows, err := db.Query(`SELECT m.session_id, s.engine_id, m.role,
		CASE WHEN m.role = 'meta' THEN m.content ELSE '' END,
		m.steered, m.client_id, m.stop_reason, m.display_content, m.at, m.provider, m.model,
		m.input_tokens, m.output_tokens, m.cache_creation_tokens, m.cache_read_tokens
		FROM session_messages m JOIN sessions s ON s.id = m.session_id
		WHERE `+where+` AND (m.role = 'user' OR
		(m.role = 'meta' AND m.content IN ('turn_terminal', 'token_usage')))
		ORDER BY m.session_id, m.seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	totals := make(map[SubscriptionActivityKey]SubscriptionUsage)
	var sessionID, engineID string
	var usage *HistoryRecord
	matches := func(key SubscriptionActivityKey, provider string) bool {
		if key.Provider != "" {
			return (engineID == "" || engineID == "wuu") && key.Provider == provider
		}
		return key.EngineID == engineID
	}
	consider := func(terminal *HistoryRecord) {
		if terminal == nil && usage == nil {
			return
		}
		candidate := SubscriptionActivity{}
		provider := ""
		requestUsage := usage
		if terminal != nil {
			candidate.Status = terminal.StopReason
			candidate.Error = strings.TrimSpace(terminal.DisplayContent)
			candidate.At = terminal.At
			candidate.Model = terminal.Model
			provider = terminal.Provider
			// Provider-tagged terminals carry their own usage snapshot. Internal
			// continuations have no user boundary, so earlier usage is unsafe.
			// Only legacy user terminals use the preceding row; internal
			// terminals have no client ID or provable request boundary.
			if terminal.Provider != "" {
				requestUsage = terminal
			} else if terminal.ClientID == "" {
				requestUsage = nil
			}
		}
		if provider == "" && requestUsage != nil {
			provider = requestUsage.Provider
		}
		if requestUsage != nil && (provider == "" || requestUsage.Provider == provider) && (requestUsage.InputTokens > 0 || requestUsage.OutputTokens > 0 || requestUsage.CacheCreationTokens > 0 || requestUsage.CacheReadTokens > 0) {
			candidate.UsageReported = true
			candidate.InputTokens = requestUsage.InputTokens
			candidate.OutputTokens = requestUsage.OutputTokens
			candidate.CacheCreationTokens = requestUsage.CacheCreationTokens
			candidate.CacheReadTokens = requestUsage.CacheReadTokens
			if candidate.Model == "" {
				candidate.Model = requestUsage.Model
			}
			if candidate.At.IsZero() {
				candidate.At = requestUsage.At
			}
		}
		for _, key := range wanted {
			if !matches(key, provider) {
				continue
			}
			if latest, found := out[key]; !found || !candidate.At.Before(latest.At) {
				candidate.Key = key
				out[key] = candidate
			}
		}
	}
	for rows.Next() {
		var id, engine string
		var record HistoryRecord
		var at sql.NullString
		if err := rows.Scan(&id, &engine, &record.Role, &record.Content, &record.Steered, &record.ClientID,
			&record.StopReason, &record.DisplayContent, &at, &record.Provider, &record.Model,
			&record.InputTokens, &record.OutputTokens, &record.CacheCreationTokens, &record.CacheReadTokens); err != nil {
			return nil, err
		}
		if id != sessionID || (record.Role == "user" && !record.Steered) {
			consider(nil)
			usage = nil
		}
		sessionID = id
		engineID = engine
		if at.Valid {
			record.At = parseTime(at.String)
		}
		switch record.Content {
		case tokenUsageContent:
			if record.InputTokens > 0 || record.OutputTokens > 0 || record.CacheCreationTokens > 0 || record.CacheReadTokens > 0 {
				usage = &record
				for _, key := range wanted {
					if !matches(key, record.Provider) {
						continue
					}
					total := totals[key]
					total.InputTokens += record.InputTokens
					total.OutputTokens += record.OutputTokens
					total.CacheCreationTokens += record.CacheCreationTokens
					total.CacheReadTokens += record.CacheReadTokens
					total.ReportedTurns++
					totals[key] = total
				}
			}
		case turnTerminalContent:
			consider(&record)
			usage = nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	consider(nil)
	for key, total := range totals {
		activity := out[key]
		activity.Key = key
		activity.LocalUsage = total
		out[key] = activity
	}
	return out, nil
}
