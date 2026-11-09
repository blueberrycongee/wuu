package extensions

import (
	"errors"
	"strings"
	"time"
)

type GrantScope string

const (
	GrantScopeAction  GrantScope = "action"
	GrantScopeSession GrantScope = "session"
	GrantScopeProject GrantScope = "project"
	GrantScopeUser    GrantScope = "user"
)

type Grant struct {
	SourceIdentity string     `json:"source_identity,omitempty"`
	SubjectID      string     `json:"subject_id"`
	Fingerprint    string     `json:"fingerprint"`
	Scope          GrantScope `json:"scope"`
	Permissions    []string   `json:"permissions,omitempty"`
	ApprovedAt     time.Time  `json:"approved_at"`
}

// PolicyDecision records a durable user decision for a specific subject and
// exact package fingerprint. If the package fingerprint changes the decision
// no longer applies.
type PolicyDecision struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

// Settings persists package grants and policy decisions (enabled/disabled/rejected).
// Grants are keyed by subject id and must match the exact fingerprint stored in
// the grant. Disabled preserves a valid grant but prevents activation.
// Rejected records the rejected fingerprint and avoids repeated prompting.
type Settings struct {
	Grants   map[string]Grant          `json:"grants,omitempty"`
	Enabled  map[string]bool           `json:"enabled,omitempty"`
	Disabled map[string]bool           `json:"disabled,omitempty"`
	Rejected map[string]PolicyDecision `json:"rejected,omitempty"`
}

// FindGrant returns the stored grant only if both the subject id and the
// fingerprint match exactly. A changed or missing fingerprint returns false.
func (s Settings) FindGrant(subjectID, fingerprint string) (Grant, bool) {
	grant, ok := s.Grants[subjectID]
	if !ok || grant.SubjectID != subjectID || grant.Fingerprint != fingerprint {
		return Grant{}, false
	}
	grant.Permissions = append([]string(nil), grant.Permissions...)
	return grant, true
}

// IsDisabled reports whether the subject has been explicitly disabled.
func (s Settings) IsDisabled(subjectID string) bool {
	return s.Disabled[subjectID]
}

// IsEnabled applies an explicit user choice over the package's default state.
func (s Settings) IsEnabled(subjectID string, defaultEnabled bool) bool {
	if s.Disabled[subjectID] {
		return false
	}
	if s.Enabled[subjectID] {
		return true
	}
	return defaultEnabled
}

// IsRejected reports whether the exact fingerprint for the subject has been
// rejected. A changed fingerprint is not considered rejected.
func (s Settings) IsRejected(subjectID, fingerprint string) bool {
	decision, ok := s.Rejected[subjectID]
	return ok && decision.Fingerprint == fingerprint
}

// SetDisabled records or clears the disabled flag for a subject. Disabling
// preserves any existing grant but prevents activation.
func (s *Settings) SetDisabled(subjectID string, disabled bool) {
	if s.Disabled == nil {
		s.Disabled = map[string]bool{}
	}
	if disabled {
		s.Disabled[subjectID] = true
		if s.Enabled != nil {
			delete(s.Enabled, subjectID)
		}
		return
	}
	delete(s.Disabled, subjectID)
}

// SetEnabled records an explicit package activation choice.
func (s *Settings) SetEnabled(subjectID string, enabled bool) {
	if s.Enabled == nil {
		s.Enabled = map[string]bool{}
	}
	if enabled {
		s.Enabled[subjectID] = true
		if s.Disabled != nil {
			delete(s.Disabled, subjectID)
		}
		return
	}
	delete(s.Enabled, subjectID)
	s.SetDisabled(subjectID, true)
}

// RecordGrant stores an approval for the exact subject and fingerprint and
// clears any prior rejection for that subject.
func (s *Settings) RecordGrant(grant Grant) error {
	if strings.TrimSpace(grant.SubjectID) == "" {
		return errors.New("subject id is required")
	}
	if strings.TrimSpace(grant.Fingerprint) == "" {
		return errors.New("fingerprint is required")
	}
	if s.Grants == nil {
		s.Grants = map[string]Grant{}
	}
	if s.Rejected == nil {
		s.Rejected = map[string]PolicyDecision{}
	}
	if prior, ok := s.FindGrant(grant.SubjectID, grant.Fingerprint); ok && grant.SourceIdentity == "" {
		grant.SourceIdentity = prior.SourceIdentity
	}
	s.Grants[grant.SubjectID] = grant
	delete(s.Rejected, grant.SubjectID)
	return nil
}

// RecordRejection records a rejected fingerprint for a subject and removes any
// matching grant.
func (s *Settings) RecordRejection(subjectID, fingerprint string) error {
	if strings.TrimSpace(subjectID) == "" {
		return errors.New("subject id is required")
	}
	if strings.TrimSpace(fingerprint) == "" {
		return errors.New("fingerprint is required")
	}
	if s.Rejected == nil {
		s.Rejected = map[string]PolicyDecision{}
	}
	if s.Grants == nil {
		s.Grants = map[string]Grant{}
	}
	s.Rejected[subjectID] = PolicyDecision{
		Fingerprint: fingerprint,
		At:          time.Now().UTC(),
	}
	delete(s.Grants, subjectID)
	return nil
}

// Revoke removes all policy state (grant, disabled, and rejection) for the
// subject.
func (s *Settings) Revoke(subjectID string) {
	if s.Grants != nil {
		delete(s.Grants, subjectID)
	}
	if s.Disabled != nil {
		delete(s.Disabled, subjectID)
	}
	if s.Enabled != nil {
		delete(s.Enabled, subjectID)
	}
	if s.Rejected != nil {
		delete(s.Rejected, subjectID)
	}
}

// TrustInstallation records the exact installed generation and its local source.
// Updates preserve an explicit disabled choice; first installation enables it.
func (s *Settings) TrustInstallation(subject, fingerprint, source string, permissions []string, first bool) error {
	if err := s.RecordGrant(Grant{SubjectID: subject, Fingerprint: fingerprint, SourceIdentity: source, Scope: GrantScopeUser, Permissions: append([]string(nil), permissions...), ApprovedAt: time.Now().UTC()}); err != nil {
		return err
	}
	if first {
		s.SetEnabled(subject, true)
	}
	return nil
}

// TrustsSource never infers a source for legacy fingerprint-only grants.
func (s Settings) TrustsSource(subject, fingerprint, source string) bool {
	grant, ok := s.FindGrant(subject, fingerprint)
	return ok && source != "" && grant.SourceIdentity == source
}
