package toolstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

var ErrHistoricalCodeStateUnavailable = errors.New("historical code state snapshot is unavailable")

// CodeStoreReference names an immutable committed program state. The enclosing
// history entry is its visibility boundary; the latest execution store is not.
type CodeStoreReference struct {
	Owner      string `json:"owner"`
	Scope      string `json:"scope"`
	Invocation string `json:"invocation"`
	Revision   uint64 `json:"revision"`
}

// ProjectedCodeStore is read in the same transaction as Project and
// the history entry. Unprojected completions cannot enter a historical snapshot.
func ProjectedCodeStore(tx *persistence.Tx, owner, invocationID string) (CodeStoreReference, bool, error) {
	var journal CodeJournalState
	found, err := tx.Get(CodeJournals, invocationID, &journal)
	if err != nil || !found {
		return CodeStoreReference{}, false, err
	}
	if journal.OwnerID == "" && journal.Scope != "" && len(journal.Outcome) > 0 {
		return CodeStoreReference{}, false, ErrHistoricalCodeStateUnavailable
	}
	if journal.CommittedStore == nil {
		return CodeStoreReference{}, false, nil
	}
	var invocation Invocation
	found, err = tx.Get(Invocations, invocationID, &invocation)
	if err != nil {
		return CodeStoreReference{}, false, err
	}
	var batch Batch
	if !found || invocation.ProjectedAt == 0 {
		return CodeStoreReference{}, false, errors.New("code store requires a projected invocation")
	}
	found, err = tx.Get(Batches, invocation.BatchID, &batch)
	if err != nil {
		return CodeStoreReference{}, false, err
	}
	if !found || batch.OwnerID != owner {
		return CodeStoreReference{}, false, errors.New("code store belongs to another owner")
	}
	return CodeStoreReference{Owner: owner, Scope: journal.Scope, Invocation: invocationID, Revision: journal.CommittedStore.Revision}, true, nil
}

// ForkCodeStores initializes only store/load values, never execution journals or
// invocation ownership. Scope keys are supplied by the host's verified mapping.
func ForkCodeStores(tx *persistence.Tx, owner string, refs map[string]CodeStoreReference) error {
	for scope, ref := range refs {
		var journal CodeJournalState
		found, err := tx.Get(CodeJournals, ref.Invocation, &journal)
		if err != nil {
			return err
		}
		if !found || journal.Scope != ref.Scope || journal.CommittedStore == nil || journal.CommittedStore.Revision != ref.Revision || len(journal.Outcome) == 0 {
			return fmt.Errorf("%w: historical code state is unavailable", persistence.ErrCorrupt)
		}
		if journal.OwnerID != ref.Owner {
			return errors.New("historical code state owner changed")
		}
		key := CodeStoreKey(owner, scope)
		var current CodeStore
		found, err = tx.Get(CodeStores, key, &current)
		if err != nil {
			return err
		}
		if found {
			return errors.New("fork code store already exists")
		}
		// The fork owns a fresh CAS generation. The reference retains the source
		// version solely for historical visibility and future forks.
		if err := tx.Put(CodeStores, key, CodeStore{Revision: 1, Values: journal.CommittedStore.Values}); err != nil {
			return err
		}
	}
	return nil
}

// RebaseConversationCodeStores accepts only toolkit conversation scopes. An
// arbitrary scope or a different working directory must not be guessed across.
func RebaseConversationCodeStores(refs map[string]CodeStoreReference, parent, child, cwd string) (map[string]CodeStoreReference, error) {
	out := make(map[string]CodeStoreReference, len(refs))
	for scope, ref := range refs {
		var identity []string
		if json.Unmarshal([]byte(scope), &identity) != nil || len(identity) != 3 || identity[0] != parent || identity[1] != parent || identity[2] != cwd {
			return nil, errors.New("historical code state cannot be mapped to the fork's conversation scope")
		}
		encoded, _ := json.Marshal([]string{child, child, cwd})
		out[string(encoded)] = ref
	}
	return out, nil
}

// CodeStoreKey preserves the existing owner/scope address used by the execution
// ledger; history projections share the selected authority, not another store.
func CodeStoreKey(owner, scope string) string {
	raw, _ := json.Marshal([]string{owner, scope})
	sum := sha256.Sum256(raw)
	return "tbatch-r-" + hex.EncodeToString(sum[:])
}
