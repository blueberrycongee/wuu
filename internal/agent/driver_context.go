package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (g *streamTurnGateway) ReadContext(ctx context.Context) (loopdriver.TurnContext, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.TurnContext{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.TurnContext{}, err
	}
	return g.contextSnapshot(), nil
}

func (g *streamTurnGateway) ArchiveContext(ctx context.Context) (loopdriver.ContextArchive, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.ContextArchive{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.ContextArchive{}, err
	}
	if g.runner.ArchiveHistory == nil {
		return loopdriver.ContextArchive{}, errors.New("history archive is unavailable")
	}
	if len(g.streamedBatches) != 0 {
		return loopdriver.ContextArchive{}, errors.New("join the pending streaming tool batch before archiving context")
	}
	if err := g.archivePending(ctx); err != nil {
		return loopdriver.ContextArchive{}, err
	}
	return loopdriver.ContextArchive{Messages: providers.CloneChatMessages(g.produced), Head: g.archiveHead}, nil
}

func (g *streamTurnGateway) ReplaceContext(ctx context.Context, replacement loopdriver.ContextReplacement) (loopdriver.TurnContext, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.TurnContext{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.TurnContext{}, err
	}
	if len(g.streamedBatches) != 0 {
		return loopdriver.TurnContext{}, errors.New("join the pending streaming tool batch before replacing context")
	}
	if replacement.Volatile {
		if g.runner.CommitFreshContext != nil {
			return loopdriver.TurnContext{}, errors.New("use durable replacement when a context checkpoint provider is configured")
		}
	} else if g.runner.ArchiveHistory == nil || g.runner.CommitFreshContext == nil {
		return loopdriver.TurnContext{}, errors.New("durable context replacement is unavailable")
	}
	if replacement.Revision != g.revision {
		return loopdriver.TurnContext{}, fmt.Errorf("context revision %d is stale; current revision is %d", replacement.Revision, g.revision)
	}
	messages := providers.CloneChatMessages(replacement.Messages)
	if err := validateDriverMessages(messages); err != nil {
		return loopdriver.TurnContext{}, err
	}
	if err := providers.ValidateToolCallHistory(messages); err != nil {
		return loopdriver.TurnContext{}, err
	}
	if g.runner.ArchiveHistory != nil {
		if err := g.archivePending(ctx); err != nil {
			return loopdriver.TurnContext{}, err
		}
	}
	// Recover addresses for retained messages before checkpoint insertion, so
	// the storage provider can distinguish them from newly authored summaries.
	seqs := make([]int, len(g.produced))
	for i, original := range g.produced {
		seqs[i] = original.Seq
	}
	messages = applyArchivedHistorySeqs(messages, g.produced, seqs)
	key := "driver-context"
	if g.policy != nil {
		key = g.policy.Key
	} else if provider := g.runner.contextWindowProvider(); provider != nil {
		key = provider.CompactionKey()
	}
	committed, head := messages, g.archiveHead
	if !replacement.Volatile {
		committed, head, err = g.runner.CommitFreshContext(ctx, messages, g.archiveHead, key, CompactionNote{})
		if err != nil {
			return loopdriver.TurnContext{}, err
		}
	}
	// Report exactly the accepted context, including a durable commit that
	// succeeded concurrently with cancellation.
	if hashMessagesForRequestShape(replacement.Messages) == g.proposedContextHash {
		g.nextModelParentID = g.proposedContextParentID
	}
	g.proposedContextParentID, g.proposedContextHash = "", ""
	g.activeHistory = providers.CloneChatMessages(committed)
	g.archiveHead = head
	g.historyRewritten = true
	g.revision++
	g.contextTokens = estimateMessages(committed)
	g.postToolContext = nil
	return g.contextSnapshot(), nil
}

func (g *streamTurnGateway) contextSnapshot() loopdriver.TurnContext {
	return loopdriver.TurnContext{
		Messages: providers.CloneChatMessages(g.activeHistory), Revision: g.revision,
		ArchiveHead: g.archiveHead, CanReplace: g.runner.ArchiveHistory != nil && g.runner.CommitFreshContext != nil,
	}
}

func (g *streamTurnGateway) archivePending(ctx context.Context) error {
	archived, err := g.runner.ArchiveHistory(ctx, providers.CloneChatMessages(g.durable))
	if err != nil {
		return err
	}
	if g.runner.ToolLedger != nil {
		var projected []string
		for _, message := range g.durable {
			if message.ToolInvocationID != "" && !g.pendingObservationAdmission[message.ToolInvocationID] {
				projected = append(projected, message.ToolInvocationID)
			}
		}
		// The history write is already confirmed. A failed cleanup retains the
		// bounded binding until a later acknowledgement or expiry.
		_ = g.runner.ToolLedger.ForgetProjectedObservationBindings(ctx, projected)
	}
	g.archiveHead = max(g.archiveHead, archived.HeadSeq)
	g.produced = applyArchivedHistorySeqs(g.produced, g.durable, archived.Seqs)
	g.activeHistory = applyArchivedHistorySeqs(g.activeHistory, g.durable, archived.Seqs)
	g.durable = nil
	g.revision++
	return nil
}

func validateDriverMessages(messages []providers.ChatMessage) error {
	for i, message := range messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			return fmt.Errorf("message %d: unsupported role %q", i, message.Role)
		}
		if message.Role != "assistant" && len(message.ToolCalls) != 0 {
			return fmt.Errorf("message %d: only assistant messages can declare tool calls", i)
		}
		if message.Role != "tool" && (message.ToolCallID != "" || message.ToolInvocationID != "" || message.ToolResult != nil) {
			return fmt.Errorf("message %d: tool results require the tool role", i)
		}
	}
	return nil
}
