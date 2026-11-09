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

// Trust-boundary cases precede implementation: request JSON cannot assert
// admission; opaque handles cannot cross consumer/task/session/action/resource,
// survive provider replacement, widen via child grants, or revive on restart.
func TestGrantAdmissionScopeRecoveryAndRevocation(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			docs, err := persistence.Open(ctx, t.TempDir(), backend)
			if err != nil {
				t.Fatal(err)
			}
			defer docs.Close()
			s := NewService("grant-provider", docs)
			if err = s.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			req := issueFixture()
			who := principal.LocalOwner().Identity()
			call := func(method string, input any, admitted bool) (json.RawMessage, error) {
				raw, _ := json.Marshal(input)
				params := pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw}
				if admitted {
					params.Admission = &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: who, Action: "execution/grant/" + method, InputDigest: RequestDigest(input), Authority: "local-installation"}
				}
				return s.InvokeService(ctx, params)
			}
			if _, err = call("issue", req, false); err == nil {
				t.Fatal("caller JSON became authenticated admission")
			}
			raw, err := call("issue", req, true)
			if err != nil {
				t.Fatal(err)
			}
			var grant IssueResult
			if json.Unmarshal(raw, &grant) != nil || grant.Reference.ID == "" {
				t.Fatal("missing opaque grant")
			}
			duplicate, err := call("issue", req, true)
			if err != nil || string(duplicate) != string(raw) {
				t.Fatalf("duplicate issuance changed identity: %s %v", duplicate, err)
			}
			changed := req
			changed.Scope.Revision++
			if _, err = call("issue", changed, true); err == nil {
				t.Fatal("request ID reused with changed scope")
			}
			check := ValidateRequest{Reference: grant.Reference, Scope: req.Scope, Action: req.Capabilities[0].Action, Resources: req.Capabilities[0].Resources}
			for _, change := range []string{"valid", "provider", "consumer", "task", "session", "revision", "action", "resource"} {
				t.Run(change, func(t *testing.T) {
					q := check
					q.Resources = append([]access.Resource(nil), q.Resources...)
					switch change {
					case "provider":
						q.Reference.Provider = "replacement"
					case "consumer":
						q.Scope.Consumer = "foreign"
					case "task":
						q.Scope.TaskID = "foreign"
					case "session":
						q.Scope.SessionID = "foreign"
					case "revision":
						q.Scope.Revision++
					case "action":
						q.Action = "ungranted"
					case "resource":
						q.Resources[0].ID = "foreign"
					}
					data, err := call("validate", q, false)
					var got Validation
					_ = json.Unmarshal(data, &got)
					if err != nil || got.Valid != (change == "valid") {
						t.Fatalf("validation %s %+v %v", change, got, err)
					}
				})
			}
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			s = NewService("grant-provider", docs)
			if err = s.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := call("validate", check, false)
			var got Validation
			_ = json.Unmarshal(data, &got)
			if err != nil || !got.Valid {
				t.Fatalf("grant did not survive GUI/provider lifetime: %+v %v", got, err)
			}
			revoke := RevokeRequest{Reference: grant.Reference, Scope: req.Scope}
			if _, err = call("revoke", revoke, false); err == nil {
				t.Fatal("unauthenticated revocation accepted")
			}
			if _, err = call("revoke", revoke, true); err != nil {
				t.Fatal(err)
			}
			data, err = call("validate", check, false)
			_ = json.Unmarshal(data, &got)
			if err != nil || got.Valid {
				t.Fatalf("revoked grant remained valid: %+v %v", got, err)
			}
		})
	}
}
func issueFixture() IssueRequest {
	return IssueRequest{RequestID: "request", Scope: Scope{Kind: "exact-run", Authority: AuthorityBinding{Service: "test.tasks", Major: 1, Provider: "tasks", Subject: "task-subject", Fingerprint: "task-fingerprint"}, Bounds: Bounds{ModelProviders: []string{"model-provider"}, Models: []string{"model"}, PermissionModes: []string{"normal"}, MaxDepth: 4}, Consumer: "task-package-subject", UserSpaceID: "space", ProductDomain: "personal", TaskID: "task", RunID: "run", SessionID: "session", Revision: 1, ContextDigest: "source"}, Capabilities: []Capability{{Action: "personal/file.execute", Resources: []access.Resource{{Kind: "session", ID: "session"}}}}, ExpiresAt: time.Now().Add(time.Hour).UTC()}
}

func TestGrantRejectsForgedAdmissionAndUnsupportedAccountValidity(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	s := NewService("provider", docs)
	_ = s.Activate(ctx)
	for _, mode := range []string{"version", "input-json", "digest", "action", "account", "identity"} {
		t.Run(mode, func(t *testing.T) {
			req := issueFixture()
			raw, _ := json.Marshal(req)
			a := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: "execution/grant/issue", InputDigest: RequestDigest(req), Authority: "local-installation"}
			switch mode {
			case "version":
				a.Version = 0
			case "input-json":
				a.InputJSON = "{}"
			case "digest":
				a.InputDigest = "forged"
			case "action":
				a.Action = "read"
			case "account":
				a.Authority = "account-identity"
			case "identity":
				a.Principal = principal.Identity{}
			}
			if _, err := s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "issue", Params: raw, Admission: a}); err == nil {
				t.Fatal("invalid authority was persisted")
			}
		})
	}
}

func TestChildGrantCannotWidenAndParentRevocationCascades(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	s := NewService("provider", docs)
	_ = s.Activate(ctx)
	var lineage *Lineage
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		a := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: "execution/grant/" + method, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}
		var request IssueRequest
		_ = json.Unmarshal(raw, &request)
		if method == "issue" && request.Parent != nil {
			a.Action = "execution/grant/derive"
			a.Authority = "task-lineage"
			if lineage != nil {
				a.Lineage, _ = json.Marshal(lineage)
			}
		}
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Admission: a})
	})
	req := issueFixture()
	req.Scope.Kind = "task-lineage"
	req.Scope.RunID = ""
	req.Scope.SessionID = ""
	req.Capabilities[0].Resources = []access.Resource{{Kind: "task", ID: req.Scope.TaskID}}
	parent, err := client.Issue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	child := issueFixture()
	child.RequestID = "child"
	child.Parent = &parent.Reference
	child.ExpiresAt = req.ExpiresAt.Add(-time.Minute)
	child.Scope.Depth = 1
	child.Scope.TaskID = "child-task"
	lineage = &Lineage{Authority: req.Scope.Authority, ParentTaskID: req.Scope.TaskID, ParentRevision: req.Scope.Revision, TaskID: child.Scope.TaskID, RunID: child.Scope.RunID, SessionID: child.Scope.SessionID, Revision: child.Scope.Revision, ContextDigest: child.Scope.ContextDigest, ModelProvider: "model-provider", Model: "model", PermissionMode: "normal", Depth: 1}
	for _, mode := range []string{"missing-proof", "wrong-authority", "wrong-child", "wrong-model", "wrong-depth", "capability", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			original := *lineage
			q := child
			switch mode {
			case "missing-proof":
				lineage = nil
			case "wrong-authority":
				lineage.Authority.Provider = "foreign"
			case "wrong-child":
				lineage.SessionID = "foreign"
			case "wrong-model":
				lineage.Model = "foreign"
			case "wrong-depth":
				lineage.Depth = 4
			case "capability":
				q.Capabilities = []Capability{{Action: "other", Resources: child.Capabilities[0].Resources}}
			case "expiry":
				q.ExpiresAt = req.ExpiresAt.Add(time.Minute)
			}
			if _, err := client.Issue(ctx, q); err == nil {
				t.Fatal("child authority widened")
			}
			lineage = &original
		})
	}
	grant, err := client.Issue(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Revoke(ctx, RevokeRequest{Reference: parent.Reference, Scope: parent.Scope}); err != nil {
		t.Fatal(err)
	}
	validation, err := client.Validate(ctx, ValidateRequest{Reference: grant.Reference, Scope: child.Scope, Action: child.Capabilities[0].Action, Resources: child.Capabilities[0].Resources})
	if err != nil || validation.Valid {
		t.Fatalf("child escaped parent revocation: %+v %v", validation, err)
	}
	if _, err = client.Inspect(ctx, InspectRequest{Reference: grant.Reference, Consumer: child.Scope.Consumer}); err == nil {
		t.Fatal("revoked child inspect succeeded")
	}
}

func TestGrantPolicyAndOpaqueInspection(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	s := NewService("provider", docs)
	_ = s.Activate(ctx)
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return s.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Admission: &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: "execution/grant/" + method, InputDigest: RawRequestDigest(raw), Authority: "local-installation"}})
	})
	policy, err := client.Describe(ctx)
	if err != nil || policy.DefaultLifetimeSeconds <= 0 || policy.MaxLifetimeSeconds < policy.DefaultLifetimeSeconds {
		t.Fatalf("unbounded policy: %+v %v", policy, err)
	}
	req := issueFixture()
	grant, err := client.Issue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Inspect(ctx, InspectRequest{Reference: grant.Reference, Consumer: req.Scope.Consumer})
	if err != nil || !got.Scope.Equal(req.Scope) {
		t.Fatalf("inspect %+v %v", got, err)
	}
	if _, err = client.Inspect(ctx, InspectRequest{Reference: grant.Reference, Consumer: "other"}); err == nil {
		t.Fatal("foreign consumer inspect")
	}
	s.now = func() time.Time { return req.ExpiresAt }
	if _, err = client.Inspect(ctx, InspectRequest{Reference: grant.Reference, Consumer: req.Scope.Consumer}); err == nil {
		t.Fatal("expired inspect")
	}
}

func TestClosedGrantProviderNeverFallsBack(t *testing.T) {
	s := NewService("provider", nil)
	_ = s.Close(context.Background())
	if _, err := s.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "validate", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("closed provider served grants")
	}
	if _, err := NewClient(nil).Issue(context.Background(), issueFixture()); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("missing selected client did not fail")
	}
}

func TestGrantClientRejectsWidenedIssuanceAndMalformedInspection(t *testing.T) {
	req := issueFixture()
	grant := IssueResult{Reference: Reference{Provider: "provider", ID: "opaque"}, Principal: principal.LocalOwner().Identity(), Scope: req.Scope, Capabilities: []Capability{{Action: "foreign", Resources: req.Capabilities[0].Resources}}, ExpiresAt: req.ExpiresAt}
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return json.Marshal(grant) })
	if _, err := client.Issue(context.Background(), req); err == nil {
		t.Fatal("provider widened issuance response")
	}
	grant.Capabilities = []Capability{{Action: "action", Resources: []access.Resource{{Kind: "global", ID: "all"}}}}
	if _, err := client.Inspect(context.Background(), InspectRequest{Reference: grant.Reference, Consumer: req.Scope.Consumer}); err == nil {
		t.Fatal("provider omitted bounded inspection authority")
	}
}

type unownedGrantDocuments struct{ persistence.Documents }

func TestGrantProviderRequiresOwnedSelectedNamespaces(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	provider := NewService("provider", unownedGrantDocuments{docs})
	if err := provider.Activate(ctx); err == nil {
		t.Fatal("unprotected selected provider became grant authority")
	}
}

func TestGrantTaskLineageNarrowsBeforeExactRunWithoutSessionWildcard(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	provider := NewService("provider", docs)
	if err = provider.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	var lineage *Lineage
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		a := &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: principal.LocalOwner().Identity(), Action: "execution/grant/" + method, Authority: "local-installation", InputDigest: RawRequestDigest(raw)}
		var r IssueRequest
		_ = json.Unmarshal(raw, &r)
		if r.Parent != nil {
			a.Action = "execution/grant/derive"
			a.Authority = "task-lineage"
			a.Lineage, _ = json.Marshal(lineage)
		}
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Admission: a})
	})
	root := issueFixture()
	root.Scope.Kind = "task-lineage"
	root.Scope.RunID = ""
	root.Scope.SessionID = ""
	root.Capabilities[0].Resources = []access.Resource{{Kind: "task", ID: root.Scope.TaskID}}
	parent, err := client.Issue(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	child := root
	child.RequestID = "child-task"
	child.Parent = &parent.Reference
	child.Scope.TaskID = "child-task"
	child.Scope.Depth = 1
	child.Capabilities = []Capability{{Action: root.Capabilities[0].Action, Resources: []access.Resource{{Kind: "task", ID: child.Scope.TaskID}}}}
	lineage = &Lineage{Authority: child.Scope.Authority, ParentTaskID: parent.Scope.TaskID, ParentRevision: parent.Scope.Revision, TaskID: child.Scope.TaskID, Revision: child.Scope.Revision, ContextDigest: child.Scope.ContextDigest, Depth: 1}
	intermediate, err := client.Issue(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := client.Validate(ctx, ValidateRequest{Reference: intermediate.Reference, Scope: intermediate.Scope, Action: child.Capabilities[0].Action, Resources: []access.Resource{{Kind: "session", ID: "arbitrary"}}})
	if err != nil || validation.Valid {
		t.Fatalf("empty lineage session became wildcard %+v %v", validation, err)
	}
	exact := issueFixture()
	exact.RequestID = "exact-run"
	exact.Parent = &intermediate.Reference
	exact.Scope.Depth = 2
	exact.Scope.TaskID = child.Scope.TaskID
	exact.ExpiresAt = child.ExpiresAt
	lineage = &Lineage{Authority: exact.Scope.Authority, ParentTaskID: child.Scope.TaskID, ParentRevision: child.Scope.Revision, TaskID: exact.Scope.TaskID, RunID: exact.Scope.RunID, SessionID: exact.Scope.SessionID, Revision: exact.Scope.Revision, ContextDigest: exact.Scope.ContextDigest, Depth: 2, ModelProvider: "model-provider", Model: "model", PermissionMode: "normal"}
	issued, err := client.Issue(ctx, exact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Revoke(ctx, RevokeRequest{Reference: parent.Reference, Scope: parent.Scope}); err != nil {
		t.Fatal(err)
	}
	validation, err = client.Validate(ctx, ValidateRequest{Reference: issued.Reference, Scope: issued.Scope, Action: exact.Capabilities[0].Action, Resources: exact.Capabilities[0].Resources})
	if err != nil || validation.Valid {
		t.Fatalf("nested lineage escaped revocation %+v %v", validation, err)
	}
}
