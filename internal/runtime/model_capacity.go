package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type modelCapacityAdmission struct {
	scope  *pluginhost.ServiceScope
	host   *pluginhost.Host
	leases []capacityAdmissionLease
	once   sync.Once
	err    error
}

type capacityAdmissionLease struct{ policy, physical string }

func callModelCapacity(ctx context.Context, host *pluginhost.Host, service, method string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, callErr := host.CallService(ctx, modelExecutionConsumer, service, method, raw)
	if callErr != nil {
		return callErr
	}
	if output != nil {
		return json.Unmarshal(result, output)
	}
	return nil
}
func acquireModelCapacity(ctx context.Context, host *pluginhost.Host, scope string, attempt providers.InferenceAttempt, requiredScopes ...string) (*modelCapacityAdmission, error) {
	scopes := map[string]bool{}
	if scope != "" {
		scopes[scope] = true
	}
	for _, required := range requiredScopes {
		if required != "" {
			scopes[required] = true
		}
	}
	if len(scopes) == 0 {
		return nil, errors.New("physical model capacity scope is required")
	}
	ordered := make([]string, 0, len(scopes))
	for item := range scopes {
		ordered = append(ordered, item)
	}
	sort.Strings(ordered)
	admission := &modelCapacityAdmission{host: host, scope: pluginhost.ServiceScopeFromContext(ctx)}
	pluginhost.DeferServiceCompletion(ctx, func() error { return admission.err })
	for _, item := range ordered {
		request := pluginprotocol.ModelCapacityAcquire{Scope: item, Profile: string(attempt.Operation().WorkloadProfile)}
		var policy, physical pluginprotocol.ModelCapacityLease
		if err := callModelCapacity(ctx, host, capacityPolicyService, "acquire", request, &policy); err != nil {
			return nil, errors.Join(err, admission.settle("abandoned", nil))
		}
		admission.leases = append(admission.leases, capacityAdmissionLease{policy: policy.Lease})
		if policy.Lease == "" {
			return nil, errors.Join(errors.New("capacity policy returned an empty lease"), admission.settle("abandoned", nil))
		}
		if err := callModelCapacity(ctx, host, capacityResourceService, "acquire", request, &physical); err != nil {
			return nil, errors.Join(err, admission.settle("abandoned", nil))
		}
		admission.leases[len(admission.leases)-1].physical = physical.Lease
		if physical.Lease == "" {
			return nil, errors.Join(errors.New("physical capacity returned an empty lease"), admission.settle("abandoned", nil))
		}
	}
	return admission, nil
}
func (a *modelCapacityAdmission) settle(outcome string, failure *pluginprotocol.ModelFailureV1) error {
	if a == nil {
		return nil
	}
	a.once.Do(func() {
		// Cleanup belongs to the retained provider graph, not the canceled request.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := len(a.leases) - 1; i >= 0; i-- {
			lease := a.leases[i]
			if lease.policy != "" {
				a.err = errors.Join(a.err, a.callCleanup(ctx, capacityPolicyService, pluginprotocol.ModelCapacitySettle{Lease: lease.policy, Outcome: outcome, Failure: failure}, nil))
			}
			if lease.physical != "" {
				a.err = errors.Join(a.err, a.callCleanup(ctx, capacityResourceService, pluginprotocol.ModelCapacitySettle{Lease: lease.physical, Outcome: outcome, Failure: failure}, nil))
			}
		}
	})
	return a.err
}
func capacityFailure(err error) *pluginprotocol.ModelFailureV1 {
	if err == nil {
		return nil
	}
	f := providers.NormalizeFailure(err)
	return &pluginprotocol.ModelFailureV1{Category: string(f.Category), RetryAfterMS: f.RetryAfter.Milliseconds(), Origin: string(f.Origin), ProviderFamily: f.ProviderFamily, ProviderCode: f.ProviderCode, HTTPStatus: f.HTTPStatus, Confidence: string(f.ClassificationConfidence)}
}

func settleModelCapacityError(capacity *modelCapacityAdmission, err error) error {
	if capacity == nil {
		return nil
	}
	if err != nil {
		return capacity.settle("failed", capacityFailure(err))
	}
	return capacity.settle("succeeded", nil)
}

func (a *modelCapacityAdmission) callCleanup(ctx context.Context, service string, input any, _ any) error {
	if a.scope == nil {
		return callModelCapacity(ctx, a.host, service, "settle", input, nil)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	_, err = a.scope.CallCleanup(ctx, service, "settle", raw)
	return err
}
