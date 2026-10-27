package executionauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const grantsNamespace = "wuu.execution_grants.v1"
const requestsNamespace = "wuu.execution_grant_requests.v1"
const eventsNamespace = "wuu.execution_grant_events.v1"

type grantRecord struct {
	IssueResult
	Parent    *Reference `json:"parent,omitempty"`
	Authority string     `json:"authority"`
	Revoked   bool       `json:"revoked"`
}
type requestRecord struct {
	Digest    string    `json:"digest"`
	Reference Reference `json:"reference"`
}

// Service owns grant semantics, not authentication or an unselected fallback.
// Documents must be captured from the generation retained by composition.
type Service struct {
	mu        sync.RWMutex
	id        string
	state     pluginhost.State
	documents persistence.Documents
	now       func() time.Time
}

func NewService(id string, documents persistence.Documents) *Service {
	return &Service{id: id, state: pluginhost.StatePrepared, documents: documents, now: time.Now}
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func Descriptor() pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"describe", "inspect", "issue", "validate", "revoke"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ServiceName + "." + name + ".input.v1", OutputSchema: ServiceName + "." + name + ".output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: ServiceName, Version: "1.0.0", Methods: methods}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{Descriptor()}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (s *Service) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.state != pluginhost.StatePrepared || s.documents == nil {
		return errors.New("selected execution grant storage unavailable")
	}
	if err := persistence.ClaimNamespaces(ctx, s.documents, []string{grantsNamespace, requestsNamespace, eventsNamespace}); err != nil {
		return err
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func decode(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid execution authorization request")
	}
	return nil
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive || p.Service != ServiceName {
		return nil, errors.New("selected execution authorization unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result any
	var err error
	switch p.Method {
	case "describe":
		var r struct{}
		if err = decode(p.Params, &r); err == nil {
			result = PolicyInfo{DefaultLifetimeSeconds: int64((7 * 24 * time.Hour) / time.Second), MaxLifetimeSeconds: int64(MaxGrantLifetime / time.Second), AuthorityKinds: []string{"local-installation"}, ScopeKinds: []string{"task-lineage", "exact-run"}}
		}
	case "inspect":
		var r InspectRequest
		if err = decode(p.Params, &r); err == nil {
			result, err = s.inspect(ctx, r)
		}
	case "issue":
		var r IssueRequest
		if err = decode(p.Params, &r); err == nil {
			var who principal.Identity
			var authority string
			action := "execution/grant/issue"
			var lineage *Lineage
			if r.Parent != nil {
				action = "execution/grant/derive"
				lineage = &Lineage{}
				if p.Admission == nil || decode(p.Admission.Lineage, lineage) != nil {
					return nil, errors.New("attested selected task lineage required")
				}
			}
			who, authority, err = admission(p, action)
			if err == nil {
				result, err = s.issue(ctx, r, who, authority, lineage)
			}
		}
	case "validate":
		var r ValidateRequest
		if err = decode(p.Params, &r); err == nil {
			result, err = s.validate(ctx, r)
		}
	case "revoke":
		var r RevokeRequest
		if err = decode(p.Params, &r); err == nil {
			var who principal.Identity
			who, _, err = admission(p, "execution/grant/revoke")
			if err == nil {
				result, err = s.revoke(ctx, r, who)
			}
		}
	default:
		err = errors.New("unknown execution authorization method")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func admission(p pluginhost.ServiceInvokeParams, action string) (principal.Identity, string, error) {
	a := p.Admission
	if a == nil || a.Version != 1 || a.Principal.Validate() != nil || a.Action != action || a.InputJSON != string(p.Params) || a.InputDigest == "" || a.InputDigest != RawRequestDigest(p.Params) {
		return principal.Identity{}, "", errors.New("authenticated execution admission required")
	}
	if a.Authority != "local-installation" && !(action == "execution/grant/derive" && a.Authority == "task-lineage") {
		return principal.Identity{}, "", errors.New("durable account validity is unsupported by selected execution provider")
	}
	return a.Principal, a.Authority, nil
}
func (s *Service) issue(ctx context.Context, r IssueRequest, who principal.Identity, authority string, lineage *Lineage) (IssueResult, error) {
	if !bounded(r.RequestID, 256) || r.Scope.Validate() != nil || len(r.Capabilities) == 0 || len(r.Capabilities) > 64 || !r.ExpiresAt.After(s.now()) || r.ExpiresAt.After(s.now().Add(MaxGrantLifetime)) {
		return IssueResult{}, errors.New("execution grant must have a bounded scope and expiry")
	}
	for _, c := range r.Capabilities {
		if !validCapability(c) {
			return IssueResult{}, errors.New("execution grant capability is invalid")
		}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return IssueResult{}, err
	}
	reference := Reference{Provider: s.id, ID: hex.EncodeToString(entropy[:])}
	result := IssueResult{Reference: reference, Principal: who, Scope: r.Scope, Capabilities: r.Capabilities, ExpiresAt: r.ExpiresAt}
	key := RequestDigest(struct {
		Principal principal.Identity
		Consumer  string
		ID        string
	}{who, r.Scope.Consumer, r.RequestID})
	digest := RequestDigest(r)
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var existing requestRecord
		found, err := tx.Get(requestsNamespace, key, &existing)
		if err != nil {
			return err
		}
		if found {
			if existing.Digest != digest {
				return errors.New("execution grant request ID conflicts with prior scope")
			}
			var stored grantRecord
			ok, err := tx.Get(grantsNamespace, existing.Reference.ID, &stored)
			if err != nil {
				return err
			}
			if !ok || stored.Reference != existing.Reference {
				return persistence.ErrCorrupt
			}
			result = stored.IssueResult
			return nil
		}
		if r.Parent != nil {
			parent, ok, err := s.activeGrant(tx, *r.Parent)
			if err != nil {
				return err
			}
			if !ok || parent.Principal != who || !narrowed(parent.IssueResult, r, lineage) {
				return errors.New("child execution grant exceeds parent scope")
			}
			authority = parent.Authority
		}
		if err = tx.Put(grantsNamespace, reference.ID, grantRecord{IssueResult: result, Parent: r.Parent, Authority: authority}); err != nil {
			return err
		}
		if err = tx.Put(requestsNamespace, key, requestRecord{Digest: digest, Reference: reference}); err != nil {
			return err
		}
		return tx.Put(eventsNamespace, reference.ID+"/issued", struct {
			Principal principal.Identity `json:"principal"`
			At        time.Time          `json:"at"`
		}{who, s.now().UTC()})
	})
	return result, err
}
func (s *Service) activeGrant(tx *persistence.Tx, reference Reference) (grantRecord, bool, error) {
	var leaf grantRecord
	seen := map[string]bool{}
	for depth := 0; depth < 16; depth++ {
		if reference.Provider != s.id || !bounded(reference.ID, 128) || seen[reference.ID] {
			return grantRecord{}, false, nil
		}
		seen[reference.ID] = true
		var r grantRecord
		found, err := tx.Get(grantsNamespace, reference.ID, &r)
		if err != nil {
			return grantRecord{}, false, err
		}
		if !found || r.Reference != reference || r.Revoked || r.Authority != "local-installation" || r.Principal.Validate() != nil || r.Scope.Validate() != nil || !validCapabilities(r.Capabilities) || !r.ExpiresAt.After(s.now()) {
			return grantRecord{}, false, nil
		}
		if depth == 0 {
			leaf = r
		}
		if r.Parent == nil {
			return leaf, true, nil
		}
		reference = *r.Parent
	}
	return grantRecord{}, false, nil
}
func (s *Service) validate(ctx context.Context, r ValidateRequest) (Validation, error) {
	result := Validation{Reason: "execution-grant-invalid"}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		result = Validation{Reason: "execution-grant-invalid"}
		record, ok, err := s.activeGrant(tx, r.Reference)
		if err != nil {
			return err
		}
		if !ok || r.Scope.Validate() != nil || !record.Scope.Equal(r.Scope) || !permits(record.Capabilities, r.Action, r.Resources) {
			return nil
		}
		result = Validation{Valid: true, Principal: record.Principal, Scope: record.Scope, ExpiresAt: record.ExpiresAt}
		return nil
	})
	return result, err
}
func (s *Service) revoke(ctx context.Context, r RevokeRequest, who principal.Identity) (RevokeResult, error) {
	result := RevokeResult{}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		if r.Reference.Provider != s.id || r.Scope.Validate() != nil {
			return errors.New("execution grant scope mismatch")
		}
		var record grantRecord
		found, err := tx.Get(grantsNamespace, r.Reference.ID, &record)
		if err != nil {
			return err
		}
		if !found || record.Reference != r.Reference || !record.Scope.Equal(r.Scope) || record.Principal != who {
			return errors.New("execution grant scope mismatch")
		}
		result.Revoked = true
		if record.Revoked {
			return nil
		}
		record.Revoked = true
		if err = tx.Put(grantsNamespace, r.Reference.ID, record); err != nil {
			return err
		}
		return tx.Put(eventsNamespace, r.Reference.ID+"/revoked", struct {
			Principal principal.Identity `json:"principal"`
			At        time.Time          `json:"at"`
		}{who, s.now().UTC()})
	})
	return result, err
}

func (s *Service) inspect(ctx context.Context, r InspectRequest) (IssueResult, error) {
	var result IssueResult
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		record, ok, err := s.activeGrant(tx, r.Reference)
		if err != nil {
			return err
		}
		if !ok || !bounded(r.Consumer, 1024) || record.Scope.Consumer != r.Consumer {
			return &pluginhost.HostServiceError{Code: "execution_authorization_required", Message: "execution grant is invalid or expired"}
		}
		result = record.IssueResult
		return nil
	})
	return result, err
}
