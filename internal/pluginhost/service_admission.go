package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/principal"
)

// ServiceAdmission is a versioned host attestation of an authenticated,
// policy-approved operation. It is only invocation metadata, never a service.call
// input. Providers must check the action and digest against their operation.
// Authority identifies a durable validity source, not ambient owner authority.
type ServiceAdmission struct {
	InputJSON   string             `json:"input_json"`
	Lineage     json.RawMessage    `json:"lineage,omitempty"`
	Version     int                `json:"version"`
	Principal   principal.Identity `json:"principal"`
	Action      string             `json:"action"`
	InputDigest string             `json:"input_digest"`
	Authority   string             `json:"authority"`
}

type serviceAdmissionKey struct{}
type serviceAdmissionBinding struct {
	consumer, service, method string
	proof                     ServiceAdmission
	admission                 context.Context
}

// WithServiceAdmission is reserved for trusted host ingress after checking the
// actual principal and selected resource policy. The proof is bound to one
// normalized call and caller; it cannot authorize nested plugin calls or outlive
// its originating admission even when a callback detaches cancellation.
func WithServiceAdmission(ctx context.Context, consumer, service, method string, proof ServiceAdmission) context.Context {
	proof.Lineage = append(json.RawMessage(nil), proof.Lineage...)
	return context.WithValue(ctx, serviceAdmissionKey{}, serviceAdmissionBinding{consumer, service, method, proof, ctx})
}

func serviceAdmission(ctx context.Context, consumer, service, method string, input []byte) *ServiceAdmission {
	binding, ok := ctx.Value(serviceAdmissionKey{}).(serviceAdmissionBinding)
	if !ok || ctx.Err() != nil || binding.admission.Err() != nil || binding.consumer != consumer || binding.service != service || binding.method != method || binding.proof.Version != 1 || binding.proof.Principal.Validate() != nil || binding.proof.Action == "" {
		return nil
	}
	digest := sha256.Sum256(input)
	if binding.proof.InputDigest != hex.EncodeToString(digest[:]) {
		return nil
	}
	proof := binding.proof
	proof.InputJSON = string(input)
	proof.Lineage = append(json.RawMessage(nil), proof.Lineage...)
	return &proof
}

// StorageStewardship identifies configured host storage coordination, not a user
// principal. Only the captured StateAuthority may attach it to an exact provider
// invocation. Consumers cannot supply this metadata through service.call.
type StorageStewardship struct {
	Consumer    string `json:"consumer,omitempty"`
	Version     int    `json:"version"`
	Authority   string `json:"authority"`
	Provider    string `json:"provider"`
	Operation   string `json:"operation"`
	InputDigest string `json:"input_digest"`
	InputJSON   string `json:"input_json"`
}
