package toolctx

import (
	"context"
	"sync/atomic"
)

type provenanceKey struct{}
type provenanceTracker struct {
	parent *provenanceTracker
	level  atomic.Int32
}

// WithProvenance creates an invocation-owned tracker linked to its parent.
func WithProvenance(ctx context.Context) context.Context {
	parent, _ := ctx.Value(provenanceKey{}).(*provenanceTracker)
	return context.WithValue(ctx, provenanceKey{}, &provenanceTracker{parent: parent})
}

// MarkUnverified propagates a host-observed source through nested owners.
func MarkUnverified(ctx context.Context, source string) {
	value := int32(1)
	if source != "extension" {
		value = 2
	}
	tracker, _ := ctx.Value(provenanceKey{}).(*provenanceTracker)
	for ; tracker != nil; tracker = tracker.parent {
		for {
			old := tracker.level.Load()
			if old >= value || tracker.level.CompareAndSwap(old, value) {
				break
			}
		}
	}
}

func UnverifiedSource(ctx context.Context) string {
	tracker, _ := ctx.Value(provenanceKey{}).(*provenanceTracker)
	if tracker == nil {
		return ""
	}
	switch tracker.level.Load() {
	case 1:
		return "extension"
	case 2:
		return "tool_result_archive"
	default:
		return ""
	}
}
