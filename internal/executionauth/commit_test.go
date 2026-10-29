package executionauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Commit admission is a persistence/security boundary: lost responses, restart,
// expiry, crossed callers, changed intents and ancestor revocation must not
// unlock or repeat the admitted operation.
func TestCommitFenceDurableAdmissionAndTerminalRecovery(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			docs, err := persistence.Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = docs.Close() }()
			service := NewService("provider", docs)
			if err = service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			caller := "consumer"
			owner := principal.LocalOwner().Identity()
			var lineage *Lineage
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				proof := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: owner, Action: "execution/grant/" + method, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}
				if method == "acquire-commit" {
					proof.Action = "execution/commit/acquire"
				}
				if method == "inspect-commit" {
					proof = nil
				}
				var issue IssueRequest
				_ = json.Unmarshal(raw, &issue)
				if method == "issue" && issue.Parent != nil {
					proof.Action = "execution/grant/derive"
					proof.Authority = "task-lineage"
					proof.Lineage, _ = json.Marshal(lineage)
				}
				return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: caller, Admission: proof})
			})
			parentRequest := issueFixture()
			parentRequest.Scope.Kind = "task-lineage"
			parentRequest.Scope.RunID = ""
			parentRequest.Scope.SessionID = ""
			parentRequest.Capabilities[0].Resources = []access.Resource{{Kind: "task", ID: parentRequest.Scope.TaskID}}
			parent, err := client.Issue(ctx, parentRequest)
			if err != nil {
				t.Fatal(err)
			}
			childRequest := issueFixture()
			childRequest.RequestID = "child"
			childRequest.Parent = &parent.Reference
			childRequest.ExpiresAt = parentRequest.ExpiresAt.Add(-time.Minute)
			childRequest.Scope.Depth = 1
			childRequest.Scope.TaskID = "child"
			lineage = &Lineage{Authority: parent.Scope.Authority, ParentTaskID: parent.Scope.TaskID, ParentRevision: parent.Scope.Revision, TaskID: childRequest.Scope.TaskID, RunID: childRequest.Scope.RunID, SessionID: childRequest.Scope.SessionID, Revision: childRequest.Scope.Revision, ContextDigest: childRequest.Scope.ContextDigest, Depth: 1, ModelProvider: "model-provider", Model: "model", PermissionMode: "normal"}
			child, err := client.Issue(ctx, childRequest)
			if err != nil {
				t.Fatal(err)
			}
			request := CommitRequest{ID: "candidate-operation", OperationDigest: RequestDigest("exact candidate"), Authorization: ValidateRequest{Reference: child.Reference, Scope: child.Scope, Action: child.Capabilities[0].Action, Resources: child.Capabilities[0].Resources}}
			guard, err := client.AcquireCommit(ctx, request)
			if err != nil || guard.ID != request.ID {
				t.Fatalf("admission %+v %v", guard, err)
			}
			again, err := client.AcquireCommit(ctx, request)
			if err != nil || RequestDigest(again) != RequestDigest(guard) {
				t.Fatalf("duplicate admission changed %+v %v", again, err)
			}
			conflict := request
			conflict.OperationDigest = RequestDigest("different candidate")
			if _, err = client.AcquireCommit(ctx, conflict); err == nil {
				t.Fatal("reused commit ID changed operation")
			}
			conflict = request
			conflict.Authorization.Scope.Revision++
			if _, err = client.AcquireCommit(ctx, conflict); err == nil {
				t.Fatal("reused commit ID changed authorization")
			}
			release := ReleaseCommitRequest{CommitRequest: request, Outcome: "committed", ReceiptDigest: RequestDigest("durable candidate receipt")}
			caller = "other"
			if _, err = client.ReleaseCommit(ctx, release); err == nil {
				t.Fatal("another caller released commit")
			}
			caller = "consumer"
			for _, grant := range []IssueResult{parent, child} {
				if _, err = client.Revoke(ctx, RevokeRequest{Reference: grant.Reference, Scope: grant.Scope}); !commitErrorCode(err, "execution_commit_active") {
					t.Fatalf("revoke did not report active fence: %v", err)
				}
			}
			// A real provider/storage reopen, not merely a replacement local mutex.
			_ = service.Close(ctx)
			_ = docs.Close()
			docs, err = persistence.Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			service = NewService("provider", docs)
			service.now = func() time.Time { return parent.ExpiresAt.Add(time.Hour) }
			if err = service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = client.Revoke(ctx, RevokeRequest{Reference: parent.Reference, Scope: parent.Scope}); !commitErrorCode(err, "execution_commit_active") {
				t.Fatalf("restart/expiry unlocked fence: %v", err)
			}
			// Inspection can recover the admitted state after expiry; acquiring again
			// does not create a new operation, and the terminal receipt decides release.
			status, err := client.InspectCommit(ctx, request)
			if err != nil || status.Outcome != "" || status.Guard.ID != request.ID {
				t.Fatalf("recover %+v %v", status, err)
			}
			invalid := release
			invalid.ReceiptDigest = ""
			if _, err = client.ReleaseCommit(ctx, invalid); err == nil {
				t.Fatal("committed outcome without receipt released fence")
			}
			if _, err = client.ReleaseCommit(ctx, release); err != nil {
				t.Fatal(err)
			}
			if _, err = client.ReleaseCommit(ctx, release); err != nil {
				t.Fatalf("release not idempotent: %v", err)
			}
			invalid = release
			invalid.Outcome = "not-committed"
			invalid.ReceiptDigest = ""
			if _, err = client.ReleaseCommit(ctx, invalid); err == nil {
				t.Fatal("terminal outcome changed")
			}
			if _, err = client.AcquireCommit(ctx, request); !commitErrorCode(err, "execution_commit_released") {
				t.Fatalf("released operation replayed: %v", err)
			}
			if _, err = client.Revoke(ctx, RevokeRequest{Reference: parent.Reference, Scope: parent.Scope}); err != nil {
				t.Fatalf("terminal receipt did not permit revoke: %v", err)
			}
			request.ID = "new-operation"
			if _, err = client.AcquireCommit(ctx, request); err == nil {
				t.Fatal("revoked/expired ancestor admitted new operation")
			}
		})
	}
}

func commitErrorCode(err error, code string) bool {
	var host *pluginhost.HostServiceError
	return errors.As(err, &host) && host.Code == code
}

func TestCommitReleaseBeforeUnknownAcquireTombstonesAndRejectsForgedAdmission(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	service := NewService("provider", docs)
	if err = service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	caller := "consumer"
	admit := true
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		proof := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: "execution/grant/" + method, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}
		if method == "acquire-commit" {
			proof.Action = "execution/commit/acquire"
			if !admit {
				proof.InputDigest = "forged"
			}
		}
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: caller, Admission: proof})
	})
	grant, err := client.Issue(ctx, issueFixture())
	if err != nil {
		t.Fatal(err)
	}
	request := CommitRequest{ID: "unknown-acquire", OperationDigest: RequestDigest("candidate"), Authorization: ValidateRequest{Reference: grant.Reference, Scope: grant.Scope, Action: grant.Capabilities[0].Action, Resources: grant.Capabilities[0].Resources}}
	admit = false
	if _, err = client.AcquireCommit(ctx, request); err == nil {
		t.Fatal("forged admission acquired fence")
	}
	admit = true
	release := ReleaseCommitRequest{CommitRequest: request, Outcome: "not-committed"}
	if _, err = client.ReleaseCommit(ctx, release); err != nil {
		t.Fatal(err)
	}
	if _, err = client.AcquireCommit(ctx, request); !commitErrorCode(err, "execution_commit_released") {
		t.Fatalf("late acquisition escaped terminal tombstone: %v", err)
	}
	if _, err = client.Revoke(ctx, RevokeRequest{Reference: grant.Reference, Scope: grant.Scope}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitClientRejectsUnknownAndMismatchedAdmissionWithoutRetry(t *testing.T) {
	req := issueFixture()
	request := CommitRequest{ID: "commit", OperationDigest: RequestDigest("operation"), Authorization: ValidateRequest{Reference: Reference{Provider: "provider", ID: "grant"}, Scope: req.Scope, Action: req.Capabilities[0].Action, Resources: req.Capabilities[0].Resources}}
	for _, response := range []string{`{}`, `null`, `{"id":"commit","operation_digest":"wrong"}`} {
		calls := 0
		client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(response), nil
		})
		if _, err := client.AcquireCommit(context.Background(), request); err == nil || calls != 1 {
			t.Fatalf("unconfirmed admission accepted/retried: %v calls=%d", err, calls)
		}
	}
	calls := 0
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("response lost")
	})
	if _, err := client.AcquireCommit(context.Background(), request); err == nil || calls != 1 {
		t.Fatalf("unknown acquisition accepted/retried: %v calls=%d", err, calls)
	}
}

func TestOwnerCommitRecoveryAfterWorkerLossAndExpiry(t *testing.T) {
	for _, outcome := range []string{"committed", "not-committed"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			docs, err := persistence.Open(ctx, dir, "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = docs.Close() }()
			service := NewService("provider", docs)
			if err = service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			caller := "worker"
			authority := "local-installation"
			admit := true
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				action := "execution/grant/" + method
				switch method {
				case "acquire-commit":
					action = "execution/commit/acquire"
				case "list-commits":
					action = "execution/commits/list"
				case "reconcile-commit":
					action = "execution/commits/reconcile"
				case "inspect-commit":
					action = "execution/commits/inspect"
				}
				proof := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: action, InputDigest: RawRequestDigest(raw), Authority: authority}
				if !admit {
					proof = nil
				}
				return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: caller, Admission: proof})
			})
			grant, err := client.Issue(ctx, issueFixture())
			if err != nil {
				t.Fatal(err)
			}
			request := CommitRequest{ID: "abandoned", OperationDigest: RequestDigest("candidate"), Authorization: ValidateRequest{Reference: grant.Reference, Scope: grant.Scope, Action: grant.Capabilities[0].Action, Resources: grant.Capabilities[0].Resources}}
			authority = "execution-grant"
			if _, err = client.AcquireCommit(ctx, request); err != nil {
				t.Fatal(err)
			}
			_ = service.Close(ctx)
			_ = docs.Close()
			docs, err = persistence.Open(ctx, dir, "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			service = NewService("provider", docs)
			service.now = func() time.Time { return grant.ExpiresAt.Add(time.Hour) }
			if err = service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			caller = "owner-maintenance"
			authority = "local-installation"
			admit = false
			if _, err = client.ListCommits(ctx); err == nil {
				t.Fatal("unauthenticated recovery discovered commits")
			}
			admit = true
			list, err := client.ListCommits(ctx)
			if err != nil || len(list.Commits) != 1 || RequestDigest(list.Commits[0].Request) != RequestDigest(request) {
				t.Fatalf("stranded commit not recoverable %+v %v", list, err)
			}
			resolve := ReconcileCommitRequest{ReleaseCommitRequest: ReleaseCommitRequest{CommitRequest: request, Outcome: outcome}, ExpectedCaller: "worker", Reason: "stopped writer and inspected durable receipt"}
			if outcome == "committed" {
				resolve.ReceiptDigest = RequestDigest("authoritative receipt")
			}
			wrong := resolve
			wrong.ExpectedCaller = "other"
			if _, err = client.ReconcileCommit(ctx, wrong); err == nil {
				t.Fatal("recovery crossed expected caller")
			}
			wrong = resolve
			wrong.Outcome = "unknown"
			if _, err = client.ReconcileCommit(ctx, wrong); err == nil {
				t.Fatal("unknown receipt released fence")
			}
			if _, err = client.Revoke(ctx, RevokeRequest{Reference: grant.Reference, Scope: grant.Scope}); !commitErrorCode(err, "execution_commit_active") {
				t.Fatalf("unknown recovery unlocked: %v", err)
			}
			if _, err = client.ReconcileCommit(ctx, resolve); err != nil {
				t.Fatal(err)
			}
			status, err := client.InspectCommit(ctx, request)
			if err != nil || status.Outcome != outcome || status.ReconciledBy == nil {
				t.Fatalf("recovery audit absent %+v %v", status, err)
			}
			list, err = client.ListCommits(ctx)
			if err != nil || len(list.Commits) != 0 {
				t.Fatalf("terminal commit remained active %+v %v", list, err)
			}
			if _, err = client.Revoke(ctx, RevokeRequest{Reference: grant.Reference, Scope: grant.Scope}); err != nil {
				t.Fatalf("reconciled fence blocked revoke: %v", err)
			}
		})
	}
}

type interceptedCommitDocuments struct {
	*persistence.Store
	before func(persistence.Transaction)
	after  func(persistence.Transaction) error
}

func (s *interceptedCommitDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if s.before != nil {
		s.before(tx)
	}
	receipt, err := s.Store.Commit(ctx, tx)
	if err == nil && s.after != nil {
		err = s.after(tx)
	}
	return receipt, err
}
func hasCommitIntent(tx persistence.Transaction) bool {
	for _, write := range tx.Writes {
		if write.Namespace == commitsNamespace {
			return true
		}
	}
	return false
}
func TestCommitAdmissionSerializesWithRevocationAndLostResponses(t *testing.T) {
	for _, mode := range []string{"revoke-wins", "acquire-response-lost", "release-response-lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, err := persistence.Open(ctx, t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			docs := &interceptedCommitDocuments{Store: store}
			service := NewService("provider", docs)
			if err = service.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				action := "execution/grant/" + method
				if method == "acquire-commit" {
					action = "execution/commit/acquire"
				}
				proof := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: action, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}
				if method == "inspect-commit" {
					proof = nil
				}
				return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: "consumer", Admission: proof})
			})
			grant, err := client.Issue(ctx, issueFixture())
			if err != nil {
				t.Fatal(err)
			}
			request := CommitRequest{ID: "operation", OperationDigest: RequestDigest("candidate"), Authorization: ValidateRequest{Reference: grant.Reference, Scope: grant.Scope, Action: grant.Capabilities[0].Action, Resources: grant.Capabilities[0].Resources}}
			revoke := RevokeRequest{Reference: grant.Reference, Scope: grant.Scope}
			if mode == "revoke-wins" {
				reached, proceed := make(chan struct{}), make(chan struct{})
				docs.before = func(tx persistence.Transaction) {
					if hasCommitIntent(tx) {
						close(reached)
						<-proceed
					}
				}
				completed := make(chan error, 1)
				go func() { _, err := client.AcquireCommit(ctx, request); completed <- err }()
				<-reached
				if _, err = client.Revoke(ctx, revoke); err != nil {
					t.Fatal(err)
				}
				close(proceed)
				if err = <-completed; !commitErrorCode(err, "execution_authorization_required") {
					t.Fatalf("stale admission escaped successful revoke: %v", err)
				}
				return
			}
			lost := errors.New("durable response lost")
			if mode == "acquire-response-lost" {
				docs.after = func(tx persistence.Transaction) error {
					if hasCommitIntent(tx) {
						return lost
					}
					return nil
				}
			}
			_, err = client.AcquireCommit(ctx, request)
			if mode == "acquire-response-lost" && !errors.Is(err, lost) || mode != "acquire-response-lost" && err != nil {
				t.Fatalf("acquire outcome %v", err)
			}
			docs.after = nil
			if _, err = client.Revoke(ctx, revoke); !commitErrorCode(err, "execution_commit_active") {
				t.Fatalf("unknown admission lost durable pins: %v", err)
			}
			status, err := client.InspectCommit(ctx, request)
			if err != nil || status.Outcome != "" {
				t.Fatalf("could not resolve admission %+v %v", status, err)
			}
			release := ReleaseCommitRequest{CommitRequest: request, Outcome: "not-committed"}
			if mode == "release-response-lost" {
				docs.after = func(tx persistence.Transaction) error {
					if hasCommitIntent(tx) {
						return lost
					}
					return nil
				}
			}
			_, err = client.ReleaseCommit(ctx, release)
			if mode == "release-response-lost" && !errors.Is(err, lost) || mode != "release-response-lost" && err != nil {
				t.Fatalf("release outcome %v", err)
			}
			docs.after = nil
			if _, err = client.ReleaseCommit(ctx, release); err != nil {
				t.Fatal(err)
			}
			if _, err = client.AcquireCommit(ctx, request); !commitErrorCode(err, "execution_commit_released") {
				t.Fatalf("lost cleanup response replayed operation: %v", err)
			}
			if _, err = client.Revoke(ctx, revoke); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommitFenceFormatRolloutPreservesLegacyAuthorityWithoutPromotion(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	if err = docs.ClaimNamespaces(ctx, []string{"wuu.execution_grants.v1", requestsNamespace, eventsNamespace}); err != nil {
		t.Fatal(err)
	}
	req := issueFixture()
	who := principal.LocalOwner().Identity()
	legacy := IssueResult{Reference: Reference{Provider: "provider", ID: "legacy-reference"}, Principal: who, Scope: req.Scope, Capabilities: req.Capabilities, ExpiresAt: req.ExpiresAt}
	requestKey := RequestDigest(struct {
		Principal principal.Identity
		Consumer  string
		ID        string
	}{who, req.Scope.Consumer, req.RequestID})
	if err = persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
		if err := tx.Put("wuu.execution_grants.v1", legacy.Reference.ID, grantRecord{IssueResult: legacy, Authority: "local-installation"}); err != nil {
			return err
		}
		return tx.Put(requestsNamespace, requestKey, requestRecord{Digest: RequestDigest(req), Reference: legacy.Reference})
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService("provider", docs)
	if err = service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		action := "execution/grant/" + method
		if method == "acquire-commit" {
			action = "execution/commit/acquire"
		}
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: "consumer", Admission: &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: who, Action: action, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}})
	})
	repeated, err := client.Issue(ctx, req)
	if err != nil || repeated.Reference != legacy.Reference {
		t.Fatalf("legacy nonce silently renewed %+v %v", repeated, err)
	}
	validation := ValidateRequest{Reference: legacy.Reference, Scope: legacy.Scope, Action: legacy.Capabilities[0].Action, Resources: legacy.Capabilities[0].Resources}
	verdict, err := client.Validate(ctx, validation)
	if err != nil || !verdict.Valid {
		t.Fatalf("legacy normal work invalidated %+v %v", verdict, err)
	}
	commit := CommitRequest{ID: "legacy-commit", OperationDigest: RequestDigest("candidate"), Authorization: validation}
	if _, err = client.AcquireCommit(ctx, commit); !commitErrorCode(err, "execution_authorization_required") {
		t.Fatalf("legacy grant acquired new publication semantics: %v", err)
	}
	req.RequestID = "explicit-live-renewal"
	renewed, err := client.Issue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// The old issuer uses the same request index. It must fail closed on the
	// new reference, not mint a second grant for an already accepted nonce.
	newKey := RequestDigest(struct {
		Principal principal.Identity
		Consumer  string
		ID        string
	}{who, req.Scope.Consumer, req.RequestID})
	oldIssueErr := persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
		var previous requestRecord
		found, err := tx.Get(requestsNamespace, newKey, &previous)
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("new issuance bypassed shared nonce journal")
		}
		var oldGrant grantRecord
		found, err = tx.Get("wuu.execution_grants.v1", previous.Reference.ID, &oldGrant)
		if err != nil {
			return err
		}
		if !found {
			return persistence.ErrCorrupt
		}
		return nil
	})
	if !errors.Is(oldIssueErr, persistence.ErrCorrupt) {
		t.Fatalf("old issuer reused new nonce: %v", oldIssueErr)
	}
	commit.ID = "renewed-commit"
	commit.Authorization.Reference = renewed.Reference
	if _, err = client.AcquireCommit(ctx, commit); err != nil {
		t.Fatal(err)
	}
	// The old implementation only reads v1. It cannot report successful revoke
	// for a v2 reference, including while the new provider holds a commit fence.
	legacyRevoke := func(reference Reference) error {
		return persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
			var grant grantRecord
			found, err := tx.Get("wuu.execution_grants.v1", reference.ID, &grant)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("execution grant scope mismatch")
			}
			grant.Revoked = true
			return tx.Put("wuu.execution_grants.v1", reference.ID, grant)
		})
	}
	if err = legacyRevoke(renewed.Reference); err == nil {
		t.Fatal("old writer revoked fence-aware grant")
	}
	if _, err = client.Revoke(ctx, RevokeRequest{Reference: renewed.Reference, Scope: renewed.Scope}); !commitErrorCode(err, "execution_commit_active") {
		t.Fatalf("old writer bypassed publication pin: %v", err)
	}
	if _, err = client.ReleaseCommit(ctx, ReleaseCommitRequest{CommitRequest: commit, Outcome: "not-committed"}); err != nil {
		t.Fatal(err)
	}
	if err = legacyRevoke(legacy.Reference); err != nil {
		t.Fatalf("legacy revocation was lost: %v", err)
	}
	verdict, err = client.Validate(ctx, validation)
	if err != nil || verdict.Valid {
		t.Fatalf("new provider ignored legacy revoke %+v %v", verdict, err)
	}
	// Even a v2 leaf must not conceal a legacy ancestor from the fence check.
	if err = persistence.Update(ctx, docs, func(tx *persistence.Tx) error {
		parent := grantRecord{IssueResult: legacy, Authority: "local-installation"}
		if err := tx.Put("wuu.execution_grants.v1", legacy.Reference.ID, parent); err != nil {
			return err
		}
		child := grantRecord{IssueResult: renewed, Parent: &legacy.Reference, Authority: "local-installation"}
		return tx.Put(grantsNamespace, renewed.Reference.ID, child)
	}); err != nil {
		t.Fatal(err)
	}
	commit.ID = "mixed-ancestor"
	if _, err = client.AcquireCommit(ctx, commit); !commitErrorCode(err, "execution_authorization_required") {
		t.Fatalf("legacy ancestor escaped publication format check: %v", err)
	}
}

// This digest is also exercised by the SDK wire fixture. Object insertion order
// and HTML/line-separator escaping cannot change a confirmed authorization bind.
func TestCommitAuthorizationDigestMatchesSDKWire(t *testing.T) {
	var request ValidateRequest
	raw := []byte(`{"resources":[{"id":"task","kind":"task"}],"action":"memory/publish","scope":{"revision":1,"context_digest":"<source>&\u2028","session_id":"session","run_id":"run","task_id":"task","product_domain":"personal","user_space_id":"space","consumer":"consumer","depth":0,"bounds":{"max_depth":1,"permission_modes":["normal"],"models":["model"],"model_providers":["provider"]},"authority":{"fingerprint":"fingerprint","subject":"subject","provider":"task-provider","major":1,"service":"tasks"},"kind":"exact-run"},"reference":{"id":"grant","provider":"provider"}}`)
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if got := RequestDigest(request); got != "db361ca5ceec9aab50bb85dfc3d041c762305ab552eaa4f3bdbd7415c5d0c34e" {
		t.Fatalf("authorization commitment differs from SDK: %s", got)
	}
}
