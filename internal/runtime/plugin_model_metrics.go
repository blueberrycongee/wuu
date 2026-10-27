package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type modelRequestMetrics struct {
	host      *pluginhost.Host
	observers []pluginhost.RegisteredCapability
	started   time.Time
	input     pluginhost.ModelRequestMetricsInput
	finished  bool
}

func beginModelRequestMetrics(ctx context.Context, host *pluginhost.Host, req providers.ChatRequest, streaming bool) *modelRequestMetrics {
	if pluginhost.IsModelMetricsObservation(ctx) {
		return nil
	}
	observers := host.Capabilities(pluginhost.CapabilityModelRequestMetrics)
	if len(observers) == 0 {
		return nil
	}
	origin, ok := ctx.Value(modelRequestOriginKey{}).(pluginhost.ModelInvocationOriginV1)
	if !ok {
		origin.Kind = "auxiliary"
	}
	origin.OperationID = req.Operation.ID
	if req.Operation.ParentOperationID != "" {
		origin.ParentOperationID = req.Operation.ParentOperationID
	}
	origin.StepIndex = req.StepIndex
	execution := loopdriver.ExecutionContextFromContext(ctx)
	if origin.SessionID == "" {
		origin.SessionID = execution.SessionID
	}
	started := time.Now()
	return &modelRequestMetrics{host: host, observers: observers, started: started, input: pluginhost.ModelRequestMetricsInput{
		Version: 1, RequestID: "metrics-" + rand.Text(), Origin: origin, TurnID: execution.ExecutionID, AttemptID: req.Attempt.ID,
		Provider: req.Provider, Model: req.Model, Adapter: req.Adapter, Streaming: streaming, StartedAt: started.UTC(),
	}}
}

func (m *modelRequestMetrics) usage(usage *providers.TokenUsage) {
	if m == nil || m.finished || usage == nil {
		return
	}
	value := modelwire.TokenUsageToWire(*usage)
	m.input.Usage = &value
	m.input.UsageSource = "adapter_reported"
}

func (m *modelRequestMetrics) observe(event providers.StreamEvent) {
	if m == nil || m.finished {
		return
	}
	m.usage(event.Usage)
	output := false
	switch event.Type {
	case providers.EventContentDelta:
		if event.Content != "" {
			m.input.TextDeltaEvents++
			m.input.TextUTF8Bytes += len(event.Content)
			output = true
		}
	case providers.EventContentReplace, providers.EventThinkingDelta, providers.EventThinkingReplace, providers.EventToolUseDelta:
		output = event.Content != ""
	case providers.EventToolUseStart, providers.EventToolUseEnd:
		output = event.ToolCall != nil
	case providers.EventImage:
		output = event.Image != nil
	}
	if output && m.input.FirstOutputMS == nil {
		elapsed := float64(time.Since(m.started)) / float64(time.Millisecond)
		m.input.FirstOutputMS = &elapsed
	}
}

func (m *modelRequestMetrics) finishError(ctx context.Context, err error) {
	status := "completed"
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = "cancelled"
	} else if err != nil {
		status = "error"
	}
	m.finish(status)
}

func (m *modelRequestMetrics) finish(status string) {
	if m == nil || m.finished {
		return
	}
	m.finished = true
	m.input.DurationMS = float64(time.Since(m.started)) / float64(time.Millisecond)
	m.input.Status = status
	for _, observer := range m.observers {
		m.host.PublishModelRequestMetrics(observer, m.input)
	}
}
