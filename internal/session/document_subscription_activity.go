package session

import (
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// The activity index is written atomically with physical history. Dashboard
// reads do not need conversation or tool payloads.
type activityDocument struct {
	SessionID string
	Record    HistoryRecord
}

func (s *Store) LatestSubscriptionActivity(keys []SubscriptionActivityKey) (map[SubscriptionActivityKey]SubscriptionActivity, error) {
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
	var result map[SubscriptionActivityKey]SubscriptionActivity
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		result, err = subscriptionActivityDocuments(tx, wanted)
		return err
	})
	return result, err
}

func subscriptionActivityDocuments(tx *persistence.Tx, wanted []SubscriptionActivityKey) (map[SubscriptionActivityKey]SubscriptionActivity, error) {
	out := make(map[SubscriptionActivityKey]SubscriptionActivity)
	sessions, err := readDocumentRows[Session](tx, "sessions", "")
	if err != nil {
		return nil, err
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
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
	for _, sess := range sessions {
		selected := false
		for _, key := range wanted {
			if key.Provider != "" && (sess.EngineID == "" || sess.EngineID == "wuu") || key.Provider == "" && key.EngineID == sess.EngineID {
				selected = true
				break
			}
		}
		if !selected {
			continue
		}
		records, err := readDocumentRows[activityDocument](tx, "session_activity", documentPrefix(sess.ID))
		if err != nil {
			return nil, err
		}
		for _, activity := range records {
			record := activity.Record
			if record.Role != "user" && (record.Role != "meta" || record.Content != turnTerminalContent && record.Content != tokenUsageContent) {
				continue
			}
			if record.Role != "meta" {
				record.Content = ""
			}
			id, engine := sess.ID, sess.EngineID
			if id != sessionID || (record.Role == "user" && !record.Steered) {
				consider(nil)
				usage = nil
			}
			sessionID = id
			engineID = engine
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

func (s *Store) ListTokenUsage() ([]TokenUsageRow, error) {
	var result []TokenUsageRow
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[activityDocument](tx, "session_activity", "")
		if err != nil {
			return err
		}
		result = nil
		for _, row := range rows {
			record := row.Record
			if record.Role != "meta" || record.Content != tokenUsageContent {
				continue
			}
			result = append(result, TokenUsageRow{SessionID: row.SessionID, At: record.At, Provider: record.Provider, Model: record.Model, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, CacheCreationTokens: record.CacheCreationTokens, CacheReadTokens: record.CacheReadTokens})
		}
		return nil
	})
	return result, err
}
