package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type metricsObserverFixture struct {
	id      string
	started chan ModelRequestMetricsInput
	release chan struct{}
	fail    bool
}

func (f *metricsObserverFixture) ID() string                { return f.id }
func (f *metricsObserverFixture) Status() Status            { return Status{ID: f.id, State: StateActive} }
func (*metricsObserverFixture) Close(context.Context) error { return nil }
func (*metricsObserverFixture) ProtocolVersion() int        { return CapabilityProtocolVersion }
func (*metricsObserverFixture) Capabilities() []CapabilityDescriptor {
	return []CapabilityDescriptor{{ID: CapabilityModelRequestMetrics, Kind: SeamObserve, Version: 1}}
}
func (f *metricsObserverFixture) InvokeCapability(ctx context.Context, p CapabilityInvokeParams) (CapabilityInvokeResult, error) {
	var input ModelRequestMetricsInput
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return CapabilityInvokeResult{}, err
	}
	select {
	case f.started <- input:
	case <-ctx.Done():
		return CapabilityInvokeResult{}, ctx.Err()
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return CapabilityInvokeResult{}, ctx.Err()
		}
	}
	if f.fail {
		return CapabilityInvokeResult{}, errors.New("fixture observer failed")
	}
	return CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, nil
}

// Delivery must not block model dispatch, grow without bound, redirect an old
// registration into its replacement, or keep the retired generation alive.
func TestModelMetricsBoundedDeliveryAndRetirement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	old := &metricsObserverFixture{id: "metrics", started: make(chan ModelRequestMetricsInput, 128), release: make(chan struct{})}
	host := New(old)
	defer host.Close(context.Background())
	capability, _ := host.Capability(old.id, CapabilityModelRequestMetrics)
	host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "first"})
	select {
	case <-old.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < modelMetricsQueueCapacity+4; i++ {
		host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "queued"})
	}
	close(old.release)
	var second ModelRequestMetricsInput
	select {
	case second = <-old.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if second.DroppedBefore != 4 {
		t.Fatalf("drop count=%d, want 4", second.DroppedBefore)
	}
	if outcome, ok := host.RetirePlugin(ctx, old.id, nil); !ok || outcome.Err != nil {
		t.Fatalf("retirement: %+v %t", outcome, ok)
	}
	replacement := &metricsObserverFixture{id: old.id, started: make(chan ModelRequestMetricsInput, 1)}
	host.Add(replacement)
	host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "stale"})
	current, _ := host.Capability(old.id, CapabilityModelRequestMetrics)
	host.PublishModelRequestMetrics(current, ModelRequestMetricsInput{RequestID: "current"})
	select {
	case value := <-replacement.started:
		if value.RequestID != "current" {
			t.Fatalf("stale generation delivered: %+v", value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestModelMetricsCancelsBlockedObserverOnShutdown(t *testing.T) {
	for _, mode := range []string{"shutdown", "execution-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			observer := &metricsObserverFixture{id: "metrics", started: make(chan ModelRequestMetricsInput, 2), release: make(chan struct{})}
			host := New(observer)
			defer host.Close(context.Background())
			capability, _ := host.Capability(observer.id, CapabilityModelRequestMetrics)
			host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "first"})
			select {
			case <-observer.started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "queued"})
			if mode == "execution-cancel" {
				host.mu.RLock()
				settled := host.modelMetrics[observer.id].done
				host.mu.RUnlock()
				host.CancelExecutions(context.Canceled)
				select {
				case <-settled:
				case <-ctx.Done():
					t.Fatal("unacknowledged cancellation did not stop metrics delivery")
				}
				host.PublishModelRequestMetrics(capability, ModelRequestMetricsInput{RequestID: "late"})
				select {
				case value := <-observer.started:
					t.Fatalf("new callback behind cancellation: %+v", value)
				default:
				}
			}
			if err := host.Close(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-host.ShutdownDone():
			default:
				t.Fatal("shutdown returned before observer delivery settled")
			}
		})
	}
}

func TestModelMetricsNegotiation(t *testing.T) {
	descriptor := CapabilityDescriptor{ID: CapabilityModelRequestMetrics, Kind: SeamObserve, Version: 1}
	for _, version := range []int{2, 3} {
		if err := ValidateCapabilityNegotiation(CapabilityInitializeResult{ProtocolVersion: version, Capabilities: []CapabilityDescriptor{descriptor}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	descriptor.Version = 2
	if err := ValidateCapabilityDescriptor(descriptor); err == nil {
		t.Fatal("unimplemented metrics version accepted")
	}
	descriptor.Version = 1
	descriptor.ErrorPolicy = ErrorPolicyPropagate
	if err := ValidateCapabilityDescriptor(descriptor); err == nil {
		t.Fatal("metrics observer may fail a model request")
	}
}
