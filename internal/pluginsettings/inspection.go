package pluginsettings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type InspectionRequest struct {
	ReadRequest
	ID string `json:"id"`
}
type StateInspection struct {
	ID string `json:"id"`
	StateView
}

// WithStateInspection prevents state CAS, direct writes and ownership transfers
// for the subject until cleanup. No uncertain acquisition can enter use.
func (c *Client) WithStateInspection(ctx context.Context, subject string, scope Scope, use func(StateDocument) error) (err error) {
	request := InspectionRequest{ReadRequest: ReadRequest{Subject: subject, Scope: scope}, ID: persistence.NewInspectionID()}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		var result struct{}
		err = errors.Join(err, inspectionError(c.invoke(cleanup, "release-inspection", request, &result)))
	}()
	var result StateInspection
	if err = c.invoke(ctx, "acquire-inspection", request, &result); err != nil {
		return inspectionError(err)
	}
	if result.ID != request.ID || result.Document.PluginID != subject || result.Revision == "" {
		return settingsError("invalid_response", "invalid state inspection response")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return use(result.Document)
}
func inspectionError(err error) error {
	var host *pluginhost.HostServiceError
	if errors.As(err, &host) && host.Code == "method_not_found" {
		return errors.Join(persistence.ErrInspectionUnsupported, settingsError("inspection_unsupported", "selected settings provider does not support fenced inspection"))
	}
	return err
}

func (s *Service) inspectState(ctx context.Context, caller string, request InspectionRequest, release bool) (StateInspection, error) {
	if caller == "" {
		return StateInspection{}, settingsError("invalid_params", "inspection caller is required")
	}
	if err := inspectionjournal.Validate(request.ID, []string{request.Subject}); err != nil {
		return StateInspection{}, settingsError("invalid_params", err.Error())
	}
	path, dir, err := stateDocumentPath(s.stateRoot, s.project, request.Subject, request.Scope)
	if err != nil {
		return StateInspection{}, err
	}
	key := caller + "\x00" + request.ID
	created := false
	if !release {
		s.guardMu.Lock()
		created = !s.guards[key]
		s.guards[key] = true
		s.guardMu.Unlock()
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return StateInspection{}, err
	}
	defer lock.Release()
	if release {
		err = inspectionjournal.Release(dir, caller, request.ID, []string{request.Subject})
		if err == nil {
			s.guardMu.Lock()
			delete(s.guards, key)
			close(s.guardChanged)
			s.guardChanged = make(chan struct{})
			s.guardMu.Unlock()
		}
		return StateInspection{}, err
	}
	if err := ctx.Err(); err != nil {
		return StateInspection{}, err
	}
	if err := inspectionjournal.Acquire(dir, caller, request.ID, []string{request.Subject}); err != nil {
		if created && (errors.Is(err, inspectionjournal.ErrReleased) || errors.Is(err, inspectionjournal.ErrConflict)) {
			s.guardMu.Lock()
			delete(s.guards, key)
			close(s.guardChanged)
			s.guardChanged = make(chan struct{})
			s.guardMu.Unlock()
		}
		return StateInspection{}, err
	}
	record, err := readStateRecord(path, request.Subject)
	return StateInspection{request.ID, StateView{visibleStateDocument(record), documentRevision(record)}}, err
}
func checkStateInspection(dir string, subjects ...string) error {
	if err := inspectionjournal.Check(dir, subjects); err != nil {
		return fmt.Errorf("plugin state inspection: %w", err)
	}
	return nil
}

type InspectionStatus struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	Subject      string `json:"subject"`
	Scope        Scope  `json:"scope"`
	ReconciledBy string `json:"reconciled_by,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Reason       string `json:"reason,omitempty"`
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

func settingsInspectionStatus(record inspectionjournal.Record, scope Scope) InspectionStatus {
	subject := ""
	if len(record.Scopes) == 1 {
		subject = record.Scopes[0]
	}
	return InspectionStatus{record.ID, record.Owner, subject, scope, record.ReconciledBy, record.Outcome, record.Reason}
}
func (c *Client) ListInspections(ctx context.Context, scope Scope) (InspectionList, error) {
	var result InspectionList
	err := c.invoke(ctx, "list-inspections", struct {
		Scope Scope `json:"scope"`
	}{scope}, &result)
	return result, inspectionError(err)
}
func (c *Client) ReconcileInspection(ctx context.Context, request InspectionResolution) (InspectionStatus, error) {
	var result InspectionStatus
	err := c.invoke(ctx, "reconcile-inspection", request, &result)
	return result, inspectionError(err)
}
func (s *Service) listInspections(ctx context.Context, scope Scope) (InspectionList, error) {
	_, dir, err := stateDocumentPath(s.stateRoot, s.project, "inspection", scope)
	if err != nil {
		return InspectionList{}, err
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return InspectionList{}, err
	}
	defer lock.Release()
	records, err := inspectionjournal.List(dir)
	result := InspectionList{Inspections: []InspectionStatus{}}
	for _, record := range records {
		result.Inspections = append(result.Inspections, settingsInspectionStatus(record, scope))
	}
	return result, err
}
func (s *Service) reconcileInspection(ctx context.Context, caller string, request InspectionResolution) (InspectionStatus, error) {
	_, dir, err := stateDocumentPath(s.stateRoot, s.project, request.Subject, request.Scope)
	if err != nil {
		return InspectionStatus{}, err
	}
	lock, err := storelock.AcquireContext(ctx, dir)
	if err != nil {
		return InspectionStatus{}, err
	}
	defer lock.Release()
	record, err := inspectionjournal.Reconcile(dir, caller, request.ID, request.ExpectedOwner, []string{request.Subject}, request.Outcome, request.Reason)
	if err == nil {
		s.guardMu.Lock()
		delete(s.guards, request.ExpectedOwner+"\x00"+request.ID)
		close(s.guardChanged)
		s.guardChanged = make(chan struct{})
		s.guardMu.Unlock()
	}
	return settingsInspectionStatus(record, request.Scope), err
}

func (c *Client) InspectionAuthority(ctx context.Context) (persistence.InspectionTopology, error) {
	var result persistence.InspectionTopology
	err := c.invoke(ctx, "inspection-authority", struct{}{}, &result)
	return result, inspectionError(err)
}
