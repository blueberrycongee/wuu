package runtime

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func readPluginSessionHistory(ctx context.Context, store *session.Store, pluginID string, binding userspace.Binding, receiptsSelected bool, params pluginhost.SessionHistoryReadParams) (pluginhost.SessionHistoryPage, error) {
	if err := pluginhost.ValidateSessionHistoryReadParams(params); err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	meta, err := loadVisibleHistorySession(ctx, store, pluginID, params.SessionID, binding)
	if err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	receiptEnabled := receiptsSelected && binding.IsUser()
	if receiptEnabled && (binding.Validate() != nil || !binding.Matches(userspace.Binding{UserSpaceID: meta.UserSpaceID, ProductDomain: meta.ProductDomain})) {
		return pluginhost.SessionHistoryPage{}, serviceError("domain_denied", "receipt history belongs to another user space")
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
	var receipts map[string]providers.ClientInputReceipt
	if receiptEnabled {
		receipts, err = store.WithContext(ctx).ClientInputHistoryReceipts(session.ClientInputScope{Binding: binding, SessionID: meta.ID, PluginID: pluginID})
		if err != nil {
			return pluginhost.SessionHistoryPage{}, historyServiceError(err)
		}
	}
	for _, record := range page.Records {
		var receipt *providers.ClientInputReceipt
		if r, found := receipts[record.ClientID]; found && r.MessageSeq == record.Seq {
			receipt = &r
		}
		attachments := HistoryAttachmentMetadata(record)
		content := record.Content
		if record.Origin == "user" && len(attachments) > 0 {
			content = record.DisplayContent
		}
		result.Records = append(result.Records, pluginhost.SessionHistoryRecord{
			Receipt: receipt, Relation: providers.CloneMessageRelation(record.Relation),
			Attachments: attachments, At: historyRecordTime(record.At),
			ProviderItemID: record.ProviderItemID, EngineTurnID: record.EngineTurnID,
			Seq: record.Seq, Role: record.Role, Name: record.Name, Content: content, DisplayContent: record.DisplayContent,
			Hidden: record.Hidden, ReadOnly: record.ReadOnly, Origin: record.Origin,
			ReasoningContent: record.ReasoningContent, ToolCalls: string(record.ToolCalls), ToolCallID: record.ToolCallID,
			ToolResult: string(record.ToolResult), FinishReason: record.FinishReason, Truncated: record.Truncated,
		})
	}
	if page.Next != nil {
		result.NextCursor = session.EncodeHistoryCursor(*page.Next)
	}
	return result, nil
}

func searchPluginSessionHistory(ctx context.Context, store *session.Store, pluginID string, binding userspace.Binding, params pluginhost.SessionHistorySearchParams) (pluginhost.SessionHistoryPage, error) {
	if err := pluginhost.ValidateSessionHistorySearchParams(params); err != nil {
		return pluginhost.SessionHistoryPage{}, err
	}
	meta, err := loadVisibleHistorySession(ctx, store, pluginID, params.SessionID, binding)
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
	minimum, _ := time.Parse(time.RFC3339Nano, params.TimeMin)
	maximum, _ := time.Parse(time.RFC3339Nano, params.TimeMax)
	page, err := store.WithContext(ctx).SearchHistoryQuery(session.HistorySearchQuery{TimeMin: minimum, TimeMax: maximum, View: params.View,
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
		excerpt := record.Content
		if params.View == "conversation" && record.DisplayContent != "" {
			excerpt = record.DisplayContent
		}
		attachments := HistoryAttachmentMetadata(record)
		if record.Origin == "user" && len(attachments) > 0 {
			excerpt = record.DisplayContent
			for _, attachment := range attachments {
				excerpt += "\n" + attachment.Name
			}
			excerpt = strings.TrimSpace(excerpt)
			// SearchHistoryQuery uses a literal substring, not query operators. Do
			// not expose a hit whose only match was a host-generated working path.
			if !strings.Contains(strings.ToLower(excerpt), strings.ToLower(strings.TrimSpace(params.Query))) {
				continue
			}
		}
		result.Matches = append(result.Matches, pluginhost.SessionHistoryMatch{At: historyRecordTime(record.At), Seq: record.Seq, Role: record.Role, Name: record.Name, Excerpt: excerpt})
	}
	if page.Next != nil {
		result.NextCursor = session.EncodeHistoryCursor(*page.Next)
	}
	return result, nil
}

func loadVisibleHistorySession(ctx context.Context, store *session.Store, pluginID, sessionID string, binding userspace.Binding) (session.Session, error) {
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
	if binding.Validate() != nil || !binding.Matches(userspace.Binding{UserSpaceID: meta.UserSpaceID, ProductDomain: meta.ProductDomain}) {
		return session.Session{}, serviceError("domain_denied", "session belongs to another user space or product domain")
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

func historyRecordTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}
