package executionenv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	OperationRunning    = "running"
	OperationCompleted  = "completed"
	OperationFailed     = "failed"
	OperationUnknown    = "outcome_unknown"
	OperationNotFound   = "not_found"
	OperationNotStarted = "operation_not_started"
	OperationConflict   = "operation_identity_conflict"
)

// OperationQuery addresses a durable operation, never a connection-local RPC ID.
// An absent receipt does not establish that a disconnected request is safe to retry.
type OperationQuery struct {
	ID             string `json:"id"`
	Digest         string `json:"digest"`
	Actor          string `json:"actor"`
	PermissionMode string `json:"permission_mode"`
}

type OperationStatus struct {
	ID     string          `json:"id"`
	Digest string          `json:"digest"`
	State  string          `json:"state"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// OperationError preserves uncertainty separately from an ordinary tool failure.
type OperationError struct {
	Kind    string
	ID      string
	Message string
	Cause   error
	Binding *OperationBinding
}

// OperationBinding preserves the original lookup authority with an unresolved
// host result. It is a diagnostic reference, not permission to retry or reconcile.
type OperationBinding struct {
	Environment       string         `json:"environment"`
	Session           string         `json:"session"`
	Workspace         string         `json:"workspace"`
	Method            string         `json:"method"`
	ReceiptsSupported bool           `json:"receipts_supported"`
	Query             OperationQuery `json:"query"`
}

func (e *OperationError) Unwrap() error         { return e.Cause }
func (e *OperationError) Error() string         { return e.Message }
func (e *OperationError) ToolErrorKind() string { return e.Kind }

// OperationDigest binds the method and canonical JSON payload, including the
// actor and permission mode. Receipts retain the result, not the request arguments.
func OperationDigest(method string, data json.RawMessage) (string, error) {
	if method != "execute" && method != "run_code" {
		return "", errors.New("method does not support operation receipts")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("operation payload must contain one JSON value")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(method+"\x00"), canonical...))
	return hex.EncodeToString(digest[:]), nil
}

func ValidateOperationQuery(query OperationQuery) error {
	if len(query.ID) < 1 || len(query.ID) > 200 {
		return errors.New("invalid durable operation id")
	}
	for _, r := range query.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return errors.New("invalid durable operation id")
		}
	}
	raw, err := hex.DecodeString(query.Digest)
	if err != nil || len(raw) != sha256.Size || query.Digest != strings.ToLower(query.Digest) {
		return errors.New("invalid durable operation digest")
	}
	if strings.TrimSpace(query.Actor) == "" || len(query.Actor) > 4096 {
		return errors.New("invalid durable operation actor")
	}
	switch query.PermissionMode {
	case "", "standard", "read_only", "unconfined":
	default:
		return fmt.Errorf("invalid operation permission mode %q", query.PermissionMode)
	}
	return nil
}
