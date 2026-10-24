package agent

import (
	"context"
	"errors"
	"slices"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (g *streamTurnGateway) AdmitInput(ctx context.Context) (loopdriver.InputAdmission, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.InputAdmission{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.InputAdmission{}, err
	}
	if len(g.streamedBatches) != 0 {
		return loopdriver.InputAdmission{}, errors.New("join the pending streaming tool batch before admitting input")
	}
	var admitted []providers.ChatMessage
	appendInput := func(messages []providers.ChatMessage) {
		messages = filterDurableHistory(messages)
		admitted = append(admitted, messages...)
		g.appendMessages(messages...)
	}
	if g.runner.BeforeStep != nil {
		appendInput(g.runner.BeforeStep())
	}
	var admissionErr error
	if preparer, ok := g.runner.Tools.(ModelStepPreparer); ok {
		var messages []providers.ChatMessage
		messages, admissionErr = preparer.PrepareModelStep(ctx, providers.CloneChatMessages(g.activeHistory))
		if admissionErr == nil {
			appendInput(messages)
		}
	}
	if admissionErr == nil && g.runner.BeforeModelStep != nil {
		var messages []providers.ChatMessage
		messages, admissionErr = g.runner.BeforeModelStep(ctx, g.step, providers.CloneChatMessages(g.activeHistory))
		if admissionErr == nil {
			appendInput(messages)
		}
	}
	// Even a later input producer's failure must settle already accepted user
	// input. Give the driver durable addresses before compaction or submission.
	for _, message := range admitted {
		if isUndispatchedSteeringMessage(message) && g.runner.ArchiveHistory != nil {
			if err := g.archivePending(ctx); err != nil {
				g.produced = slices.DeleteFunc(g.produced, isUndispatchedSteeringMessage)
				g.durable = slices.DeleteFunc(g.durable, isUndispatchedSteeringMessage)
				g.activeHistory = slices.DeleteFunc(g.activeHistory, isUndispatchedSteeringMessage)
				return loopdriver.InputAdmission{}, errors.Join(admissionErr, err)
			}
			seqs := make([]int, len(g.produced))
			for i, produced := range g.produced {
				seqs[i] = produced.Seq
			}
			admitted = applyArchivedHistorySeqs(admitted, g.produced, seqs)
			break
		}
	}
	return loopdriver.InputAdmission{Messages: providers.CloneChatMessages(admitted), Context: g.contextSnapshot()}, admissionErr
}

func (g *streamTurnGateway) ReadRequestContext(ctx context.Context) ([]loopdriver.ContextSegment, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	segments := append(requestContextSegments(g.runner.BeforeRequestContext), g.postToolContext...)
	if g.policy != nil {
		segments = append(segments, RequestOnlyContextMessages(g.policy.Guidance)...)
	} else if g.runner.contextWindowProvider() != nil && g.runner.ArchiveHistory != nil {
		segments = append(segments, withContextWindowGuidance(nil)()...)
	}
	result := slices.Clone(segments)
	for i := range result {
		result[i].Messages = providers.CloneChatMessages(result[i].Messages)
		result[i].Blocks = slices.Clone(result[i].Blocks)
	}
	return result, nil
}

func (g *streamTurnGateway) TransformRequest(ctx context.Context, request providers.ChatRequest) (providers.ChatRequest, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return providers.ChatRequest{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return providers.ChatRequest{}, err
	}
	request = cloneDriverRequest(request)
	request.StepIndex = g.step
	if g.runner.BeforeRequest != nil {
		if err := g.runner.BeforeRequest(ctx, &request); err != nil {
			return providers.ChatRequest{}, err
		}
	}
	if request.Provider != g.runner.ProviderName {
		return providers.ChatRequest{}, errors.New("model request must use the turn's bound provider")
	}
	if err := validateTransformedRequest(request); err != nil {
		return providers.ChatRequest{}, err
	}
	return request, nil
}

// Isolate mutable request data just as the process transport does. Retry and
// routing objects belong to execution services, never to a driver callback.
func cloneDriverRequest(request providers.ChatRequest) providers.ChatRequest {
	request.Messages = providers.CloneChatMessages(request.Messages)
	request.ProviderOptions = providers.CloneRequestObject(request.ProviderOptions)
	request.Tools = slices.Clone(request.Tools)
	for i := range request.Tools {
		request.Tools[i].InputSchema = providers.CloneRequestObject(request.Tools[i].InputSchema)
	}
	if request.CacheHint != nil {
		hint := *request.CacheHint
		request.CacheHint = &hint
	}
	request.Operation = providers.InferenceOperation{}
	request.Execution = nil
	request.Attempt = providers.InferenceAttempt{}
	request.TurnRouting = nil
	return request
}
