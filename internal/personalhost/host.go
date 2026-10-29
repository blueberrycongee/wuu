// Package personalhost binds Personal account controls to live owner transport
// admission. It never exposes action execution or a credential API to plugins.
package personalhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

type Calendar interface {
	Status(context.Context, personalcalendar.Actor) (personalcalendar.Connection, error)
	Authorize(context.Context, personalcalendar.Actor) (personalcalendar.Connection, error)
	Calendars(context.Context, personalcalendar.Actor, string) ([]personalcalendar.Calendar, error)
	EventsPage(context.Context, personalcalendar.Actor, string, personalcalendar.EventQuery) (json.RawMessage, error)
	Select(context.Context, personalcalendar.Actor, string, []string) error
	Start(context.Context, personalcalendar.Actor) (personalcalendar.AuthorizationAttempt, error)
	Attempt(personalcalendar.Actor, string) (personalcalendar.AuthorizationAttempt, error)
	Cancel(personalcalendar.Actor, string) error
	Disconnect(context.Context, personalcalendar.Actor, string, bool) (personalcalendar.DisconnectResult, error)
	Close() error
}
type Options struct {
	// Authorize is the selected resource policy. Live owner transport admission
	// and verified session identity are additional proof, never substitutes.
	Authorize                     func(context.Context, string, string) error
	Connectors                    Connectors
	ConnectorsUnavailableCode     string
	OpenConnectorAuthorizationURL func(context.Context, string) error
	// Review is the host-owned durable action coordinator, never a plugin callback.
	Review func(context.Context, string, json.RawMessage, string) (json.RawMessage, error)
	// Memory publishes exact source-bound records through selected durable providers.
	Memory func(context.Context, string, json.RawMessage, string) (json.RawMessage, error)
	// Leave nil until an approved OAuth client and secure store are configured.
	Calendar Calendar
	// Opens the provider URL in the trusted host. Never return that URL to UI.
	OpenAuthorizationURL func(context.Context, string) error
	UnavailableCode      string
}
type Host struct {
	mu                       sync.Mutex
	binding                  userspace.Binding
	options                  Options
	closed                   bool
	closeErr                 error
	authorizationDiagnostics map[string]personalcalendar.Failure
}

func New(binding userspace.Binding, options Options) *Host {
	return &Host{binding: binding, options: options}
}
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return h.closeErr
	}
	h.closed = true
	if h.options.Calendar != nil {
		h.closeErr = h.options.Calendar.Close()
	}
	if h.options.Connectors != nil {
		h.closeErr = errors.Join(h.closeErr, h.options.Connectors.Close())
	}
	return h.closeErr
}

// Call's verifiedSession is resolved from host session metadata before dispatch.
// Control operations derive their actor from the separate owner admission.
func (h *Host) Call(ctx context.Context, operation string, raw json.RawMessage, verifiedSession string) (json.RawMessage, error) {
	if _, _, known := OperationAccess(operation); !known {
		return nil, errors.New("Personal host operation is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.options.Authorize == nil {
		return nil, errors.New("Personal resource authorization is unavailable")
	}
	if err := h.options.Authorize(ctx, operation, verifiedSession); err != nil {
		return nil, err
	}

	if strings.HasPrefix(operation, "memory.") {
		h.mu.Lock()
		closed, handler := h.closed, h.options.Memory
		h.mu.Unlock()
		if closed || handler == nil {
			return nil, errors.New("Personal memory coordinator unavailable")
		}
		if operation == "memory.operations" || operation == "memory.reconcile" {
			if err := clientauthority.RequireOwner(ctx, h.binding); err != nil {
				return nil, err
			}
		} else if verifiedSession == "" {
			return nil, errors.New("memory requires a host-resolved session")
		}
		return handler(ctx, operation, raw, verifiedSession)
	}
	if strings.HasPrefix(operation, "connectors.") {
		return h.callConnectors(ctx, operation, raw, verifiedSession)
	}
	if strings.HasPrefix(operation, "review.") {
		h.mu.Lock()
		closed, handler := h.closed, h.options.Review
		h.mu.Unlock()
		if closed || ctx.Err() != nil {
			return nil, errors.New("Personal host is closed")
		}
		if h.binding.Validate() != nil || !h.binding.IsUser() || h.binding.Domain() != "personal" {
			return nil, errors.New("Personal host binding is required")
		}
		if operation == "review.execution.status" {
			if verifiedSession == "" {
				return nil, errors.New("host-resolved execution is required")
			}
		} else if err := clientauthority.RequireOwner(ctx, h.binding); err != nil {
			return nil, err
		}
		// The coordinator holds its own dispatch boundary, not the calendar
		// mutex. Model calls cannot block unrelated owner controls.
		if handler != nil {
			return handler(ctx, operation, raw, verifiedSession)
		}
		if operation == "review.list" {
			if err := decode(raw, &struct{}{}); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"schema": "personal-review.v1", "kind": "unavailable", "reason": "动作审批当前不可用。"})
		}
		return nil, errors.New("action review is unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || ctx.Err() != nil {
		return nil, errors.New("Personal host is closed")
	}
	if h.binding.Validate() != nil || !h.binding.IsUser() || h.binding.Domain() != "personal" {
		return nil, errors.New("Personal host binding is required")
	}
	actor := personalcalendar.Actor{ProductDomain: h.binding.ProductDomain, UserSpaceID: h.binding.UserSpaceID, SessionID: verifiedSession}
	var lifetime context.Context
	if strings.HasPrefix(operation, "calendar.read.") {
		if verifiedSession == "" {
			return nil, errors.New("host-resolved Personal session is required")
		}
	} else {
		id, live, err := clientauthority.Connection(ctx, h.binding)
		if err != nil {
			return nil, err
		}
		actor.SessionID = "owner-ui:" + id
		lifetime = live
	}
	if operation == "owner.check" {
		if err := decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"ok": true})
	}
	var input struct {
		ConnectionID string                      `json:"connection_id,omitempty"`
		AttemptID    string                      `json:"attempt_id,omitempty"`
		IDs          []string                    `json:"calendar_ids,omitempty"`
		Revoke       bool                        `json:"revoke,omitempty"`
		Query        personalcalendar.EventQuery `json:"query,omitempty"`
	}
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	b := h.options.Calendar
	if b == nil {
		if operation == "calendar.status" || operation == "calendar.read.status" || operation == "calendar.read.authorize" {
			code := h.options.UnavailableCode
			if code == "" {
				code = "oauth_client_not_configured"
			}
			return json.Marshal(personalcalendar.Connection{ProductDomain: "personal", Provider: "google-calendar", UserSpaceID: h.binding.UserSpaceID, State: "unavailable", SelectedCalendars: []string{}, ErrorCode: code})
		}
		return nil, &personalcalendar.Failure{Code: "oauth_client_not_configured"}
	}
	var result any
	var err error
	switch operation {
	case "calendar.status", "calendar.read.status":
		var connection personalcalendar.Connection
		connection, err = b.Status(ctx, actor)
		result = connection
		if operation == "calendar.status" && len(h.authorizationDiagnostics) > 0 {
			// Cancellation belongs to its immutable attempt, not whichever
			// grant happens to be current when the callback finishes.
			ids := make([]string, 0, len(h.authorizationDiagnostics))
			for id := range h.authorizationDiagnostics {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			diagnostics := make([]authorizationDiagnostic, 0, len(ids))
			for _, id := range ids {
				failure := h.authorizationDiagnostics[id]
				diagnostics = append(diagnostics, authorizationDiagnostic{AttemptID: id, ErrorCode: failure.Code, PersistenceFailed: failure.PersistenceFailed})
			}
			result = struct {
				personalcalendar.Connection
				Diagnostics []authorizationDiagnostic `json:"authorization_diagnostics"`
			}{connection, diagnostics}
		}
	case "calendar.read.authorize":
		result, err = b.Authorize(ctx, actor)
	case "calendar.calendars":
		result, err = b.Calendars(ctx, actor, input.ConnectionID)
	case "calendar.read.events":
		result, err = b.EventsPage(ctx, actor, input.ConnectionID, input.Query)
	case "calendar.select":
		err = b.Select(ctx, actor, input.ConnectionID, input.IDs)
		result = map[string]bool{"ok": err == nil}
	case "calendar.disconnect":
		result, err = b.Disconnect(ctx, actor, input.ConnectionID, input.Revoke)
	case "calendar.cancel":
		err = b.Cancel(actor, input.AttemptID)
		h.recordAuthorizationDiagnostic(input.AttemptID, err)
		result = map[string]bool{"ok": err == nil}
	case "calendar.attempt":
		var attempt personalcalendar.AuthorizationAttempt
		attempt, err = b.Attempt(actor, input.AttemptID)
		attempt.AuthorizationURL = ""
		result = attempt
	case "calendar.start":
		if h.options.OpenAuthorizationURL == nil {
			return nil, &personalcalendar.Failure{Code: "authorization_browser_unavailable"}
		}
		var attempt personalcalendar.AuthorizationAttempt
		attempt, err = b.Start(ctx, actor)
		if err == nil {
			context.AfterFunc(lifetime, func() {
				cancelErr := b.Cancel(actor, attempt.ID)
				h.mu.Lock()
				h.recordAuthorizationDiagnostic(attempt.ID, cancelErr)
				h.mu.Unlock()
			})
			err = h.options.OpenAuthorizationURL(ctx, attempt.AuthorizationURL)
			if err != nil {
				if cancelErr := b.Cancel(actor, attempt.ID); cancelErr != nil {
					h.recordAuthorizationDiagnostic(attempt.ID, cancelErr)
					return nil, cancelErr
				}
				return nil, &personalcalendar.Failure{Code: "authorization_browser_unavailable"}
			}
			attempt.AuthorizationURL = ""
			result = attempt
		}
	default:
		return nil, errors.New("Personal host operation is unavailable")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

type authorizationDiagnostic struct {
	AttemptID         string `json:"attempt_id"`
	ErrorCode         string `json:"error_code"`
	PersistenceFailed bool   `json:"persistence_failed,omitempty"`
}

// The caller holds h.mu. A later successful grant or disconnect is not proof
// that cleanup of an earlier attempt succeeded; retain that attempt's fact for
// this host lifetime, separately from the broker's current connection status.
func (h *Host) recordAuthorizationDiagnostic(attemptID string, err error) {
	var failure *personalcalendar.Failure
	if !errors.As(err, &failure) {
		return
	}
	// Lookup/scope errors can contain a client-supplied attempt ID. Only these
	// outcomes establish that Cancel reached a broker-owned attempt.
	if failure.Code != "credential_cleanup_failed" && failure.Code != "authorization_already_completed" {
		return
	}
	if h.authorizationDiagnostics == nil {
		h.authorizationDiagnostics = make(map[string]personalcalendar.Failure)
	}
	previous, exists := h.authorizationDiagnostics[attemptID]
	if exists && (previous.Code == "credential_cleanup_failed" || previous.PersistenceFailed) {
		return
	}
	h.authorizationDiagnostics[attemptID] = *failure
}

func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid Personal host parameters")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid Personal host parameters")
	}
	return nil
}
