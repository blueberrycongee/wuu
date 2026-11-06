package loopdriver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

// CompactionGateway allows a proposal to run beside model inference and input
// admission. Start commits intent before dispatch; Read returns a reference to
// a committed result; Apply alone may replace context at a safe boundary.
type CompactionGateway interface {
	StartCompaction(context.Context, pluginprotocol.DriverCompactionParams) (pluginprotocol.DriverCompactionResult, error)
	ReadCompaction(context.Context, string, bool) (pluginprotocol.DriverCompactionResult, error)
	ApplyCompaction(context.Context, string) (pluginprotocol.DriverCompactionResult, error)
	CancelCompaction(context.Context, string) (pluginprotocol.DriverCompactionResult, error)
}
