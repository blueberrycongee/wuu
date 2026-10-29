// Package executionauth defines selected, revocable unattended execution grants.
// A grant is never a persisted owner identity. Every resource policy evaluates
// its opaque proof through the currently selected provider before an effect.
package executionauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const ServiceName = "execution.authorization"
const ConsumerModule = "wuu.execution-access"
const NativeModule = "wuu.execution-authorization.default"
const MaxGrantLifetime = 30 * 24 * time.Hour

type Reference = pluginhost.ExecutionAuthorizationReference
type AuthorityBinding struct {
	Service     string `json:"service"`
	Major       int    `json:"major"`
	Provider    string `json:"provider"`
	Subject     string `json:"subject"`
	Fingerprint string `json:"fingerprint"`
}
type Bounds struct {
	ModelProviders  []string `json:"model_providers"`
	Models          []string `json:"models"`
	PermissionModes []string `json:"permission_modes"`
	MaxDepth        int      `json:"max_depth"`
}
type Scope struct {
	Kind          string           `json:"kind"`
	Authority     AuthorityBinding `json:"authority"`
	Bounds        Bounds           `json:"bounds"`
	Depth         int              `json:"depth"`
	Consumer      string           `json:"consumer"`
	UserSpaceID   string           `json:"user_space_id"`
	ProductDomain string           `json:"product_domain"`
	TaskID        string           `json:"task_id"`
	RunID         string           `json:"run_id"`
	SessionID     string           `json:"session_id"`
	ContextDigest string           `json:"context_digest"`
	Revision      int64            `json:"revision"`
}

// Lineage is returned by the selected task authority and carried only in
// host-attested invocation metadata, never accepted from issuance request JSON.
type Lineage struct {
	Authority      AuthorityBinding `json:"authority"`
	ParentTaskID   string           `json:"parent_task_id"`
	ParentRevision int64            `json:"parent_revision"`
	TaskID         string           `json:"task_id"`
	RunID          string           `json:"run_id"`
	SessionID      string           `json:"session_id"`
	Revision       int64            `json:"revision"`
	ContextDigest  string           `json:"context_digest"`
	ModelProvider  string           `json:"model_provider"`
	Model          string           `json:"model"`
	PermissionMode string           `json:"permission_mode"`
	Depth          int              `json:"depth"`
}
type PolicyInfo struct {
	DefaultLifetimeSeconds int64    `json:"default_lifetime_seconds"`
	MaxLifetimeSeconds     int64    `json:"max_lifetime_seconds"`
	AuthorityKinds         []string `json:"authority_kinds"`
	ScopeKinds             []string `json:"scope_kinds"`
}
type InspectRequest struct {
	Reference Reference `json:"reference"`
	Consumer  string    `json:"consumer"`
}
type Capability struct {
	Action    string            `json:"action"`
	Resources []access.Resource `json:"resources"`
}
type IssueRequest struct {
	RequestID    string       `json:"request_id"`
	Scope        Scope        `json:"scope"`
	Capabilities []Capability `json:"capabilities"`
	ExpiresAt    time.Time    `json:"expires_at"`
	Parent       *Reference   `json:"parent,omitempty"`
}
type IssueResult struct {
	Reference    Reference          `json:"reference"`
	Principal    principal.Identity `json:"principal"`
	Scope        Scope              `json:"scope"`
	Capabilities []Capability       `json:"capabilities"`
	ExpiresAt    time.Time          `json:"expires_at"`
}
type ValidateRequest struct {
	Reference Reference         `json:"reference"`
	Scope     Scope             `json:"scope"`
	Action    string            `json:"action"`
	Resources []access.Resource `json:"resources"`
}

// Validation is a fresh admission verdict for one exact live operation, not a
// reusable permission or cross-provider transaction. Consumers validate after
// review/preflight immediately before each durable dispatch claim. Revocation
// after admission cannot undo an accepted effect; unknown validation admits none.
type Validation struct {
	Valid     bool               `json:"valid"`
	Reason    string             `json:"reason,omitempty"`
	Principal principal.Identity `json:"principal,omitempty"`
	Scope     Scope              `json:"scope,omitempty"`
	ExpiresAt time.Time          `json:"expires_at,omitempty"`
}
type RevokeRequest struct {
	Reference Reference `json:"reference"`
	Scope     Scope     `json:"scope"`
}
type RevokeResult struct {
	Revoked bool `json:"revoked"`
}

func RequestDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return RawRequestDigest(raw)
}
func RawRequestDigest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func Proof(reference Reference, scope Scope) access.Delegation {
	raw, _ := json.Marshal(scope)
	return access.Delegation{Provider: reference.Provider, Grant: reference.ID, Scope: raw}
}
func bounded(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsAny(value, "\x00\r\n*")
}
func (a AuthorityBinding) Validate() error {
	if !bounded(a.Service, 256) || a.Major < 1 || a.Major > 1<<31-1 || !bounded(a.Provider, 1024) || !bounded(a.Subject, 1024) || !bounded(a.Fingerprint, 1024) {
		return errors.New("execution authority binding is incomplete")
	}
	return nil
}
func boundedList(values []string) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !bounded(v, 256) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func (s Scope) Validate() error {
	if !bounded(s.Consumer, 1024) || !bounded(s.UserSpaceID, 512) || !bounded(s.ProductDomain, 128) || !bounded(s.TaskID, 512) || !bounded(s.ContextDigest, 1024) || s.Revision < 1 || s.Revision > 1<<53-1 || s.Authority.Validate() != nil || s.Depth < 0 || s.Bounds.MaxDepth < 0 || s.Bounds.MaxDepth > 15 || s.Depth > s.Bounds.MaxDepth || !boundedList(s.Bounds.ModelProviders) || !boundedList(s.Bounds.Models) || !boundedList(s.Bounds.PermissionModes) {
		return errors.New("execution grant scope is incomplete")
	}
	switch s.Kind {
	case "task-lineage":
		if s.RunID != "" || s.SessionID != "" {
			return errors.New("task-lineage scope cannot name an exact run")
		}
	case "exact-run":
		if !bounded(s.RunID, 512) || !bounded(s.SessionID, 512) {
			return errors.New("exact-run scope requires run and session")
		}
	default:
		return errors.New("unknown execution grant scope kind")
	}
	return nil
}
func (s Scope) Equal(other Scope) bool {
	return s.Kind == other.Kind && s.Authority == other.Authority && s.Depth == other.Depth && s.Consumer == other.Consumer && s.UserSpaceID == other.UserSpaceID && s.ProductDomain == other.ProductDomain && s.TaskID == other.TaskID && s.RunID == other.RunID && s.SessionID == other.SessionID && s.ContextDigest == other.ContextDigest && s.Revision == other.Revision && s.Bounds.MaxDepth == other.Bounds.MaxDepth && slices.Equal(s.Bounds.ModelProviders, other.Bounds.ModelProviders) && slices.Equal(s.Bounds.Models, other.Bounds.Models) && slices.Equal(s.Bounds.PermissionModes, other.Bounds.PermissionModes)
}

func validCapability(c Capability) bool {
	if !bounded(c.Action, 256) || len(c.Resources) == 0 || len(c.Resources) > 32 {
		return false
	}
	for _, r := range c.Resources {
		if !bounded(r.Kind, 128) || r.Kind == "global" || !bounded(r.ID, 2048) {
			return false
		}
	}
	return true
}
func permits(capabilities []Capability, action string, resources []access.Resource) bool {
	if !validCapability(Capability{Action: action, Resources: resources}) {
		return false
	}
	for _, c := range capabilities {
		if c.Action != action {
			continue
		}
		all := true
		for _, wanted := range resources {
			found := false
			for _, allowed := range c.Resources {
				if wanted == allowed {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func subset(values, allowed []string) bool {
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
	}
	return true
}
func narrowed(parent IssueResult, child IssueRequest, lineage *Lineage) bool {
	p, c := parent.Scope, child.Scope
	if lineage == nil || p.Kind != "task-lineage" || c.Authority != p.Authority || lineage.Authority != p.Authority ||
		c.Consumer != p.Consumer || c.UserSpaceID != p.UserSpaceID || c.ProductDomain != p.ProductDomain ||
		lineage.ParentTaskID != p.TaskID || lineage.ParentRevision != p.Revision || lineage.TaskID != c.TaskID ||
		lineage.RunID != c.RunID || lineage.SessionID != c.SessionID || lineage.Revision != c.Revision ||
		lineage.ContextDigest != c.ContextDigest || lineage.Depth != c.Depth || c.Depth != p.Depth+1 ||
		c.Bounds.MaxDepth > p.Bounds.MaxDepth || !subset(c.Bounds.ModelProviders, p.Bounds.ModelProviders) ||
		!subset(c.Bounds.Models, p.Bounds.Models) || !subset(c.Bounds.PermissionModes, p.Bounds.PermissionModes) || child.ExpiresAt.After(parent.ExpiresAt) {
		return false
	}
	switch c.Kind {
	case "exact-run":
		if !slices.Contains(c.Bounds.ModelProviders, lineage.ModelProvider) || !slices.Contains(c.Bounds.Models, lineage.Model) || !slices.Contains(c.Bounds.PermissionModes, lineage.PermissionMode) {
			return false
		}
	case "task-lineage":
		// An intermediate task has no executing session or model admission.
		// Its bounded delegation can only become an effect after a subsequent
		// selected-authority attestation of an exact run and fresh validation.
		if lineage.RunID != "" || lineage.SessionID != "" || lineage.ModelProvider != "" || lineage.Model != "" || lineage.PermissionMode != "" {
			return false
		}
	default:
		return false
	}
	for _, capability := range child.Capabilities {
		resources := append([]access.Resource(nil), capability.Resources...)
		// Only an explicit same-action parent task resource admits its attested
		// child's task/session. Empty parent fields never act as resource wildcards.
		for i, r := range resources {
			if c.Kind == "exact-run" && r.Kind == "session" && r.ID == c.SessionID || r.Kind == "task" && r.ID == c.TaskID {
				resources[i] = access.Resource{Kind: "task", ID: p.TaskID}
			}
		}
		if !permits(parent.Capabilities, capability.Action, resources) {
			return false
		}
	}
	return true
}

func validCapabilities(values []Capability) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	for _, value := range values {
		if !validCapability(value) {
			return false
		}
	}
	return true
}
func capabilitiesEqual(a, b []Capability) bool {
	if len(a) != len(b) {
		return false
	}
	for i, c := range a {
		if c.Action != b[i].Action || !slices.Equal(c.Resources, b[i].Resources) {
			return false
		}
	}
	return true
}

// CommitRequest identifies one immutable publication intent. Persist it before
// acquisition. A confirmed acquisition admits only this exact operation, not a
// reusable grant. Unknown acquisition admits no effect.
type CommitRequest struct {
	ID              string          `json:"id"`
	OperationDigest string          `json:"operation_digest"`
	Authorization   ValidateRequest `json:"authorization"`
}

// CommitGuard holds leaf and ancestor revocation until terminal disposition.
// ExpiresAt is the grant's admission cutoff, not a fence timeout. An admitted
// operation may finish after expiry; its fence never expires or unlocks on loss.
type CommitGuard struct {
	ID                  string             `json:"id"`
	OperationDigest     string             `json:"operation_digest"`
	AuthorizationDigest string             `json:"authorization_digest"`
	Principal           principal.Identity `json:"principal"`
	ExpiresAt           time.Time          `json:"expires_at"`
}

// ReleaseCommitRequest is cleanup, never permission to perform or retry an
// effect. Committed requires a digest of the authoritative durable receipt;
// not-committed requires proof no effect can still commit. Unknown stays fenced.
// Releasing not-committed before acquisition tombstones a delayed acquire.
type ReleaseCommitRequest struct {
	CommitRequest
	Outcome       string `json:"outcome"`
	ReceiptDigest string `json:"receipt_digest,omitempty"`
}
type CommitRelease struct {
	Released bool `json:"released"`
}
type CommitStatus struct {
	Guard         CommitGuard         `json:"guard"`
	Caller        string              `json:"caller"`
	Outcome       string              `json:"outcome,omitempty"`
	ReceiptDigest string              `json:"receipt_digest,omitempty"`
	ReconciledBy  *principal.Identity `json:"reconciled_by,omitempty"`
	Reason        string              `json:"reason,omitempty"`
}

// ReconcileCommitRequest requires host-attested owner maintenance after stopping
// the original writer and establishing the exact durable receipt disposition.
// It cannot release a different caller, request, or unresolved publication.
type ReconcileCommitRequest struct {
	ReleaseCommitRequest
	ExpectedCaller string `json:"expected_caller"`
	Reason         string `json:"reason"`
}

// CommitList exposes unresolved intents only to authenticated owner maintenance;
// it is for recovery, never permission to replay a publication.
type CommitEntry struct {
	Request CommitRequest `json:"request"`
	CommitStatus
}
type CommitList struct {
	Commits []CommitEntry `json:"commits"`
}
