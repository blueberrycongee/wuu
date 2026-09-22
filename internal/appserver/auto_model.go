package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/automodel"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const autoDecisionPrefix = "auto_model:"

func (s *Server) prepareAutoModel(ctx context.Context, th *threadState, rt *runtime.ThreadRuntime, turnID string, history []providers.ChatMessage, requestContext []agent.ContextSegment, snapshot *config.Config) (*automodel.Decision, error) {
	th.mu.Lock()
	auto := th.Model == config.AutoModelID
	engine := agentengine.NormalizeEngineID(th.EngineID)
	internalTurn := false
	for _, turn := range th.Turns {
		if turn.ID == turnID {
			internalTurn = turn.Kind == TurnKindInternal || turn.Kind == TurnKindCompact
		}
	}
	th.mu.Unlock()
	if !auto {
		return nil, nil
	}
	if engine != agentengine.EngineWuu {
		return nil, fmt.Errorf("Auto requires the Wuu engine")
	}
	var cfg config.Config
	if snapshot != nil {
		cfg = *snapshot
	} else {
		var err error
		cfg, _, err = s.rt.LoadEffectiveConfig()
		if err != nil {
			return nil, err
		}
	}
	decision, err := s.findAutoDecision(th, turnID, internalTurn)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		s.updateAutoTurn(th, turnID, nil, true)
		defer func() { s.updateAutoTurn(th, turnID, nil, false) }()
		input := providers.CloneChatMessages(history)
		// Request-only document context participates in classification without
		// becoming a durable user message or changing the execution history.
		if len(requestContext) > 0 {
			var text strings.Builder
			for _, segment := range requestContext {
				for _, m := range segment.Messages {
					text.WriteString(m.Content)
					text.WriteByte('\n')
				}
				for _, b := range segment.Blocks {
					text.WriteString(b.Content)
					text.WriteByte('\n')
				}
				text.WriteByte('\n')
			}
			for i := len(input) - 1; i >= 0; i-- {
				if input[i].Role == "user" {
					input[i].Content += "\nActive task context:\n" + text.String()
					break
				}
			}
		}
		resolved, err := automodel.Resolve(providers.WithInferenceJournal(ctx, s.rt.InferenceJournalForOwner(th.ID)), cfg, input, nil)
		if err != nil {
			s.updateAutoTurn(th, turnID, nil, false)
			return nil, err
		}
		decision = &resolved
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if th.PersistHistory {
			data, err := json.Marshal(decision)
			if err != nil {
				return nil, err
			}
			record := persistedMessage{Role: "meta", Content: autoDecisionPrefix + string(data), ClientID: turnID, At: time.Now().UTC(), Provider: decision.Classifier.Provider, Model: decision.Classifier.Model}
			if usage := decision.Usage; usage != nil {
				record.InputTokens = usage.InputTokens
				record.OutputTokens = usage.OutputTokens
				record.CacheReadTokens = usage.CacheReadTokens
				record.CacheCreationTokens = usage.CacheCreationTokens
			}
			if err := session.AppendHistoryRecord(s.rt.SessionDir, th.ID, historyRecordFromPersistedMessage(record)); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.rt.ApplyAutoModel(rt, cfg, decision.Selection); err != nil {
		return nil, err
	}
	th.mu.Lock()
	th.History = replaceBaseSystemPrompt(th.History, rt.StreamRunner.SystemPrompt)
	th.mu.Unlock()
	if internalTurn {
		inherited := *decision
		inherited.Usage = nil
		inherited.DurationMS = 0
		decision = &inherited
	}
	s.updateAutoTurn(th, turnID, decision, false)
	return decision, nil
}

func (s *Server) updateAutoTurn(th *threadState, turnID string, decision *automodel.Decision, selecting bool) {
	th.mu.Lock()
	for i := range th.Turns {
		turn := &th.Turns[i]
		if turn.ID != turnID {
			continue
		}
		turn.SelectingModel = selecting
		if decision != nil {
			turn.AutoModel = decision
			turn.ModelProvider, turn.Model = decision.Selection.Provider, decision.Selection.Model
			th.runningProviderName, th.runningModel = turn.ModelProvider, turn.Model
		}
	}
	snapshot := th.snapshotLocked()
	th.mu.Unlock()
	_ = s.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: snapshot})
}

// findAutoDecision reads a turn's decision, or the last execution decision for
// continuation/runtime recovery. It never changes the session's Auto intent.
func (s *Server) findAutoDecision(th *threadState, turnID string, latest bool) (*automodel.Decision, error) {
	var decision *automodel.Decision
	th.mu.Lock()
	for _, turn := range th.Turns {
		if turn.AutoModel != nil && (latest || turn.ID == turnID) {
			decision = turn.AutoModel
		}
	}
	persist := th.PersistHistory
	th.mu.Unlock()
	if decision != nil || !persist {
		return decision, nil
	}
	records, err := loadMetaMessages(s.rt.SessionDir, th.ID)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if (latest || record.ClientID == turnID) && strings.HasPrefix(record.Content, autoDecisionPrefix) {
			var stored automodel.Decision
			if err := json.Unmarshal([]byte(strings.TrimPrefix(record.Content, autoDecisionPrefix)), &stored); err != nil {
				return nil, fmt.Errorf("load Auto decision: %w", err)
			}
			decision = &stored
		}
	}
	return decision, nil
}
