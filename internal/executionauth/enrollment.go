package executionauth

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const ownerEnrollmentsNamespace = "wuu.execution_owner_enrollments.v1"

// OwnerEnrollment is an installation-owner-approved messaging identity. The
// immutable ID is the authority revision: a revoked ID is never reactivated.
// Re-enabling a channel requires a fresh explicit enrollment.
type OwnerEnrollment struct {
	ID             string             `json:"id"`
	Principal      principal.Identity `json:"principal"`
	UserSpaceID    string             `json:"user_space_id"`
	ProductDomain  string             `json:"product_domain"`
	ConversationID string             `json:"conversation_id"`
	Revoked        bool               `json:"revoked"`
}
type OwnerEnrollmentRef struct {
	ID string `json:"id"`
}
type ownerEnrollmentRecord struct {
	OwnerEnrollment
	Roots []Reference `json:"root_grants,omitempty"`
}

func (e OwnerEnrollment) valid() bool {
	return bounded(e.ID, 128) && e.Principal.Validate() == nil && e.Principal.Kind == "user" && e.Principal != principal.LocalOwner().Identity() && bounded(e.UserSpaceID, 256) && e.ProductDomain == "personal" && bounded(e.ConversationID, 256)
}
func (s *Service) ownerEnrollment(ctx context.Context, p pluginhost.ServiceInvokeParams) (OwnerEnrollment, error) {
	if p.Method != "read-owner" {
		who, authority, err := admission(p, "execution/owner/"+p.Method)
		if err != nil {
			return OwnerEnrollment{}, err
		}
		if authority != "local-installation" || who != principal.LocalOwner().Identity() {
			return OwnerEnrollment{}, errors.New("owner enrollment requires the local installation owner")
		}
	}
	var requested OwnerEnrollment
	if p.Method == "enroll-owner" {
		if err := decode(p.Params, &requested); err != nil {
			return OwnerEnrollment{}, err
		}
		if !requested.valid() || requested.Revoked {
			return OwnerEnrollment{}, errors.New("invalid owner enrollment")
		}
	} else {
		var ref OwnerEnrollmentRef
		if err := decode(p.Params, &ref); err != nil {
			return OwnerEnrollment{}, err
		}
		if !bounded(ref.ID, 128) {
			return OwnerEnrollment{}, errors.New("owner enrollment identity is required")
		}
		requested.ID = ref.ID
	}
	var result OwnerEnrollment
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var record ownerEnrollmentRecord
		found, err := tx.Get(ownerEnrollmentsNamespace, requested.ID, &record)
		if err != nil {
			return err
		}
		if p.Method == "enroll-owner" {
			if found {
				if record.OwnerEnrollment != requested || record.Revoked {
					return errors.New("owner enrollment is immutable or revoked; create a new enrollment")
				}
			} else {
				record.OwnerEnrollment = requested
				if err := tx.Put(ownerEnrollmentsNamespace, requested.ID, record); err != nil {
					return err
				}
			}
		} else {
			if !found || !record.valid() {
				return errors.New("owner enrollment not found")
			}
			if p.Method == "revoke-owner" && !record.Revoked {
				// A publication already admitted under a root grant must finish before
				// revocation can acknowledge that this enrollment has stopped effects.
				for _, ref := range record.Roots {
					var grant grantRecord
					found, err := tx.Get(grantNamespace(ref), ref.ID, &grant)
					if err != nil {
						return err
					}
					if !found {
						return persistence.ErrCorrupt
					}
					if len(grant.ActiveCommits) > 0 {
						return commitError("execution_commit_active", "owner enrollment has an admitted publication awaiting its terminal receipt")
					}
				}
				record.Revoked = true
				if err := tx.Put(ownerEnrollmentsNamespace, requested.ID, record); err != nil {
					return err
				}
			}
		}
		result = record.OwnerEnrollment
		return nil
	})
	return result, err
}

func (s *Service) enrollmentForGrant(tx *persistence.Tx, authority string, who principal.Identity, scope Scope) (ownerEnrollmentRecord, bool, error) {
	id, ok := principal.OwnerEnrollmentID(authority)
	if !ok {
		return ownerEnrollmentRecord{}, false, nil
	}
	var record ownerEnrollmentRecord
	found, err := tx.Get(ownerEnrollmentsNamespace, id, &record)
	if err != nil {
		return record, false, err
	}
	valid := found && record.valid() && !record.Revoked && record.Principal == who && record.UserSpaceID == scope.UserSpaceID && record.ProductDomain == scope.ProductDomain
	return record, valid, nil
}

func (c *Client) EnrollOwner(ctx context.Context, input OwnerEnrollment) (OwnerEnrollment, error) {
	var result OwnerEnrollment
	err := c.invoke(ctx, "enroll-owner", input, &result)
	if err == nil && (result != input || !result.valid() || result.Revoked) {
		err = errors.New("invalid owner enrollment receipt")
	}
	return result, err
}
func (c *Client) ReadOwner(ctx context.Context, input OwnerEnrollmentRef) (OwnerEnrollment, error) {
	var result OwnerEnrollment
	err := c.invoke(ctx, "read-owner", input, &result)
	if err == nil && (result.ID != input.ID || !result.valid()) {
		err = errors.New("invalid owner enrollment receipt")
	}
	return result, err
}
func (c *Client) RevokeOwner(ctx context.Context, input OwnerEnrollmentRef) (OwnerEnrollment, error) {
	var result OwnerEnrollment
	err := c.invoke(ctx, "revoke-owner", input, &result)
	if err == nil && (result.ID != input.ID || !result.valid() || !result.Revoked) {
		err = errors.New("invalid owner enrollment revocation receipt")
	}
	return result, err
}
