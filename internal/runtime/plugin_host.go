package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type pluginClientStarter func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error)

func startPluginClient(ctx context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
	return pluginhost.Start(ctx, cfg)
}

func startPluginHost(plugins []pluginpkg.Plugin, projectRoot, workspaceID, wuuHome, workspaceStateDir string, turnRouter *PluginSessionRouter, userQuestions *pluginhost.UserQuestionBroker, selection *composition.Config, driverProfile string) (*pluginhost.Host, *kernelHostServices, error) {
	host, kernel, err := buildPluginHost(plugins, projectRoot, workspaceID, wuuHome, workspaceStateDir, nil, startPluginClient, turnRouter, userQuestions, selection, driverProfile)
	if err != nil {
		// A fatal negotiation closes the entire prepared generation. Preserve
		// each unavailable runtime and the graph so initial-session consumers
		// cannot keep their hooks, skills, or MCP servers alive without it.
		failed := newPluginHostWithRequirements(plugins)
		for _, item := range plugins {
			if item.Runtime != nil {
				failed.Add(pluginhost.Failed(item.ID, err))
			}
		}
		failed.BlockFailedDependencies(context.Background())
		return failed, nil, err
	}
	if err := activatePluginHost(context.Background(), host); err != nil {
		providers.DebugLogf("activate initial plugin generation: %v", err)
	}
	return host, kernel, nil
}

func activatePluginHost(ctx context.Context, host *pluginhost.Host) error {
	return host.Activate(ctx)
}

func buildPluginHost(plugins []pluginpkg.Plugin, projectRoot, workspaceID, wuuHome, workspaceStateDir string, required map[string]bool, start pluginClientStarter, turnRouter *PluginSessionRouter, userQuestions *pluginhost.UserQuestionBroker, selection *composition.Config, driverProfile string) (*pluginhost.Host, *kernelHostServices, error) {
	plugins, resolved, err := selectCompositionPlugins(selection, plugins)
	if err != nil {
		return nil, nil, err
	}
	registry, err := pluginhost.NewServiceRegistryWithBindings(resolved.Services)
	if err != nil {
		return nil, nil, err
	}
	host := newPluginHostWithRequirements(plugins)
	var started []pluginhost.Client
	kernel := newKernelHostServices(func() uint64 {
		epoch, err := session.ReadPluginGenerationEpoch(wuuHome)
		if err != nil {
			return 0
		}
		return epoch
	}, host)
	nativeConsumers := runtimeServiceConsumers(driverProfile)
	required = maps.Clone(required)
	if required == nil {
		required = make(map[string]bool)
	}
	var nativeModules []pluginhost.Client
	if resolved.Includes(kernel.ID()) {
		nativeModules = append(nativeModules, kernel)
		kernel.bindUserQuestions(userQuestions)
		kernel.bindWorkspaceStateDir(workspaceStateDir)
		kernel.bindWuuHome(wuuHome)
		host.SetToolResultMaterializer(kernel.materializeToolResult)
		kernel.bindRegistry(registry)
	}
	if resolved.Includes(loopdriver.DefaultDriverID) {
		nativeModules = append(nativeModules, loopdriver.NewService(loopdriver.DefaultDriverID, DriverServicePrefix+"default", agent.DefaultDriver{}, registry, []pluginhost.ServiceRequirement{
			{Name: pluginprotocol.DriverTurnService, MajorVersion: pluginprotocol.DriverTurnMajor, Required: true},
			{Name: loopdriver.DriverCheckpointService, MajorVersion: 1, Required: true},
		}))
		if resolved.Modules != nil {
			required[loopdriver.DefaultDriverID] = true
		}
	}
	for _, consumer := range nativeConsumers {
		if resolved.Includes(consumer.ID()) {
			nativeModules = append(nativeModules, consumer)
			required[consumer.ID()] = true
		}
	}
	for _, module := range nativeModules {
		host.Add(module)
	}
	diagnostic := pluginhost.CompositionSnapshot{Profile: resolved.Profile, ModuleSource: resolved.ModuleSource, Layers: resolved.Layers}
	for _, module := range nativeModules {
		diagnostic.Modules = append(diagnostic.Modules, pluginhost.CompositionModuleSnapshot{ID: module.ID(), Source: resolved.SourceForModule(module.ID()), Version: currentWuuVersion()})
	}
	for _, item := range plugins {
		diagnostic.Modules = append(diagnostic.Modules, pluginhost.CompositionModuleSnapshot{ID: item.ID, Source: resolved.SourceForModule(item.ID), Version: item.Version})
	}
	registry.SetComposition(diagnostic)
	conflicts := registry.RegisterClients(nativeModules...)
	closeStarted := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := host.Close(ctx)
		registry.Close(ctx)
		return err
	}
	for _, item := range plugins {
		if resolved.Modules != nil {
			required[item.ID] = true
		}
		if dependencyErr := host.DependencyFailures()[item.ID]; dependencyErr != nil {
			host.Add(pluginhost.Failed(item.ID, dependencyErr))
			if required[item.ID] {
				return nil, nil, pluginActivationError(item.ID, dependencyErr, closeStarted())
			}
			continue
		}
		if item.Runtime == nil {
			continue
		}
		timeout := time.Duration(item.Runtime.Timeout) * time.Second
		var onClose func()
		if resolved.Includes(kernel.ID()) {
			handler := newPluginHostServices(item, projectRoot, wuuHome, turnRouter)
			kernel.add(item.ID, handler)
			onClose = handler.close
		}
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
			OnClose:           onClose,
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
		host.Add(client)
	}
	// Resolve the complete graph before activation. Prepared registrations are
	// unpublished; missing transitive dependencies cannot expose capabilities.
	conflicts = append(conflicts, registry.RegisterClients(started...)...)
	host.AttachServiceRegistry(registry, conflicts)
	if err := registry.ValidateBindings(); err != nil {
		return nil, nil, errors.Join(err, closeStarted())
	}
	if _, err := host.DependencyOrder(); err != nil {
		return nil, nil, errors.Join(err, closeStarted())
	}
	host.BlockFailedDependencies(context.Background())
	if err := registry.ValidateBindings(); err != nil {
		return nil, nil, errors.Join(err, closeStarted())
	}
	for id, failure := range host.DependencyFailures() {
		if required[id] {
			return nil, nil, pluginActivationError(id, failure, closeStarted())
		}
	}
	if err := host.ValidateCapabilities(); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr := host.Close(ctx)
		cancel()
		return nil, nil, errors.Join(err, closeErr)
	}
	if err := host.Prepare(context.Background()); err != nil {
		return nil, nil, errors.Join(err, closeStarted())
	}
	return host, kernel, nil
}

// Recovery admits no module or service binding, including unavailable choices
// in the user's composition. Discovery remains available for diagnostics.
func runtimeComposition(selection *composition.Config, safeMode bool) *composition.Config {
	if !safeMode {
		return selection
	}
	modules := []string{}
	return &composition.Config{Version: composition.Version, Modules: &modules}
}

// Apply the same module selection before package capabilities and runtime
// processes are materialized. An explicit list never enables an untrusted package.
func selectCompositionPlugins(selection *composition.Config, candidates []pluginpkg.Plugin) ([]pluginpkg.Plugin, composition.Resolved, error) {
	resolved, err := selection.Resolve()
	if err != nil {
		return nil, resolved, err
	}
	available := []string{kernelHostModule, loopdriver.DefaultDriverID, toolExecutionConsumer, sessionExecutionConsumer}
	identities := map[string]bool{}
	for _, id := range available {
		identities[id] = true
	}
	var selected []pluginpkg.Plugin
	for _, item := range candidates {
		if identities[item.ID] {
			return nil, resolved, fmt.Errorf("duplicate runtime identity %q in composition", item.ID)
		}
		identities[item.ID] = true
		available = append(available, item.ID)
		if resolved.Includes(item.ID) {
			selected = append(selected, item)
		}
	}
	if err := resolved.ValidateAvailable(available); err != nil {
		return nil, resolved, err
	}
	return selected, resolved, nil
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
				// Thread-local chains can outlive explicit disable within a turn.
				// Failed live registrations still follow their declared error policy.
				if errors.Is(err, pluginhost.ErrCapabilityRetired) {
					return nil
				}
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

// availablePluginPackages keeps independent declarative features of a failed
// runtime, but excludes every package whose required runtime is unavailable.
func availablePluginPackages(plugins []pluginpkg.Plugin, host *pluginhost.Host) []pluginpkg.Plugin {
	blocked := host.DependencyFailures()
	available := make([]pluginpkg.Plugin, 0, len(plugins))
	for _, item := range plugins {
		if blocked[item.ID] == nil {
			available = append(available, item)
		}
	}
	return available
}

func newPluginHostWithRequirements(plugins []pluginpkg.Plugin) *pluginhost.Host {
	host := pluginhost.New()
	requirements := make(map[string][]string, len(plugins))
	for _, item := range plugins {
		requirements[item.ID] = item.RequiredPluginIDs()
	}
	host.SetPackageRequirements(requirements)
	return host
}
