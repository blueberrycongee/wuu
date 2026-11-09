package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// ValidateInspectionAdmission verifies invocation-only attestation from the host
// after owner authentication, resource policy and stopped-writer checks. A service
// declaration alone is not authorization to inspect or release another effect.
func ValidateInspectionAdmission(params pluginhost.ServiceInvokeParams, action string) error {
	proof := params.Admission
	digest := sha256.Sum256(params.Params)
	if proof == nil || proof.Version != 1 || proof.Principal.Validate() != nil || proof.Action != action || proof.InputJSON != string(params.Params) || proof.InputDigest != hex.EncodeToString(digest[:]) {
		return &pluginhost.HostServiceError{Code: "permission_denied", Message: "inspection maintenance requires exact authenticated host admission"}
	}
	return nil
}

func namespaceInvocationContext(ctx context.Context, provider string, params pluginhost.ServiceInvokeParams) (context.Context, error) {
	identity := namespaceIdentity{caller: params.Caller}
	if proof := params.StorageStewardship; proof != nil {
		digest := sha256.Sum256(params.Params)
		if proof.Version != 1 || strings.TrimSpace(proof.Authority) == "" || proof.Provider != provider || proof.InputJSON != string(params.Params) || proof.InputDigest != hex.EncodeToString(digest[:]) {
			return nil, ErrNamespaceOwnership
		}
		switch proof.Operation {
		case "verify":
			if params.Method != "read" && params.Method != "export" && params.Method != "checkpoint" && params.Method != "authority" {
				return nil, ErrNamespaceOwnership
			}
			identity.steward = true
		case "migration":
			identity.migration = true
			if params.Method != "read" && params.Method != "export" && params.Method != "checkpoint" && params.Method != "import" {
				return nil, ErrNamespaceOwnership
			}
			identity.steward = true
		case "recover":
			if params.Method != "commit" && params.Method != "claim-namespaces" {
				return nil, ErrNamespaceOwnership
			}
			identity.caller = proof.Consumer
		default:
			return nil, ErrNamespaceOwnership
		}
	}
	if params.Method == "list-inspections" || params.Method == "reconcile-inspection" {
		action := "state/inspections/list"
		if params.Method == "reconcile-inspection" {
			action = "state/inspections/reconcile"
		}
		if err := ValidateInspectionAdmission(params, action); err != nil {
			return nil, err
		}
		identity.steward = true
	}
	return context.WithValue(ctx, namespaceIdentityKey{}, identity), nil
}

// InspectionReconciler records the authenticated operator, not a guessed user
// ID or the intermediary service consumer. Call only after admission validation.
func InspectionReconciler(params pluginhost.ServiceInvokeParams) string {
	raw, _ := json.Marshal(params.Admission.Principal)
	return string(raw)
}
