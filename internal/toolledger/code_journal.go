package toolledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

const codeJournals = toolstate.CodeJournals
const codeJournalEntries = toolstate.CodeJournalEntries
const codeStores = toolstate.CodeStores

type codeJournal = toolstate.CodeJournalState
type codeStore = toolstate.CodeStore

// CodeJournal binds every operation to the currently running ledger attempt.
// The execution worker holds no document authority or reusable receipt token.
func (l *Ledger) CodeJournal(ctx context.Context, parentID string, attempt uint64, request toolctx.CodeJournalRequest) (out toolctx.CodeJournalResponse, err error) {
	err = l.write(ctx, func(tx *persistence.Tx) error {
		parent, e := l.invocation(tx, parentID)
		if e != nil {
			return e
		}
		batch, e := l.batch(tx, parent.BatchID)
		if e != nil {
			return e
		}
		if parent.Attempt != attempt || parent.State != string(InvocationRunning) || batch.RecoveryScope == "" {
			return errors.New("code journal parent attempt is not running")
		}
		var journal codeJournal
		found, e := tx.Get(codeJournals, parentID, &journal)
		if e != nil {
			return e
		}
		if request.Operation == "open" {
			if request.Fingerprint == "" || !json.Valid(request.Start) {
				return errors.New("invalid code journal start")
			}
			if found {
				if journal.Fingerprint != request.Fingerprint || journal.Scope != request.Scope {
					return errors.New("code journal program or runtime changed")
				}
			} else {
				var state codeStore
				if request.Scope != "" {
					if _, e := tx.Get(codeStores, toolstate.CodeStoreKey(l.ownerID, request.Scope), &state); e != nil {
						return e
					}
				}
				journal = codeJournal{OwnerID: l.ownerID, Fingerprint: request.Fingerprint, Scope: request.Scope, Start: request.Start, State: state.Values, StoreRevision: state.Revision}
				if e := tx.Put(codeJournals, parentID, journal); e != nil {
					return e
				}
			}
			out = toolctx.CodeJournalResponse{Start: journal.Start, State: journal.State, Length: journal.Length, Outcome: journal.Outcome}
			return nil
		}
		if !found {
			return errors.New("code journal was not opened")
		}
		entryKey := fmt.Sprintf("%s/%020d", parentID, request.Index)
		switch request.Operation {
		case "read":
			if request.Index < 0 || request.Index >= journal.Length {
				return errors.New("code journal entry is outside the recorded prefix")
			}
			found, e := tx.Get(codeJournalEntries, entryKey, &out.Entry)
			if e != nil {
				return e
			}
			if !found {
				return fmt.Errorf("%w: code journal entry missing", persistence.ErrCorrupt)
			}
		case "append":
			if len(journal.Outcome) > 0 || request.Index != journal.Length || !json.Valid(request.Entry) {
				return errors.New("code journal append conflicts with recorded prefix")
			}
			if e := tx.Put(codeJournalEntries, entryKey, request.Entry); e != nil {
				return e
			}
			journal.Length++
			if e := tx.Put(codeJournals, parentID, journal); e != nil {
				return e
			}
		case "complete":
			if len(journal.Outcome) > 0 || request.Index != journal.Length || !json.Valid(request.Outcome) {
				return errors.New("code journal completion conflicts with recorded prefix")
			}
			if request.State != nil && journal.Scope != "" {
				key := toolstate.CodeStoreKey(l.ownerID, journal.Scope)
				var state codeStore
				if _, e := tx.Get(codeStores, key, &state); e != nil {
					return e
				}
				if state.Revision != journal.StoreRevision {
					return errors.New("code journal state changed during the program")
				}
				state.Revision++
				state.Values = request.State
				journal.CommittedStore = &state
				if e := tx.Put(codeStores, key, state); e != nil {
					return e
				}
			}
			journal.Outcome = request.Outcome
			if e := tx.Put(codeJournals, parentID, journal); e != nil {
				return e
			}
		default:
			return errors.New("unknown code journal operation")
		}
		out.Length = journal.Length
		return nil
	})
	return
}
