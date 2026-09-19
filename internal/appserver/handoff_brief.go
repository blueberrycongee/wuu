package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) prepareHandoffSeed(ctx context.Context, params pluginhost.SessionCreateParams) (pluginhost.SessionCreateParams, error) {
	if params.ContextSource != pluginhost.SessionContextSourceSeed || params.Seed == nil || strings.TrimSpace(params.Seed.Body) != "" {
		return params, nil
	}
	if s == nil || s.rt == nil || s.rt.StreamRunner == nil {
		return params, errors.New("handoff brief generation requires a runtime")
	}
	provider, ok := s.rt.StreamRunner.CompactionRegistry.Resolve(nil).(agent.HandoffBriefProvider)
	if !ok || provider == nil {
		return params, errors.New("handoff brief generator is unavailable")
	}
	sourceID := strings.TrimSpace(params.Seed.Source.SessionID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(params.ParentSessionID)
	}
	if sourceID == "" {
		return params, errors.New("handoff brief requires a source session")
	}
	if params.Seed.Source.SessionID == "" {
		params.Seed.Source.SessionID = sourceID
	}
	page, err := session.ReadHistoryQuery(ctx, s.rt.SessionDir, session.HistoryReadQuery{
		SessionID: sourceID, StartSeq: 1, EndSeq: params.Seed.Source.ThroughSeq, SnapshotSeq: params.Seed.Source.ThroughSeq, Limit: 50,
	})
	if err != nil {
		return params, err
	}
	if params.Seed.Source.ThroughSeq < 1 {
		params.Seed.Source.ThroughSeq = page.HeadSeq
	}
	messages := chatMessagesFromHistoryRecords(page.Records)
	previous := agent.CompactionNote{}
	if note, ok, err := session.LoadCompactionNote(s.rt.SessionDir, sourceID, provider.CompactionKey()); err != nil {
		return params, err
	} else if ok {
		previous = agent.CompactionNote{Markdown: note.Markdown, CoveredMessages: note.CoveredMessages, CoveredHash: note.CoveredHash}
	}
	intent := ""
	if params.Launch != nil {
		intent = strings.TrimSpace(params.Launch.Intent)
	}
	plan, err := provider.PlanHandoffBrief(ctx, s.rt.Model, messages, previous, intent, sourceID, params.Seed.Source.ThroughSeq)
	if err != nil {
		return params, err
	}
	if strings.TrimSpace(plan.Prompt) == "" {
		return params, errors.New("handoff brief prompt is empty")
	}
	if plan.MaxBytes <= 0 {
		plan.MaxBytes = 24 * 1024
	}
	fork := s.rt.StreamRunner.CompactionNoteFork()
	if fork == nil {
		return params, errors.New("handoff brief generator is unavailable")
	}
	result, err := fork(ctx, messages, plan)
	if err != nil {
		return params, err
	}
	body := strings.TrimSpace(result.Markdown)
	if body == "" {
		return params, errors.New("handoff brief generator returned empty Markdown")
	}
	if len([]byte(body)) > plan.MaxBytes {
		return params, fmt.Errorf("handoff brief exceeds %d bytes", plan.MaxBytes)
	}
	params.Seed.Body = body
	if strings.TrimSpace(params.Seed.Provenance.Producer) == "" {
		if _, builtin := provider.(agent.DefaultContextWindowProvider); builtin {
			params.Seed.Provenance.Producer = provider.CompactionKey()
		} else {
			params.Seed.Provenance.Producer = "plugin:" + strings.TrimSpace(provider.CompactionKey())
		}
	}
	if strings.TrimSpace(params.Seed.Provenance.SourceModel) == "" {
		params.Seed.Provenance.SourceModel = strings.TrimSpace(s.rt.Model)
	}
	return params, nil
}

func chatMessagesFromHistoryRecords(records []session.HistoryRecord) []providers.ChatMessage {
	messages := make([]providers.ChatMessage, 0, len(records))
	for _, record := range records {
		messages = append(messages, providers.ChatMessage{
			Role: record.Role, Content: record.Content, Name: record.Name, Seq: record.Seq,
		})
	}
	return messages
}
