package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Native migration belongs to the selected default domain's activation. An
// external domain never opens or imports the default provider's session data.
type nativeSessionDomain struct {
	*session.Service
	documents *persistence.Client
	directory string
	registry  *pluginhost.ServiceRegistry
}

func newNativeSessionDomain(registry *pluginhost.ServiceRegistry, directory string, host *pluginhost.Host) *nativeSessionDomain {
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, session.DefaultModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	background := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := host.CallService(ctx, session.DefaultModule, persistence.ServiceName, method, params)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return &nativeSessionDomain{Service: session.NewServiceWithJournalBackgroundDocuments(session.DefaultModule, documents, background), documents: documents, directory: directory, registry: registry}
}

// The native domain's leases coordinate OS processes on one host. Database CAS
// can safely serve multiple storage clients, but does not fence tool execution.
func (s *nativeSessionDomain) Prepare(ctx context.Context) error {
	descriptor, found := s.registry.ServiceDescriptorFor(s.ID(), persistence.ServiceName, 1)
	if found {
		for _, method := range descriptor.Methods {
			if method.Name != "authority" {
				continue
			}
			contract, err := s.documents.Authority(ctx)
			if err != nil {
				return err
			}
			if contract.ExecutionTopology != persistence.SingleHost {
				return fmt.Errorf("native session domain requires one execution host; select an independent distributed execution coordinator/domain before enabling multi-host execution")
			}
		}
	}
	return s.Service.Prepare(ctx)
}

func (s *nativeSessionDomain) Activate(ctx context.Context) error {
	if err := s.Service.Activate(ctx); err != nil {
		return err
	}
	client := session.NewClient(ctx, func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return s.Service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Caller: session.DefaultModule, Service: session.ServiceName, Method: method, Params: params})
	})
	if err := initializeSessionState(ctx, s.directory, s.documents, client); err != nil {
		_ = s.Service.Close(ctx)
		return err
	}
	return nil
}
