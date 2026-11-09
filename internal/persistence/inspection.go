package persistence

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

var ErrInspectionActive = inspectionjournal.ErrActive
var ErrInspectionReleased = inspectionjournal.ErrReleased
var ErrInspectionUnsupported = errors.New("selected provider does not support fenced inspection")

type InspectionRequest struct {
	ID         string   `json:"id"`
	Namespaces []string `json:"namespaces"`
}
type InspectionGuard struct {
	ID         string   `json:"id"`
	Namespaces []string `json:"namespaces"`
	Revision   Revision `json:"revision,string"`
}

// Inspector is optional. Acquisition fences namespace writes until an explicit,
// idempotent release; it never expires on a timer or unlocks on transport loss.
type Inspector interface {
	AcquireInspection(context.Context, InspectionRequest) (InspectionGuard, error)
	ReleaseInspection(context.Context, InspectionRequest) error
}

func NewInspectionID() string {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

// WithInspection calls use only after a confirmed acquisition. Cleanup uses the
// same captured provider even on unknown acquisition or caller cancellation.
// Failed cleanup leaves a durable fence for explicit recovery, never a replay.
func WithInspection(ctx context.Context, documents Documents, namespaces []string, use func() error) (err error) {
	inspector, ok := documents.(Inspector)
	if !ok {
		return ErrInspectionUnsupported
	}
	if use == nil {
		return fmt.Errorf("%w: inspection callback is required", ErrInvalid)
	}
	request := InspectionRequest{ID: NewInspectionID(), Namespaces: slices.Clone(namespaces)}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err = errors.Join(err, inspector.ReleaseInspection(cleanup, request))
	}()
	guard, err := inspector.AcquireInspection(ctx, request)
	if err != nil {
		return err
	}
	if guard.ID != request.ID || !sameNamespaces(guard.Namespaces, request.Namespaces) {
		return fmt.Errorf("%w: invalid inspection response", ErrCorrupt)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return use()
}
func sameNamespaces(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
func (c *Client) AcquireInspection(ctx context.Context, request InspectionRequest) (InspectionGuard, error) {
	var guard InspectionGuard
	err := c.invoke(ctx, "acquire-inspection", request, &guard)
	return guard, inspectionClientError(err)
}
func (c *Client) ReleaseInspection(ctx context.Context, request InspectionRequest) error {
	var result struct{}
	return inspectionClientError(c.invoke(ctx, "release-inspection", request, &result))
}
func inspectionClientError(err error) error {
	var host *pluginhost.HostServiceError
	if errors.As(err, &host) && host.Code == "method_not_found" {
		return errors.Join(ErrInspectionUnsupported, err)
	}
	return err
}
func (s *Store) AcquireInspection(ctx context.Context, request InspectionRequest) (InspectionGuard, error) {
	return s.acquireInspection(ctx, namespaceIdentityFrom(ctx).caller, request)
}
func (s *Store) ReleaseInspection(ctx context.Context, request InspectionRequest) error {
	return s.releaseInspection(ctx, namespaceIdentityFrom(ctx).caller, request)
}
func (s *Store) acquireInspection(ctx context.Context, owner string, request InspectionRequest) (InspectionGuard, error) {
	if err := inspectionjournal.Validate(request.ID, request.Namespaces); err != nil {
		return InspectionGuard{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return InspectionGuard{}, err
	}
	defer lock.Release()
	if err := s.checkNamespaceAccess(ctx, request.Namespaces); err != nil {
		return InspectionGuard{}, err
	}
	if err := ctx.Err(); err != nil {
		return InspectionGuard{}, err
	}
	if err := inspectionjournal.Acquire(s.dir, owner, request.ID, request.Namespaces); err != nil {
		return InspectionGuard{}, err
	}
	view, err := s.backend.read(ctx, Query{Namespace: ServiceName, Keys: []string{}})
	return InspectionGuard{request.ID, slices.Clone(request.Namespaces), view.Revision}, err
}
func (s *Store) releaseInspection(ctx context.Context, owner string, request InspectionRequest) error {
	if err := inspectionjournal.Validate(request.ID, request.Namespaces); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := s.checkNamespaceAccess(ctx, request.Namespaces); err != nil {
		return err
	}
	return inspectionjournal.Release(s.dir, owner, request.ID, request.Namespaces)
}

// InspectionStatus exposes only fence ownership and scope, never protected data.
type InspectionStatus struct {
	ID           string   `json:"id"`
	Owner        string   `json:"owner"`
	Namespaces   []string `json:"namespaces"`
	ReconciledBy string   `json:"reconciled_by,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}
type InspectionList struct {
	Inspections []InspectionStatus `json:"inspections"`
}
type InspectionResolution struct {
	InspectionRequest
	ExpectedOwner string `json:"expected_owner"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason"`
}

// InspectionRecovery is administrative recovery, not ordinary effect admission.
// Callers must obtain operator authorization and establish terminal disposition.
type InspectionRecovery interface {
	ListInspections(context.Context) (InspectionList, error)
	ReconcileInspection(context.Context, InspectionResolution) (InspectionStatus, error)
}

func inspectionStatus(r inspectionjournal.Record) InspectionStatus {
	return InspectionStatus{r.ID, r.Owner, r.Scopes, r.ReconciledBy, r.Outcome, r.Reason}
}
func (c *Client) ListInspections(ctx context.Context) (InspectionList, error) {
	var result InspectionList
	err := c.invoke(ctx, "list-inspections", struct{}{}, &result)
	return result, inspectionClientError(err)
}
func (c *Client) ReconcileInspection(ctx context.Context, request InspectionResolution) (InspectionStatus, error) {
	var result InspectionStatus
	err := c.invoke(ctx, "reconcile-inspection", request, &result)
	return result, inspectionClientError(err)
}
func (s *Store) ListInspections(ctx context.Context) (InspectionList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return InspectionList{}, err
	}
	defer lock.Release()
	records, err := inspectionjournal.List(s.dir)
	result := InspectionList{Inspections: []InspectionStatus{}}
	for _, r := range records {
		if accessErr := s.checkNamespaceAccess(ctx, r.Scopes); errors.Is(accessErr, ErrNamespaceOwnership) {
			continue
		} else if accessErr != nil {
			return InspectionList{}, accessErr
		}
		result.Inspections = append(result.Inspections, inspectionStatus(r))
	}
	return result, err
}
func (s *Store) ReconcileInspection(ctx context.Context, request InspectionResolution) (InspectionStatus, error) {
	return s.reconcileInspection(ctx, namespaceIdentityFrom(ctx).caller, request)
}
func (s *Store) reconcileInspection(ctx context.Context, caller string, request InspectionResolution) (InspectionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return InspectionStatus{}, err
	}
	defer lock.Release()
	if err := s.checkNamespaceAccess(ctx, request.Namespaces); err != nil {
		return InspectionStatus{}, err
	}
	record, err := inspectionjournal.Reconcile(s.dir, caller, request.ID, request.ExpectedOwner, request.Namespaces, request.Outcome, request.Reason)
	return inspectionStatus(record), err
}

// InspectionTopology declares this selected deployment, not a guessed local path.
// Multi-host maintenance needs an additional exclusive-writer proof; this v1
// declaration alone never authorizes reconciliation on multiple hosts.
type InspectionTopology struct {
	Version           int    `json:"version"`
	StorageTopology   string `json:"storage_topology"`
	ExecutionTopology string `json:"execution_topology"`
}

func (s *Store) Authority(context.Context) (Authority, error) { return LegacyAuthority(), nil }
