package tools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// RestoreRemoteNativeRead resolves the legacy native cursor without trusting it
// as an authority. It returns a path-bearing call for fresh host authorization;
// the worker independently validates the captured file snapshot and range.
func (t *Toolkit) RestoreRemoteNativeRead(call providers.ToolCall) (providers.ToolCall, bool, error) {
	var input struct {
		Path         string `json:"path"`
		Continuation string `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return providers.ToolCall{}, false, err
	}
	if len(input.Continuation) > 12000 {
		return providers.ToolCall{}, false, errors.New("native read continuation is too large")
	}
	cursor, err := decodeReadFileContinuation(input.Continuation)
	if err != nil {
		return providers.ToolCall{}, false, err
	}
	if t == nil || t.env == nil {
		return providers.ToolCall{}, false, errors.New("read environment unavailable")
	}
	managed := false
	if cursor.Path == "$SESSION_DIR" || strings.HasPrefix(cursor.Path, "$SESSION_DIR/") || filepath.IsAbs(cursor.Path) {
		_, _, managed, err = t.env.ResolveReadPath(cursor.Path)
		if err != nil {
			return providers.ToolCall{}, false, err
		}
	}
	clean := path.Clean(cursor.Path)
	windowsVolume := len(cursor.Path) >= 2 && cursor.Path[1] == ':' && ((cursor.Path[0] >= 'a' && cursor.Path[0] <= 'z') || (cursor.Path[0] >= 'A' && cursor.Path[0] <= 'Z'))
	if cursor.Path == "" || (!managed && (path.IsAbs(cursor.Path) || windowsVolume || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(cursor.Path, "\\\x00"))) || (input.Path != "" && input.Path != cursor.Path) {
		return providers.ToolCall{}, false, errors.New("remote native continuation requires a relative workspace path")
	}
	digest, err := hex.DecodeString(cursor.ExpectedSHA256)
	if err != nil || len(digest) != 32 {
		return providers.ToolCall{}, false, errors.New("native read continuation requires a valid snapshot digest")
	}
	args := map[string]any{"path": cursor.Path, "expected_sha256": cursor.ExpectedSHA256}
	if cursor.ByteOffset != nil {
		if *cursor.ByteOffset < 0 || cursor.Limit <= 0 || cursor.Limit > maxRecoveryPageBytes || (cursor.ByteEndOffset != nil && *cursor.ByteEndOffset <= *cursor.ByteOffset) {
			return providers.ToolCall{}, false, errors.New("invalid native byte continuation")
		}
		window := map[string]any{"offset": *cursor.ByteOffset, "limit": cursor.Limit}
		if cursor.ByteEndOffset != nil {
			window["end_offset"] = *cursor.ByteEndOffset
		}
		args["byte_range"] = window
	} else {
		if cursor.Offset <= 0 || cursor.Limit <= 0 {
			return providers.ToolCall{}, false, errors.New("invalid native line continuation")
		}
		args["offset"], args["limit"] = cursor.Offset, cursor.Limit
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return providers.ToolCall{}, false, err
	}
	call.Arguments = string(raw)
	return call, managed, nil
}

// ExecuteRemoteNativeReadOn preserves native observation semantics. The legacy
// token is not authenticated; its restored path is authorized as a new read.
func (t *Toolkit) ExecuteRemoteNativeReadOn(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	same := func() bool {
		if t == nil || t.env == nil || remote == nil || t.env.ExecutionEnvironment == nil {
			return false
		}
		a, b := reflect.ValueOf(remote), reflect.ValueOf(t.env.ExecutionEnvironment)
		return a.IsValid() && b.IsValid() && a.Comparable() && b.Comparable() && a == b
	}
	if !same() {
		return toolresult.Result{}, errors.New("remote execution environment changed")
	}
	sessionID := t.env.SessionID
	req, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return toolresult.Result{}, err
	}
	result, err := remote.Execute(ctx, req)
	if err != nil {
		return toolresult.Result{}, err
	}
	if !same() {
		return toolresult.Result{}, errors.New("remote execution environment changed")
	}
	rechecked, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return toolresult.Result{}, err
	}
	currentActor, currentPath := t.ExecutionActor()
	if !same() || currentActor != actor || currentPath != actorPath || t.env.SessionID != sessionID || req.PermissionMode != rechecked.PermissionMode || t.env.PermissionMode != req.PermissionMode {
		return toolresult.Result{}, errors.New("remote read authority changed")
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	return result, nil
}

// ExecuteNativeSessionRead uses the host-owned, pinned session artifact root.
// Authorization sees the restored path, but the native reader receives the
// original token so archive provenance remains unverified.
func (t *Toolkit) ExecuteNativeSessionRead(ctx context.Context, actor, actorPath string, original Tool, originalCall, restored providers.ToolCall) (toolresult.Result, error) {
	if t == nil || t.env == nil || original == nil || t.IsToolDisabled("read_file") || t.nativeReadExposure(original) == ToolExposureHidden {
		return toolresult.Result{}, errors.New("native read unavailable")
	}
	mode, sessionID, session, root := t.env.PermissionMode, t.env.SessionID, t.env.SessionDir, t.env.sessionArtifactRoot
	valid := func() bool {
		a, p := t.ExecutionActor()
		return a != "" && a == actor && p == actorPath && t.env.PermissionMode == mode && t.env.SessionID == sessionID && t.env.SessionDir == session && t.env.sessionArtifactRoot == root
	}
	check := func() error {
		if !valid() {
			return errors.New("native read authority changed")
		}
		if err := t.checkPermission(ctx, t.toolInfoForCall(original, restored.Arguments), restored); err != nil {
			return err
		}
		if !valid() {
			return errors.New("native read authority changed")
		}
		return nil
	}
	if err := check(); err != nil {
		return toolresult.Result{}, err
	}
	native, ok := original.(RichTool)
	if !ok {
		return toolresult.Result{}, errors.New("native read unavailable")
	}
	result, err := native.ExecuteResult(ctx, originalCall.Arguments)
	if err != nil {
		return toolresult.Result{}, err
	}
	if err := check(); err != nil {
		return toolresult.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	return result, nil
}

// ContinueRemoteReadOnly rechecks the original host policy and dispatches the
// exact captured call to the selected remote worker. It is not a generic plugin
// RPC: only a host-owned continuation may supply the captured call and tool.
func (t *Toolkit) ContinueRemoteReadOnly(ctx context.Context, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	if t == nil || t.env == nil {
		return toolresult.Result{}, errors.New("remote execution environment is unavailable")
	}
	actor, actorPath := t.ExecutionActor()
	return t.ContinueRemoteReadOnlyOn(ctx, t.env.ExecutionEnvironment, actor, actorPath, original, call)
}

// ContinueRemoteReadOnlyOn uses the environment captured when the host admitted
// the call; plugin input cannot select or replace this executor.
func (t *Toolkit) ContinueRemoteReadOnlyOn(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	request, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return toolresult.Result{}, err
	}
	if call.Name == "read_file" {
		executor, ok := remote.(executionenv.UnobservedReadExecutor)
		if !ok {
			return toolresult.Result{}, errors.New("remote execution environment does not support unobserved reads")
		}
		return executor.ExecuteUnobservedRead(ctx, request)
	}
	return remote.Execute(ctx, request)
}

// ContinueRemoteReadOnWorker additionally binds pagination to one worker
// instance. The expected instance is empty only for the first page.
func (t *Toolkit) ContinueRemoteReadOnWorker(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall, expected string) (toolresult.Result, string, error) {
	request, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return toolresult.Result{}, "", err
	}
	if call.Name != "read_file" {
		return toolresult.Result{}, "", errors.New("pinned remote continuation requires read_file")
	}
	executor, ok := remote.(executionenv.PinnedUnobservedReadExecutor)
	if !ok {
		return toolresult.Result{}, "", errors.New("remote execution environment does not support worker-bound reads")
	}
	return executor.ExecuteUnobservedReadOnWorker(ctx, request, expected)
}

// PrepareRemoteReadOnlyOn admits an exact original call without committing
// observation invalidation. The host commits only after validating its result.
func (t *Toolkit) PrepareRemoteReadOnlyOn(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (executionenv.PreparedRead, error) {
	request, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return executionenv.PreparedRead{}, err
	}
	if call.Name != "read_file" {
		return executionenv.PreparedRead{}, errors.New("prepared observation requires read_file")
	}
	executor, ok := remote.(executionenv.PreparedReadExecutor)
	if !ok {
		return executionenv.PreparedRead{}, errors.New("remote execution environment does not support read observation commit")
	}
	return executor.PrepareUnobservedRead(ctx, request)
}

func (t *Toolkit) PrepareRemoteReadOnWorker(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall, expected string) (executionenv.PreparedRead, error) {
	request, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return executionenv.PreparedRead{}, err
	}
	if call.Name != "read_file" {
		return executionenv.PreparedRead{}, errors.New("pinned remote continuation requires read_file")
	}
	executor, ok := remote.(executionenv.PinnedPreparedReadExecutor)
	if !ok {
		return executionenv.PreparedRead{}, errors.New("remote execution environment does not support worker-bound reads")
	}
	return executor.PrepareUnobservedReadOnWorker(ctx, request, expected)
}

// RecheckRemoteReadOnlyOn repeats original actor and permission authorization
// immediately before the worker commits a prepared observation.
func (t *Toolkit) RecheckRemoteReadOnlyOn(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (string, error) {
	request, err := t.authorizeRemoteReadOnly(ctx, remote, actor, actorPath, original, call)
	if err != nil {
		return "", err
	}
	return request.PermissionMode, nil
}

func (t *Toolkit) authorizeRemoteReadOnly(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (executionenv.ToolRequest, error) {
	var empty executionenv.ToolRequest
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if t == nil || t.env == nil || remote == nil {
		return empty, errors.New("remote execution environment is unavailable")
	}
	currentActor, currentPath := t.ExecutionActor()
	if actor == "" || currentActor != actor || currentPath != actorPath {
		return empty, errors.New("remote continuation actor changed")
	}
	location, ok := remote.(interface {
		Root() string
		Backend() string
		Identity() string
	})
	if !ok || location.Root() == "" || location.Backend() == "" || location.Identity() == "" {
		return empty, errors.New("authenticated remote workspace unavailable")
	}
	ctx = context.WithValue(ctx, permissionWorkspaceContextKey{}, location.Root())
	if original == nil || original.Name() != call.Name || (call.Name != "read_file" && call.Name != "grep") {
		return empty, errors.New("unsupported remote read continuation")
	}
	if t.IsToolDisabled(call.Name) {
		return empty, fmt.Errorf("tool %q is disabled", call.Name)
	}
	if err := validateToolArgumentsJSON(call.Arguments); err != nil {
		return empty, err
	}
	if validator, ok := original.(InputValidatingTool); ok {
		if err := validator.ValidateInput(call.Arguments); err != nil {
			return empty, err
		}
	}
	if err := t.checkPermission(ctx, t.toolInfoForCall(original, call.Arguments), call); err != nil {
		return empty, err
	}
	return executionenv.ToolRequest{Call: call, PermissionMode: t.env.PermissionMode, Actor: actor, ShellEarlyHandoffDisabled: t.env.ShellEarlyHandoffDisabled, GitAttributionEnabled: !t.env.GitAttributionDisabled}, nil
}

// AuthorizeRemoteTool applies plugin authority to the captured remote root.
func (t *Toolkit) AuthorizeRemoteTool(ctx context.Context, remote executionenv.Executor, call providers.ToolCall, metadata agent.ToolMetadata) error {
	location, ok := remote.(interface {
		Root() string
		Backend() string
		Identity() string
	})
	if !ok || location.Root() == "" || location.Backend() == "" || location.Identity() == "" {
		return errors.New("authenticated remote workspace unavailable")
	}
	return t.AuthorizeTool(context.WithValue(ctx, permissionWorkspaceContextKey{}, location.Root()), call, metadata)
}
