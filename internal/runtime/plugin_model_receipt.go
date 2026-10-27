package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type modelReceiptParams = pluginprotocol.ModelReceiptParams

type modelReceiptInvoker struct{ parent *kernelHostServices }

func (k *modelReceiptInvoker) ID() string                { return k.parent.ID() }
func (k *modelReceiptInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *modelReceiptInvoker) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var input modelReceiptParams
	if err := decodeServiceParams(p.Params, &input); err != nil {
		return nil, err
	}
	k.parent.mu.RLock()
	host := k.parent.modelHost
	k.parent.mu.RUnlock()
	if host == nil {
		return nil, serviceError("service_unavailable", "model receipts unavailable")
	}
	return host.InvokeModelReceipt(p.Caller, input.Receipt, p.Method, input.Input)
}

func registerModelReceipt(ctx context.Context, host *pluginhost.Host, owner string, attempt providers.InferenceAttempt, fallbackScope ...string) (string, func()) {
	submissions := map[string]providers.InferenceSubmission{}
	admissions := map[string]*modelCapacityAdmission{}
	var cleanupErr error
	pluginhost.DeferServiceCompletion(ctx, func() error { return cleanupErr })
	id, unregister := host.RegisterModelReceipt(ctx, owner, func(method string, raw json.RawMessage) (json.RawMessage, error) {
		switch method {
		case "create":
			var wire pluginprotocol.ModelSubmissionMeta
			if err := decodeServiceParams(raw, &wire); err != nil {
				return nil, err
			}
			if wire.RequestBytes < 0 || len(wire.Provider) > 256 || len(wire.Protocol) > 256 || len(wire.Transport) > 128 || len(wire.Mode) > 128 || len(wire.Reason) > 1024 {
				return nil, serviceError("invalid_params", "invalid submission metadata")
			}
			scope := wire.CapacityScope
			if scope == "" {
				scope = "provider:" + owner
				if len(fallbackScope) > 0 && fallbackScope[0] != "" {
					scope = "provider:" + fallbackScope[0]
				}
			}
			routeScope := "provider:" + owner
			if len(fallbackScope) > 0 && fallbackScope[0] != "" {
				routeScope = "provider:" + fallbackScope[0]
			}
			admission, err := acquireModelCapacity(ctx, host, scope, attempt, routeScope)
			if err != nil {
				return nil, err
			}
			submission, err := attempt.RecordSubmission(providers.InferenceSubmissionMeta{CapacityScope: scope, Provider: wire.Provider, Protocol: wire.Protocol, Transport: wire.Transport, Mode: wire.Mode, Reason: wire.Reason, RequestBytes: wire.RequestBytes})
			if err != nil {
				return nil, errors.Join(err, admission.settle("abandoned", nil))
			}
			admissions[submission.ID] = admission
			submissions[submission.ID] = submission
			return json.Marshal(modelSubmissionWire(submission))
		case "snapshot":
			snapshots := attempt.SubmissionSnapshot()
			result := make([]pluginprotocol.ModelSubmission, 0, len(snapshots))
			for _, submission := range snapshots {
				result = append(result, modelSubmissionWire(submission))
			}
			return json.Marshal(result)
		case "observe", "settle":
			var wire pluginprotocol.ModelSubmissionUpdate
			if err := decodeServiceParams(raw, &wire); err != nil {
				return nil, err
			}
			if err := validateLogicalModelUsage(wire.Usage); err != nil {
				return nil, err
			}
			if len(wire.Output) > 1<<20 {
				return nil, serviceError("invalid_params", "receipt output observation exceeds limit")
			}
			update := providers.RemoteSubmissionUpdate{ID: wire.ID, Output: wire.Output, Estimated: wire.Estimated, FromStream: wire.FromStream, Outcome: providers.InferenceSubmissionOutcome(wire.Outcome)}
			if wire.Usage != nil {
				usage := modelwire.TokenUsageFromWire(*wire.Usage)
				update.Usage = &usage
			}
			if wire.Failure != nil {
				f := wire.Failure
				update.RetryAfter = time.Duration(f.RetryAfterMS) * time.Millisecond
				update.Failure = providers.InferenceJournalFailure{Origin: providers.FailureOrigin(f.Origin), Category: providers.FailureCategory(f.Category), ProviderFamily: f.ProviderFamily, ProviderCode: f.ProviderCode, HTTPStatus: f.HTTPStatus, Confidence: providers.ClassificationConfidence(f.Confidence)}
			}
			submission, ok := submissions[update.ID]
			if !ok {
				return nil, serviceError("receipt_unavailable", "submission is not owned by this receipt")
			}

			if err := submission.ApplyRemoteSubmissionUpdate(update, method == "settle"); err != nil {
				return nil, err
			}

			if method == "settle" {
				if admission := admissions[wire.ID]; admission != nil {
					if err := admission.settle(wire.Outcome, wire.Failure); err != nil {
						return nil, err
					}
					delete(admissions, wire.ID)
				}
			}
			if err := attempt.JournalError(); err != nil {
				return nil, err
			}
			return json.RawMessage(`{}`), nil
		default:
			return nil, serviceError("method_not_found", "unknown model receipt operation")
		}
	})
	return id, func() {
		// unregister waits for any in-flight receipt callback. Logical cancellation
		// alone must not release a slot while its physical transport is still live.
		unregister()
		for _, admission := range admissions {
			if err := admission.settle("abandoned", nil); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
}

func modelSubmissionWire(s providers.InferenceSubmission) pluginprotocol.ModelSubmission {
	wire := pluginprotocol.ModelSubmission{ID: s.ID, Outcome: string(s.Outcome)}
	if s.ReportedUsage != nil {
		usage := modelwire.TokenUsageToWire(*s.ReportedUsage)
		wire.Usage = &usage
	}
	return wire
}
func modelSubmissionFromWire(wire pluginprotocol.ModelSubmission) providers.InferenceSubmission {
	s := providers.InferenceSubmission{ID: wire.ID, Outcome: providers.InferenceSubmissionOutcome(wire.Outcome)}
	if wire.Usage != nil {
		usage := modelwire.TokenUsageFromWire(*wire.Usage)
		s.ReportedUsage = &usage
	}
	return s
}
func modelReceiptTransportInput(method string, input any) any {
	if method == "create" {
		meta := input.(providers.InferenceSubmissionMeta)
		return pluginprotocol.ModelSubmissionMeta{CapacityScope: meta.CapacityScope, Provider: meta.Provider, Protocol: meta.Protocol, Transport: meta.Transport, Mode: meta.Mode, Reason: meta.Reason, RequestBytes: meta.RequestBytes}
	}
	if method == "observe" || method == "settle" {
		u := input.(providers.RemoteSubmissionUpdate)
		wire := pluginprotocol.ModelSubmissionUpdate{ID: u.ID, Output: u.Output, Estimated: u.Estimated, FromStream: u.FromStream, Outcome: string(u.Outcome)}
		if u.Usage != nil {
			usage := modelwire.TokenUsageToWire(*u.Usage)
			wire.Usage = &usage
		}
		if u.Failure.Category != "" {
			f := u.Failure
			wire.Failure = &pluginprotocol.ModelFailureV1{Origin: string(f.Origin), Category: string(f.Category), ProviderFamily: f.ProviderFamily, ProviderCode: f.ProviderCode, HTTPStatus: f.HTTPStatus, Confidence: string(f.Confidence), RetryAfterMS: u.RetryAfter.Milliseconds()}
		}
		return wire
	}
	return nil
}
