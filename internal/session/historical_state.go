package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// SessionCompositionNamespace is the existing persisted product selection. The
// runtime owns availability and admission; history only retains the selection.
const SessionCompositionNamespace = "wuu.session_compositions.v1"

// HistoricalAgentConfiguration excludes grants, permission modes and approvals.
// Those are evaluated using current authority rather than historical state.
type HistoricalAgentConfiguration struct {
	CWD            string            `json:"cwd"`
	WorkspaceID    string            `json:"workspace_id,omitempty"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Variant        string            `json:"variant,omitempty"`
	Effort         string            `json:"effort,omitempty"`
	Speed          string            `json:"speed,omitempty"`
	ModelOptions   map[string]string `json:"model_options,omitempty"`
	Instructions   string            `json:"instructions,omitempty"`
	ToolPolicyJSON string            `json:"tool_policy_json,omitempty"`
	ProjectRole    string            `json:"project_role,omitempty"`
}

type HistoricalState struct {
	Version     int                                     `json:"version"`
	Agent       HistoricalAgentConfiguration            `json:"agent"`
	Composition json.RawMessage                         `json:"composition,omitempty"`
	Driver      pluginprotocol.DriverBinding            `json:"driver"`
	Stores      map[string]toolstate.CodeStoreReference `json:"stores"`
}

func (s *Store) LoadHistoricalState(id string, seq int) (HistoricalState, error) {
	var out HistoricalState
	err := s.invoke("LoadHistoricalState", []any{id, seq}, []any{&out})
	return out, err
}
func (s *documentStore) LoadHistoricalState(id string, seq int) (HistoricalState, error) {
	var out HistoricalState
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		out, err = loadHistoricalState(tx, id, seq)
		return err
	})
	return out, err
}
func loadHistoricalState(tx *persistence.Tx, id string, seq int) (HistoricalState, error) {
	if _, err := requireSessionDocument(tx, id); err != nil {
		return HistoricalState{}, err
	}
	var retractions []historyRetraction
	if _, err := tx.Get(documentNamespace("session_history_retractions"), id, &retractions); err != nil {
		return HistoricalState{}, err
	}
	var record HistoryRecord
	found, err := tx.Get(documentNamespace("session_messages"), historyDocumentKey(id, seq), &record)
	if err != nil {
		return HistoricalState{}, err
	}
	if seq < 1 || !found || historySequenceRetracted(seq, retractions) {
		return HistoricalState{}, errors.New("fork_seq is not a persisted sequence in the parent's active branch")
	}
	if record.HistoricalState == nil || record.HistoricalState.Version != 1 {
		return HistoricalState{}, errors.New("historical fork state snapshot is unavailable for this entry")
	}
	return *record.HistoricalState, nil
}

func captureHistoricalState(tx *persistence.Tx, id string, seq int, record *HistoryRecord) error {
	record.HistoricalState = nil
	var driver pluginprotocol.DriverBinding
	found, err := tx.Get(documentNamespace("session_driver_bindings"), id, &driver)
	if err != nil || !found || !driver.HistoryState {
		return err
	}
	sess, err := requireSessionDocument(tx, id)
	if err != nil {
		return err
	}
	state := &HistoricalState{Version: 1, Driver: driver, Stores: make(map[string]toolstate.CodeStoreReference), Agent: HistoricalAgentConfiguration{
		CWD: sess.CWD, WorkspaceID: sess.WorkspaceID, Provider: sess.Provider, Model: sess.Model, Variant: sess.Variant, Effort: sess.Effort, Speed: sess.Speed, ModelOptions: sess.ModelOptions, Instructions: sess.Instructions, ToolPolicyJSON: sess.ToolPolicyJSON, ProjectRole: sess.ProjectRole,
	}}
	if _, err := tx.Get(SessionCompositionNamespace, id, &state.Composition); err != nil {
		return err
	}
	if seq > 1 {
		var previous HistoryRecord
		if _, err := tx.Get(documentNamespace("session_messages"), historyDocumentKey(id, seq-1), &previous); err != nil {
			return err
		}
		if previous.HistoricalState != nil {
			for scope, ref := range previous.HistoricalState.Stores {
				state.Stores[scope] = ref
			}
		} else {
			records, err := loadHistoryDocuments(tx, id, true, true)
			if err != nil {
				return err
			}
			for _, prior := range records {
				if prior.Seq >= seq || prior.ToolInvocationID == "" {
					continue
				}
				ref, found, err := toolstate.ProjectedCodeStore(tx, id, prior.ToolInvocationID)
				if errors.Is(err, toolstate.ErrHistoricalCodeStateUnavailable) {
					return nil
				}
				if err != nil {
					return err
				}
				if found {
					if old, exists := state.Stores[ref.Scope]; !exists || old.Owner != ref.Owner || ref.Revision > old.Revision {
						state.Stores[ref.Scope] = ref
					}
				}
			}
		}
	}
	if record.ToolInvocationID != "" {
		ref, found, err := toolstate.ProjectedCodeStore(tx, id, record.ToolInvocationID)
		if err != nil {
			return err
		}
		if found {
			if prior, exists := state.Stores[ref.Scope]; !exists || prior.Owner != ref.Owner || ref.Revision > prior.Revision {
				state.Stores[ref.Scope] = ref
			}
		}
	}
	record.HistoricalState = state
	return nil
}

// HistoricalPrefixDigest fences the exact source prefix across artifact/workspace
// preparation. It is a consistency check, not authority to read or execute it.
func HistoricalPrefixDigest(records []HistoryRecord, seq int) string {
	prefix := make([]HistoryRecord, 0, len(records))
	for _, r := range records {
		if r.Seq <= seq {
			prefix = append(prefix, r)
		}
	}
	encoded, _ := json.Marshal(prefix)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func initializeHistoricalFork(tx *persistence.Tx, current Session, initial []HistoryRecord) ([]HistoryRecord, error) {
	active, err := loadHistoryDocuments(tx, current.ForkedFromID, true, true)
	if err != nil {
		return nil, err
	}
	if current.ForkHistoryDigest == "" || HistoricalPrefixDigest(active, current.ForkedFromSeq) != current.ForkHistoryDigest {
		return nil, errors.New("historical fork source changed before creation; reload the source prefix")
	}

	state, err := loadHistoricalState(tx, current.ForkedFromID, current.ForkedFromSeq)
	if err != nil {
		return nil, err
	}
	if len(state.Stores) > 0 && current.CWD != state.Agent.CWD {
		return nil, errors.New("historical code state cannot cross working directories")
	}
	refs, err := toolstate.RebaseConversationCodeStores(state.Stores, current.ForkedFromID, current.ID, current.CWD)
	if err != nil {
		return nil, err
	}
	if err := toolstate.ForkCodeStores(tx, current.ID, refs); err != nil {
		return nil, err
	}
	if err := tx.Put(documentNamespace("session_driver_bindings"), current.ID, state.Driver); err != nil {
		return nil, err
	}
	if len(state.Composition) > 0 {
		if err := tx.Put(SessionCompositionNamespace, current.ID, state.Composition); err != nil {
			return nil, err
		}
	} else if err := tx.Delete(SessionCompositionNamespace, current.ID); err != nil {
		return nil, err
	}
	for i := range initial {
		// These are copied semantic records, not new events at the child's current
		// configuration. Preserve each source entry's proof, including absence.
		if initial[i].Seq < 1 || initial[i].Seq > current.ForkedFromSeq {
			return nil, fmt.Errorf("fork record has invalid source sequence %d", initial[i].Seq)
		}
		var source HistoryRecord
		found, err := tx.Get(documentNamespace("session_messages"), historyDocumentKey(current.ForkedFromID, initial[i].Seq), &source)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrHistorySnapshotGone
		}
		initial[i].HistoricalState = source.HistoricalState
		if source.HistoricalState != nil {
			mapped, err := toolstate.RebaseConversationCodeStores(source.HistoricalState.Stores, current.ForkedFromID, current.ID, source.HistoricalState.Agent.CWD)
			if err != nil {
				return nil, err
			}
			initial[i].HistoricalState.Stores = mapped
		}
	}
	return initial, nil
}
