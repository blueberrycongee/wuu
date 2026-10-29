// Package personalconnectors owns credential and effect boundaries for Personal integrations.
package personalconnectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type Actor struct{ ProductDomain, UserSpaceID, SessionID string }
type Connection struct {
	AccountLabel      string   `json:"account_label,omitempty"`
	WorkReadPolicy    string   `json:"work_read_policy,omitempty"`
	ScopeVerification string   `json:"scope_verification,omitempty"`
	ID                string   `json:"connection_id"`
	Provider          string   `json:"provider"`
	WorkspaceID       string   `json:"workspace_id,omitempty"`
	AccountID         string   `json:"account_id"`
	Domain            string   `json:"domain"`
	Scopes            []string `json:"scopes"`
	Resources         []string `json:"resources"`
	Revision          string   `json:"revision"`
	State             string   `json:"state"`
}
type Request struct {
	ConnectionID string          `json:"connection_id"`
	Operation    string          `json:"operation"`
	Arguments    json.RawMessage `json:"arguments"`
	RequestID    string          `json:"request_id,omitempty"`
}
type Plan struct {
	AcceptGone     bool
	Preflight      []Plan
	Method         string
	URL            string
	Headers        map[string]string
	Body           json.RawMessage
	RequiredScopes []string
	Resource       string
	Write          bool
	Summary        string
	ResponseKind   string
	Verify         func(json.RawMessage) ([]Plan, error)
	Filter         func([]byte) (json.RawMessage, error)
}
type Result struct {
	ArgumentsJSON string          `json:"arguments_json,omitempty"`
	Verification  string          `json:"verification,omitempty"`
	State         string          `json:"state"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	AccountID     string          `json:"account_id,omitempty"`
	Domain        string          `json:"domain,omitempty"`
	Resource      string          `json:"resource,omitempty"`
	ActionID      string          `json:"action_id,omitempty"`
	Digest        string          `json:"digest,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
	Code          string          `json:"code,omitempty"`
	ConnectionID  string          `json:"connection_id,omitempty"`
	Operation     string          `json:"operation,omitempty"`
}
type Failure struct {
	Code string `json:"code"`
}

func (e *Failure) Error() string { return e.Code }
func fault(code string) error    { return &Failure{Code: code} }
func decode(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return fault("invalid_arguments")
	}
	return nil
}
func code(err error) string {
	var f *Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return "connector_unavailable"
}
