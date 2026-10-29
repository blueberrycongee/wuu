package session

import "github.com/blueberrycongee/wuu/internal/providers"

// These additive methods share the session domain transport. Their inventory
// is maintained here; existing generated session methods remain untouched.
var clientInputDomainMethods = []string{
	"AppendAcceptedUserHistoryRecordWithControl", "ReadClientInput", "ListUnsettledClientInputs", "AcceptClientInput", "ChangeClientInput",
	"AppendAcceptedClientHistoryRecord", "SettleClientInputExecution",
	"ListClientInputRecoveryCandidates", "RecoverClientInput", "ClientInputHistoryReceipts",
}

func (s *Store) ReadClientInput(scope ClientInputScope, hostRequestID string) (AcceptedClientInput, bool, error) {
	var result AcceptedClientInput
	var found bool
	err := s.invoke("ReadClientInput", []any{scope, hostRequestID}, []any{&result, &found})
	return result, found, err
}
func (s *Store) ListUnsettledClientInputs(scope ClientInputScope) ([]AcceptedClientInput, error) {
	var result []AcceptedClientInput
	err := s.invoke("ListUnsettledClientInputs", []any{scope}, []any{&result})
	return result, err
}
func (s *Store) AcceptClientInput(input AcceptedClientInput, control, baseline *Control) (AcceptedClientInput, bool, error) {
	var result AcceptedClientInput
	var fresh bool
	err := s.invoke("AcceptClientInput", []any{input, control, baseline}, []any{&result, &fresh})
	return result, fresh, err
}
func (s *Store) ChangeClientInput(expected AcceptedClientInput, state, owner string, runtimeJSON []byte, control, baseline *Control) (AcceptedClientInput, error) {
	var result AcceptedClientInput
	err := s.invoke("ChangeClientInput", []any{expected, state, owner, runtimeJSON, control, baseline}, []any{&result})
	return result, err
}
func (s *Store) AppendAcceptedClientHistoryRecord(id string, record HistoryRecord, accepted AcceptedClientInput, turnID string, control, baseline *Control) (int, error) {
	var seq int
	err := s.invoke("AppendAcceptedClientHistoryRecord", []any{id, record, accepted, turnID, control, baseline}, []any{&seq})
	return seq, err
}
func (s *Store) SettleClientInputExecution(scope ClientInputScope, hostRequestID, turnID, state, reason string) error {
	return s.invoke("SettleClientInputExecution", []any{scope, hostRequestID, turnID, state, reason}, []any{})
}
func (s *Store) ListClientInputRecoveryCandidates(scope ClientInputScope) ([]AcceptedClientInput, error) {
	var result []AcceptedClientInput
	err := s.invoke("ListClientInputRecoveryCandidates", []any{scope}, []any{&result})
	return result, err
}
func (s *Store) RecoverClientInput(scope ClientInputScope, expected AcceptedClientInput) error {
	return s.invoke("RecoverClientInput", []any{scope, expected}, []any{})
}

// RecoverClientInputs probes transient host leases, not business files. The
// selected provider performs only a fenced transition; it never replays work.
func (s *Store) RecoverClientInputs(leaseDir string, scope ClientInputScope) error {
	candidates, err := s.ListClientInputRecoveryCandidates(scope)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		active, err := ThreadExecutionActive(leaseDir, candidate.LiveOwner)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		if err := s.RecoverClientInput(scope, candidate); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ClientInputHistoryReceipts(scope ClientInputScope) (map[string]providers.ClientInputReceipt, error) {
	var result map[string]providers.ClientInputReceipt
	err := s.invoke("ClientInputHistoryReceipts", []any{scope}, []any{&result})
	return result, err
}

func (s *Store) AppendAcceptedUserHistoryRecordWithControl(id string, record HistoryRecord, accepted HeldUserWork, control, baseline *Control) (int, error) {
	var seq int
	err := s.invoke("AppendAcceptedUserHistoryRecordWithControl", []any{id, record, accepted, control, baseline}, []any{&seq})
	return seq, err
}
