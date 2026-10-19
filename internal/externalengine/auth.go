package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/version"
)

// AuthMethod is an agent-advertised sign-in choice. Terminal methods are not
// advertised as invocable choices; local-cli is a separate explicit action.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
}

type AuthResult struct {
	Methods       []AuthMethod `json:"methods"`
	Authenticated bool         `json:"authenticated"`
}

func initializeACP(ctx context.Context, r *rpc) (acpInitialize, error) {
	var init acpInitialize
	err := r.call(ctx, "initialize", acpInitializeParams(), &init)
	if err == nil && init.Version != 1 {
		err = fmt.Errorf("unsupported ACP version %d (requires version 1)", init.Version)
	}
	return init, err
}

// ACP clients that omit fs/terminal capabilities can leave agents waiting on
// host I/O they never advertised. Decline those surfaces so agents use their
// own filesystem and sandbox instead.
func acpInitializeParams() map[string]any {
	clientVersion := strings.TrimSpace(version.Info().Version)
	if clientVersion == "" {
		clientVersion = "1"
	}
	return map[string]any{
		"protocolVersion": 1,
		"clientInfo": map[string]string{
			"name":    "wuu",
			"title":   "Wuu",
			"version": clientVersion,
		},
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"session":  map[string]any{"configOptions": map[string]any{"boolean": map[string]any{}}},
		},
	}
}

// Authenticate launches an explicit, bounded sign-in operation. An empty method
// only discovers choices; it never calls authenticate or creates a chat session.
// The agent owns browser interaction and credential persistence. The reserved
// local-cli method runs the fixed Grok login command and must be restricted to
// a local caller by the host transport.
func (e *Engine) Authenticate(ctx context.Context, methodID string) (AuthResult, error) {
	result := AuthResult{Methods: []AuthMethod{}}
	if methodID == "local-cli" {
		return e.authenticateLocalCLI(ctx)
	}
	if e.entry.Protocol != "acp" {
		return result, errors.New("sign in using this engine's CLI")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, err := startChild(e.binary, e.entry.Args, e.root, nil)
	if err != nil {
		return result, err
	}
	defer p.close()
	r := &rpc{child: p, handle: func(_ context.Context, method string, _ json.RawMessage, request bool) (any, error) {
		if request {
			return nil, &rpcError{Code: -32601, Message: "unsupported sign-in request: " + method}
		}
		return nil, nil
	}}
	setupCtx, cancelSetup := context.WithTimeout(ctx, acpInitializeTimeout)
	init, err := initializeACP(setupCtx, r)
	cancelSetup()
	if err != nil {
		return result, acpEngineError(e.entry, p, err)
	}
	for _, method := range init.AuthMethods {
		if method.ID != "" && (method.Type == "" || method.Type == "agent") {
			result.Methods = append(result.Methods, method)
		}
	}
	if methodID == "" {
		return result, nil
	}
	for _, method := range result.Methods {
		if method.ID == methodID {
			if err := r.call(ctx, "authenticate", map[string]string{"methodId": methodID}, nil); err != nil {
				// A sign-in flow that fails reports why on stderr (a missing
				// key, a refused redirect) rather than in the RPC error.
				return result, acpEngineError(e.entry, p, err)
			}
			result.Authenticated = true
			return result, nil
		}
	}
	return result, fmt.Errorf("authentication method %q is not advertised or requires terminal login", methodID)
}

func (e *Engine) authenticateLocalCLI(ctx context.Context) (AuthResult, error) {
	result := AuthResult{Methods: []AuthMethod{}}
	if e.entry.ID != "grok" {
		return result, errors.New("local CLI sign-in is not supported for this engine")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// Keep the CLI's browser flow and credential storage intact. There is no
	// shell, input command, installation, or update; output may contain OAuth
	// codes, so neither stdout nor stderr is captured or forwarded.
	// Force browser OAuth because configured device-code flows need terminal I/O.
	cmd := exec.CommandContext(ctx, e.binary, "--no-auto-update", "login", "--oauth")
	cmd.Dir = e.root
	configureChild(cmd)
	cmd.Cancel = func() error { killChild(cmd); return nil }
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, fmt.Errorf("Grok CLI sign-in failed (exit code %d); try again", exitErr.ExitCode())
		}
		return result, errors.New("Grok CLI sign-in could not start; check the configured executable")
	}
	home, _ := os.UserHomeDir()
	if _, err := grokbuild.LocalOAuthStatus(home); err != nil {
		return result, errors.New("Grok CLI finished without a reusable login; try signing in again")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Authenticated = true
	return result, nil
}
