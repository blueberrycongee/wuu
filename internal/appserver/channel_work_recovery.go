package appserver

import (
	"context"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) reconcileChannelWorkRuns(ctx context.Context) error {
	if s == nil || s.channelService == nil || s.rt == nil {
		return nil
	}
	runs, err := s.channelService.ListUnsettledWorkRuns(ctx)
	if err != nil {
		return err
	}
	recoveries := make([]channels.WorkRunRecovery, 0, len(runs))
	for _, run := range runs {
		active, activeErr := session.ThreadExecutionActive(s.rt.SessionDir, run.SessionRef)
		if activeErr != nil {
			return activeErr
		}
		stored, found, findErr := session.Find(s.rt.SessionDir, run.SessionRef)
		if findErr != nil {
			return findErr
		}
		terminalState := channels.WorkRunRecoveryActive
		if !active && found {
			turnID := ""
			if run.NamedAgentID != "" {
				turnID = run.TurnID
			}
			if run.NamedAgentID == "" || strings.TrimSpace(turnID) != "" {
				terminalState, findErr = channelWorkTurnTerminalState(s.rt.SessionDir, run.SessionRef, turnID)
			}
			if findErr != nil {
				return findErr
			}
		}
		recoveries = append(recoveries, channels.WorkRunRecovery{
			RunID: run.ID, SessionRef: run.SessionRef,
			State: channelWorkRunRecoveryState(run, active, stored, found, terminalState),
		})
	}
	return s.channelService.ReconcileWorkRuns(ctx, recoveries)
}

func channelWorkRunRecoveryState(run channels.WorkRun, active bool, stored session.Session, found bool, terminalState channels.WorkRunRecoveryState) channels.WorkRunRecoveryState {
	if active {
		return channels.WorkRunRecoveryActive
	}
	if !found {
		return channels.WorkRunRecoveryMissing
	}
	// Named sessions are reusable, so only the terminal marker for the turn
	// admitted to this exact run can settle it. Older session completion cannot.
	if run.NamedAgentID != "" {
		if strings.TrimSpace(run.TurnID) == "" || terminalState == channels.WorkRunRecoveryActive {
			return channels.WorkRunRecoveryActive
		}
		return terminalState
	}
	if terminalState != channels.WorkRunRecoveryActive {
		return terminalState
	}
	// Hidden verifier/selector sessions are fresh per run, so their completion
	// marker belongs to this run and can be handed back to its owner even
	// when an older session has no explicit terminal history record.
	if strings.TrimSpace(stored.LatestCompletedTurnID) != "" {
		return channels.WorkRunRecoveryCompleted
	}
	return channels.WorkRunRecoveryActive
}

func channelWorkTurnTerminalState(sessionDir, sessionRef, turnID string) (channels.WorkRunRecoveryState, error) {
	records, err := session.LoadHistoryRecords(sessionDir, strings.TrimSpace(sessionRef), true)
	if err != nil {
		return channels.WorkRunRecoveryActive, err
	}
	turnID = strings.TrimSpace(turnID)
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if record.Role != "meta" || record.Content != turnTerminalHistoryRecord {
			continue
		}
		if turnID != "" && strings.TrimSpace(record.ClientID) != turnID {
			continue
		}
		switch TurnStatus(record.StopReason) {
		case TurnStatusCompleted:
			return channels.WorkRunRecoveryCompleted, nil
		case TurnStatusInterrupted:
			return channels.WorkRunRecoveryInterrupted, nil
		case TurnStatusFailed:
			return channels.WorkRunRecoveryFailed, nil
		default:
			// A terminal marker proves the turn ended. Treat an unknown legacy
			// status as failed so recovery never repeats its side effects.
			return channels.WorkRunRecoveryFailed, nil
		}
	}
	return channels.WorkRunRecoveryActive, nil
}
