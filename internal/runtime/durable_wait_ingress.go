package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/durablewait"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const DurableIngressConsumer = "wuu.durable-client"
const DurableTasksService = "durable.tasks"

// NewDurableIngressConsumer selects the authenticated remote view capability.
// It does not start a driver or enable durable behavior in default compositions.
func NewDurableIngressConsumer() pluginhost.Client {
	return &runtimeServiceConsumer{id: DurableIngressConsumer, state: pluginhost.StatePrepared,
		requirements: []pluginhost.ServiceRequirement{
			{Name: DurableTasksService, MajorVersion: 1},
			{Name: durablewait.ServiceName, MajorVersion: 1},
		}}
}

func durableIngressFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: DurableIngressConsumer, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return NewDurableIngressConsumer(), nil
	}}
}

type DurableTaskQuery struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

type DurableWaitQuery struct {
	SessionID string `json:"session_id"`
	WaitID    string `json:"wait_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
}

// ReadDurableTasks calls only the selected task read contract. authorize must
// recheck the session resource policy after acquiring the provider scope.
func (s *State) ReadDurableTasks(ctx context.Context, method string, query DurableTaskQuery, authorize func(context.Context) error) (json.RawMessage, error) {
	if strings.TrimSpace(query.SessionID) == "" || (method != "list" && method != "get") || (method == "list" && query.TaskID != "") || (method == "get" && strings.TrimSpace(query.TaskID) == "") {
		return nil, errors.New("invalid durable task read")
	}
	return s.callDurableIngress(ctx, DurableTasksService, method, "durable-tasks."+method, query, authorize)
}

// CallDurableWaits is a host-only authenticated ingress, not a service proxy.
// Decisions never come from plugin JSON and do not confer execution permission.
func (s *State) CallDurableWaits(ctx context.Context, method string, query DurableWaitQuery, authorize func(context.Context) error) (json.RawMessage, error) {
	if strings.TrimSpace(query.SessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	switch method {
	case "list":
		if query.WaitID != "" || query.Decision != "" {
			return nil, errors.New("invalid durable wait list")
		}
	case "decide":
		if strings.TrimSpace(query.WaitID) == "" || (query.Decision != "approve" && query.Decision != "deny") {
			return nil, errors.New("invalid durable wait decision")
		}
		caller, ok := principal.FromContext(ctx)
		if !ok || !caller.Valid() || (caller.Identity().Kind != "user" && caller.Identity().Kind != "device") || access.DelegationFromContext(ctx) != nil {
			return nil, &access.Denied{Action: "durable/waits/decide", Reason: "authenticated-human-decision-required"}
		}
	default:
		return nil, errors.New("unsupported durable wait operation")
	}
	return s.callDurableIngress(ctx, durablewait.ServiceName, method, "durable-wait."+method, query, authorize)
}

func (s *State) callDurableIngress(ctx context.Context, service, method, action string, query any, authorize func(context.Context) error) (json.RawMessage, error) {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.Valid() || authorize == nil {
		return nil, &access.Denied{Action: action, Reason: "authenticated-resource-admission-required"}
	}
	if s == nil || s.Host == nil || !selectsRuntimeModule(s.Host, DurableIngressConsumer) || s.Host.ServiceRegistry() == nil {
		return nil, errors.New("durable remote ingress is not selected")
	}
	if _, ok := s.Host.ServiceRegistry().ProviderPluginIDFor(DurableIngressConsumer, service, 1); !ok {
		return nil, errors.New("selected durable provider is unavailable")
	}
	raw, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	scope, err := s.Host.OpenServiceScope(ctx, DurableIngressConsumer, service)
	if err != nil {
		return nil, err
	}
	defer scope.Close()
	ctx = scope.Context
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	if err := caller.Check(ctx); err != nil {
		return nil, err
	}
	ctx = pluginhost.WithServiceAdmission(ctx, DurableIngressConsumer, service, method, pluginhost.ServiceAdmission{
		Version: 1, Principal: caller.Identity(), Authority: caller.DurableAuthority(), Action: action,
		InputDigest: executionauth.RawRequestDigest(raw),
	})
	result, callErr := s.Host.CallService(ctx, DurableIngressConsumer, service, method, raw)
	if callErr != nil {
		return nil, callErr
	}
	return result, nil
}
