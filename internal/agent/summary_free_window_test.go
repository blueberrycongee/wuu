package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestManualContextWindowCommitsWithoutInference(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		step := &contextSwitchStep{}
		commits := 0
		result, err := RunToolLoop(context.Background(), fallbackHistory(), LoopConfig{
			Model: "test", ForceInitialCompact: true, CompactOnly: true, FreshContextTokens: 4000,
			Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
				t.Fatal("manual window generated a summary")
				return nil, nil
			},
			ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
				return HistoryArchive{HeadSeq: 100}, nil
			},
			FreshContext: func(_ context.Context, m []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
				return buildFreshContext(m, head, fixed, target)
			},
			AcceptFreshContext: func(_ context.Context, m []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
				commits++
				if commitFailure {
					return nil, head, errors.New("storage failed")
				}
				return m, head, nil
			},
		}, step)
		if (err != nil) != commitFailure || result.HistoryRewritten == commitFailure || commits != 1 || len(step.requests) != 0 {
			t.Fatalf("commitFailure=%v result=%+v err=%v commits=%d requests=%d", commitFailure, result, err, commits, len(step.requests))
		}
	}
}

func TestDefaultDriverUsesSelectedWindowProposal(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls, commits := 0, 0
		failure := errors.New("selected window rejected")
		selected := compactionservice.NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			switch method {
			case "describe":
				return json.Marshal(compactionservice.Policy{Key: "independent-window", ContextWindows: true, TargetTokens: 4000, Guidance: []providers.ChatMessage{}})
			case "window":
				calls++
				var in compactionservice.WindowRequest
				if err := json.Unmarshal(raw, &in); err != nil {
					t.Fatal(err)
				}
				if in.HistoryHead != 100 || in.PolicyKey != "independent-window" {
					t.Fatalf("lost binding/archive: %+v", in)
				}
				if fail {
					return nil, failure
				}
				return json.Marshal(compactionservice.Replacement{Messages: []providers.ChatMessage{{Role: "system", Content: "independent policy"}, {Role: "user", Content: "retained task"}}})
			default:
				t.Fatalf("unexpected policy method %s", method)
				return nil, nil
			}
		})
		client := &mockStreamClient{}
		runner := &StreamRunner{LoopDriver: DefaultDriver{}, Client: client, Model: "synthetic", SelectedCompaction: selected, ForceInitialCompact: true, CompactOnly: true,
			ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
				return HistoryArchive{HeadSeq: 100}, nil
			},
			CommitFreshContext: func(_ context.Context, m []providers.ChatMessage, head int, key string, _ CompactionNote) ([]providers.ChatMessage, int, error) {
				commits++
				if key != "independent-window" || len(m) != 2 || m[0].Content != "independent policy" {
					t.Fatalf("proposal altered: %s %+v", key, m)
				}
				return m, head, nil
			},
		}
		result, err := runner.RunWithCallback(context.Background(), fallbackHistory(), nil)
		if fail {
			if err == nil || commits != 0 || result.HistoryRewritten {
				t.Fatalf("failure committed: %+v %v", result, err)
			}
		} else if err != nil || commits != 1 || !result.HistoryRewritten {
			t.Fatalf("proposal not committed: %+v %v", result, err)
		}
		if calls != 1 || len(client.requests) != 0 {
			t.Fatalf("unexpected inference/calls: %d %d", calls, len(client.requests))
		}
	}
}
