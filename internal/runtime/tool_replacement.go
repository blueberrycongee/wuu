package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	pathpkg "path"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/readcursor"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// replacementTool retains the native name and surface while using the selected
// plugin's schema, classification and authenticated host execution.
type replacementTool struct {
	original       tools.Tool
	definition     providers.ToolDefinition
	registration   pluginhost.ToolRegistration
	publicName     string
	owner          string
	executor       *pluginToolExecutor
	toolkit        *tools.Toolkit
	continueNative func(context.Context, providers.ToolCall) (toolresult.Result, error)
}

// RemoteWorkspaceReplacement opts in only to a remote read adapter that
// explicitly promises to consume the selected worker's native continuation.
func (r *replacementTool) RemoteWorkspaceReplacement() bool {
	if r.Name() != "read_file" {
		return false
	}
	for _, contract := range r.registration.ReplacementContracts {
		if contract == "read_file.remote.v1" {
			return true
		}
	}
	return false
}

func (r *replacementTool) ToolAvailable() bool {
	if r.toolkit.IsToolDisabled(r.publicName) || !r.executor.pluginToolAllowed(r.publicName) {
		return false
	}
	tool, ok := r.executor.host.Tool(r.publicName)
	return ok && tool.PluginID == r.owner && tool.Registration.ID == r.registration.ID
}
func (r *replacementTool) Name() string                         { return r.original.Name() }
func (r *replacementTool) Definition() providers.ToolDefinition { return r.definition }
func (r *replacementTool) IsReadOnly() bool {
	return r.original.IsReadOnly() && r.registration.Activity != nil && r.registration.Activity.ReadOnly
}
func (r *replacementTool) IsConcurrencySafe() bool {
	return r.original.IsConcurrencySafe() && r.registration.Activity != nil && r.registration.Activity.ConcurrencySafe
}
func (r *replacementTool) IsOrchestrator(args string) bool { return false }
func (r *replacementTool) DeclaredCapability() (capability.Capability, bool) {
	if d, ok := r.original.(tools.CapabilityDeclaringTool); ok {
		return d.DeclaredCapability()
	}
	return "", false
}
func (r *replacementTool) NativeToolForCall(args string) (tools.Tool, bool) {
	if r.Name() == "read_file" && r.toolkit.IsNativeReadRequest(args) {
		return r.original, true
	}
	return nil, false
}
func (r *replacementTool) Classify(args string) tools.ToolClassification {
	original := tools.ClassifyTool(r.original, args)
	activity := r.registration.Activity
	original.ReadOnly = original.ReadOnly && activity != nil && activity.ReadOnly
	original.ConcurrencySafe = original.ConcurrencySafe && activity != nil && activity.ConcurrencySafe
	if activity == nil || (activity.Risk != "low" && activity.Risk != "medium" && activity.Risk != "high") {
		original.Risk = tools.ToolRiskHigh
	}
	if activity != nil {
		original.Destructive = original.Destructive || activity.Destructive
		if activity.Risk == "high" {
			original.Risk = tools.ToolRiskHigh
		} else if activity.Risk == "medium" && original.Risk != tools.ToolRiskHigh {
			original.Risk = tools.ToolRiskMedium
		}
	}
	return original
}
func (r *replacementTool) ValidateInput(args string) error {
	if validator, ok := r.original.(tools.InputValidatingTool); ok {
		return validator.ValidateInput(args)
	}
	return nil
}
func (r *replacementTool) Execute(ctx context.Context, args string) (string, error) {
	result, err := r.ExecuteResultCall(ctx, providers.ToolCall{Name: r.Name(), Arguments: args})
	return result.TextProjection(), err
}
func (r *replacementTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	var externalRead *tools.ExternalReadScope
	var resumed *pluginhost.ReadCursorState
	var pagination bool
	var path string
	var providerArguments = call.Arguments
	nativeCall := call
	var remoteRoot string
	var remoteImage, remoteIsImage bool
	remoteEnv := r.toolkit.ExecutionEnvironment()
	remoteRead := r.Name() == "read_file" && remoteEnv != nil
	actor, actorPath := r.toolkit.ExecutionActor()
	var remoteBackend, remoteIdentity, remoteWorker, remoteDigest string
	var remoteTotal int
	var remoteCompleted atomic.Bool
	var remoteCommitKnown atomic.Bool
	var remoteTransferred atomic.Bool
	useSettlement := remoteRead && toolctx.HasSettlement(ctx)
	var remotePrepared executionenv.PreparedRead
	if remoteRead {
		if !r.RemoteWorkspaceReplacement() {
			return toolresult.Result{}, fmt.Errorf("remote read contract is required")
		}
		location, ok := remoteEnv.(interface {
			Root() string
			Backend() string
			Identity() string
		})
		if !ok || location.Root() == "" || location.Backend() == "" || location.Identity() == "" {
			return toolresult.Result{}, fmt.Errorf("authenticated remote workspace unavailable")
		}
		remoteRoot, remoteBackend, remoteIdentity = location.Root(), location.Backend(), location.Identity()
		for _, contract := range r.registration.ReplacementContracts {
			pagination = pagination || contract == "read_file.pagination.v1"
		}
		var input struct {
			Path         string `json:"path"`
			Continuation string `json:"continuation"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			return toolresult.Result{}, err
		}
		path = input.Path
		prepareArguments := call.Arguments
		if strings.HasPrefix(input.Continuation, readcursor.Prefix) {
			if !pagination {
				return toolresult.Result{}, fmt.Errorf("external read continuation unavailable")
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(call.Arguments), &fields); err != nil || len(fields) != 1 {
				return toolresult.Result{}, fmt.Errorf("external read continuation must be used alone")
			}
			owner := pluginhost.ReadCursorOwner{SessionID: r.executor.threadID, ActorID: actor, ActorPath: actorPath, CWD: r.executor.cwd, PluginID: r.owner, ToolID: r.registration.ID, RemoteBackend: remoteBackend, RemoteIdentity: remoteIdentity, RemoteRoot: remoteRoot}
			state, err := r.executor.host.ResolveReadCursor(input.Continuation, owner)
			if err != nil {
				return toolresult.Result{}, err
			}
			resumed, path, remoteWorker = &state, state.Path, state.WorkerInstance
			args := map[string]any{"path": state.Path, "offset": state.Offset}
			if state.Limit > 0 {
				args["limit"] = state.Limit
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				return toolresult.Result{}, err
			}
			prepareArguments = string(encoded)
			args["expected_sha256"] = state.NativeSHA256
			encoded, err = json.Marshal(args)
			if err != nil {
				return toolresult.Result{}, err
			}
			nativeCall.Arguments = string(encoded)
			delete(args, "expected_sha256")
			args["continuation"] = state.PluginCursor
			encoded, err = json.Marshal(args)
			if err != nil {
				return toolresult.Result{}, err
			}
			providerArguments = string(encoded)
		}
		var err error
		externalRead, remoteRoot, err = r.toolkit.PrepareRemoteExternalRead(ctx, remoteEnv, prepareArguments)
		if err != nil {
			return toolresult.Result{}, err
		}
		for _, contract := range r.registration.ReplacementContracts {
			remoteImage = remoteImage || contract == "read_file.image.v1"
		}
	} else if r.Name() == "read_file" {
		var input struct {
			Path         string `json:"path"`
			Continuation string `json:"continuation"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			return toolresult.Result{}, err
		}
		path = input.Path
		var err error
		allowImage := false
		for _, contract := range r.registration.ReplacementContracts {
			allowImage = allowImage || contract == "read_file.image.v1"
			pagination = pagination || contract == "read_file.pagination.v1"
		}
		prepareArguments := call.Arguments
		if strings.HasPrefix(input.Continuation, readcursor.Prefix) {
			if !pagination {
				return toolresult.Result{}, fmt.Errorf("external read continuation unavailable")
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(call.Arguments), &fields); err != nil || len(fields) != 1 {
				return toolresult.Result{}, fmt.Errorf("external read continuation must be used alone")
			}
			actorID, actorPath := r.toolkit.ExecutionActor()
			owner := pluginhost.ReadCursorOwner{SessionID: r.executor.threadID, ActorID: actorID, ActorPath: actorPath, CWD: r.executor.cwd, PluginID: r.owner, ToolID: r.registration.ID}
			state, err := r.executor.host.ResolveReadCursor(input.Continuation, owner)
			if err != nil {
				return toolresult.Result{}, err
			}
			resumed = &state
			path = state.Path
			args := map[string]any{"path": state.Path, "offset": state.Offset}
			if state.Limit > 0 {
				args["limit"] = state.Limit
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				return toolresult.Result{}, err
			}
			prepareArguments = string(encoded)
			if err := r.toolkit.AuthorizeExternalReadContinuation(ctx, providers.ToolCall{ID: call.ID, Name: call.Name, Arguments: prepareArguments}); err != nil {
				return toolresult.Result{}, err
			}
			args["continuation"] = state.PluginCursor
			encoded, err = json.Marshal(args)
			if err != nil {
				return toolresult.Result{}, err
			}
			providerArguments = string(encoded)
		}
		externalRead, err = r.toolkit.PrepareExternalRead(ctx, prepareArguments, allowImage)
		if err != nil {
			return toolresult.Result{}, err
		}
		if externalRead.UseNative() {
			original, ok := r.original.(tools.RichTool)
			if !ok {
				return toolresult.Result{}, fmt.Errorf("native read_file implementation unavailable")
			}
			return original.ExecuteResult(ctx, call.Arguments)
		}
	}
	if r.toolkit.IsToolDisabled(r.publicName) {
		return toolresult.Result{}, fmt.Errorf("plugin tool %q is disabled", r.publicName)
	}
	registered, ok := r.executor.host.Tool(r.publicName)
	if !ok || registered.PluginID != r.owner || registered.Registration.ID != r.registration.ID {
		return toolresult.Result{}, fmt.Errorf("replacement for %q is unavailable", r.Name())
	}
	if !r.executor.pluginToolAllowed(r.publicName) {
		return toolresult.Result{}, fmt.Errorf("replacement for %q is unavailable in this execution scope", r.Name())
	}
	if r.registration.DirectOnly && toolctx.IsNestedCall(ctx) {
		return toolresult.Result{}, fmt.Errorf("tool %q must be called directly", r.Name())
	}
	pluginCall := call
	pluginCall.Name = r.publicName
	pluginCall.Arguments = providerArguments
	metadata, _ := r.executor.ToolMetadata(pluginCall)
	var authErr error
	if remoteRead {
		authErr = r.toolkit.AuthorizeRemoteTool(ctx, remoteEnv, pluginCall, metadata)
	} else {
		authErr = r.toolkit.AuthorizeTool(ctx, pluginCall, metadata)
	}
	if authErr != nil {
		return toolresult.Result{}, authErr
	}
	input, err := r.executor.toolInput(ctx, pluginCall)
	if err != nil {
		return toolresult.Result{}, err
	}
	ctx = context.WithValue(ctx, pluginProcessToolkitKey{}, r.toolkit)
	if remoteRead {
		location := remoteEnv.(interface {
			Root() string
			Backend() string
			Identity() string
		})
		ctx = context.WithValue(ctx, remoteProcessBindingKey{}, remoteProcessBinding{root: location.Root(), backend: location.Backend(), identity: location.Identity(), actor: actor, actorPath: actorPath, executor: remoteEnv})
		// A nested invocation must not inherit a parent scope with stale policy.
		ctx = context.WithValue(ctx, pluginProcessScopeKey{}, (*tools.ProcessScope)(nil))
	}
	releasePrepared := func(prepared executionenv.PreparedRead) {
		if prepared.Abort == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if remoteCommitKnown.Load() && prepared.Release != nil {
			_ = prepared.Release(cleanup)
		} else {
			_ = prepared.Abort(cleanup)
		}
	}
	captureRemoteSnapshot := func(result toolresult.Result) error {
		if remoteWorker == "" {
			return fmt.Errorf("remote worker identity unavailable")
		}
		digest, total, err := remoteReadSnapshot(result, nativeCall)
		if err != nil {
			return err
		}
		if resumed != nil && (remoteWorker != resumed.WorkerInstance || digest != resumed.NativeSHA256 || total != resumed.TotalLines) {
			return fmt.Errorf("remote read continuation is stale")
		}
		remoteDigest, remoteTotal = digest, total
		return nil
	}
	continuation := newNativeToolContinuation(ctx, func(nativeCtx context.Context) (toolresult.Result, error) {
		if !r.ToolAvailable() {
			return toolresult.Result{}, fmt.Errorf("replacement for %q is unavailable", r.Name())
		}
		if remoteRead {
			currentActor, currentPath := r.toolkit.ExecutionActor()
			if actor == "" || currentActor != actor || currentPath != actorPath {
				return toolresult.Result{}, fmt.Errorf("remote continuation actor changed")
			}
		}
		metadata, _ := r.executor.ToolMetadata(pluginCall)
		var authErr error
		if remoteRead {
			authErr = r.toolkit.AuthorizeRemoteTool(nativeCtx, remoteEnv, pluginCall, metadata)
		} else {
			authErr = r.toolkit.AuthorizeTool(nativeCtx, pluginCall, metadata)
		}
		if authErr != nil {
			return toolresult.Result{}, authErr
		}
		var result toolresult.Result
		var err error
		if remoteRead && !useSettlement {
			if pagination {
				result, remoteWorker, err = r.toolkit.ContinueRemoteReadOnWorker(nativeCtx, remoteEnv, actor, actorPath, r.original, nativeCall, remoteWorker)
			} else {
				result, err = r.toolkit.ContinueRemoteReadOnlyOn(nativeCtx, remoteEnv, actor, actorPath, r.original, nativeCall)
			}
			if err != nil {
				return toolresult.Result{}, err
			}
			if pagination && len(result.Content) == 2 && result.Content[1].Type == toolresult.ContentTypeImage {
				if resumed != nil {
					return toolresult.Result{}, fmt.Errorf("remote read continuation changed to an image")
				}
				remoteIsImage = true
			} else if pagination {
				if err := captureRemoteSnapshot(result); err != nil {
					return toolresult.Result{}, err
				}
			}
			result, err = sanitizeRemoteReadContinuation(nativeCtx, externalRead, result, remoteImage)
			if err != nil {
				return toolresult.Result{}, err
			}
			remoteCompleted.Store(true)
			return result, nil
		}
		if remoteRead {
			if useSettlement && toolctx.IsNestedCall(nativeCtx) {
				toolctx.MarkRemoteReadExposure(nativeCtx)
			}
			var prepared executionenv.PreparedRead
			if pagination {
				prepared, err = r.toolkit.PrepareRemoteReadOnWorker(nativeCtx, remoteEnv, actor, actorPath, r.original, nativeCall, remoteWorker)
			} else {
				prepared, err = r.toolkit.PrepareRemoteReadOnlyOn(nativeCtx, remoteEnv, actor, actorPath, r.original, nativeCall)
			}
			if err != nil {
				return toolresult.Result{}, err
			}
			published := false
			defer func() {
				if !published || nativeCtx.Err() != nil {
					releasePrepared(prepared)
				}
			}()
			if pagination {
				remoteWorker = prepared.WorkerInstance()
				if remoteWorker == "" {
					return toolresult.Result{}, fmt.Errorf("remote worker identity unavailable")
				}
				if len(prepared.Result.Content) == 2 && prepared.Result.Content[1].Type == toolresult.ContentTypeImage {
					if resumed != nil {
						return toolresult.Result{}, fmt.Errorf("remote read continuation changed to an image")
					}
					remoteIsImage = true
				} else if err := captureRemoteSnapshot(prepared.Result); err != nil {
					return toolresult.Result{}, err
				}
			}
			result, err = sanitizeRemoteReadContinuation(nativeCtx, externalRead, prepared.Result, remoteImage)
			if err != nil {
				return toolresult.Result{}, err
			}
			remotePrepared = prepared
			remoteCompleted.Store(true)
			published = true
			return result, nil
		} else {
			result, err = r.continueNative(nativeCtx, call)
		}
		return result, err
	})
	defer func() {
		if remoteCompleted.Load() && !remoteTransferred.Load() {
			releasePrepared(remotePrepared)
		}
	}()
	defer continuation.close()
	ctx = context.WithValue(ctx, nativeToolContinuationKey{}, continuation)
	ctx = pluginhost.WithNativeToolContinuationBinding(ctx, r.owner)
	var result toolresult.Result
	if remoteRead {
		result, err = r.executor.host.ExecuteToolWithVisibleCWD(ctx, r.publicName, input, remoteRoot)
	} else {
		result, err = r.executor.host.ExecuteTool(ctx, r.publicName, input)
	}
	if externalRead != nil {
		toolctx.MarkUnverified(ctx, "extension")
	}
	if err != nil {
		return result, err
	}
	if externalRead != nil {
		if err := ctx.Err(); err != nil {
			return toolresult.Result{}, err
		}
	}
	authorizeRemote := func(settleCtx context.Context) (string, error) {
		if !r.ToolAvailable() {
			return "", fmt.Errorf("replacement for %q is unavailable", r.Name())
		}
		metadata, _ := r.executor.ToolMetadata(pluginCall)
		if err := r.toolkit.AuthorizeRemoteTool(settleCtx, remoteEnv, pluginCall, metadata); err != nil {
			return "", err
		}
		return r.toolkit.RecheckRemoteReadOnlyOn(settleCtx, remoteEnv, actor, actorPath, r.original, nativeCall)
	}
	if result.IsError {
		if externalRead != nil {
			if len(result.Content) != 1 || result.Content[0].Type != toolresult.ContentTypeText {
				return toolresult.Result{}, fmt.Errorf("external read_file returned an invalid error result")
			}
			if remoteRead && remoteCompleted.Load() {
				if _, err := authorizeRemote(ctx); err != nil {
					return toolresult.Result{}, err
				}
			}
			if err := ctx.Err(); err != nil {
				return toolresult.Result{}, err
			}
			return toolresult.FromErrorText(result.Content[0].Text), nil
		}
		return result, nil
	}
	if remoteRead && !remoteCompleted.Load() {
		return toolresult.Result{}, fmt.Errorf("remote read provider did not complete its worker continuation")
	}
	if externalRead != nil {
		var rendered toolresult.Result
		var err error
		if pagination && !(remoteRead && remoteIsImage) {
			var expected *int
			if remoteRead {
				expected = &remoteTotal
			} else if resumed != nil {
				expected = &resumed.TotalLines
			}
			rendered, err = externalRead.RenderPaginated(ctx, result, expected, func(cursor string, next, limit, total int) (string, error) {
				actorID, currentPath := r.toolkit.ExecutionActor()
				state := pluginhost.ReadCursorState{SessionID: r.executor.threadID, ActorID: actorID, ActorPath: currentPath, CWD: r.executor.cwd, PluginID: r.owner, ToolID: r.registration.ID, Path: path, Offset: next, Limit: limit, TotalLines: total, PluginCursor: cursor}
				if remoteRead {
					if actorID != actor || currentPath != actorPath {
						return "", fmt.Errorf("remote continuation actor changed")
					}
					state.RemoteBackend, state.RemoteIdentity, state.RemoteRoot = remoteBackend, remoteIdentity, remoteRoot
					state.WorkerInstance, state.NativeSHA256 = remoteWorker, remoteDigest
				}
				return r.executor.host.IssueReadCursor(state)
			})
			if err != nil || ctx.Err() != nil {
				if err == nil {
					err = ctx.Err()
				}
				return toolresult.Result{}, err
			}
		} else {
			rendered, err = externalRead.Render(ctx, result)
		}
		if err != nil || !remoteRead {
			return rendered, err
		}
		if !useSettlement {
			if _, err := authorizeRemote(ctx); err != nil {
				return toolresult.Result{}, err
			}
			return rendered, ctx.Err()
		}
		commit := func(settleCtx context.Context) error {
			mode, err := authorizeRemote(settleCtx)
			if err != nil {
				return err
			}
			if mode != remotePrepared.PermissionMode {
				return fmt.Errorf("remote read permission mode changed")
			}
			if remotePrepared.Commit == nil {
				return fmt.Errorf("remote read commit unavailable")
			}
			return nil
		}
		participant := &remoteReadSettlement{prepared: remotePrepared, authorize: commit, committed: &remoteCommitKnown}
		if err := toolctx.RegisterGroupedSettlement(ctx, remotePrepared.GroupKey(), participant, prepareRemoteReadSettlements, func() { releasePrepared(remotePrepared) }); err != nil {
			return toolresult.Result{}, err
		}
		remoteTransferred.Store(true)
		return rendered, nil
	}
	text, err := validateSearchReplacement(result, call.Arguments)
	if err != nil {
		return toolresult.Result{}, err
	}
	r.toolkit.RecordWebSearchEvidence(text)
	return result, nil
}

type remoteReadSettlement struct {
	prepared  executionenv.PreparedRead
	authorize func(context.Context) error
	committed *atomic.Bool
}

// Every participant is reauthorized before any receipt is sent. The worker
// applies the same-actor batch atomically; a lost acknowledgement is unknown.
func prepareRemoteReadSettlements(ctx context.Context, values []any) (*toolctx.PreparedSettlement, error) {
	participants := make([]*remoteReadSettlement, 0, len(values))
	prepared := make([]executionenv.PreparedRead, 0, len(values))
	for _, value := range values {
		participant, ok := value.(*remoteReadSettlement)
		if !ok || participant == nil || participant.authorize == nil {
			return nil, fmt.Errorf("invalid remote read participant")
		}
		if err := participant.authorize(ctx); err != nil {
			return nil, err
		}
		participants = append(participants, participant)
		prepared = append(prepared, participant.prepared)
	}
	batch, err := executionenv.PrepareReadBatch(ctx, prepared)
	if err != nil {
		return nil, err
	}
	identity := batch.Identity
	return &toolctx.PreparedSettlement{
		Identity: &toolctx.SettlementIdentity{OperationID: identity.OperationID, WorkerInstance: identity.WorkerInstance, Actor: identity.Actor, EnvironmentIdentity: identity.EnvironmentIdentity},
		Commit: func(ctx context.Context) error {
			// Staging can wait on durable storage. Recheck every participant
			// immediately before admitting the worker commit.
			for _, participant := range participants {
				if err := participant.authorize(ctx); err != nil {
					return err
				}
			}
			if err := batch.Commit(ctx); err != nil {
				if errors.Is(err, executionenv.ErrReadObservationCommitUnknown) {
					return errors.Join(toolctx.ErrSettlementOutcomeUnknown, err)
				}
				return err
			}
			for _, participant := range participants {
				participant.committed.Store(true)
			}
			return nil
		},
		Abort: batch.Abort, Release: batch.Release,
	}, nil
}

func sanitizeRemoteReadContinuation(ctx context.Context, scope *tools.ExternalReadScope, result toolresult.Result, imageAllowed bool) (toolresult.Result, error) {
	if len(result.Content) == 2 && result.Content[1].Type == toolresult.ContentTypeImage {
		return scope.AcceptRemoteNativeImage(ctx, result, imageAllowed)
	}
	return sanitizeRemoteReadResult(result)
}

// remoteReadSnapshot validates the worker-owned whole-file digest and range
// separately from the extension's unverified content. It remains host-only.
func remoteReadSnapshot(result toolresult.Result, call providers.ToolCall) (string, int, error) {
	invalid := func() (string, int, error) { return "", 0, fmt.Errorf("invalid remote read snapshot") }
	if _, err := sanitizeRemoteReadResult(result); err != nil {
		return invalid()
	}
	var args struct {
		Path     string `json:"path"`
		Offset   int    `json:"offset"`
		Limit    *int   `json:"limit"`
		Expected string `json:"expected_sha256"`
	}
	var view struct {
		SHA string `json:"content_sha256"`
	}
	var body struct {
		Path  string `json:"path"`
		Text  string `json:"text"`
		SHA   string `json:"content_sha256"`
		Start int    `json:"start_line"`
		Count int    `json:"num_lines"`
		Total int    `json:"total_lines"`
	}
	if json.Unmarshal([]byte(call.Arguments), &args) != nil || json.Unmarshal([]byte(result.Content[0].Text), &view) != nil || json.Unmarshal(result.StructuredContent, &body) != nil {
		return invalid()
	}
	decoded, err := hex.DecodeString(body.SHA)
	if err != nil || len(decoded) != 32 || body.SHA != view.SHA || (args.Expected != "" && body.SHA != args.Expected) || pathpkg.Clean(strings.ReplaceAll(body.Path, "\\", "/")) != pathpkg.Clean(strings.TrimSpace(args.Path)) {
		return invalid()
	}
	start := args.Offset
	if start <= 0 {
		start = 1
	}
	lines := strings.SplitAfter(body.Text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if body.Text == "" {
		lines = nil
	}
	count := max(0, body.Total-start+1)
	if args.Limit != nil && *args.Limit > 0 {
		count = min(count, *args.Limit)
	}
	if body.Start != start || body.Count != len(lines) || body.Count != count {
		return invalid()
	}
	return body.SHA, body.Total, nil
}

// Only the bounded structured text is transferred to an extension. Other
// worker-provided media, activity and projections are never delegated.
func sanitizeRemoteReadResult(result toolresult.Result) (toolresult.Result, error) {
	invalid := func() (toolresult.Result, error) {
		return toolresult.Result{}, fmt.Errorf("remote worker returned an unsupported read result")
	}
	if result.IsError || result.Validate() != nil || result.Activity != nil || len(result.Meta) != 0 || len(result.Content) != 1 || result.Content[0].Type != toolresult.ContentTypeText {
		return invalid()
	}
	var view struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Start  int    `json:"start_line"`
		Count  int    `json:"num_lines"`
		Total  int    `json:"total_lines"`
	}
	var body struct {
		Text       *string `json:"text"`
		Path       string  `json:"path"`
		Start      int     `json:"start_line"`
		Count      int     `json:"num_lines"`
		TotalLines *int    `json:"total_lines"`
	}
	if json.Unmarshal([]byte(result.Content[0].Text), &view) != nil || json.Unmarshal(result.StructuredContent, &body) != nil || body.Text == nil || body.TotalLines == nil || view.Action != "read" || view.Path == "" || view.Path != body.Path || view.Start != body.Start || view.Count != body.Count || view.Total != *body.TotalLines || view.Start < 1 || view.Count < 0 || *body.TotalLines < 0 || !utf8.ValidString(*body.Text) || strings.IndexByte(*body.Text, 0) >= 0 || len(*body.Text) > 256*1024 {
		return invalid()
	}
	safe, err := json.Marshal(map[string]any{"text": *body.Text, "path": body.Path, "start_line": body.Start, "num_lines": body.Count, "total_lines": *body.TotalLines})
	if err != nil {
		return invalid()
	}
	return toolresult.Result{StructuredContent: safe}, nil
}

var evidenceToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Both views, when supplied, describe exactly the same evidence.
func validateSearchReplacement(result toolresult.Result, argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	expectedVersion, err := tools.WebSearchEvidenceVersion(argsJSON)
	if err != nil {
		return "", err
	}
	if result.ModelText != nil || len(result.Content) > 1 || (len(result.Content) == 1 && result.Content[0].Type != toolresult.ContentTypeText) {
		return "", fmt.Errorf("invalid web_search replacement content")
	}
	var text []byte
	if len(result.Content) == 1 {
		text = []byte(result.Content[0].Text)
	}
	structured := result.StructuredContent
	if len(text) == 0 && len(structured) == 0 {
		return "", fmt.Errorf("missing web_search replacement result")
	}
	if len(text) > 0 && len(structured) > 0 {
		var a, b any
		if json.Unmarshal(text, &a) != nil || json.Unmarshal(structured, &b) != nil || !reflect.DeepEqual(a, b) {
			return "", fmt.Errorf("web_search result views disagree")
		}
	}
	if len(text) == 0 {
		text = structured
	}
	var response struct {
		Action   string `json:"action"`
		Query    string `json:"query"`
		Evidence struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Source    string `json:"source"`
			Tier      string `json:"source_tier"`
			Retrieved string `json:"retrieved_at"`
			Version   string `json:"version_matched_to_repo"`
		} `json:"evidence"`
		Results json.RawMessage `json:"results"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(text, &response); err != nil || response.Action != "web_search" || response.Query != args.Query || !evidenceToken.MatchString(response.Evidence.ID) || response.Evidence.Kind != "search" || !evidenceToken.MatchString(response.Evidence.Tier) || strings.TrimSpace(response.Evidence.Source) == "" || response.Evidence.Version != expectedVersion {
		return "", fmt.Errorf("invalid web_search replacement result")
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Evidence.Retrieved); err != nil {
		return "", fmt.Errorf("invalid web_search evidence timestamp")
	}
	if len(response.Results) == 0 {
		if strings.TrimSpace(response.Error) == "" {
			return "", fmt.Errorf("web_search result requires results or error")
		}
	} else {
		if strings.TrimSpace(response.Error) != "" {
			return "", fmt.Errorf("web_search cannot return results and error together")
		}
		var items []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		}
		if err := json.Unmarshal(response.Results, &items); err != nil || len(items) > 10 {
			return "", fmt.Errorf("invalid web_search results")
		}
		for _, item := range items {
			if strings.TrimSpace(item.URL) == "" {
				return "", fmt.Errorf("invalid web_search result URL")
			}
		}
	}
	// Preserve provider-specific output while recording only validated search evidence.
	evidenceJSON, err := json.Marshal(response)
	return string(evidenceJSON), err
}

func configureToolReplacements(executor agent.ToolExecutor, kit *tools.Toolkit, bindings map[string]config.ToolReplacement) error {
	if len(bindings) == 0 {
		kit.SetReplacementBindings(nil)
		if e, ok := executor.(*pluginToolExecutor); ok {
			e.replacementTargets = nil
		}
		return kit.SetNativeToolReplacements(nil)
	}
	e, ok := executor.(*pluginToolExecutor)
	if !ok {
		return fmt.Errorf("tool replacements require native plugin tool executor")
	}
	replacements := make(map[string]func(tools.Tool) tools.Tool, len(bindings))
	definitions := e.host.ToolDefinitions()
	for name, binding := range bindings {
		if name != "web_search" && name != "read_file" {
			return fmt.Errorf("tool %q cannot be replaced", name)
		}
		var definition providers.ToolDefinition
		var registration pluginhost.ToolRegistration
		var publicName string
		for _, d := range definitions {
			tool, found := e.host.Tool(d.Name)
			if found && tool.PluginID == binding.PluginID && tool.Registration.ID == binding.ToolID {
				if publicName != "" {
					return fmt.Errorf("ambiguous replacement for %q", name)
				}
				publicName = d.Name
				definition = d
				registration = tool.Registration
			}
		}
		if publicName == "" {
			return fmt.Errorf("replacement for %q not found", name)
		}
		if registration.Activity != nil && registration.Activity.Orchestrator {
			return fmt.Errorf("replacement for %q cannot be an orchestrator", name)
		}
		if name == "read_file" {
			if err := validateExternalReadRegistration(registration); err != nil {
				return err
			}
		}
		definition.Name = name
		replacements[name] = func(original tools.Tool) tools.Tool {
			return &replacementTool{original: original, definition: definition, registration: registration, publicName: publicName, executor: e, toolkit: kit, owner: binding.PluginID,
				continueNative: func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
					if call.Name == "read_file" {
						return kit.ContinueRemoteReadOnly(ctx, original, call)
					}
					return kit.ContinueNativeWebSearch(ctx, original, call)
				}}
		}
	}
	if err := kit.SetNativeToolReplacements(replacements); err != nil {
		return err
	}
	kit.SetReplacementBindings(bindings)
	e.replacementTargets = make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		for _, d := range definitions {
			tool, found := e.host.Tool(d.Name)
			if found && tool.PluginID == binding.PluginID && tool.Registration.ID == binding.ToolID {
				e.replacementTargets[d.Name] = struct{}{}
			}
		}
	}
	return nil
}

// validateToolReplacements inspects only initialized declarations and a local
// registry; it creates no MCP or process resources.
func validateToolReplacements(root string, host *pluginhost.Host, bindings map[string]config.ToolReplacement) error {
	if len(bindings) == 0 {
		return nil
	}
	selected, found := host.ServiceRegistry().ProviderPluginIDFor(toolExecutionConsumer, toolservice.ServiceName, 1)
	if !found || selected != toolservice.DefaultModule {
		return fmt.Errorf("tool replacements require the native agent.tools provider")
	}
	kit, err := tools.New(root)
	if err != nil {
		return err
	}
	replacements := make(map[string]func(tools.Tool) tools.Tool, len(bindings))
	for name, binding := range bindings {
		if name != "web_search" && name != "read_file" {
			return fmt.Errorf("tool %q cannot be replaced", name)
		}
		tool, ok := host.DeclaredTool(binding.PluginID, binding.ToolID)
		if !ok {
			return fmt.Errorf("replacement for %q not found", name)
		}
		if tool.Registration.Activity != nil && tool.Registration.Activity.Orchestrator {
			return fmt.Errorf("replacement for %q cannot be an orchestrator", name)
		}
		if name == "read_file" {
			if err := validateExternalReadRegistration(tool.Registration); err != nil {
				return err
			}
		}
		d := providers.ToolDefinition{Name: name, Description: tool.Registration.Description, InputSchema: tool.Registration.InputSchema, DirectOnly: tool.Registration.DirectOnly}
		replacements[name] = func(original tools.Tool) tools.Tool { return &replacementTool{original: original, definition: d} }
	}
	return kit.SetNativeToolReplacements(replacements)
}

// read_file.text.v1 is a read-only extension text provider; old host cursors
// and host-managed session artifacts retain their original native reader.
func validateExternalReadRegistration(registration pluginhost.ToolRegistration) error {
	if registration.Activity == nil || !registration.Activity.ReadOnly || registration.Activity.Orchestrator {
		return fmt.Errorf("read_file replacement requires a read-only leaf")
	}
	for _, contract := range registration.ReplacementContracts {
		if contract == "read_file.text.v1" {
			return nil
		}
	}
	return fmt.Errorf("read_file replacement requires read_file.text.v1")
}
