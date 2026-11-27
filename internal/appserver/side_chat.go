package appserver

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func sideChatParent(metadata session.Session) string {
	if metadata.Placement == session.PlacementSide {
		return metadata.ParentID
	}
	return ""
}

func ownThreadHistory(history []providers.ChatMessage, inheritedThrough int) []providers.ChatMessage {
	if inheritedThrough == 0 {
		return history
	}
	first := slices.IndexFunc(history, func(message providers.ChatMessage) bool {
		// A prompt admitted on a nonpersistent presentation can lack a seq.
		return message.Seq > inheritedThrough || message.Seq == 0 && isThreadTitleUserMessage(message)
	})
	if first < 0 {
		return nil
	}
	return history[first:]
}

func latestCompletedSideChatHistory(source forkSourceThread, sourceIdle bool) ([]providers.ChatMessage, []forkDisplayRecord, string, int, error) {
	raw := source.rawHistory
	projection := projectPersistedHistory(source.thread.ID, raw, source.thread.UpdatedAt, nil)
	for i := len(projection.Turns) - 1; i >= 0; i-- {
		turn := projection.Turns[i]
		if turn.Status != TurnStatusCompleted {
			continue
		}
		if live, ok := turnByID(source.thread.Turns, turn.ID); ok && live.Status != TurnStatusCompleted {
			continue
		}
		// Legacy transcripts lack a settlement record. Their final visible
		// answer is the best available boundary, provided its tool batch closed.
		if !slices.ContainsFunc(turn.Items, func(item ThreadItem) bool { return item.Type == ThreadItemAgentMessage && item.Terminal }) {
			continue
		}
		span := projection.TurnSpans[turn.ID]
		// A peer can have durably streamed an answer without settling the
		// running turn. Require its durable terminal record before branching
		// the last turn while that connection owns execution.
		if !sourceIdle && i == len(projection.Turns)-1 && !slices.ContainsFunc(raw[span.EndIndex+1:], func(record persistedMessage) bool {
			return record.Role == "meta" && record.Content == turnTerminalHistoryRecord &&
				(record.ClientID == "" || record.ClientID == turn.ID) && record.StopReason == string(TurnStatusCompleted)
		}) {
			continue
		}
		prefix := raw[:span.EndIndex+1]
		history, err := providers.RepairAndValidateToolCallHistory(cloneForkHistory(chatMessagesFromPersistedMessages(prefix)))
		if err != nil {
			return nil, nil, "", 0, err
		}
		return history, forkDisplayRecords(prefix, history), turn.ID, prefix[len(prefix)-1].Seq, nil
	}
	return nil, nil, "", 0, nil
}

func sideChatContextSeed(parentID string, through int, history []providers.ChatMessage) session.ContextSeed {
	const budget = 18 * 1024
	var lines []string
	used := 0
	for i := len(history) - 1; i >= 0 && used < budget; i-- {
		message := history[i]
		if message.Hidden || (message.Role != "user" && message.Role != "assistant") {
			continue
		}
		content := strings.TrimSpace(chatMessageDisplayContent(message))
		if content == "" && len(message.Images)+len(message.Files) > 0 {
			content = fmt.Sprintf("[%d image attachments, %d file attachments]", len(message.Images), len(message.Files))
		}
		if content == "" {
			continue
		}
		line := message.Role + ": " + content + "\n"
		if len(line) > budget-used {
			line = strings.ToValidUTF8(line[:budget-used], "")
		}
		lines = append(lines, line)
		used += len(line)
	}
	slices.Reverse(lines)
	return session.ContextSeed{
		Version:    session.ContextSeedVersionV1,
		Body:       strings.Join(lines, ""),
		Source:     session.HistorySnapshot{SessionID: parentID, ThroughSeq: through},
		Provenance: session.ContextSeedProvenance{Producer: "thread/fork"},
	}
}

func (s *Server) listSideChats(binding *runtime.State, req Request, params ThreadListParams) error {
	parentID := strings.TrimSpace(params.ParentThreadID)
	if parentID == "" || params.Placement != session.PlacementSide {
		return s.writeResponse(req.ID, nil, errors.New("parent_thread_id and placement side are required"))
	}
	if _, err := s.requireRequestAccess(req, MethodThreadList, nil, access.Resource{Kind: "session", ID: parentID}); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	rows, err := binding.Sessions.List(0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	rows = slices.DeleteFunc(rows, func(row session.Session) bool {
		return row.Placement != session.PlacementSide || row.ParentID != parentID
	})
	s.refreshListedSessionMetadata(binding, rows)
	slices.SortStableFunc(rows, func(a, b session.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	build, cancel := s.threadEntryBuilder(binding)
	defer cancel()
	threads := make([]Thread, 0, len(rows))
	for _, row := range rows {
		thread := build(row).thread
		if live := s.threadForState(binding, row.ID); live != nil {
			live.mu.Lock()
			snapshot := live.listSnapshotLocked(params.SummaryOnly)
			live.mu.Unlock()
			thread.Turns = snapshot.Turns
			thread.Status = snapshot.Status
		}
		threads = append(threads, thread)
	}
	return s.writeResponse(req.ID, ThreadListResult{Threads: threads}, nil)
}

func (s *Server) handleThreadPromote(req Request) error {
	var params struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	id := strings.TrimSpace(params.ThreadID)
	metadata, err := binding.Sessions.PromoteSideChat(id)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	build, cancel := s.threadEntryBuilder(binding)
	defer cancel()
	thread := build(metadata).thread
	if live := s.threadForState(binding, id); live != nil {
		live.mu.Lock()
		live.SideParentID = ""
		thread = live.listSnapshotLocked(true)
		live.mu.Unlock()
	}
	if err := s.writeResponse(req.ID, ThreadStartedNotification{Thread: thread}, nil); err != nil {
		return err
	}
	return s.notifyThreadStarted(binding, thread)
}
