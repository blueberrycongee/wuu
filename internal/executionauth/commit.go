package executionauth

import (
	"context"
	"encoding/hex"
	"slices"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const commitsNamespace = "wuu.execution_commits.v1"

type commitRecord struct {
	Request CommitRequest `json:"request"`
	CommitStatus
	References []Reference `json:"references,omitempty"`
}

func commitError(code, message string) error {
	return &pluginhost.HostServiceError{Code: code, Message: message}
}
func digestValid(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == hex.EncodeToString(raw)
}
func (r CommitRequest) valid() bool {
	return bounded(r.ID, 256) && digestValid(r.OperationDigest) && bounded(r.Authorization.Reference.Provider, 1024) && bounded(r.Authorization.Reference.ID, 128) && r.Authorization.Scope.Validate() == nil && r.Authorization.Scope.Kind == "exact-run" && validCapability(Capability{Action: r.Authorization.Action, Resources: r.Authorization.Resources})
}
func (r ReleaseCommitRequest) valid() bool {
	return r.CommitRequest.valid() && (r.Outcome == "committed" && digestValid(r.ReceiptDigest) || r.Outcome == "not-committed" && r.ReceiptDigest == "")
}
func (g CommitGuard) matches(r CommitRequest) bool {
	return g.ID == r.ID && g.OperationDigest == r.OperationDigest && g.AuthorizationDigest == RequestDigest(r.Authorization)
}
func (s *Service) invokeCommit(ctx context.Context, p pluginhost.ServiceInvokeParams) (any, error) {
	if !bounded(p.Caller, 1024) {
		return nil, commitError("permission_denied", "authenticated commit caller required")
	}
	switch p.Method {
	case "acquire-commit":
		var request CommitRequest
		if err := decode(p.Params, &request); err != nil {
			return nil, err
		}
		who, _, err := admission(p, "execution/commit/acquire")
		if err != nil {
			return nil, err
		}
		return s.acquireCommit(ctx, request, p.Caller, who)
	case "release-commit":
		var request ReleaseCommitRequest
		if err := decode(p.Params, &request); err != nil {
			return nil, err
		}
		return s.releaseCommit(ctx, request, p.Caller, nil, "")
	case "inspect-commit":
		var request CommitRequest
		if err := decode(p.Params, &request); err != nil {
			return nil, err
		}
		owner := false
		if p.Admission != nil {
			if _, _, err := admission(p, "execution/commits/inspect"); err != nil {
				return nil, err
			}
			owner = true
		}
		return s.inspectCommit(ctx, request, p.Caller, owner)
	case "list-commits":
		var request struct{}
		if err := decode(p.Params, &request); err != nil {
			return nil, err
		}
		if _, _, err := admission(p, "execution/commits/list"); err != nil {
			return nil, err
		}
		return s.listCommits(ctx)
	case "reconcile-commit":
		var request ReconcileCommitRequest
		if err := decode(p.Params, &request); err != nil {
			return nil, err
		}
		who, _, err := admission(p, "execution/commits/reconcile")
		if err != nil {
			return nil, err
		}
		if !bounded(request.ExpectedCaller, 1024) || !bounded(request.Reason, 2048) {
			return nil, commitError("invalid_request", "commit reconciliation requires exact caller and reason")
		}
		return s.releaseCommit(ctx, request.ReleaseCommitRequest, request.ExpectedCaller, &who, request.Reason)
	}
	return nil, commitError("method_not_found", "unknown commit method")
}
func (s *Service) acquireCommit(ctx context.Context, r CommitRequest, caller string, who principal.Identity) (CommitGuard, error) {
	var result CommitGuard
	if !r.valid() {
		return result, commitError("invalid_request", "bounded exact commit intent required")
	}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		result = CommitGuard{}
		var existing commitRecord
		found, err := tx.Get(commitsNamespace, r.ID, &existing)
		if err != nil {
			return err
		}
		if found {
			if existing.Caller != caller || RequestDigest(existing.Request) != RequestDigest(r) {
				return commitError("execution_commit_conflict", "commit ID belongs to another caller or intent")
			}
			if existing.Outcome != "" {
				return commitError("execution_commit_released", "terminal commit cannot be acquired again")
			}
			if existing.Guard.Principal != who || !existing.Guard.matches(r) || existing.Guard.ExpiresAt.IsZero() {
				return commitError("permission_denied", "commit admission principal mismatch")
			}
			result = existing.Guard
			return nil
		}
		chain, ok, err := s.activeGrantChain(tx, r.Authorization.Reference)
		if err != nil {
			return err
		}
		if !ok || chain[0].Principal != who || !chain[0].Scope.Equal(r.Authorization.Scope) || !permits(chain[0].Capabilities, r.Authorization.Action, r.Authorization.Resources) {
			return commitError("execution_authorization_required", "execution commit grant is invalid, expired, or out of scope")
		}
		for _, grant := range chain {
			if grantNamespace(grant.Reference) != grantsNamespace {
				return commitError("execution_authorization_required", "commit fencing requires explicit live-owner renewal of legacy grant ancestry")
			}
		}
		result = CommitGuard{ID: r.ID, OperationDigest: r.OperationDigest, AuthorizationDigest: RequestDigest(r.Authorization), Principal: who, ExpiresAt: chain[0].ExpiresAt}
		record := commitRecord{Request: r, CommitStatus: CommitStatus{Guard: result, Caller: caller}}
		for _, grant := range chain {
			record.References = append(record.References, grant.Reference)
			grant.ActiveCommits = append(grant.ActiveCommits, r.ID)
			if err = tx.Put(grantsNamespace, grant.Reference.ID, grant); err != nil {
				return err
			}
		}
		return tx.Put(commitsNamespace, r.ID, record)
	})
	if err != nil {
		return CommitGuard{}, err
	}
	return result, nil
}
func (s *Service) releaseCommit(ctx context.Context, r ReleaseCommitRequest, caller string, reconciler *principal.Identity, reason string) (CommitRelease, error) {
	if !r.valid() {
		return CommitRelease{}, commitError("invalid_request", "terminal commit disposition and committed receipt digest required")
	}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var record commitRecord
		found, err := tx.Get(commitsNamespace, r.ID, &record)
		if err != nil {
			return err
		}
		if !found {
			if r.Outcome != "not-committed" || reconciler != nil {
				return commitError("execution_commit_missing", "no admitted commit exists")
			}
			record = commitRecord{Request: r.CommitRequest, CommitStatus: CommitStatus{Caller: caller, Guard: CommitGuard{ID: r.ID, OperationDigest: r.OperationDigest, AuthorizationDigest: RequestDigest(r.Authorization)}}}
		} else if record.Caller != caller || RequestDigest(record.Request) != RequestDigest(r.CommitRequest) {
			return commitError("execution_commit_conflict", "commit ID belongs to another caller or intent")
		}
		if record.Outcome != "" {
			if record.Outcome != r.Outcome || record.ReceiptDigest != r.ReceiptDigest {
				return commitError("execution_commit_conflict", "terminal commit disposition cannot change")
			}
			return nil
		}
		for _, reference := range record.References {
			var grant grantRecord
			found, err := tx.Get(grantsNamespace, reference.ID, &grant)
			if err != nil {
				return err
			}
			if !found || grant.Reference != reference || !slices.Contains(grant.ActiveCommits, r.ID) {
				return persistence.ErrCorrupt
			}
			grant.ActiveCommits = slices.DeleteFunc(grant.ActiveCommits, func(id string) bool { return id == r.ID })
			if err = tx.Put(grantsNamespace, reference.ID, grant); err != nil {
				return err
			}
		}
		record.Outcome = r.Outcome
		record.ReceiptDigest = r.ReceiptDigest
		record.ReconciledBy = reconciler
		record.Reason = reason
		return tx.Put(commitsNamespace, r.ID, record)
	})
	return CommitRelease{Released: err == nil}, err
}
func (s *Service) inspectCommit(ctx context.Context, r CommitRequest, caller string, owner bool) (CommitStatus, error) {
	var result CommitStatus
	if !r.valid() {
		return result, commitError("invalid_request", "bounded exact commit intent required")
	}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		result = CommitStatus{}
		var record commitRecord
		found, err := tx.Get(commitsNamespace, r.ID, &record)
		if err != nil {
			return err
		}
		if !found {
			return commitError("execution_commit_missing", "commit intent not found")
		}
		if (!owner && record.Caller != caller) || RequestDigest(record.Request) != RequestDigest(r) {
			return commitError("execution_commit_conflict", "commit ID belongs to another caller or intent")
		}
		result = record.CommitStatus
		return nil
	})
	return result, err
}

func (s *Service) listCommits(ctx context.Context) (CommitList, error) {
	result := CommitList{Commits: []CommitEntry{}}
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		result = CommitList{Commits: []CommitEntry{}}
		documents, err := tx.Read(persistence.Query{Namespace: commitsNamespace})
		if err != nil {
			return err
		}
		for _, document := range documents {
			var record commitRecord
			if err := decode(document.Value, &record); err != nil {
				return persistence.ErrCorrupt
			}
			if record.Outcome == "" {
				result.Commits = append(result.Commits, CommitEntry{Request: record.Request, CommitStatus: record.CommitStatus})
			}
		}
		return nil
	})
	return result, err
}
