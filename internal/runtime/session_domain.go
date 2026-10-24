package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Native migration belongs to the selected default domain's activation. An
// external domain never opens or imports the default provider's session data.
type nativeSessionDomain struct {
	*session.Service
	documents persistence.Documents
	directory string
}

func newNativeSessionDomain(registry *pluginhost.ServiceRegistry, directory string) *nativeSessionDomain {
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, session.DefaultModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return &nativeSessionDomain{Service: session.NewService(session.DefaultModule, documents), documents: documents, directory: directory}
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
