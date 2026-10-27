package runtime

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

func readPluginSessionHistory(ctx context.Context, store *session.Store, pluginID string, params pluginhost.SessionHistoryReadParams) (pluginhost.SessionHistoryPage, error) {
	if err := pluginhost.ValidateSessionHistoryReadParams(params); err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	meta, err := loadVisibleHistorySession(ctx, store, pluginID, params.SessionID)
	if err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	limit := params.Limit
	if limit == 0 {
		limit = pluginhost.SessionHistoryDefaultLimit
	}
	var cursor *session.HistoryCursor
	if strings.TrimSpace(params.Cursor) != "" {
		decoded, decodeErr := session.DecodeHistoryCursor(params.Cursor)
		if decodeErr != nil {
			return pluginhost.SessionHistoryPage{}, serviceError("invalid_params", decodeErr.Error())
		}
		cursor = &decoded
	}
	page, err := store.WithContext(ctx).ReadHistoryQuery(session.HistoryReadQuery{
		SessionID: meta.ID, StartSeq: params.StartSeq, EndSeq: params.EndSeq, SnapshotSeq: params.SnapshotSeq, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return pluginhost.SessionHistoryPage{}, historyServiceError(err)
	}
	result := pluginhost.SessionHistoryPage{
		SessionID: meta.ID, HeadSeq: page.HeadSeq, SnapshotSeq: page.SnapshotSeq, HasMore: page.HasMore, BudgetExhausted: page.BudgetExhausted,
		Records: make([]pluginhost.SessionHistoryRecord, 0, len(page.Records)),
	}
	for _, record := range page.Records {
		result.Records = append(result.Records, pluginhost.SessionHistoryRecord{
			Seq: record.Seq, Role: record.Role, Name: record.Name, Content: record.Content, DisplayContent: record.DisplayContent,
			ReasoningContent: record.ReasoningContent, ToolCalls: string(record.ToolCalls), ToolCallID: record.ToolCallID,
			ToolResult: string(record.ToolResult), FinishReason: record.FinishReason, Truncated: record.Truncated,
		})
	}
	if page.Next != nil {
		result.NextCursor = session.EncodeHistoryCursor(*page.Next)
	}
	return result, nil
}

func searchPluginSessionHistory(ctx context.Context, store *session.Store, pluginID string, params pluginhost.SessionHistorySearchParams) (pluginhost.SessionHistoryPage, error) {
	if err := pluginhost.ValidateSessionHistorySearchParams(params); err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	meta, err := loadVisibleHistorySession(ctx, store, pluginID, params.SessionID)
	if err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	limit := params.Limit
	if limit == 0 {
		limit = pluginhost.SessionHistorySearchDefault
	}
	var cursor *session.HistoryCursor
	if strings.TrimSpace(params.Cursor) != "" {
		decoded, decodeErr := session.DecodeHistoryCursor(params.Cursor)
		if decodeErr != nil {
			return pluginhost.SessionHistoryPage{}, serviceError("invalid_params", decodeErr.Error())
		}
		cursor = &decoded
	}
	page, err := store.WithContext(ctx).SearchHistoryQuery(session.HistorySearchQuery{
		SessionID: meta.ID, Query: params.Query, StartSeq: params.StartSeq, EndSeq: params.EndSeq,
		SnapshotSeq: params.SnapshotSeq, BeforeSeq: params.BeforeSeq, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return pluginhost.SessionHistoryPage{}, historyServiceError(err)
	}
	result := pluginhost.SessionHistoryPage{
		SessionID: meta.ID, HeadSeq: page.HeadSeq, SnapshotSeq: page.SnapshotSeq, HasMore: page.HasMore, BudgetExhausted: page.BudgetExhausted,
		Matches: make([]pluginhost.SessionHistoryMatch, 0, len(page.Records)),
	}
	for _, record := range page.Records {
		result.Matches = append(result.Matches, pluginhost.SessionHistoryMatch{
			Seq: record.Seq, Role: record.Role, Name: record.Name, Excerpt: record.Content,
		})
	}
	if page.Next != nil {
		result.NextCursor = session.EncodeHistoryCursor(*page.Next)
	}
	return result, nil
}

func loadVisibleHistorySession(ctx context.Context, store *session.Store, pluginID, sessionID string) (session.Session, error) {
	if store == nil {
		return session.Session{}, serviceError("service_unavailable", "session history is unavailable")
	}
	meta, ok, err := store.WithContext(ctx).Find(sessionID)
	if err != nil {
		return session.Session{}, historyServiceError(err)
	}
	if !ok {
		return session.Session{}, serviceError("not_found", "session history is unavailable")
	}
	owner := "plugin:" + strings.TrimSpace(pluginID)
	if meta.Visibility == pluginhost.SessionVisibilityPlugin && meta.Owner != owner {
		return session.Session{}, serviceError("not_found", "session history is unavailable")
	}
	return meta, nil
}

func historyServiceError(err error) error {
	switch {
	case errors.Is(err, session.ErrSessionNotFound), errors.Is(err, session.ErrHistoryUnavailable):
		return serviceError("not_found", "session history is unavailable")
	case errors.Is(err, session.ErrHistorySnapshotGone):
		return serviceError("invalid_params", err.Error())
	default:
		return err
	}
}
