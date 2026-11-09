package loopdriver

import (
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ContextSegmentLifecycle describes how long a context segment lives.
type ContextSegmentLifecycle string

const (
	// ContextSegmentRequestOnly is visible to the model for the current
	// provider request but is never appended to live or durable history.
	ContextSegmentRequestOnly ContextSegmentLifecycle = "request_only"
)

// ContextSegmentPlacement describes where a segment is placed in the provider
// request. The first supported placement keeps dynamic context after the live
// conversation so it cannot become part of the stable prompt prefix.
type ContextSegmentPlacement string

const (
	ContextSegmentAfterHistory ContextSegmentPlacement = "after_history"
)

// ContextSegmentCachePolicy records the cache intent of a segment. The
// provider adapters still receive plain ChatMessages; this metadata exists so
// request assembly, telemetry, and future provider-specific cache handling have
// one owner instead of inferring intent from ChatMessage.Hidden.
type ContextSegmentCachePolicy string

const (
	ContextSegmentVolatile ContextSegmentCachePolicy = "volatile"
)

// ContextSegment is model-visible context assembled for a provider request.
// It makes the request lifecycle explicit instead of overloading
// providers.ChatMessage.Hidden with durability, UI visibility, and cache intent.
type ContextSegment struct {
	Messages    []providers.ChatMessage
	Blocks      []wuucontext.Block
	Lifecycle   ContextSegmentLifecycle
	Placement   ContextSegmentPlacement
	CachePolicy ContextSegmentCachePolicy
	Durable     bool
	VisibleInUI bool
}

func ContextSegmentsToWire(segments []ContextSegment) []pluginprotocol.DriverContextSegment {
	result := make([]pluginprotocol.DriverContextSegment, 0, len(segments))
	for _, segment := range segments {
		item := pluginprotocol.DriverContextSegment{
			Messages:  modelwire.DriverMessagesToWire(segment.Messages),
			Lifecycle: string(segment.Lifecycle), Placement: string(segment.Placement), CachePolicy: string(segment.CachePolicy),
			Durable: segment.Durable, VisibleInUI: segment.VisibleInUI,
		}
		for _, block := range segment.Blocks {
			item.Blocks = append(item.Blocks, pluginprotocol.DriverContextBlock{
				Kind: string(block.Kind), Title: block.Title, Source: block.Source, Content: block.Content, TokenBudget: block.TokenBudget,
			})
		}
		result = append(result, item)
	}
	return result
}

func ContextSegmentsFromWire(segments []pluginprotocol.DriverContextSegment) []ContextSegment {
	result := make([]ContextSegment, 0, len(segments))
	for _, segment := range segments {
		item := ContextSegment{
			Messages:  modelwire.DriverMessagesFromWire(segment.Messages),
			Lifecycle: ContextSegmentLifecycle(segment.Lifecycle), Placement: ContextSegmentPlacement(segment.Placement), CachePolicy: ContextSegmentCachePolicy(segment.CachePolicy),
			Durable: segment.Durable, VisibleInUI: segment.VisibleInUI,
		}
		for _, block := range segment.Blocks {
			item.Blocks = append(item.Blocks, wuucontext.Block{
				Kind: wuucontext.BlockKind(block.Kind), Title: block.Title, Source: block.Source, Content: block.Content, TokenBudget: block.TokenBudget,
			})
		}
		result = append(result, item)
	}
	return result
}
