// Package personalreview implements an opt-in Personal execution boundary.
// It is not registered with production tools or client capabilities yet. Hosts
// must supply authenticated identity, serialized task authority, and vetted
// operation handlers; none of these may come from model or GenUI input.
package personalreview

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/approvefor"
)

type Effect string

const (
	Bookkeeping        Effect = "internal_bookkeeping"
	ReadOnly           Effect = "read_only"
	DataWrite          Effect = "data_write"
	ExternalWrite      Effect = "external_write"
	SensitiveAuthority Effect = "sensitive_authority"
	Unknown            Effect = "unknown"
)

type Status string

const (
	Proposed             Status = "proposed"
	Ready                Status = "ready"
	AwaitingConfirmation Status = "awaiting_confirmation"
	Approved             Status = "approved"
	Blocked              Status = "blocked"
	Denied               Status = "denied"
	Revoked              Status = "revoked"
	Expired              Status = "expired"
	Stale                Status = "stale"
	Executing            Status = "executing"
	Succeeded            Status = "succeeded"
	Uncertain            Status = "uncertain"
)

type Key struct {
	SpaceID string
	ID      string
}
type Scope struct {
	SpaceID              string `json:"space_id"`
	SessionID            string `json:"session_id"`
	TaskID               string `json:"task_id"`
	RequirementsRevision int64  `json:"requirements_revision"`
}

// Proposal contains no consent, effect, policy, identity, or executable callback.
// Scope must be derived from the owning host execution, not arbitrary tool args.
type Proposal struct {
	ID        string          `json:"id"`
	Scope     Scope           `json:"scope"`
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
}
type Summary struct {
	Title       string `json:"title"`
	Account     string `json:"account"`
	Target      string `json:"target"`
	Consequence string `json:"consequence"`
}
type Action struct {
	Proposal
	OwnerID        string    `json:"owner_id"`
	PolicyRevision int64     `json:"policy_revision"`
	HandlerVersion string    `json:"handler_version"`
	Effect         Effect    `json:"effect"`
	Summary        Summary   `json:"summary"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Digest         string    `json:"digest"`
}

func (a Action) Key() Key { return Key{SpaceID: a.Scope.SpaceID, ID: a.ID} }

// Describe validates the full schema and derives account/target/consequences.
// Bump Version whenever handler behavior, validation or classification changes.
// Bookkeeping handlers must never dispatch work, write user data, schedule work,
// grant authority, or communicate externally, including through callbacks.
type Operation struct {
	Version  string
	Effect   Effect
	Describe func(json.RawMessage) (Summary, error)
	Execute  func(context.Context, Action) (json.RawMessage, error)
}
type HumanMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Consent is an exact, expiring authorization resolved from host-owned human
// input. It is not a textual grant, a model result, or a persistent allow rule.
// The host must retain the source message and withdraw consent on revocation.
type Consent struct {
	ID              string    `json:"id"`
	ActionID        string    `json:"action_id"`
	MessageID       string    `json:"message_id"`
	OwnerID         string    `json:"owner_id"`
	Scope           Scope     `json:"scope"`
	PolicyRevision  int64     `json:"policy_revision"`
	Operation       string    `json:"operation"`
	HandlerVersion  string    `json:"handler_version"`
	ArgumentsDigest string    `json:"arguments_digest"`
	ExpiresAt       time.Time `json:"expires_at"`
}
type Authority struct {
	Domain         string
	OwnerID        string
	Scope          Scope
	PolicyRevision int64
	Active         bool
	BoundaryReason string
	UserMessages   []HumanMessage
	Consents       []Consent
}

const OwnerUI = "owner_ui"

type Identity struct {
	UserID  string
	SpaceID string
	Channel string
}
type Choice string

const (
	ApproveOnce Choice = "approve_once"
	Reject      Choice = "reject"
)

type Resolution struct {
	Digest           string `json:"digest"`
	ExpectedRevision int64  `json:"expected_revision"`
	Choice           Choice `json:"choice"`
}
type ActionBinding struct {
	Digest           string `json:"digest"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type Approval struct {
	Resolution
	OwnerID string    `json:"owner_id"`
	At      time.Time `json:"at"`
}
type Record struct {
	Action    Action          `json:"action"`
	Revision  int64           `json:"revision"`
	Status    Status          `json:"status"`
	Outcome   string          `json:"outcome"`
	Reason    string          `json:"reason"`
	Approval  *Approval       `json:"approval,omitempty"`
	ConsentID string          `json:"consent_id,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}
type Event struct {
	Revision int64     `json:"revision"`
	Status   Status    `json:"status"`
	Outcome  string    `json:"outcome"`
	Reason   string    `json:"reason"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
}
type ReviewInput struct {
	Action       Action
	UserMessages []HumanMessage
	Confirmation *Approval
	Consent      *Consent
}
type Options struct {
	Store      *Store
	Operations map[string]Operation
	Clock      func() time.Time
	// WithAuthority must hold the host's task/policy/connection revision lock
	// through use, including handler dispatch. Revocation and task changes must
	// acquire that same lock. The callback may run more than once per action.
	WithAuthority func(context.Context, Scope, func(Authority) error) error
	// Authenticate reads host transport identity. Never decode it from the
	// Resolution or accept plugin/worker/GenUI-declared identity as proof.
	Authenticate func(context.Context) (Identity, error)
	Review       func(context.Context, ReviewInput) (approvefor.Decision, error)
}

var ErrConflict = errors.New("personal_review_revision_conflict")
var ErrUnauthorized = errors.New("personal_review_owner_confirmation_required")
var ErrNotFound = errors.New("personal_review_not_found")
