package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// newSelectedCompactionClient captures this generation's host. The runner binds
// its admitted model transport on each invocation; no current-host lookup or
// native fallback is permitted after removal or a service error.
func newSelectedCompactionClient(host *pluginhost.Host) *compactionservice.Client {
	return compactionservice.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if host == nil {
			return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "selected compaction host is unavailable"}
		}
		ctx = compactionservice.WithCapabilityHost(ctx, host)
		result, err := host.CallService(ctx, sessionExecutionConsumer, compactionservice.ServiceName, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func compactionProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: compactionservice.NativeModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return compactionservice.NewService(), nil }}
}
