package externalengine

import (
	"context"
	"encoding/json"
	"time"
)

// GrokBilling reads the Grok CLI's account allowance through its native RPC.
// It neither creates a conversation nor starts an authentication flow.
func (e *Engine) GrokBilling(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	p, err := startChild(e.binary, e.entry.Args, e.root, nil)
	if err != nil {
		return nil, err
	}
	defer p.close()
	r := &rpc{child: p, handle: func(_ context.Context, _ string, _ json.RawMessage, request bool) (any, error) {
		if request {
			return nil, &rpcError{Code: -32601, Message: "unsupported billing request"}
		}
		return nil, nil
	}}
	if _, err := initializeACP(ctx, r); err != nil {
		return nil, err
	}
	var result json.RawMessage
	err = r.call(ctx, "x.ai/billing", struct{}{}, &result)
	return result, err
}
