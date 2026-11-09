package compactionservice

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type modelClientKey struct{}

// WithModelClient carries only the execution-bound model transport. The host
// must admit it for this provider's identity; JSON cannot construct this binding.
func WithModelClient(ctx context.Context, client providers.Client) context.Context {
	return context.WithValue(ctx, modelClientKey{}, client)
}

func Describe(in DescribeRequest) Policy {
	target := FreshContextTargetTokens
	if in.CompactThresholdTokens > 0 {
		target = min(target, in.CompactThresholdTokens)
	}
	input := in.InputTokens
	if input <= 0 && in.ContextTokens > 0 {
		input = in.ContextTokens - max(0, in.OutputReserveTokens)
	}
	if input > 0 {
		target = min(target, input)
	}
	guidance := `For work spanning context windows or a requested handoff, keep a concise notes checkpoint: goal, constraints, key decisions, verified progress, remaining work and file or History Seq references. Update it for meaningful changes and before new_context, then pass its path and the latest notes collection revision as checkpoint; routine activity or a final reply alone does not require an update. After a reset, read the supplied checkpoint directly and recover missing evidence before acting. Use history_read for known Seq addresses, otherwise history_search.`
	return Policy{ContextWindows: true, TargetTokens: max(1, target), Guidance: []providers.ChatMessage{{Role: "user", Name: "wuu_context_window", Content: "<system-reminder>\n" + guidance + "\n</system-reminder>", Hidden: true}}}
}

func nativeCompact(ctx context.Context, in Request, client providers.Client) (Replacement, error) {
	host, selected, err := legacyPolicy(ctx, in.PolicyKey)
	if err != nil {
		return Replacement{}, err
	}
	if selected != nil {
		var out pluginhost.CompactionOutput
		if err = host.InvokeCapability(ctx, *selected, pluginhost.CompactionInput{Model: in.Model, Messages: in.Messages}, &out); err != nil {
			if policyErr := host.HandleCapabilityError(*selected, err); policyErr != nil {
				return Replacement{}, policyErr
			}
			return Replacement{Messages: providers.CloneChatMessages(in.Messages)}, nil
		}
		if !out.Unavailable {
			if err = providers.ValidateToolCallHistory(out.Messages); err != nil {
				return Replacement{}, err
			}
			return Replacement{Messages: out.Messages}, nil
		}
	}
	return traditionalCompact(ctx, in, client)
}

func traditionalCompact(ctx context.Context, in Request, client providers.Client) (Replacement, error) {
	if client == nil {
		return Replacement{}, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "compaction model transport is not bound"}
	}
	budget, err := adaptiveBudget(in.Messages, in.Budget, in.Hint)
	if err != nil {
		return Replacement{}, err
	}
	messages, err := compact.CompactWithNativeOrSummary(ctx, providers.CloneChatMessages(in.Messages), client, in.Model, budget, in.Options)
	if err != nil {
		return Replacement{}, err
	}
	return Replacement{Messages: messages}, nil
}

// Recovery is invoked on the SAME selected service. Only the native policy
// chooses traditional fallback; an external provider need not implement it.
func nativeRecover(ctx context.Context, in RecoveryRequest) (Replacement, error) {
	if in.Failure.Code == "canceled" || in.Failure.Code == "deadline_exceeded" {
		return Replacement{}, &pluginhost.HostServiceError{Code: in.Failure.Code, Message: in.Failure.Message}
	}
	if _, _, err := legacyPolicy(ctx, in.PolicyKey); err != nil {
		return Replacement{}, err
	}
	client, _ := ctx.Value(modelClientKey{}).(providers.Client)
	if client == nil {
		return Replacement{}, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "compaction model transport is not bound"}
	}
	if in.Failure.Code == "note_unsupported" {
		return nativeCompact(ctx, in.Request, client)
	}
	replacement, err := traditionalCompact(ctx, in.Request, client)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = providers.ValidateToolCallHistory(replacement.Messages)
	}
	if err == nil && (len(replacement.Messages) == 0 || estimateFreshContextMessages(replacement.Messages) >= estimateFreshContextMessages(in.Messages)) {
		err = fmt.Errorf("traditional compaction did not produce a smaller nonempty context")
	}
	if err != nil {
		return Replacement{}, fmt.Errorf("%s; native note recovery: %w", in.Failure.Message, err)
	}
	return replacement, nil
}
