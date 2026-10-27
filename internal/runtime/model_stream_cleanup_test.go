package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"testing"
)

func TestModelStreamReleasesReceiptBeforeDispatchOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	releases := 0
	stream, err := invokePluginModelStream(ctx, nil, pluginhost.RegisteredCapability{}, nil, providers.InferenceSubmission{}, func() { releases++ })
	if !errors.Is(err, context.Canceled) || stream != nil {
		t.Fatalf("dispatch = %v, %v", stream, err)
	}
	if releases != 1 {
		t.Fatalf("released %d times, want once", releases)
	}
}

type malformedStreamReceiptFixture struct {
	negative bool
	emitted  string
	host     *pluginhost.Host
}

func (*malformedStreamReceiptFixture) ID() string { return "fixture.malformed" }
func (*malformedStreamReceiptFixture) Status() pluginhost.Status {
	return pluginhost.Status{State: pluginhost.StateActive}
}
func (*malformedStreamReceiptFixture) Close(context.Context) error { return nil }
func (*malformedStreamReceiptFixture) ProtocolVersion() int        { return 2 }
func (*malformedStreamReceiptFixture) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityModelAdapter, Version: 1}}
}
func (f *malformedStreamReceiptFixture) InvokeCapability(ctx context.Context, p pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	tokens := 3
	if f.negative {
		tokens = -1
	}
	if f.emitted != "" {
		event := pluginprotocol.ModelStreamEventV1{Type: "usage", Usage: &pluginprotocol.ModelRequestUsage{InputTokens: tokens, OutputTokens: 2}}
		switch f.emitted {
		case "phase":
			event.Phase = "invalid-phase"
		case "state":
			event.ProviderState = json.RawMessage(`"invalid-state"`)
		case "tool":
			event.Type = "tool_use_end"
		}
		err := f.host.EmitModelEvent(ctx, f.ID(), p.ExecutionID, event)
		return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{}`)}, err
	}
	raw, err := json.Marshal(pluginprotocol.ModelAdapterOutput{Response: &pluginprotocol.ModelResponseV1{Content: "invalid logical response", Phase: "invalid-phase", Usage: &pluginprotocol.ModelRequestUsage{InputTokens: tokens, OutputTokens: 2}}})
	return pluginhost.CapabilityInvokeResult{Output: raw}, err
}
func TestMalformedStreamResponsePreservesOnlyIndependentlyValidUsage(t *testing.T) {
	for _, emitted := range []string{"", "phase", "state", "tool"} {
		for _, negative := range []bool{false, true} {
			fixture := &malformedStreamReceiptFixture{negative: negative, emitted: emitted}
			host := pluginhost.New(fixture)
			fixture.host = host
			capability, ok := host.Capability("fixture.malformed", pluginhost.CapabilityModelAdapter)
			if !ok {
				t.Fatal("missing fixture")
			}
			req, err := providers.EnsureInferenceAttemptContext(context.Background(), providers.ChatRequest{Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			submission, err := req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{})
			if err != nil {
				t.Fatal(err)
			}
			stream, err := invokePluginModelStream(context.Background(), host, capability, struct{}{}, submission)
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for event := range stream {
				if event.Type == providers.EventDone {
					t.Fatal("malformed response became successful")
				}
				if event.Error != nil {
					failed = true
				}
			}
			entries := req.Attempt.SubmissionSnapshot()
			if !failed || len(entries) != 1 || entries[0].Outcome != providers.InferenceSubmissionFailed {
				t.Fatalf("lost failure: %+v", entries)
			}
			usage := entries[0].ReportedUsage
			if negative && usage != nil {
				t.Fatal("invalid usage was recorded")
			}
			if !negative && (usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 2) {
				t.Fatalf("valid physical usage erased: %+v", usage)
			}
		}
	}
}
