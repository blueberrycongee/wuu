package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type pluginClientStarter func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error)

func startPluginClient(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
	return pluginhost.Start(ctx, cfg)
}

func startPluginHost(plugins []pluginpkg.Plugin, projectRoot, workspaceID, wuuHome, workspaceStateDir string, turnRouter *PluginSessionRouter, userQuestions *pluginhost.UserQuestionBroker) (*pluginhost.Host, *kernelHostServices) {
	host, kernel, err := buildPluginHost(plugins, projectRoot, workspaceID, wuuHome, workspaceStateDir, nil, startPluginClient, turnRouter, userQuestions)
	if err != nil {
		return pluginhost.New(pluginhost.Failed("capability-negotiation", err)), nil
	}
	if err := activatePluginHost(context.Background(), host); err != nil {
		providers.DebugLogf("activate initial plugin generation: %v", err)
	}
	return host, kernel
}

func activatePluginHost(ctx context.Context, host *pluginhost.Host) error {
	if registry := host.ServiceRegistry(); registry != nil {
		registry.Activate()
	}
	return host.Activate(ctx)
}

func buildPluginHost(plugins []pluginpkg.Plugin, projectRoot, workspaceID, wuuHome, workspaceStateDir string, required map[string]bool, start pluginClientStarter, turnRouter *PluginSessionRouter, userQuestions *pluginhost.UserQuestionBroker) (*pluginhost.Host, *kernelHostServices, error) {
	host := pluginhost.New()
	var started []pluginhost.Client
	kernel := newKernelHostServices(func() uint64 {
		epoch, err := session.ReadPluginGenerationEpoch(wuuHome)
		if err != nil {
			return 0
		}
		return epoch
	}, host)
	kernel.bindUserQuestions(userQuestions)
	kernel.bindWorkspaceStateDir(workspaceStateDir)
	kernel.bindWuuHome(wuuHome)
	host.SetToolResultMaterializer(kernel.materializeToolResult)
	registry, conflicts := pluginhost.BuildServiceRegistry(kernel)
	kernel.bindRegistry(registry)
	closeStarted := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var err error
		for index := len(started) - 1; index >= 0; index-- {
			err = errors.Join(err, started[index].Close(ctx))
		}
		return err
	}
	for _, item := range plugins {
		if item.Runtime == nil {
			continue
		}
		timeout := time.Duration(item.Runtime.Timeout) * time.Second
		handler := newPluginHostServices(item, projectRoot, wuuHome, turnRouter)
		kernel.add(item.ID, handler)
		registry.AllowPreflight(item.ID, pluginhost.KernelPreflightRequirements())
		client, err := start(context.Background(), pluginhost.ProcessConfig{
			ID:                item.ID,
			Command:           item.Runtime.Command,
			Args:              item.Runtime.Args,
			Env:               item.Runtime.Env,
			PluginRoot:        item.Root,
			ProjectRoot:       projectRoot,
			WorkspaceID:       workspaceID,
			WuuHome:           wuuHome,
			WorkspaceStateDir: workspaceStateDir,
			Timeout:           timeout,
			ServiceRouter:     registry,
			PrepareOnly:       true,
		})
		if err != nil {
			host.Add(pluginhost.Failed(item.ID, err))
			if required[item.ID] || pluginhost.IsCapabilityNegotiationError(err) {
				closeErr := closeStarted()
				return nil, nil, pluginActivationError(item.ID, err, closeErr)
			}
			continue
		}
		started = append(started, client)
	}
	// The service registry is built from the whole generation's initialize
	// results before any client is registered, so an unsatisfied required
	// service blocks that consumer without its capabilities ever going live.
	// There is no dependency solver; failures are deterministic diagnostics.
	conflicts = append(conflicts, registry.RegisterClients(started...)...)
	host.AttachServiceRegistry(registry, conflicts)
	for _, client := range started {
		if serviceClient, ok := client.(pluginhost.ServiceClient); ok {
			if err := registry.CheckSatisfaction(serviceClient.RequiredServices()); err != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = client.Close(ctx)
				cancel()
				host.Add(pluginhost.Failed(client.ID(), err))
				continue
			}
		}
		host.Add(client)
	}
	if err := host.ValidateCapabilities(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr := host.Close(ctx)
		cancel()
		return nil, nil, errors.Join(err, closeErr)
	}
	return host, kernel, nil
}

func pluginActivationError(id string, startErr, closeErr error) error {
	err := fmt.Errorf("activate plugin %q: %w", id, startErr)
	if closeErr != nil {
		return fmt.Errorf("%w (close candidate generation: %v)", err, closeErr)
	}
	return err
}

func pluginRequestInterceptor(host *pluginhost.Host, provider, threadID, cwd string) func(context.Context, *providers.ChatRequest) error {
	return pluginRequestInterceptorWithTransforms(host, buildPluginRequestTransforms(host, provider, threadID, cwd), provider, threadID, cwd)
}

func pluginPreStepInjector(host *pluginhost.Host, provider, model, threadID, cwd string) func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error) {
	if host == nil || len(host.Capabilities(pluginhost.CapabilityAgentPreStep)) == 0 {
		return nil
	}
	return func(ctx context.Context, stepIndex int, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
		var messages []providers.ChatMessage
		currentHistory := providers.CloneChatMessages(history)
		for _, capability := range host.Capabilities(pluginhost.CapabilityAgentPreStep) {
			output := pluginhost.AgentPreStepOutput{}
			if err := host.InvokeCapability(ctx, capability, pluginhost.AgentPreStepInput{
				SessionID: threadID,
				ThreadID:  threadID,
				CWD:       cwd,
				Provider:  provider,
				Model:     model,
				StepIndex: stepIndex,
				Messages:  modelMessageViewsV1(currentHistory),
			}, &output); err != nil {
				if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
					return nil, policyErr
				}
				continue
			}
			converted, err := pluginPreStepMessages(capability.PluginID, output.AppendMessages)
			if err != nil {
				if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
					return nil, policyErr
				}
				continue
			}
			messages = append(messages, converted...)
			currentHistory = append(currentHistory, providers.CloneChatMessages(converted)...)
		}
		return messages, nil
	}
}

func pluginPreStepMessages(pluginID string, input []pluginhost.AgentPreStepMessage) ([]providers.ChatMessage, error) {
	if len(input) > pluginhost.MaxPreStepMessages {
		return nil, fmt.Errorf("plugin %q pre-step exceeds %d messages", pluginID, pluginhost.MaxPreStepMessages)
	}
	seen := make(map[string]struct{}, len(input))
	messages := make([]providers.ChatMessage, 0, len(input))
	total := 0
	for _, inputMessage := range input {
		id := strings.TrimSpace(inputMessage.ID)
		if id == "" || len([]byte(id)) > pluginhost.MaxPreStepMessageIDBytes || !validPluginContributionID(id) {
			return nil, fmt.Errorf("plugin %q pre-step message has invalid id %q", pluginID, id)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("plugin %q pre-step message id %q is duplicated", pluginID, id)
		}
		seen[id] = struct{}{}
		content := strings.TrimSpace(inputMessage.Content)
		if content == "" {
			return nil, fmt.Errorf("plugin %q pre-step message %q has empty content", pluginID, id)
		}
		size := len([]byte(content))
		if size > pluginhost.MaxPreStepMessageBytes {
			return nil, fmt.Errorf("plugin %q pre-step message %q exceeds %d bytes", pluginID, id, pluginhost.MaxPreStepMessageBytes)
		}
		total += size
		if total > pluginhost.MaxPreStepTotalBytes {
			return nil, fmt.Errorf("plugin %q pre-step exceeds %d total bytes", pluginID, pluginhost.MaxPreStepTotalBytes)
		}
		messages = append(messages, providers.ChatMessage{
			Role: "user", Content: content, Hidden: true, ReadOnly: true,
			Origin: "plugin", OriginID: pluginID + ":" + id,
			Cause: pluginhost.CapabilityAgentPreStep,
		})
	}
	return messages, nil
}

func validPluginContributionID(id string) bool {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func pluginRequestInterceptorWithTransforms(host *pluginhost.Host, transforms *agent.RequestTransformChain, provider, threadID, cwd string) func(context.Context, *providers.ChatRequest) error {
	if transforms == nil || transforms.Count() == 0 {
		return nil
	}
	return func(ctx context.Context, request *providers.ChatRequest) error {
		if request == nil {
			return nil
		}
		return transforms.Apply(ctx, request, nil)
	}
}

func buildPluginRequestTransforms(host *pluginhost.Host, provider, threadID, cwd string) *agent.RequestTransformChain {
	chain := agent.NewRequestTransformChain()
	if host == nil {
		return chain
	}
	for _, registered := range host.Capabilities(pluginhost.CapabilityAgentRequestTransform) {
		capability := registered
		key := capability.PluginID + ":" + capability.Descriptor.ID
		chain.AddWithOwner(agent.NewRequestTransform(key, func(ctx context.Context, request *providers.ChatRequest) error {
			output := pluginhost.RequestTransformOutput{}
			if err := host.InvokeCapability(ctx, capability, pluginhost.RequestTransformInput{
				SessionID: threadID,
				ThreadID:  threadID,
				CWD:       cwd,
				Provider:  provider,
				StepIndex: request.StepIndex,
				Request:   modelRequestViewV1(request),
			}, &output); err != nil {
				return host.HandleCapabilityError(capability, err)
			}
			if err := applyRequestTransformPatch(request, output); err != nil {
				return host.HandleCapabilityError(capability, fmt.Errorf("plugin %q request transform patch: %w", capability.PluginID, err))
			}
			return nil
		}, capability.Descriptor.Priority), capability.PluginID)
	}
	return chain
}

func modelRequestViewV1(request *providers.ChatRequest) pluginhost.ModelRequestViewV1 {
	view := pluginhost.ModelRequestViewV1{Version: 1}
	if request == nil {
		return view
	}
	view.Model = request.Model
	view.Temperature = request.Temperature
	view.MaxTokens = request.MaxTokens
	view.Effort = request.Effort
	view.NativeDeferredToolDiscovery = request.NativeDeferredToolDiscovery
	view.ForceToolName = request.ForceToolName
	view.Messages = modelMessageViewsV1(request.Messages)
	view.Tools = make([]pluginhost.ModelToolViewV1, 0, len(request.Tools))
	for _, tool := range request.Tools {
		view.Tools = append(view.Tools, pluginhost.ModelToolViewV1{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, DeferLoading: tool.DeferLoading,
		})
	}
	return view
}

func modelMessageViewsV1(messages []providers.ChatMessage) []pluginhost.ModelMessageViewV1 {
	view := make([]pluginhost.ModelMessageViewV1, 0, len(messages))
	for _, message := range messages {
		item := pluginhost.ModelMessageViewV1{
			Role: message.Role, Name: message.Name, Content: message.Content, Hidden: message.Hidden,
			Origin: message.Origin, OriginID: message.OriginID, Cause: message.Cause, ReadOnly: message.ReadOnly,
			HasImages: len(message.Images) != 0, HasFiles: len(message.Files) != 0,
			ToolCallID: message.ToolCallID, HasToolResult: message.ToolResult != nil,
		}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, pluginhost.ModelToolCallViewV1{
				ID: call.ID, Name: call.Name, Arguments: call.Arguments, Kind: string(call.Kind),
			})
		}
		for _, tool := range message.DiscoveredTools {
			item.DiscoveredTools = append(item.DiscoveredTools, tool.Name)
		}
		view = append(view, item)
	}
	return view
}

func applyRequestTransformPatch(request *providers.ChatRequest, patch pluginhost.RequestTransformOutput) error {
	if request == nil || len(patch.PrependSystemMessages) == 0 {
		return nil
	}
	if len(patch.PrependSystemMessages) > 16 {
		return errors.New("prepend_system_messages exceeds 16 entries")
	}
	prefix := make([]providers.ChatMessage, 0, len(patch.PrependSystemMessages))
	totalBytes := 0
	for _, content := range patch.PrependSystemMessages {
		content = strings.TrimSpace(content)
		if content == "" {
			return errors.New("prepend_system_messages contains an empty message")
		}
		totalBytes += len([]byte(content))
		if totalBytes > 64*1024 {
			return errors.New("prepend_system_messages exceeds 65536 bytes")
		}
		prefix = append(prefix, providers.ChatMessage{Role: "system", Content: content, Hidden: true})
	}
	request.Messages = append(prefix, request.Messages...)
	return nil
}

func buildPluginAgentCapabilities(ctx context.Context, host *pluginhost.Host, provider, model, cwd string) (*agent.SystemPromptAssembler, *agent.CompactionRegistry, error) {
	prompts := agent.NewSystemPromptAssembler()
	compactions := agent.NewCompactionRegistry()
	compactions.Default = agent.DefaultContextWindowProvider{}
	if host == nil {
		return prompts, compactions, nil
	}
	for _, registered := range host.Capabilities(pluginhost.CapabilityAgentSystemPromptSection) {
		output := pluginhost.SystemPromptSectionOutput{}
		if err := host.InvokeCapability(ctx, registered, pluginhost.SystemPromptSectionInput{
			CWD: cwd, Provider: provider, Model: model,
		}, &output); err != nil {
			if policyErr := host.HandleCapabilityError(registered, err); policyErr != nil {
				return nil, nil, policyErr
			}
			continue
		}
		key := registered.PluginID + ":" + registered.Descriptor.ID
		prompts.AddWithOwner(agent.NewStaticPromptSection(key, output.Text, registered.Descriptor.Priority), registered.PluginID)
	}
	for _, registered := range host.Capabilities(pluginhost.CapabilityAgentCompaction) {
		capability := registered
		key := capability.PluginID + ":" + capability.Descriptor.ID
		compactions.RegisterWithOwner(&pluginCompactionProvider{
			key: key, priority: capability.Descriptor.Priority, host: host, capability: capability,
		}, capability.PluginID)
	}
	return prompts, compactions, nil
}

type pluginCompactionProvider struct {
	key        string
	priority   int
	host       *pluginhost.Host
	capability pluginhost.RegisteredCapability
}

func (p *pluginCompactionProvider) CompactionKey() string   { return p.key }
func (p *pluginCompactionProvider) CompactionPriority() int { return p.priority }
func (p *pluginCompactionProvider) CompactionNotesEnabled() bool {
	return p.capability.Descriptor.Version == 2
}
func (p *pluginCompactionProvider) ContextWindowsEnabled() bool {
	return p.capability.Descriptor.Version >= 3
}
func (p *pluginCompactionProvider) PlanCompactionNote(ctx context.Context, model string, messages, delta []providers.ChatMessage, previous agent.CompactionNote) (agent.CompactionNotePlan, error) {
	if !p.CompactionNotesEnabled() {
		return agent.CompactionNotePlan{}, agent.ErrCompactionNoteUnsupported
	}
	// With no prior note, messages and delta describe the same transcript.
	// Sending both doubles the largest plugin-protocol frame for no additional
	// information; update plans still receive only the messages added since the
	// previous checkpoint.
	pluginDelta := delta
	if strings.TrimSpace(previous.Markdown) == "" {
		pluginDelta = nil
	}
	output := pluginhost.CompactionOutput{}
	if err := p.host.InvokeCapability(ctx, p.capability, pluginhost.CompactionInput{
		Operation: "note_plan", Model: model, Messages: providers.CloneChatMessages(messages),
		Delta: providers.CloneChatMessages(pluginDelta), PreviousNote: previous.Markdown,
		PreviousCoveredMessages: previous.CoveredMessages,
	}, &output); err != nil {
		if policyErr := p.host.HandleCapabilityError(p.capability, err); policyErr != nil {
			return agent.CompactionNotePlan{}, policyErr
		}
		return agent.CompactionNotePlan{}, err
	}
	if strings.TrimSpace(output.NotePrompt) == "" {
		return agent.CompactionNotePlan{}, agent.ErrCompactionNoteUnsupported
	}
	return agent.CompactionNotePlan{
		Prompt: output.NotePrompt, IntervalTokens: output.CheckpointIntervalTokens, MaxBytes: output.MaxNoteBytes,
	}, nil
}

func (p *pluginCompactionProvider) PlanHandoffBrief(ctx context.Context, model string, messages []providers.ChatMessage, previous agent.CompactionNote, intent, sourceSessionID string, sourceThroughSeq int) (agent.CompactionNotePlan, error) {
	if p.capability.Descriptor.Version < 2 {
		return agent.CompactionNotePlan{}, agent.ErrCompactionNoteUnsupported
	}
	output := pluginhost.CompactionOutput{}
	if err := p.host.InvokeCapability(ctx, p.capability, pluginhost.CompactionInput{
		Operation: "handoff_brief_plan", Model: model, Messages: providers.CloneChatMessages(messages),
		PreviousNote: previous.Markdown, PreviousCoveredMessages: previous.CoveredMessages,
		Intent: intent, SourceSessionID: sourceSessionID, SourceThroughSeq: sourceThroughSeq,
	}, &output); err != nil {
		if policyErr := p.host.HandleCapabilityError(p.capability, err); policyErr != nil {
			return agent.CompactionNotePlan{}, policyErr
		}
		return agent.CompactionNotePlan{}, err
	}
	if strings.TrimSpace(output.NotePrompt) == "" {
		return agent.CompactionNotePlan{}, agent.ErrCompactionNoteUnsupported
	}
	return agent.CompactionNotePlan{Prompt: output.NotePrompt, MaxBytes: output.MaxNoteBytes}, nil
}

func (p *pluginCompactionProvider) CompactWithNote(ctx context.Context, model string, messages []providers.ChatMessage, note agent.CompactionNote) (agent.CompactionNoteReplacement, error) {
	output := pluginhost.CompactionOutput{}
	if err := p.host.InvokeCapability(ctx, p.capability, pluginhost.CompactionInput{
		Operation: "compact_with_note", Model: model, Messages: providers.CloneChatMessages(messages),
		Note: note.Markdown, CoveredMessages: note.CoveredMessages,
	}, &output); err != nil {
		if policyErr := p.host.HandleCapabilityError(p.capability, err); policyErr != nil {
			return agent.CompactionNoteReplacement{}, policyErr
		}
		return agent.CompactionNoteReplacement{}, err
	}
	compacted := providers.CloneChatMessages(output.Messages)
	if err := providers.ValidateToolCallHistory(compacted); err != nil {
		return agent.CompactionNoteReplacement{}, fmt.Errorf("plugin compaction returned invalid tool-call history: %w", err)
	}
	if output.CoveredMessages <= 0 || output.CoveredMessages > len(compacted) {
		return agent.CompactionNoteReplacement{}, fmt.Errorf("plugin compaction returned invalid note coverage %d for %d messages", output.CoveredMessages, len(compacted))
	}
	return agent.CompactionNoteReplacement{Messages: compacted, CoveredMessages: output.CoveredMessages}, nil
}

func (p *pluginCompactionProvider) Compact(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	output := pluginhost.CompactionOutput{}
	if err := p.host.InvokeCapability(ctx, p.capability, pluginhost.CompactionInput{
		Model: model, Messages: providers.CloneChatMessages(messages),
	}, &output); err != nil {
		if policyErr := p.host.HandleCapabilityError(p.capability, err); policyErr != nil {
			return nil, policyErr
		}
		return providers.CloneChatMessages(messages), nil
	}
	if output.Unavailable {
		return nil, agent.ErrCompactionUnavailable
	}
	compacted := providers.CloneChatMessages(output.Messages)
	if err := providers.ValidateToolCallHistory(compacted); err != nil {
		return nil, fmt.Errorf("plugin compaction returned invalid tool-call history: %w", err)
	}
	return compacted, nil
}
