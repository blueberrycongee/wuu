package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const processCompletionOutputBytes = 2 * 1024

func (s *Server) forwardProcessNotifications(binding *runtime.State, threadID string, control *agentcontrol.AgentControl, manager *process.Manager, ch <-chan process.Event, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if manager != nil {
				// The channel is only a low-latency hint. Pull every persisted
				// obligation so a terminal event dropped behind a full channel is
				// recovered when any retained event is consumed.
				s.replayPendingProcessCompletions(binding, threadID, control, manager)
				continue
			}
			if event.Cause != process.EventCauseNaturalExit || !processEventBelongsToThread(threadID, control, event) {
				continue
			}
			s.enqueueProcessCompletionTurn(binding, threadID, event.Process.ID, processCompletionChatMessage(manager, event))
		}
	}
}

func processEventBelongsToThread(threadID string, control *agentcontrol.AgentControl, event process.Event) bool {
	ownerID := strings.TrimSpace(event.Process.OwnerID)
	switch event.Process.OwnerKind {
	case process.OwnerMainAgent:
		return ownerID != "" && ownerID == strings.TrimSpace(threadID)
	case process.OwnerSubagent:
		if ownerID == "" || control == nil {
			return false
		}
		for _, snapshot := range control.List() {
			if strings.TrimSpace(snapshot.ID) == ownerID {
				return true
			}
		}
	}
	return false
}

// Notifications reach the model as plain text inside their tags, like other
// command results: what happened, the output tail, and how to read the rest.
func processCompletionChatMessage(manager *process.Manager, event process.Event) providers.ChatMessage {
	lines := []string{fmt.Sprintf("Background process %s exited with code %d: %s", event.Process.ID, event.Process.ExitCode, tools.RedactToolOutput(event.Process.Command))}
	lines = append(lines, processOutputTailLines(manager, event.Process.ID)...)
	lines = append(lines, "This is the final result; continue from it without polling the process again.")
	return providers.ChatMessage{
		Role:     "user",
		Name:     wuucontext.ProcessNotificationMessageName,
		ClientID: processCompletionClientID([]string{event.Process.ID}),
		Content:  "<process_notification>\n" + strings.Join(lines, "\n") + "\n</process_notification>",
	}
}

// processOutputTailLines returns the last processCompletionOutputBytes of a
// process log and, when earlier output exists, how to page it.
func processOutputTailLines(manager *process.Manager, id string) []string {
	if manager == nil {
		return nil
	}
	snapshot, err := manager.ReadOutputSnapshot(context.Background(), id, process.OutputReadOptions{MaxBytes: processCompletionOutputBytes})
	if err != nil {
		return nil
	}
	output := strings.TrimRight(tools.StripTerminalControls(tools.RedactToolOutput(snapshot.Output)), " \t\n")
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	lines := []string{output}
	if snapshot.StartOffset > 0 {
		lines = append(lines, fmt.Sprintf("[last %d of %d output bytes; read earlier output with bash action=read process_id=%s offset_bytes=0]", snapshot.EndOffset-snapshot.StartOffset, snapshot.TotalBytes, id))
	}
	return lines
}

func (s *Server) replayPendingProcessCompletions(binding *runtime.State, threadID string, control *agentcontrol.AgentControl, manager *process.Manager) {
	if s == nil || manager == nil {
		return
	}
	pending, err := manager.PendingCompletions()
	if err != nil {
		providers.DebugLogf("restore pending process completions for thread %q: %v", threadID, err)
		return
	}
	for _, p := range pending {
		event := process.Event{Type: process.EventStopped, Cause: process.EventCauseNaturalExit, Process: p}
		if p.Status == process.StatusFailed {
			event.Type = process.EventFailed
		}
		if processEventBelongsToThread(threadID, control, event) {
			s.enqueueProcessCompletionTurn(binding, threadID, p.ID, processCompletionChatMessage(manager, event))
		}
	}
}

func threadHasOutstandingProcessCompletion(th *threadState, control *agentcontrol.AgentControl, manager *process.Manager) bool {
	if th == nil || manager == nil {
		return false
	}
	threadID := th.ID
	processes, err := manager.List()
	if err != nil {
		providers.DebugLogf("inspect outstanding process completions for thread %q: %v", threadID, err)
		return false
	}
	for _, p := range processes {
		event := process.Event{Process: p}
		if !processEventBelongsToThread(threadID, control, event) {
			continue
		}
		if p.CompletionMode == process.CompletionModeDetached {
			continue
		}
		switch p.Status {
		case process.StatusStarting, process.StatusRunning, process.StatusStopping:
			return true
		case process.StatusStopped, process.StatusFailed:
			if p.TerminalCause == process.EventCauseNaturalExit && p.CompletionDeliveredAt.IsZero() && !th.completionDeliveryPaused(nil, []string{p.ID}) {
				return true
			}
		}
	}
	return false
}

func (s *Server) restorePendingProcessCompletionsOnThreadResume(binding *runtime.State, threadID string) {
	if s == nil || s.rt == nil || s.rt.ProcessManager == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	manager := s.processManagerForThread(threadID)
	if manager == nil {
		return
	}
	pending, err := manager.PendingCompletions()
	if err != nil {
		providers.DebugLogf("inspect pending process completions while resuming thread %q: %v", threadID, err)
		return
	}
	needsRuntime := false
	for _, p := range pending {
		if (p.OwnerKind == process.OwnerMainAgent && strings.TrimSpace(p.OwnerID) == threadID) || p.OwnerKind == process.OwnerSubagent {
			needsRuntime = true
			break
		}
	}
	if !needsRuntime {
		return
	}
	th := s.threadForState(binding, threadID)
	if th == nil || !canResumeAgentCompletionThread(th) {
		return
	}
	if _, err := s.ensureThreadRuntime(th); err != nil {
		providers.DebugLogf("restore pending process completions for resumed thread %q: %v", threadID, err)
	}
}

const (
	processCompletionClientIDPrefix       = "wuu-process-completion:"
	processCompletionAnswerClientIDPrefix = "wuu-process-completion-answer:"
)

func processCompletionClientID(processIDs []string) string {
	ids := uniqueSortedCompletionIDs(processIDs)
	if len(ids) == 0 {
		return ""
	}
	return processCompletionClientIDPrefix + strings.Join(ids, ",")
}

func processCompletionIDs(clientID string) []string {
	clientID = strings.TrimSpace(clientID)
	if !strings.HasPrefix(clientID, processCompletionClientIDPrefix) {
		return nil
	}
	return splitAgentCompletionResultIDs(strings.TrimPrefix(clientID, processCompletionClientIDPrefix))
}

func processCompletionAnswerIDs(clientID string) []string {
	return completionReceipts(clientID).Processes
}

func uniqueSortedCompletionIDs(ids []string) []string {
	clean := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	sort.Strings(clean)
	return clean
}

func markProcessCompletionAnswer(res *agent.LoopResult, processIDs []string) bool {
	return markCompletionAnswer(res, completionAnswerReceipts{Processes: processIDs})
}

func processCompletionMarkerAnswered(history []providers.ChatMessage, processID string) bool {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return false
	}
	markerIndex := -1
	for i, msg := range history {
		for _, id := range processCompletionIDs(msg.ClientID) {
			if id == processID {
				markerIndex = i
				break
			}
		}
	}
	if markerIndex < 0 {
		return false
	}
	for _, msg := range history[markerIndex+1:] {
		for _, id := range processCompletionAnswerIDs(msg.ClientID) {
			if id == processID {
				return true
			}
		}
	}
	return false
}

func (s *Server) enqueueProcessCompletionTurn(binding *runtime.State, threadID, processID string, msg providers.ChatMessage) {
	if s == nil || s.closed.Load() {
		return
	}
	threadID = strings.TrimSpace(threadID)
	processID = strings.TrimSpace(processID)
	if threadID == "" || processID == "" || !chatMessageHasUserPayload(msg) {
		return
	}
	th := s.threadForState(binding, threadID)
	if th == nil || !canResumeAgentCompletionThread(th) {
		return
	}

	s.agentCompletionMu.Lock()
	if s.closed.Load() {
		s.agentCompletionMu.Unlock()
		return
	}
	pending := s.pendingAgentCompletionTurns[threadID]
	kept := pending[:0]
	for _, existing := range pending {
		if existing.processID == processID {
			s.agentCompletionMu.Unlock()
			return
		}
		kept = append(kept, existing)
	}
	s.pendingAgentCompletionTurns[threadID] = append(kept, agentCompletionTurn{
		processID: processID,
		msg:       msg,
	})
	s.agentCompletionMu.Unlock()

	s.kickAgentCompletionDrain(binding, threadID)
}
