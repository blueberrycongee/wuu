package executionauth

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Enrollment, revocation, restart and exact binding are one persisted authority
// contract. An authenticated account alone must never gain unattended execution.
func TestOwnerEnrollmentFencesGrantAcrossRestart(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	service := NewService(NativeModule, docs)
	if err = service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := principal.LocalOwner().Identity()
	identity := principal.Identity{Kind: "user", Issuer: "wuu:im:slack", Tenant: "team", Subject: "user"}
	enrolled := OwnerEnrollment{ID: "enrollment-1", Principal: identity, UserSpaceID: "space", ProductDomain: "personal", ConversationID: "channel"}
	invoke := func(method string, input any, who principal.Identity, authority string) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw, Caller: "fixture", Admission: &pluginhost.ServiceAdmission{Version: 1, InputJSON: string(raw), Principal: who, Authority: authority, Action: "execution/owner/" + method, InputDigest: RawRequestDigest(raw)}})
	}
	if _, err = invoke("enroll-owner", enrolled, identity, "owner-enrollment:forged"); err == nil {
		t.Fatal("remote account enrolled itself")
	}
	if _, err = invoke("enroll-owner", enrolled, owner, "local-installation"); err != nil {
		t.Fatal(err)
	}
	req := issueFixture()
	req.Scope.UserSpaceID = enrolled.UserSpaceID
	req.Scope.ProductDomain = enrolled.ProductDomain
	authority := principal.OwnerEnrollmentAuthority(enrolled.ID)
	grant, err := service.issue(ctx, req, identity, authority, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrong := req
	wrong.RequestID = "cross-space"
	wrong.Scope.UserSpaceID = "other"
	if _, err = service.issue(ctx, wrong, identity, authority, nil); err == nil {
		t.Fatal("enrollment crossed user space")
	}
	validation := ValidateRequest{Reference: grant.Reference, Scope: grant.Scope, Action: grant.Capabilities[0].Action, Resources: grant.Capabilities[0].Resources}
	if result, err := service.validate(ctx, validation); err != nil || !result.Valid {
		t.Fatalf("enrolled grant: %+v %v", result, err)
	}
	changed := enrolled
	changed.ConversationID = "other"
	if _, err = invoke("enroll-owner", changed, owner, "local-installation"); err == nil {
		t.Fatal("enrollment identity mutated")
	}
	// Revocation cannot acknowledge that effects stopped while a publication
	// already admitted under this enrollment is still awaiting its receipt.
	commit := CommitRequest{ID: "enrolled-publication", OperationDigest: RequestDigest("publication"), Authorization: validation}
	if _, err = service.acquireCommit(ctx, commit, "publisher", identity); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke("revoke-owner", OwnerEnrollmentRef{ID: enrolled.ID}, owner, "local-installation"); err == nil {
		t.Fatal("enrollment revoked across an admitted publication")
	}
	if _, err = service.releaseCommit(ctx, ReleaseCommitRequest{CommitRequest: commit, Outcome: "not-committed"}, "publisher", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke("revoke-owner", OwnerEnrollmentRef{ID: enrolled.ID}, owner, "local-installation"); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke("enroll-owner", enrolled, owner, "local-installation"); err == nil {
		t.Fatal("revoked enrollment revived")
	}
	service.Close(ctx)
	service = NewService(NativeModule, docs)
	if err = service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := service.validate(ctx, validation); err != nil || result.Valid {
		t.Fatalf("revocation lost on restart: %+v %v", result, err)
	}
	req.RequestID = "fresh-after-revoke"
	if _, err = service.issue(ctx, req, identity, authority, nil); err == nil {
		t.Fatal("revoked owner issued a grant")
	}
}
