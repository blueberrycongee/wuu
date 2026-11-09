package personalreview

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Maintenance is restricted to this ledger's exact policy namespace. Provider
// reconciliation additionally requires the host's authenticated maintenance
// admission; these wrappers never manufacture an operator permission.
func (s *Store) ListPolicyInspections(ctx context.Context) (persistence.InspectionList, error) {
	recovery, ok := s.documents.(persistence.InspectionRecovery)
	if !ok {
		return persistence.InspectionList{}, persistence.ErrInspectionUnsupported
	}
	if s.closed.Load() {
		return persistence.InspectionList{}, persistence.ErrClosed
	}
	all, err := recovery.ListInspections(ctx)
	if err != nil {
		return persistence.InspectionList{}, err
	}
	result := persistence.InspectionList{Inspections: []persistence.InspectionStatus{}}
	for _, guard := range all.Inspections {
		if len(guard.Namespaces) == 1 && guard.Namespaces[0] == reviewSettings {
			result.Inspections = append(result.Inspections, guard)
		}
	}
	return result, nil
}
func (s *Store) ReconcilePolicyInspection(ctx context.Context, request persistence.InspectionResolution) (persistence.InspectionStatus, error) {
	if len(request.Namespaces) != 1 || request.Namespaces[0] != reviewSettings {
		return persistence.InspectionStatus{}, errors.New("review reconciliation requires its exact policy namespace")
	}
	recovery, ok := s.documents.(persistence.InspectionRecovery)
	if !ok {
		return persistence.InspectionStatus{}, persistence.ErrInspectionUnsupported
	}
	if s.closed.Load() {
		return persistence.InspectionStatus{}, persistence.ErrClosed
	}
	return recovery.ReconcileInspection(ctx, request)
}
func (s *Store) PolicyInspectionAuthority(ctx context.Context) (persistence.InspectionTopology, error) {
	authorityProvider, ok := s.documents.(interface {
		Authority(context.Context) (persistence.Authority, error)
	})
	if !ok {
		return persistence.InspectionTopology{}, persistence.ErrInspectionUnsupported
	}
	if s.closed.Load() {
		return persistence.InspectionTopology{}, persistence.ErrClosed
	}
	authority, err := authorityProvider.Authority(ctx)
	if err != nil {
		return persistence.InspectionTopology{}, err
	}
	if err = authority.Validate(); err != nil {
		return persistence.InspectionTopology{}, err
	}
	return persistence.InspectionTopology{Version: 1, StorageTopology: authority.StorageTopology, ExecutionTopology: authority.ExecutionTopology}, nil
}
