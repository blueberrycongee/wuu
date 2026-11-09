package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type requestMetricsObserver struct {
	received  chan pluginhost.ModelRequestMetricsInput
	host      *pluginhost.Host
	recursive bool
}

func (*requestMetricsObserver) ID() string { return "request-metrics" }
func (*requestMetricsObserver) Status() pluginhost.Status {
	return pluginhost.Status{State: pluginhost.StateActive}
}
func (*requestMetricsObserver) Close(context.Context) error { return nil }
func (*requestMetricsObserver) ProtocolVersion() int        { return 3 }
func (*requestMetricsObserver) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityModelRequestMetrics, Kind: pluginhost.SeamObserve, Version: 1}}
}
func (f *requestMetricsObserver) InvokeCapability(ctx context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.ModelRequestMetricsInput
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if beginModelRequestMetrics(ctx, f.host, providers.ChatRequest{}, false) != nil {
		f.recursive = true
	}
	f.received <- input
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

// Output events are not tokens; absent usage is unknown. Terminal capture must
// survive cancellation, remain exactly once, and use trusted turn correlation.
func TestModelMetricsOutputCancellationAndPrivacy(t *testing.T) {
	observer := &requestMetricsObserver{received: make(chan pluginhost.ModelRequestMetricsInput, 4)}
	host := pluginhost.New(observer)
	observer.host = host
	defer host.Close(context.Background())
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "turn"})
	ctx = context.WithValue(ctx, modelRequestOriginKey{}, pluginhost.ModelInvocationOriginV1{Kind: "agent", SessionID: "session", ThreadID: "session", ParentOperationID: "trusted-parent"})
	req := providers.ChatRequest{Provider: "fixture", Model: "model", Adapter: "adapter", StepIndex: 2, Messages: []providers.ChatMessage{{Role: "user", Content: "private-prompt"}}}
	req.Operation = providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	metrics := beginModelRequestMetrics(ctx, host, req, true)
	metrics.observe(providers.StreamEvent{Type: providers.EventContentDelta, Content: "secret-output"})
	metrics.observe(providers.StreamEvent{Type: providers.EventContentDelta, Content: "猫"})
	metrics.observe(providers.StreamEvent{Type: providers.EventUsage, Usage: &providers.TokenUsage{OutputTokens: 19}})
	metrics.finish("cancelled")
	metrics.finish("completed")
	select {
	case value := <-observer.received:
		if observer.recursive {
			t.Fatal("observer callback recursively admitted its own inference metrics")
		}
		if value.TurnID != "turn" || value.Origin.SessionID != "session" || value.Origin.OperationID != req.Operation.ID || value.Origin.ParentOperationID != "trusted-parent" || value.Origin.StepIndex != 2 || value.RequestID == "" {
			t.Fatalf("correlation: %+v", value)
		}
		if value.Status != "cancelled" || value.TextDeltaEvents != 2 || value.TextUTF8Bytes != 16 || value.Usage.OutputTokens != 19 || value.UsageSource != "adapter_reported" || value.FirstOutputMS == nil {
			t.Fatalf("metrics: %+v", value)
		}
		raw, _ := json.Marshal(value)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		for _, key := range []string{"request", "messages", "result", "error", "content"} {
			if _, ok := fields[key]; ok {
				t.Fatalf("private field %s exported", key)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metrics not delivered")
	}
	select {
	case duplicate := <-observer.received:
		t.Fatalf("duplicate terminal: %+v", duplicate)
	default:
	}
	unknown := beginModelRequestMetrics(context.Background(), host, req, true)
	unknown.finish("incomplete")
	select {
	case value := <-observer.received:
		if value.Usage != nil || value.UsageSource != "" || value.TurnID != "" || value.FirstOutputMS != nil {
			t.Fatalf("unknown values fabricated: %+v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("empty stream not delivered")
	}
}
