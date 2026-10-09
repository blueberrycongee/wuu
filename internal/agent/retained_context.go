package agent

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// RetainedContextMessage is one request-only context message retained in the
// provider transcript, keyed to the durable-history position it follows.
type RetainedContextMessage struct {
	// AfterDurable is the number of durable (live-history) messages that
	// precede this message in the provider transcript.
	AfterDurable int
	Message      providers.ChatMessage
}

// RetainedRequestContextState carries the provider-transcript request-only
// context across runs so the next run's first request can byte-extend this
// run's last request instead of diverging at the first context injection
// point of the previous turn. A compact checkpoint also preserves it
// across process restarts. It remains best-effort: dropping it costs a prompt
// cache miss, never correctness. The durable fingerprint invalidates positions
// after a history rewrite or external edit.
type RetainedRequestContextState struct {
	Messages []RetainedContextMessage
	// DurableLen and DurableHash fingerprint the filtered durable history
	// this state was derived from. The next run must see the exact same
	// messages as its history prefix for the retained positions to be valid.
	DurableLen  int
	DurableHash string
}

// validFor reports whether the state can be spliced onto durable, i.e. the
// durable history still starts with the exact messages the state was built
// against and every retained position lands inside that prefix.
func (s *RetainedRequestContextState) validFor(durable []providers.ChatMessage) bool {
	if s == nil || len(s.Messages) == 0 {
		return false
	}
	if s.DurableLen < 0 || s.DurableLen > len(durable) {
		return false
	}
	prev := -1
	for _, retained := range s.Messages {
		if retained.AfterDurable < prev || retained.AfterDurable > s.DurableLen {
			return false
		}
		prev = retained.AfterDurable
	}
	return hashMessagesForRequestShape(durable[:s.DurableLen]) == s.DurableHash
}

// spliceRetainedContext rebuilds the provider transcript from durable history
// plus retained request-only context at its recorded positions. Retained
// entries must be ordered by AfterDurable (validFor enforces this).
func spliceRetainedContext(durable []providers.ChatMessage, retained []RetainedContextMessage) []providers.ChatMessage {
	out := make([]providers.ChatMessage, 0, len(durable)+len(retained))
	next := 0
	for i := 0; i <= len(durable); i++ {
		for next < len(retained) && retained[next].AfterDurable == i {
			out = append(out, providers.CloneChatMessage(retained[next].Message))
			next++
		}
		if i < len(durable) {
			out = append(out, providers.CloneChatMessage(durable[i]))
		}
	}
	return out
}

// buildRetainedRequestContextState snapshots the run's retained request-only
// context against the durable history the run ended with. Returns nil when
// nothing was retained (no state to carry).
func buildRetainedRequestContextState(retained []RetainedContextMessage, durable []providers.ChatMessage) *RetainedRequestContextState {
	if len(retained) == 0 {
		return nil
	}
	return &RetainedRequestContextState{
		Messages:    append([]RetainedContextMessage(nil), retained...),
		DurableLen:  len(durable),
		DurableHash: hashMessagesForRequestShape(durable),
	}
}

// RequestContextCheckpoint stores only the request-only insertions and their
// durable-history fingerprint, never another copy of the conversation.
type RequestContextCheckpoint struct {
	Provider      string                       `json:"provider"`
	Model         string                       `json:"model"`
	DriverID      string                       `json:"driver_id"`
	DriverVersion string                       `json:"driver_version"`
	State         *RetainedRequestContextState `json:"state"`
}

type RequestContextStore interface {
	Load(context.Context) (RequestContextCheckpoint, bool, error)
	Save(context.Context, RequestContextCheckpoint) error
}
