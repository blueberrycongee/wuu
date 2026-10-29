package session

import (
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

var ErrClientInputConflict = errors.New("request_id_conflict")
var ErrClientInputSettled = errors.New("accepted client input changed or settled")

// AcceptedClientInput is host-owned. SelectionJSON retains the complete original
// attachment bytes, addressed by receipt ID and ordinal, independently of expiring
// tool working copies. Only validated live client submissions may create a row.
type AcceptedClientInput struct {
	// OriginalText is derived from the authenticated pre-materialization message
	// at history settlement. nil means unavailable; a pointer to "" proves an
	// explicitly captionless input. Callers cannot supply this evidence.
	OriginalText  *string `json:"OriginalText,omitempty"`
	Receipt       providers.ClientInputReceipt
	Fingerprint   string
	MessageJSON   []byte
	RuntimeJSON   []byte
	SelectionJSON []byte
	LiveOwner     string
}

// ClientInputScope comes from the immutable host binding, never plugin parameters.
// Payload queries require a target session. Recovery may span that binding at
// startup, or only this host's owner at shutdown.
type ClientInputScope struct {
	Binding   userspace.Binding
	SessionID string
	PluginID  string
	LiveOwner string
}

func (scope ClientInputScope) validate(requireSession bool) error {
	if scope.Binding.Validate() != nil || !scope.Binding.IsUser() || scope.Binding.ProductDomain != "personal" || (requireSession && scope.SessionID == "") {
		return errors.New("client receipts require a Personal host binding and target session")
	}
	return nil
}
