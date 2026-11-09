package pluginhost

import (
	"context"
	"errors"
	"time"
)

const (
	modelMetricsQueueCapacity   = 64
	modelMetricsDeliveryTimeout = 5 * time.Second
)

type modelMetricsObservationKey struct{}

// IsModelMetricsObservation marks observer-descended inference so telemetry
// cannot recursively trigger its own callbacks. It is never accepted from JSON.
func IsModelMetricsObservation(ctx context.Context) bool {
	return ctx.Value(modelMetricsObservationKey{}) == true
}

// WithModelMetricsObservation carries only telemetry provenance, not execution
// authority, when an observer starts generation-owned background work.
func WithModelMetricsObservation(ctx context.Context) context.Context {
	return context.WithValue(ctx, modelMetricsObservationKey{}, true)
}

type modelMetricsDelivery struct {
	capability RegisteredCapability
	queue      chan ModelRequestMetricsInput
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	dropped    uint64 // guarded by Host.mu
	suspended  bool   // a timeout did not acknowledge physical callback completion
}

// PublishModelRequestMetrics never waits for an observer. The captured exact
// registration cannot redirect queued data into a replacement plugin.
func (h *Host) PublishModelRequestMetrics(capability RegisteredCapability, input ModelRequestMetricsInput) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shutdown != nil {
		return
	}
	active := false
	for _, current := range h.capabilities {
		if current.PluginID == capability.PluginID && current.Descriptor.ID == CapabilityModelRequestMetrics && current.client == capability.client {
			active = capabilityStateReady(current.client.Status().State)
			break
		}
	}
	if !active {
		return
	}
	if h.modelMetrics == nil {
		h.modelMetrics = make(map[string]*modelMetricsDelivery)
	}
	delivery := h.modelMetrics[capability.PluginID]
	if delivery == nil {
		ctx, cancel := context.WithCancel(context.Background())
		delivery = &modelMetricsDelivery{capability: capability, queue: make(chan ModelRequestMetricsInput, modelMetricsQueueCapacity), ctx: ctx, cancel: cancel, done: make(chan struct{})}
		h.modelMetrics[capability.PluginID] = delivery
		go h.deliverModelMetrics(delivery)
	}
	if delivery.suspended {
		return
	}
	select {
	case delivery.queue <- input:
	default:
		delivery.dropped++
		h.recordModelMetricsDiagnosticLocked(capability.PluginID, "metrics queue full; observations dropped")
	}
}

func (h *Host) deliverModelMetrics(delivery *modelMetricsDelivery) {
	defer close(delivery.done)
	for {
		select {
		case <-delivery.ctx.Done():
			return
		case input := <-delivery.queue:
			if delivery.ctx.Err() != nil {
				return
			}
			h.mu.Lock()
			input.DroppedBefore = delivery.dropped
			delivery.dropped = 0
			h.mu.Unlock()
			ctx, cancel := context.WithTimeout(WithModelMetricsObservation(delivery.ctx), modelMetricsDeliveryTimeout)
			var output struct{}
			err := h.InvokeCapability(ctx, delivery.capability, input, &output)
			timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
			cancel()
			if (timedOut || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) && delivery.ctx.Err() == nil {
				// execution.cancel is not an acknowledgement. Never launch another
				// callback behind a remote handler that may still be running.
				h.mu.Lock()
				delivery.suspended = true
				for len(delivery.queue) > 0 {
					<-delivery.queue
				}
				if EffectiveErrorPolicy(delivery.capability.Descriptor) != ErrorPolicyIgnore {
					reason := "metrics observer cancelled; delivery suspended until plugin reload"
					if timedOut || errors.Is(err, context.DeadlineExceeded) {
						reason = "metrics observer timed out; delivery suspended until plugin reload"
					}
					h.recordModelMetricsDiagnosticLocked(delivery.capability.PluginID, reason)
				}
				h.mu.Unlock()
				return
			}
			if err != nil && delivery.ctx.Err() == nil && !errors.Is(err, ErrCapabilityRetired) {
				// Callback errors may themselves contain private text. Metrics diagnostics
				// deliberately expose only a fixed reason, never arbitrary plugin output.
				message := "metrics observer failed"
				if EffectiveErrorPolicy(delivery.capability.Descriptor) != ErrorPolicyIgnore {
					h.mu.Lock()
					h.recordModelMetricsDiagnosticLocked(delivery.capability.PluginID, message)
					h.mu.Unlock()
				}
			}
		}
	}
}

func (h *Host) recordModelMetricsDiagnosticLocked(pluginID, message string) {
	if h.diagnostics[pluginID] == nil {
		h.diagnostics[pluginID] = make(map[string]string)
	}
	h.diagnostics[pluginID][CapabilityModelRequestMetrics] = message
}

// Detach under the same admission lock used by Publish, then cancel and join
// outside it. Accepted work is generation-owned, never a detached goroutine.
func (h *Host) stopModelMetrics(pluginID string) {
	h.mu.Lock()
	var deliveries []*modelMetricsDelivery
	for id, delivery := range h.modelMetrics {
		if pluginID == "" || id == pluginID {
			deliveries = append(deliveries, delivery)
			delete(h.modelMetrics, id)
		}
	}
	h.mu.Unlock()
	for _, delivery := range deliveries {
		delivery.cancel()
	}
	for _, delivery := range deliveries {
		<-delivery.done
	}
}
