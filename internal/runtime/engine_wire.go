package runtime

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func engineProviderStateSummaryToWire(in providers.ProviderStateSummary) (out pluginprotocol.EngineProviderStateV1) {
	out.StepIndex = in.StepIndex
	out.Provider = in.Provider
	out.Protocol = in.Protocol
	out.Transport = in.Transport
	out.ConfiguredTransport = in.ConfiguredTransport
	out.ReplayMode = in.ReplayMode
	out.PreviousResponseIDUsed = in.PreviousResponseIDUsed
	out.ConnectionReused = in.ConnectionReused
	out.Diagnostic = in.Diagnostic
	out.TransportFailurePhase = in.TransportFailurePhase
	out.FailedTransport = in.FailedTransport
	out.FallbackTransport = in.FallbackTransport
	out.EventsEmitted = in.EventsEmitted
	out.FallbackActive = in.FallbackActive
	out.FallbackReason = in.FallbackReason
	out.FallbackPinStatus = in.FallbackPinStatus
	out.FallbackRetryAfterMS = in.FallbackRetryAfterMS
	out.FallbackTTLMS = in.FallbackTTLMS
	out.InputItems = in.InputItems
	out.FullInputItems = in.FullInputItems
	out.DeltaInputItems = in.DeltaInputItems
	return out
}

func engineProviderStateSummaryFromWire(in pluginprotocol.EngineProviderStateV1) (out providers.ProviderStateSummary) {
	out.StepIndex = in.StepIndex
	out.Provider = in.Provider
	out.Protocol = in.Protocol
	out.Transport = in.Transport
	out.ConfiguredTransport = in.ConfiguredTransport
	out.ReplayMode = in.ReplayMode
	out.PreviousResponseIDUsed = in.PreviousResponseIDUsed
	out.ConnectionReused = in.ConnectionReused
	out.Diagnostic = in.Diagnostic
	out.TransportFailurePhase = in.TransportFailurePhase
	out.FailedTransport = in.FailedTransport
	out.FallbackTransport = in.FallbackTransport
	out.EventsEmitted = in.EventsEmitted
	out.FallbackActive = in.FallbackActive
	out.FallbackReason = in.FallbackReason
	out.FallbackPinStatus = in.FallbackPinStatus
	out.FallbackRetryAfterMS = in.FallbackRetryAfterMS
	out.FallbackTTLMS = in.FallbackTTLMS
	out.InputItems = in.InputItems
	out.FullInputItems = in.FullInputItems
	out.DeltaInputItems = in.DeltaInputItems
	return out
}

func engineStreamLifecycleToWire(in providers.StreamLifecycle) (out pluginprotocol.EngineLifecycleV1) {
	out.Phase = string(in.Phase)
	out.OperationID = in.OperationID
	out.OperationKind = string(in.OperationKind)
	out.WorkloadProfile = string(in.WorkloadProfile)
	out.PayloadVersion = in.PayloadVersion
	out.AttemptID = in.AttemptID
	out.Attempt = in.Attempt
	out.MaxAttempts = in.MaxAttempts
	out.SubmissionID = in.SubmissionID
	out.SubmissionCount = in.SubmissionCount
	out.RetryCount = in.RetryCount
	out.MaxRetries = in.MaxRetries
	out.RetryInNS = int64(in.RetryIn)
	out.ElapsedNS = int64(in.Elapsed)
	out.Reason = in.Reason
	out.FailureCategory = in.FailureCategory
	out.RecoveryAction = in.RecoveryAction
	out.BudgetDimension = in.BudgetDimension
	out.ReplayReason = in.ReplayReason
	out.Workflow = engineWorkflowBudgetSnapshotToWire(in.Workflow)
	out.ResetPartial = in.ResetPartial
	return out
}

func engineStreamLifecycleFromWire(in pluginprotocol.EngineLifecycleV1) (out providers.StreamLifecycle) {
	out.Phase = providers.StreamLifecyclePhase(in.Phase)
	out.OperationID = in.OperationID
	out.OperationKind = providers.InferenceOperationKind(in.OperationKind)
	out.WorkloadProfile = providers.InferenceWorkloadProfile(in.WorkloadProfile)
	out.PayloadVersion = in.PayloadVersion
	out.AttemptID = in.AttemptID
	out.Attempt = in.Attempt
	out.MaxAttempts = in.MaxAttempts
	out.SubmissionID = in.SubmissionID
	out.SubmissionCount = in.SubmissionCount
	out.RetryCount = in.RetryCount
	out.MaxRetries = in.MaxRetries
	out.RetryIn = time.Duration(in.RetryInNS)
	out.Elapsed = time.Duration(in.ElapsedNS)
	out.Reason = in.Reason
	out.FailureCategory = in.FailureCategory
	out.RecoveryAction = in.RecoveryAction
	out.BudgetDimension = in.BudgetDimension
	out.ReplayReason = in.ReplayReason
	out.Workflow = engineWorkflowBudgetSnapshotFromWire(in.Workflow)
	out.ResetPartial = in.ResetPartial
	return out
}

func engineWorkflowBudgetSnapshotToWire(in providers.WorkflowBudgetSnapshot) (out pluginprotocol.EngineWorkflowBudgetV1) {
	out.WorkflowID = in.WorkflowID
	out.Spec = engineWorkflowBudgetSpecToWire(in.Spec)
	out.Operations = in.Operations
	out.Attempts = in.Attempts
	out.Submissions = in.Submissions
	out.TransportSwitches = in.TransportSwitches
	out.CredentialRefreshes = in.CredentialRefreshes
	out.PayloadTransforms = in.PayloadTransforms
	out.ChildOperations = in.ChildOperations
	out.RecoveryWaitMillis = in.RecoveryWaitMillis
	out.KnownSubmissions = in.KnownSubmissions
	out.EstimatedSubmissions = in.EstimatedSubmissions
	out.UnknownBillableSubmissions = in.UnknownBillableSubmissions
	out.KnownUsage = modelwire.TokenUsageToWire(in.KnownUsage)
	out.EstimatedUsage = modelwire.TokenUsageToWire(in.EstimatedUsage)
	return out
}

func engineWorkflowBudgetSnapshotFromWire(in pluginprotocol.EngineWorkflowBudgetV1) (out providers.WorkflowBudgetSnapshot) {
	out.WorkflowID = in.WorkflowID
	out.Spec = engineWorkflowBudgetSpecFromWire(in.Spec)
	out.Operations = in.Operations
	out.Attempts = in.Attempts
	out.Submissions = in.Submissions
	out.TransportSwitches = in.TransportSwitches
	out.CredentialRefreshes = in.CredentialRefreshes
	out.PayloadTransforms = in.PayloadTransforms
	out.ChildOperations = in.ChildOperations
	out.RecoveryWaitMillis = in.RecoveryWaitMillis
	out.KnownSubmissions = in.KnownSubmissions
	out.EstimatedSubmissions = in.EstimatedSubmissions
	out.UnknownBillableSubmissions = in.UnknownBillableSubmissions
	out.KnownUsage = modelwire.TokenUsageFromWire(in.KnownUsage)
	out.EstimatedUsage = modelwire.TokenUsageFromWire(in.EstimatedUsage)
	return out
}

func engineWorkflowBudgetSpecToWire(in providers.WorkflowBudgetSpec) (out pluginprotocol.EngineBudgetSpecV1) {
	out.MaxOperations = engineBudgetLimitToWire(in.MaxOperations)
	out.MaxAttempts = engineBudgetLimitToWire(in.MaxAttempts)
	out.MaxSubmissions = engineBudgetLimitToWire(in.MaxSubmissions)
	out.MaxSamePayloadReplays = engineBudgetLimitToWire(in.MaxSamePayloadReplays)
	out.MaxTransportSwitches = engineBudgetLimitToWire(in.MaxTransportSwitches)
	out.MaxCredentialRefreshes = engineBudgetLimitToWire(in.MaxCredentialRefreshes)
	out.MaxPayloadTransforms = engineBudgetLimitToWire(in.MaxPayloadTransforms)
	out.MaxChildOperations = engineBudgetLimitToWire(in.MaxChildOperations)
	out.MaxRecoveryWaitMillis = engineBudgetLimitToWire(in.MaxRecoveryWaitMillis)
	out.MaxUnknownBillableSubmissions = engineBudgetLimitToWire(in.MaxUnknownBillableSubmissions)
	out.MaxUsageTokens = engineBudgetLimitToWire(in.MaxUsageTokens)
	return out
}

func engineWorkflowBudgetSpecFromWire(in pluginprotocol.EngineBudgetSpecV1) (out providers.WorkflowBudgetSpec) {
	out.MaxOperations = engineBudgetLimitFromWire(in.MaxOperations)
	out.MaxAttempts = engineBudgetLimitFromWire(in.MaxAttempts)
	out.MaxSubmissions = engineBudgetLimitFromWire(in.MaxSubmissions)
	out.MaxSamePayloadReplays = engineBudgetLimitFromWire(in.MaxSamePayloadReplays)
	out.MaxTransportSwitches = engineBudgetLimitFromWire(in.MaxTransportSwitches)
	out.MaxCredentialRefreshes = engineBudgetLimitFromWire(in.MaxCredentialRefreshes)
	out.MaxPayloadTransforms = engineBudgetLimitFromWire(in.MaxPayloadTransforms)
	out.MaxChildOperations = engineBudgetLimitFromWire(in.MaxChildOperations)
	out.MaxRecoveryWaitMillis = engineBudgetLimitFromWire(in.MaxRecoveryWaitMillis)
	out.MaxUnknownBillableSubmissions = engineBudgetLimitFromWire(in.MaxUnknownBillableSubmissions)
	out.MaxUsageTokens = engineBudgetLimitFromWire(in.MaxUsageTokens)
	return out
}

func engineBudgetLimitToWire(in providers.BudgetLimit) (out pluginprotocol.EngineBudgetLimitV1) {
	out.Set = in.Set
	out.Value = in.Value
	return out
}

func engineBudgetLimitFromWire(in pluginprotocol.EngineBudgetLimitV1) (out providers.BudgetLimit) {
	out.Set = in.Set
	out.Value = in.Value
	return out
}

func engineTodoUpdateToWire(in providers.TodoUpdate) (out pluginprotocol.EngineTodoUpdateV1) {
	out.Explanation = in.Explanation
	if in.Todos != nil {
		out.Todos = make([]pluginprotocol.EngineTodoItemV1, 0, len(in.Todos))
		for _, item := range in.Todos {
			out.Todos = append(out.Todos, engineTodoItemToWire(item))
		}
	}
	return out
}

func engineTodoUpdateFromWire(in pluginprotocol.EngineTodoUpdateV1) (out providers.TodoUpdate) {
	out.Explanation = in.Explanation
	if in.Todos != nil {
		out.Todos = make([]providers.TodoItem, 0, len(in.Todos))
		for _, item := range in.Todos {
			out.Todos = append(out.Todos, engineTodoItemFromWire(item))
		}
	}
	return out
}

func engineTodoItemToWire(in providers.TodoItem) (out pluginprotocol.EngineTodoItemV1) {
	out.Content = in.Content
	out.Status = in.Status
	return out
}

func engineTodoItemFromWire(in pluginprotocol.EngineTodoItemV1) (out providers.TodoItem) {
	out.Content = in.Content
	out.Status = in.Status
	return out
}

func engineAgentActivityToWire(in providers.AgentActivity) (out pluginprotocol.EngineAgentActivityV1) {
	out.ID = in.ID
	out.Engine = in.Engine
	out.Label = in.Label
	out.State = string(in.State)
	return out
}

func engineAgentActivityFromWire(in pluginprotocol.EngineAgentActivityV1) (out providers.AgentActivity) {
	out.ID = in.ID
	out.Engine = in.Engine
	out.Label = in.Label
	out.State = providers.AgentActivityState(in.State)
	return out
}

func engineUserInputRequestToWire(in agentengine.UserInputRequest) (out pluginprotocol.EngineUserInputRequestV1) {
	out.EngineID = string(in.EngineID)
	out.ThreadID = in.ThreadID
	out.TurnID = in.TurnID
	out.ItemID = in.ItemID
	out.Blocking = in.Blocking
	if in.Questions != nil {
		out.Questions = make([]pluginprotocol.EngineUserInputQuestionV1, 0, len(in.Questions))
		for _, item := range in.Questions {
			out.Questions = append(out.Questions, engineUserInputQuestionToWire(item))
		}
	}
	return out
}

func engineUserInputRequestFromWire(in pluginprotocol.EngineUserInputRequestV1) (out agentengine.UserInputRequest) {
	out.EngineID = agentengine.EngineID(in.EngineID)
	out.ThreadID = in.ThreadID
	out.TurnID = in.TurnID
	out.ItemID = in.ItemID
	out.Blocking = in.Blocking
	if in.Questions != nil {
		out.Questions = make([]agentengine.UserInputQuestion, 0, len(in.Questions))
		for _, item := range in.Questions {
			out.Questions = append(out.Questions, engineUserInputQuestionFromWire(item))
		}
	}
	return out
}

func engineUserInputQuestionToWire(in agentengine.UserInputQuestion) (out pluginprotocol.EngineUserInputQuestionV1) {
	out.ID = in.ID
	out.Header = in.Header
	out.Question = in.Question
	out.AllowCustom = in.AllowCustom
	out.Secret = in.Secret
	if in.Options != nil {
		out.Options = make([]pluginprotocol.EngineUserInputOptionV1, 0, len(in.Options))
		for _, item := range in.Options {
			out.Options = append(out.Options, engineUserInputOptionToWire(item))
		}
	}
	return out
}

func engineUserInputQuestionFromWire(in pluginprotocol.EngineUserInputQuestionV1) (out agentengine.UserInputQuestion) {
	out.ID = in.ID
	out.Header = in.Header
	out.Question = in.Question
	out.AllowCustom = in.AllowCustom
	out.Secret = in.Secret
	if in.Options != nil {
		out.Options = make([]agentengine.UserInputOption, 0, len(in.Options))
		for _, item := range in.Options {
			out.Options = append(out.Options, engineUserInputOptionFromWire(item))
		}
	}
	return out
}

func engineUserInputOptionToWire(in agentengine.UserInputOption) (out pluginprotocol.EngineUserInputOptionV1) {
	out.Label = in.Label
	out.Description = in.Description
	return out
}

func engineUserInputOptionFromWire(in pluginprotocol.EngineUserInputOptionV1) (out agentengine.UserInputOption) {
	out.Label = in.Label
	out.Description = in.Description
	return out
}

func engineResultToWire(in agent.LoopResult) (out pluginprotocol.EngineResultV1) {
	out.Version = 1
	out.Content = in.Content
	out.NewMessages = modelwire.DriverMessagesToWire(in.NewMessages)
	out.DurableNewMessages = modelwire.DriverMessagesToWire(in.DurableNewMessages)
	out.DurableMessagesTracked = in.DurableMessagesTracked
	out.HistoryArchiveHeadSeq = in.HistoryArchiveHeadSeq
	out.HistoryRewritten = in.HistoryRewritten
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.ContextTokens = in.ContextTokens
	out.FinishReason = string(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	out.ReceiptDiagnostic = in.ReceiptDiagnostic
	out.DriverID = in.DriverID
	out.DriverVersion = in.DriverVersion
	out.DriverContractVersion = in.DriverContractVersion
	out.DriverStatus = in.DriverStatus
	out.DriverCheckpoint = in.DriverCheckpoint
	if in.RetainedRequestContext != nil {
		r := in.RetainedRequestContext
		out.RetainedRequestContext = &pluginprotocol.DriverRetainedContext{DurableLen: r.DurableLen, DurableHash: r.DurableHash}
		for _, item := range r.Messages {
			out.RetainedRequestContext.Messages = append(out.RetainedRequestContext.Messages, pluginprotocol.DriverRetainedMessage{AfterDurable: item.AfterDurable, Message: modelwire.DriverMessagesToWire([]providers.ChatMessage{item.Message})[0]})
		}
	}
	return out
}

func engineResultFromWire(in pluginprotocol.EngineResultV1) (out agent.LoopResult, err error) {
	if in.Version != 1 {
		return out, errors.New("unsupported engine result version")
	}
	out.Content = in.Content
	out.NewMessages = modelwire.DriverMessagesFromWire(in.NewMessages)
	out.DurableNewMessages = modelwire.DriverMessagesFromWire(in.DurableNewMessages)
	out.DurableMessagesTracked = in.DurableMessagesTracked
	out.HistoryArchiveHeadSeq = in.HistoryArchiveHeadSeq
	out.HistoryRewritten = in.HistoryRewritten
	out.InputTokens = in.InputTokens
	out.OutputTokens = in.OutputTokens
	out.CacheCreationTokens = in.CacheCreationTokens
	out.CacheReadTokens = in.CacheReadTokens
	out.ContextTokens = in.ContextTokens
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.StopReason = in.StopReason
	out.Truncated = in.Truncated
	out.ReceiptDiagnostic = in.ReceiptDiagnostic
	out.DriverID = in.DriverID
	out.DriverVersion = in.DriverVersion
	out.DriverContractVersion = in.DriverContractVersion
	out.DriverStatus = in.DriverStatus
	out.DriverCheckpoint = in.DriverCheckpoint
	if in.RetainedRequestContext != nil {
		r := in.RetainedRequestContext
		out.RetainedRequestContext = &agent.RetainedRequestContextState{DurableLen: r.DurableLen, DurableHash: r.DurableHash}
		for _, item := range r.Messages {
			out.RetainedRequestContext.Messages = append(out.RetainedRequestContext.Messages, agent.RetainedContextMessage{AfterDurable: item.AfterDurable, Message: modelwire.DriverMessagesFromWire([]pluginprotocol.DriverMessageV1{item.Message})[0]})
		}
	}
	return out, nil
}

func engineEventToWire(in providers.StreamEvent) (out pluginprotocol.EngineEventV1) {
	out.Version = 1
	out.Failure = loopdriver.FailureToWire(in.Error)
	if in.Image != nil {
		value := modelwire.InputImageToWire(*in.Image)
		out.Image = &value
	}
	out.Type = string(in.Type)
	out.Content = in.Content
	out.Phase = string(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	if in.Message != nil {
		value := modelwire.DriverMessagesToWire([]providers.ChatMessage{*in.Message})[0]
		out.Message = &value
	}
	if in.ReasoningBlock != nil {
		value := modelwire.ReasoningBlockToWire(*in.ReasoningBlock)
		out.ReasoningBlock = &value
	}
	if in.ToolCall != nil {
		value := modelwire.ToolCallToWire(*in.ToolCall)
		out.ToolCall = &value
	}
	out.ToolResult = in.ToolResult
	out.ToolResultDetail = modelwire.ChatMessageToWire(providers.ChatMessage{ToolResult: in.ToolResultDetail}).ToolResult
	out.CompactReason = in.CompactReason
	out.CompactPhase = string(in.CompactPhase)
	out.CompactSummary = in.CompactSummary
	if in.TodoUpdate != nil {
		value := engineTodoUpdateToWire(*in.TodoUpdate)
		out.TodoUpdate = &value
	}
	if in.AgentActivity != nil {
		value := engineAgentActivityToWire(*in.AgentActivity)
		out.AgentActivity = &value
	}
	out.RequestContext = in.RequestContext
	if in.ProviderState != nil {
		value := engineProviderStateSummaryToWire(*in.ProviderState)
		out.ProviderState = &value
	}
	if in.ProviderItem != nil {
		value := modelwire.ProviderItemToWire(*in.ProviderItem)
		out.ProviderItem = &value
	}
	out.ProviderEventType = in.ProviderEventType
	if in.Lifecycle != nil {
		value := engineStreamLifecycleToWire(*in.Lifecycle)
		out.Lifecycle = &value
	}
	if in.Usage != nil {
		value := modelwire.TokenUsageToWire(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = string(in.FinishReason)
	out.Truncated = in.Truncated
	return out
}

func engineEventFromWire(in pluginprotocol.EngineEventV1) (out providers.StreamEvent, err error) {
	if in.Version != 1 {
		return out, errors.New("unsupported engine event version")
	}
	if in.Failure != nil {
		out.Error = &loopdriver.FailureError{Failure: *in.Failure}
	}
	if in.Image != nil {
		value := modelwire.InputImageFromWire(*in.Image)
		out.Image = &value
	}
	out.Type = providers.StreamEventType(in.Type)
	out.Content = in.Content
	out.Phase = providers.MessagePhase(in.Phase)
	out.ProviderItemID = in.ProviderItemID
	if in.Message != nil {
		value := modelwire.DriverMessagesFromWire([]pluginprotocol.DriverMessageV1{*in.Message})[0]
		out.Message = &value
	}
	if in.ReasoningBlock != nil {
		value := modelwire.ReasoningBlockFromWire(*in.ReasoningBlock)
		out.ReasoningBlock = &value
	}
	if in.ToolCall != nil {
		value := modelwire.ToolCallFromWire(*in.ToolCall)
		out.ToolCall = &value
	}
	out.ToolResult = in.ToolResult
	out.ToolResultDetail = modelwire.ChatMessageFromWire(pluginprotocol.ModelMessageV1{ToolResult: in.ToolResultDetail}).ToolResult
	out.CompactReason = in.CompactReason
	out.CompactPhase = providers.CompactPhase(in.CompactPhase)
	out.CompactSummary = in.CompactSummary
	if in.TodoUpdate != nil {
		value := engineTodoUpdateFromWire(*in.TodoUpdate)
		out.TodoUpdate = &value
	}
	if in.AgentActivity != nil {
		value := engineAgentActivityFromWire(*in.AgentActivity)
		out.AgentActivity = &value
	}
	out.RequestContext = in.RequestContext
	if in.ProviderState != nil {
		value := engineProviderStateSummaryFromWire(*in.ProviderState)
		out.ProviderState = &value
	}
	if in.ProviderItem != nil {
		value := modelwire.ProviderItemFromWire(*in.ProviderItem)
		out.ProviderItem = &value
	}
	out.ProviderEventType = in.ProviderEventType
	if in.Lifecycle != nil {
		value := engineStreamLifecycleFromWire(*in.Lifecycle)
		out.Lifecycle = &value
	}
	if in.Usage != nil {
		value := modelwire.TokenUsageFromWire(*in.Usage)
		out.Usage = &value
	}
	out.StopReason = in.StopReason
	out.FinishReason = providers.FinishReason(in.FinishReason)
	out.Truncated = in.Truncated
	return out, nil
}

func engineApprovalToWire(in agentengine.ApprovalRequest) (pluginprotocol.EngineApprovalRequestV1, error) {
	out := pluginprotocol.EngineApprovalRequestV1{Kind: string(in.Kind), EngineID: string(in.EngineID), ThreadID: in.ThreadID, TurnID: in.TurnID, ItemID: in.ItemID, Command: in.Command, CWD: in.CWD, FilePath: in.FilePath, Reason: in.Reason}
	if in.Permissions != nil {
		raw, err := json.Marshal(in.Permissions)
		if err != nil {
			return out, err
		}
		out.Permissions = raw
	}
	return out, nil
}
func engineApprovalFromWire(in pluginprotocol.EngineApprovalRequestV1) agentengine.ApprovalRequest {
	out := agentengine.ApprovalRequest{Kind: agentengine.ApprovalKind(in.Kind), EngineID: agentengine.EngineID(in.EngineID), ThreadID: in.ThreadID, TurnID: in.TurnID, ItemID: in.ItemID, Command: in.Command, CWD: in.CWD, FilePath: in.FilePath, Reason: in.Reason}
	if len(in.Permissions) > 0 {
		out.Permissions = in.Permissions
	}
	return out
}
