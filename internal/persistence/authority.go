package persistence

import (
	"context"
	"fmt"
	"strings"
)

const (
	AuthorityInputSchema  = "state.documents.authority.input.v1"
	AuthorityOutputSchema = "state.documents.authority.output.v1"
	HostJournalAuthority  = "host-journal"
	ProviderAuthority     = "provider"
	SingleHost            = "single-host"
	MultiHost             = "multi-host"
)

// Authority describes this binding's configured deployment, not merely backend
// capabilities. StoreID identifies the logical authoritative dataset across
// reopen and verified offline migration; it must never identify a fresh copy.
// Coordinator names the common transaction domain, not an execution lease.
// A provider must fence replaced/reset identities for every operation on an
// active binding. Declaring multi-host storage does not coordinate OS effects.
type Authority struct {
	Version           int    `json:"version"`
	Mode              string `json:"mode"`
	StoreID           string `json:"store_id,omitempty"`
	StorageTopology   string `json:"storage_topology"`
	ExecutionTopology string `json:"execution_topology"`
	Coordinator       string `json:"coordinator,omitempty"`
	CAS               string `json:"cas"`
	DurableReceipts   bool   `json:"durable_receipts"`
}

// LegacyAuthority is the documented state.documents v1 contract for providers
// that do not advertise authority. It never selects another storage provider.
func LegacyAuthority() Authority {
	return Authority{Version: 1, Mode: HostJournalAuthority, StorageTopology: SingleHost, ExecutionTopology: SingleHost, CAS: "store", DurableReceipts: true}
}

func (a Authority) Validate() error {
	if a.Version != 1 || a.CAS != "store" || !a.DurableReceipts {
		return fmt.Errorf("%w: authority v1 requires atomic store-wide CAS and durable idempotency receipts", ErrInvalid)
	}
	if a.StorageTopology != SingleHost && a.StorageTopology != MultiHost || a.ExecutionTopology != SingleHost && a.ExecutionTopology != MultiHost {
		return fmt.Errorf("%w: unsupported configured storage or execution topology", ErrInvalid)
	}
	switch a.Mode {
	case HostJournalAuthority:
		if a.StorageTopology != SingleHost || a.ExecutionTopology != SingleHost {
			return fmt.Errorf("%w: host-journal authority coordinates one host only", ErrInvalid)
		}
	case ProviderAuthority:
		if strings.TrimSpace(a.StoreID) == "" || a.StorageTopology == MultiHost && strings.TrimSpace(a.Coordinator) == "" {
			return fmt.Errorf("%w: provider authority requires store_id and multi-host storage requires a common CAS coordinator", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported authority mode %q", ErrInvalid, a.Mode)
	}
	return nil
}

func (c *Client) Authority(ctx context.Context) (Authority, error) {
	var authority Authority
	if err := c.invoke(ctx, "authority", struct{}{}, &authority); err != nil {
		return Authority{}, err
	}
	return authority, authority.Validate()
}
