package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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
	err := r.call(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]string{"name": "wuu", "version": "1"}}, &init)
	if err == nil && init.Version != 1 {
		err = fmt.Errorf("unsupported ACP version %d (requires version 1)", init.Version)
	}
	return init, err
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
	setupCtx, cancelSetup := context.WithTimeout(ctx, 30*time.Second)
	init, err := initializeACP(setupCtx, r)
	cancelSetup()
	if err != nil {
		return result, err
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
			err := r.call(ctx, "authenticate", map[string]string{"methodId": methodID}, nil)
			result.Authenticated = err == nil
			return result, err
		}
	}
	return result, fmt.Errorf("authentication method %q is not advertised or requires terminal login", methodID)
}
