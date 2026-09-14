package appserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/channels"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const (
	namedAgentInboxContextSource = "runtime.named_agent_inbox"
	namedAgentInboxInterval      = 3 * time.Second
)

// Sampling is driven only by normal provider requests. No timer starts a turn,
// injects a user message, interrupts a tool, or reads a message body.
func attachNamedAgentInboxContext(threadRuntime *runtime.ThreadRuntime, client *channels.AgentClient) {
	if threadRuntime == nil || threadRuntime.StreamRunner == nil || client == nil || client.SessionRef() == "" {
		return
	}
	base := threadRuntime.StreamRunner.BeforeRequestContext
	inbox := &namedAgentInboxContext{peek: client.PeekInbox, now: time.Now}
	baseStep := threadRuntime.StreamRunner.BeforeModelStep
	threadRuntime.StreamRunner.BeforeModelStep = func(ctx context.Context, step int, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
		if step == 0 {
			// The continuing identity may now serve a different room. Do not
			// carry a sampled count into that room while the interval expires.
			inbox.mu.Lock()
			inbox.nextCheck = time.Time{}
			inbox.blocks = nil
			inbox.mu.Unlock()
		}
		if baseStep != nil {
			return baseStep(ctx, step, history)
		}
		return nil, nil
	}
	threadRuntime.StreamRunner.BeforeRequestContext = func() []agent.ContextSegment {
		var segments []agent.ContextSegment
		if base != nil {
			segments = append(segments, base()...)
		}
		return append(segments, inbox.segments()...)
	}
}

type namedAgentInboxContext struct {
	mu        sync.Mutex
	peek      func(context.Context) (channels.InboxSummary, error)
	now       func() time.Time
	nextCheck time.Time
	blocks    []wuucontext.Block
}

func (inbox *namedAgentInboxContext) segments() []agent.ContextSegment {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	now := inbox.now()
	if !now.Before(inbox.nextCheck) {
		inbox.nextCheck = now.Add(namedAgentInboxInterval)
		summary, err := inbox.peek(context.Background())
		if err != nil {
			providers.DebugLogf("read named agent inbox reminder: %v", err)
		} else {
			inbox.blocks = nil
			if summary.Unread > 0 {
				inbox.blocks = []wuucontext.Block{{
					Kind: wuucontext.BlockAdditionalContext, Title: "Unread inbox", Source: namedAgentInboxContextSource,
					Content: fmt.Sprintf("Your inbox has %d unread message(s) in room %s. Use chat_check when ready; recently pulled messages may still appear until the next refresh. Inbox snapshot: %d/%d.", summary.Unread, summary.RoomID, summary.DeliverySequence, summary.ItemSequence),
				}}
			}
		}
	}
	// Re-emit the last snapshot while throttled. The typed context gate drops
	// identical snapshots and appends changes at the tail, retaining earlier
	// versions in place for prefix-cache reuse (including across turns).
	return agent.RequestOnlyContextBlocks(inbox.blocks)
}
