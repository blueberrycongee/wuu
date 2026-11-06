package loopdriver

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type modelRequestIDKey struct{}

// WithModelRequestID carries correlation only, never replay authorization.
func WithModelRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, modelRequestIDKey{}, id)
}
func ModelRequestID(ctx context.Context) string {
	id, _ := ctx.Value(modelRequestIDKey{}).(string)
	return id
}

type ModelReceiptReader interface {
	ReadModelReceipts(context.Context, string) (pluginprotocol.DriverModelReceipts, error)
}
