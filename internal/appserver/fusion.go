package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const fusionSelectionPrefix = "fusion:"

func (s *Server) prepareFusion(ctx context.Context, th *threadState, rt *runtime.ThreadRuntime, turnID string, snapshot *config.Config) (*config.FusionSelection, error) {
	th.mu.Lock()
	enabled := th.Model == config.FusionID
	engine := agentengine.NormalizeEngineID(th.EngineID)
	persist := th.PersistHistory
	th.mu.Unlock()
	if !enabled {
		return nil, nil
	}
	if engine != agentengine.EngineWuu {
		return nil, fmt.Errorf("Fusion requires the Wuu engine")
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
	pair, err := s.findFusionSelection(th)
	if err != nil {
		return nil, err
	}
	if pair == nil {
		pair = rt.FusionPair()
	}
	if pair == nil {
		if _, err := cfg.FusionSelection(); err != nil {
			return nil, err
		}
		pair = &config.FusionSelection{Lead: cfg.Agent.Fusion.Lead, Sidekick: cfg.Agent.Fusion.Sidekick}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.rt.ApplyFusion(rt, cfg, *pair); err != nil {
		return nil, err
	}
	if err := rt.SettleFusion(ctx); err != nil {
		return nil, fmt.Errorf("settle previous Fusion delegation: %w", err)
	}
	// Every turn carries the pair for history projection. Recovery always reads
	// the first binding, so editing global defaults cannot retarget this session.
	if persist {
		data, err := json.Marshal(pair)
		if err != nil {
			return nil, err
		}
		record := persistedMessage{Role: "meta", Content: fusionSelectionPrefix + string(data), ClientID: turnID, At: time.Now().UTC()}
		if err := session.AppendHistoryRecord(s.rt.SessionDir, th.ID, historyRecordFromPersistedMessage(record)); err != nil {
			return nil, err
		}
	}
	th.mu.Lock()
	th.History = replaceBaseSystemPrompt(th.History, rt.StreamRunner.SystemPrompt)
	for i := range th.Turns {
		turn := &th.Turns[i]
		if turn.ID == turnID {
			turn.Fusion = pair
			turn.ModelProvider, turn.Model = pair.Lead.Provider, pair.Lead.Model
			th.runningProviderName, th.runningModel = turn.ModelProvider, turn.Model
		}
	}
	notification := th.snapshotLocked()
	th.mu.Unlock()
	_ = s.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: notification})
	return pair, nil
}

func (s *Server) findFusionSelection(th *threadState) (*config.FusionSelection, error) {
	th.mu.Lock()
	for _, turn := range th.Turns {
		if turn.Fusion != nil {
			pair := *turn.Fusion
			th.mu.Unlock()
			return &pair, nil
		}
	}
	persist := th.PersistHistory
	th.mu.Unlock()
	if !persist {
		return nil, nil
	}
	records, err := loadMetaMessages(s.rt.SessionDir, th.ID)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if strings.HasPrefix(record.Content, fusionSelectionPrefix) {
			var pair config.FusionSelection
			if err := json.Unmarshal([]byte(strings.TrimPrefix(record.Content, fusionSelectionPrefix)), &pair); err != nil {
				return nil, fmt.Errorf("load Fusion selection: %w", err)
			}
			return &pair, nil
		}
	}
	return nil, nil
}
