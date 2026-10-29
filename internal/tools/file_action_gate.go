package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// FileActionGate is a host execution boundary, independent of the optional
// Coding reviewer. It never returns a reusable allow token to the model.
type FileActionGate func(context.Context, *Toolkit, providers.ToolCall) (toolresult.Result, error)

func (t *Toolkit) SetFileActionGate(gate FileActionGate) { t.fileActionGate = gate }

type FileAction struct {
	CallID    string          `json:"call_id"`
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
	CWD       string          `json:"cwd"`
	Target    string          `json:"target"`
	Exists    bool            `json:"exists"`
	SHA256    string          `json:"sha256"`
}
type fileActionContextKey struct{}

// The host predicates describe this kit's captured hook generation, not the
// latest catalog. Unknown callbacks and snapshot-only authorizers are unsupported;
// selected authorization must remain fenced rather than being skipped.
func (t *Toolkit) LocalFileActionCallbacks() bool {
	authorizer, atomic := t.authorizer.(AtomicAuthorizer)
	atomic = atomic && authorizer.SupportsAtomicAuthorization()
	return (t.authorizer == nil || atomic) && (t.permissionRequestHook == nil || t.permissionHookLocalOnly != nil && t.permissionHookLocalOnly()) && (t.env.OnFileChanged == nil || t.fileChangedLocalOnly != nil && t.fileChangedLocalOnly())
}

func fileMutation(name string) bool { return name == "write_file" || name == "edit_file" }

// PrepareFileAction freezes the concrete path and the pre-write bytes, not just
// a lexical path. Symlinked targets/parents are deliberately outside this first
// bounded adapter. It does not expand the tool's existing file scope.
func (t *Toolkit) PrepareFileAction(ctx context.Context, call providers.ToolCall) (FileAction, error) {
	if !fileMutation(call.Name) || t.env.ExecutionEnvironment != nil || !t.LocalFileActionCallbacks() {
		return FileAction{}, errors.New("Personal approval requires local write_file/edit_file with fenced authorization and no extension hooks")
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return FileAction{}, err
	}
	target, err := t.env.ResolvePath(args.Path)
	if err != nil {
		return FileAction{}, err
	}
	if err = rejectSensitiveToolPath(t.env, call.Name, "write", target); err != nil {
		return FileAction{}, err
	}
	target, err = t.env.ExecPath(ctx, target)
	if err != nil {
		return FileAction{}, err
	}
	if err = rejectFileActionSymlinks(target); err != nil {
		return FileAction{}, err
	}
	a := FileAction{CallID: call.ID, Operation: call.Name, Arguments: json.RawMessage(call.Arguments), CWD: t.RootDir(), Target: target}
	data, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return FileAction{}, err
	}
	a.Exists = err == nil
	if a.Exists {
		a.SHA256 = sha256Hex(data)
	}
	return a, nil
}
func rejectFileActionSymlinks(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Personal file approval does not follow symlinks")
		}
		if err == nil && p == filepath.Clean(path) && (!info.Mode().IsRegular() || !singleFileActionLink(info)) {
			return errors.New("Personal file approval requires a regular file with exactly one link")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func (t *Toolkit) ValidateFileAction(ctx context.Context, a FileAction) error {
	current, err := t.PrepareFileAction(ctx, providers.ToolCall{Name: a.Operation, Arguments: string(a.Arguments)})
	if err != nil {
		return err
	}
	if current.CWD != a.CWD || current.Target != a.Target || current.Exists != a.Exists || current.SHA256 != a.SHA256 {
		return errors.New("personal_action_target_changed")
	}
	return nil
}

// ExecuteFileAction repeats ordinary permission/schema checks and installs an
// exact precondition checked inside the existing per-file mutation queue.
func (t *Toolkit) ExecuteFileAction(ctx context.Context, a FileAction) (toolresult.Result, error) {
	if err := t.ValidateFileAction(ctx, a); err != nil {
		return toolresult.Result{}, err
	}
	call := providers.ToolCall{ID: a.CallID, Name: a.Operation, Arguments: string(a.Arguments)}
	execute := func(ctx context.Context) (toolresult.Result, error) {
		if err := ctx.Err(); err != nil {
			return toolresult.Result{}, err
		}
		return t.ExecuteResult(context.WithValue(ctx, fileActionContextKey{}, a), call)
	}
	if t.authorizer == nil {
		return execute(ctx)
	}
	authorizer, ok := t.authorizer.(AtomicAuthorizer)
	if !ok {
		return toolresult.Result{}, errors.New("selected authorization effect fence unavailable")
	}
	tool := t.LookupTool(call.Name)
	if tool == nil {
		return toolresult.Result{}, errors.New("reviewed file tool unavailable")
	}
	request := t.authorizationRequest(buildToolInfoForArgs(tool, t.toolExposure(call.Name), call.Arguments), call)
	var result toolresult.Result
	entered := false
	err := authorizer.WithAuthorization(ctx, request, func(effectCtx context.Context) error {
		if entered {
			return errors.New("authorization effect callback repeated")
		}
		entered = true
		if effectCtx == nil {
			return errors.New("authorization effect context unavailable")
		}
		var err error
		result, err = execute(context.WithValue(effectCtx, fileAuthorizationContextKey{}, fileAuthorization{t, request}))
		return err
	})
	if err == nil && !entered {
		return toolresult.Result{}, errors.New("selected authorization effect fence unavailable")
	}
	return result, err
}
func checkFileActionPrecondition(ctx context.Context, path string, data []byte, exists bool) error {
	a, ok := ctx.Value(fileActionContextKey{}).(FileAction)
	if !ok {
		return nil
	}
	if err := rejectFileActionSymlinks(path); err != nil {
		return err
	}
	if path != a.Target || exists != a.Exists || exists && sha256Hex(data) != a.SHA256 {
		return fmt.Errorf("personal_action_target_changed: %s", path)
	}
	return ctx.Err()
}
