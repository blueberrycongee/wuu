package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSelectedCompactionGatewayDoesNotUseLegacyFallback(t *testing.T) {
	failure := errors.New("selected offline")
	calls := []string{}
	client := compactionservice.NewClient(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		if method == "describe" {
			return json.Marshal(compactionservice.Policy{Key: "independent", Guidance: []providers.ChatMessage{}, ContextWindows: false})
		}
		return nil, failure
	})
	runner := &StreamRunner{SelectedCompaction: client, Model: "synthetic"}
	gateway := newStreamTurnGateway(context.Background(), runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "task"}}}, nil)
	_, err := gateway.CompactContext(context.Background(), []providers.ChatMessage{{Role: "user", Content: "task"}}, pluginprotocol.DriverCompactBudget{})
	if !errors.Is(err, failure) {
		t.Fatalf("failure lost: %v", err)
	}
	if len(calls) != 2 || calls[0] != "describe" || calls[1] != "compact" {
		t.Fatalf("unexpected fallback: %v", calls)
	}
}

func TestSelectedCompactionFailureStopsTurnPreparation(t *testing.T) {
	failure := errors.New("selected missing")
	runner := &StreamRunner{SelectedCompaction: compactionservice.NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, failure })}
	gateway := newStreamTurnGateway(context.Background(), runner, loopdriver.PersistedInput{}, nil)
	if _, err := gateway.ReadTurn(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("missing policy swallowed: %v", err)
	}
}
