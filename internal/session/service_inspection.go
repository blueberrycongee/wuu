package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

var inspectionDomainMethods = []string{"AcquireInspection", "ReleaseInspection", "ListInspections", "ReconcileInspection", "InspectionAuthority"}
var sessionInspectionNamespaces = []string{
	documentNamespace("sessions"), documentNamespace("session_messages"), documentNamespace("session_history_heads"), documentNamespace("session_controls"), documentNamespace("session_history_checkpoints"), documentNamespace("session_history_retractions"), documentNamespace("client_input_receipts"),
}

func sessionInspectionID(caller, id string) string {
	return "session." + base64.RawURLEncoding.EncodeToString([]byte(caller)) + "." + id
}

func (s *documentStore) AcquireInspection(id string) (persistence.InspectionGuard, error) {
	inspector, ok := s.documents.(persistence.Inspector)
	if !ok {
		return persistence.InspectionGuard{}, persistence.ErrInspectionUnsupported
	}
	guard, err := inspector.AcquireInspection(s.ctx, persistence.InspectionRequest{ID: sessionInspectionID(s.caller, id), Namespaces: sessionInspectionNamespaces})
	guard.ID = id
	return guard, err
}
func (s *documentStore) ReleaseInspection(id string) error {
	inspector, ok := s.documents.(persistence.Inspector)
	if !ok {
		return persistence.ErrInspectionUnsupported
	}
	return inspector.ReleaseInspection(s.ctx, persistence.InspectionRequest{ID: sessionInspectionID(s.caller, id), Namespaces: sessionInspectionNamespaces})
}

// AcquireInspection and ReleaseInspection allow a durable coordinator to keep
// the exact inspection identity through an uncertain publication or restart.
func (s *Store) AcquireInspection(ctx context.Context, id string) (persistence.InspectionGuard, error) {
	var guard persistence.InspectionGuard
	err := s.WithContext(ctx).invoke("AcquireInspection", []any{id}, []any{&guard})
	if err == nil && guard.ID != id {
		return persistence.InspectionGuard{}, persistence.ErrCorrupt
	}
	return guard, sessionInspectionError(err)
}
func (s *Store) ReleaseInspection(ctx context.Context, id string) error {
	return sessionInspectionError(s.WithContext(ctx).invoke("ReleaseInspection", []any{id}, []any{}))
}

// WithInspection retains the selected session provider's authority boundary.
// It never invokes the effect after an uncertain acquire and never retries it.
func (s *Store) WithInspection(ctx context.Context, use func() error) (err error) {
	id := persistence.NewInspectionID()
	client := s.WithContext(ctx)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		releaseErr := s.WithContext(cleanup).invoke("ReleaseInspection", []any{id}, []any{})
		err = errors.Join(err, sessionInspectionError(releaseErr))
	}()
	var guard persistence.InspectionGuard
	if err = client.invoke("AcquireInspection", []any{id}, []any{&guard}); err != nil {
		return sessionInspectionError(err)
	}
	if guard.ID != id {
		return persistence.ErrCorrupt
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return use()
}
func sessionInspectionError(err error) error {
	var host *pluginhost.HostServiceError
	if errors.As(err, &host) && host.Code == "method_not_found" {
		return errors.Join(persistence.ErrInspectionUnsupported, err)
	}
	return err
}

// Maintenance uses the exact raw provider identity returned by ListInspections,
// not an inferred host path or the short admission handle.
func (s *documentStore) ListInspections() (persistence.InspectionList, error) {
	recovery, ok := s.documents.(persistence.InspectionRecovery)
	if !ok {
		return persistence.InspectionList{}, persistence.ErrInspectionUnsupported
	}
	all, err := recovery.ListInspections(s.inspectionMaintenanceContext("list-inspections", struct{}{}))
	if err != nil {
		return all, err
	}
	result := persistence.InspectionList{Inspections: []persistence.InspectionStatus{}}
	for _, entry := range all.Inspections {
		if matchingSessionInspection(entry.Namespaces) {
			result.Inspections = append(result.Inspections, entry)
		}
	}
	return result, nil
}
func matchingSessionInspection(namespaces []string) bool {
	expected := slices.Clone(sessionInspectionNamespaces)
	actual := slices.Clone(namespaces)
	slices.Sort(expected)
	slices.Sort(actual)
	return slices.Equal(expected, actual)
}
func (s *documentStore) ReconcileInspection(request persistence.InspectionResolution) (persistence.InspectionStatus, error) {
	recovery, ok := s.documents.(persistence.InspectionRecovery)
	if !ok {
		return persistence.InspectionStatus{}, persistence.ErrInspectionUnsupported
	}
	if !matchingSessionInspection(request.Namespaces) {
		return persistence.InspectionStatus{}, persistence.ErrInvalid
	}
	return recovery.ReconcileInspection(s.inspectionMaintenanceContext("reconcile-inspection", request), request)
}
func (s *Store) ListInspections(ctx context.Context) (persistence.InspectionList, error) {
	var result persistence.InspectionList
	err := s.WithContext(ctx).invoke("ListInspections", []any{}, []any{&result})
	return result, sessionInspectionError(err)
}
func (s *Store) ReconcileInspection(ctx context.Context, request persistence.InspectionResolution) (persistence.InspectionStatus, error) {
	var result persistence.InspectionStatus
	err := s.WithContext(ctx).invoke("ReconcileInspection", []any{request}, []any{&result})
	return result, sessionInspectionError(err)
}

func (s *documentStore) InspectionAuthority() (persistence.InspectionTopology, error) {
	reader, ok := s.documents.(interface {
		Authority(context.Context) (persistence.Authority, error)
	})
	if !ok {
		return persistence.InspectionTopology{}, persistence.ErrInspectionUnsupported
	}
	authority, err := reader.Authority(s.ctx)
	if err != nil {
		return persistence.InspectionTopology{}, err
	}
	if err := authority.Validate(); err != nil {
		return persistence.InspectionTopology{}, err
	}
	return persistence.InspectionTopology{Version: 1, StorageTopology: authority.StorageTopology, ExecutionTopology: authority.ExecutionTopology}, nil
}
func (s *Store) InspectionAuthority(ctx context.Context) (persistence.InspectionTopology, error) {
	var result persistence.InspectionTopology
	err := s.WithContext(ctx).invoke("InspectionAuthority", []any{}, []any{&result})
	return result, sessionInspectionError(err)
}

// The session provider delegates only the verified operation, re-bound to the
// exact document call bytes and its own selected dependency identity.
func (s *documentStore) inspectionMaintenanceContext(method string, input any) context.Context {
	if s.admission == nil {
		return s.ctx
	}
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	proof := *s.admission
	proof.InputJSON = string(raw)
	proof.InputDigest = hex.EncodeToString(digest[:])
	return pluginhost.WithServiceAdmission(s.ctx, s.providerID, persistence.ServiceName, method, proof)
}
