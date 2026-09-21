package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/version"
)

// AuthMethod is an agent-advertised sign-in choice. Only agent-driven methods
// can be invoked here; terminal authentication is left to the installed CLI.
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
		},
	}
}

// Authenticate launches an explicit, bounded sign-in operation. An empty method
// only discovers choices; it never calls authenticate or creates a chat session.
// The agent owns browser interaction and credential persistence.
func (e *Engine) Authenticate(ctx context.Context, methodID string) (AuthResult, error) {
	result := AuthResult{Methods: []AuthMethod{}}
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
