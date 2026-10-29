package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const HostServiceSessionRoute HostServiceMethod = "host.session.route"
const KernelSessionRouteService = "host.session.route"
const SessionTransportService = "session.transport"

// SessionLocatorV1 addresses an execution authority, not a filesystem. Storage
// and display hosts are descriptive; changing them cannot migrate execution.
type SessionLocatorV1 struct {
	Version       int    `json:"version"`
	ExecutionHost string `json:"execution_host"`
	SessionID     string `json:"session_id,omitempty"`
	StorageHost   string `json:"storage_host,omitempty"`
	DisplayHost   string `json:"display_host,omitempty"`
}

// SessionTransportTicket is minted by the source host, bound to one selected
// provider invocation, and never accepted from plugin callers.
type SessionTransportTicket struct {
	ID         string `json:"id"`
	SourceHost string `json:"source_host"`
}

type SessionRouteRequest struct {
	TransportTicket *SessionTransportTicket `json:"transport_ticket,omitempty"`
	Version         int                     `json:"version"`
	Operation       string                  `json:"operation"`
	OperationID     string                  `json:"operation_id"`
	Locator         SessionLocatorV1        `json:"locator"`
	Params          json.RawMessage         `json:"params"`
}

type SessionRouteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SessionRouteResult struct {
	Version     int                `json:"version"`
	OperationID string             `json:"operation_id"`
	Locator     SessionLocatorV1   `json:"locator"`
	Status      string             `json:"status"`
	Result      json.RawMessage    `json:"result,omitempty"`
	Error       *SessionRouteError `json:"error,omitempty"`
}

func (r SessionRouteRequest) Validate() error {
	if r.Version != 1 || r.Locator.Version != 1 {
		return errors.New("unsupported session routing version")
	}
	if strings.TrimSpace(r.Locator.ExecutionHost) == "" || r.Locator.ExecutionHost != strings.TrimSpace(r.Locator.ExecutionHost) {
		return errors.New("execution_host is required")
	}
	if strings.TrimSpace(r.OperationID) == "" || len(r.OperationID) > MaxSessionSendRequestIDBytes {
		return errors.New("operation_id is required and must fit the session request limit")
	}
	switch r.Operation {
	case "create", "list":
		if r.Locator.SessionID != "" {
			return errors.New("create/list locator must not identify a session")
		}
	case "send", "inspect", "cancel":
		if strings.TrimSpace(r.Locator.SessionID) == "" {
			return errors.New("session locator is required")
		}
	default:
		return fmt.Errorf("unsupported session operation %q", r.Operation)
	}
	if len(r.Params) == 0 || !json.Valid(r.Params) {
		return errors.New("valid operation params are required")
	}
	return nil
}

func SessionTransportDescriptor() ServiceDescriptor {
	return ServiceDescriptor{Name: SessionTransportService, Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "dispatch", InputSchema: "session.transport.dispatch.input.v1", OutputSchema: "session.transport.dispatch.output.v1"}}}
}
