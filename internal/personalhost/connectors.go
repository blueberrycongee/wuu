package personalhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"strings"
)

// Connectors exposes data and exact-action receipts, never credential material.
// Only host composition can supply an implementation; plugins cannot register one.
type Connectors interface {
	List(context.Context, personalconnectors.Actor) ([]personalconnectors.Connection, error)
	Call(context.Context, personalconnectors.Actor, personalconnectors.Request) (personalconnectors.Result, error)
	Actions(context.Context, personalconnectors.Actor) ([]personalconnectors.Result, error)
	Decide(context.Context, personalconnectors.Actor, string, string, bool) (personalconnectors.Result, error)
	Disconnect(context.Context, personalconnectors.Actor, string) error
	Close() error
}

type ConnectorPoller interface {
	Poll(context.Context, personalconnectors.Actor, personalconnectors.PollRequest) (personalconnectors.PollResult, error)
}

type ConnectorOAuth interface {
	Start(context.Context, personalconnectors.Actor, personalconnectors.ConnectRequest) (personalconnectors.AuthorizationAttempt, error)
	Attempt(personalconnectors.Actor, string) (personalconnectors.AuthorizationAttempt, error)
	Cancel(personalconnectors.Actor, string) error
}

func (h *Host) callConnectors(ctx context.Context, operation string, raw json.RawMessage, verifiedSession string) (json.RawMessage, error) {
	h.mu.Lock()
	closed, broker, unavailable := h.closed, h.options.Connectors, h.options.ConnectorsUnavailableCode
	h.mu.Unlock()
	if closed || ctx.Err() != nil {
		return nil, errors.New("Personal host is closed")
	}
	if h.binding.Validate() != nil || !h.binding.IsUser() || h.binding.Domain() != "personal" {
		return nil, errors.New("Personal host binding is required")
	}
	var lifetime context.Context
	actor := personalconnectors.Actor{ProductDomain: h.binding.ProductDomain, UserSpaceID: h.binding.UserSpaceID}
	if (operation == "connectors.call" || operation == "connectors.poll") || strings.HasPrefix(operation, "connectors.read.") {
		if verifiedSession == "" {
			return nil, errors.New("host-resolved Personal execution is required")
		}
		actor.SessionID = verifiedSession
	} else {
		id, live, err := clientauthority.Connection(ctx, h.binding)
		if err != nil {
			return nil, err
		}
		actor.SessionID = "owner-ui:" + id
		lifetime = live
	}
	if broker == nil {
		if operation == "connectors.list" || operation == "connectors.read.list" {
			if err := decode(raw, &struct{}{}); err != nil {
				return nil, err
			}
			if unavailable == "" {
				unavailable = "secure_store_unavailable"
			}
			return json.Marshal(map[string]any{"state": "unavailable", "code": unavailable, "connections": []any{}})
		}
		return nil, &personalconnectors.Failure{Code: "connector_unavailable"}
	}
	var result any
	var err error
	switch operation {
	case "connectors.start":
		oauth, ok := broker.(ConnectorOAuth)
		if !ok {
			return nil, &personalconnectors.Failure{Code: "oauth_unavailable"}
		}
		if h.options.OpenConnectorAuthorizationURL == nil {
			return nil, &personalconnectors.Failure{Code: "authorization_browser_unavailable"}
		}
		var input personalconnectors.ConnectRequest
		if err = decode(raw, &input); err != nil {
			return nil, err
		}
		attempt, startErr := oauth.Start(ctx, actor, input)
		if startErr != nil {
			return nil, startErr
		}
		context.AfterFunc(lifetime, func() { _ = oauth.Cancel(actor, attempt.ID) })
		if err = h.options.OpenConnectorAuthorizationURL(ctx, attempt.AuthorizationURL); err != nil {
			_ = oauth.Cancel(actor, attempt.ID)
			return nil, &personalconnectors.Failure{Code: "authorization_browser_unavailable"}
		}
		attempt.AuthorizationURL = ""
		result = attempt
	case "connectors.attempt", "connectors.cancel":
		oauth, ok := broker.(ConnectorOAuth)
		if !ok {
			return nil, &personalconnectors.Failure{Code: "oauth_unavailable"}
		}
		var input struct {
			AttemptID string `json:"attempt_id"`
		}
		if err = decode(raw, &input); err != nil {
			return nil, err
		}
		if operation == "connectors.cancel" {
			err = oauth.Cancel(actor, input.AttemptID)
			result = map[string]bool{"ok": err == nil}
		} else {
			var attempt personalconnectors.AuthorizationAttempt
			attempt, err = oauth.Attempt(actor, input.AttemptID)
			attempt.AuthorizationURL = ""
			result = attempt
		}

	case "connectors.list", "connectors.read.list":
		if err = decode(raw, &struct{}{}); err == nil {
			var connections []personalconnectors.Connection
			connections, err = broker.List(ctx, actor)
			if connections == nil {
				connections = []personalconnectors.Connection{}
			}
			result = map[string]any{"state": "available", "connections": connections}
		}
	case "connectors.actions", "connectors.read.actions":
		if err = decode(raw, &struct{}{}); err == nil {
			var actions []personalconnectors.Result
			actions, err = broker.Actions(ctx, actor)
			if actions == nil {
				actions = []personalconnectors.Result{}
			}
			result = map[string]any{"actions": actions}
		}
	case "connectors.poll":
		poller, ok := broker.(ConnectorPoller)
		if !ok {
			return nil, &personalconnectors.Failure{Code: "poll_unavailable"}
		}
		var input personalconnectors.PollRequest
		if err = decode(raw, &input); err == nil {
			result, err = poller.Poll(ctx, actor, input)
		}
	case "connectors.call":
		var input personalconnectors.Request
		if err = decode(raw, &input); err == nil {
			result, err = broker.Call(ctx, actor, input)
		}
	case "connectors.decide":
		var input struct {
			ActionID string `json:"action_id"`
			Digest   string `json:"digest"`
			Approve  bool   `json:"approve"`
		}
		if err = decode(raw, &input); err == nil {
			result, err = broker.Decide(ctx, actor, input.ActionID, input.Digest, input.Approve)
		}
	case "connectors.disconnect":
		var input struct {
			ConnectionID string `json:"connection_id"`
		}
		if err = decode(raw, &input); err == nil {
			err = broker.Disconnect(ctx, actor, input.ConnectionID)
			result = map[string]bool{"ok": err == nil}
		}
	default:
		return nil, &personalconnectors.Failure{Code: "unknown_connector_operation"}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
