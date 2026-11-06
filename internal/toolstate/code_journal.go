package toolstate

import "encoding/json"

const CodeJournals = "wuu.code_journals.v1"
const CodeJournalEntries = "wuu.code_journal_entries.v1"
const CodeStores = "wuu.code_stores.v1"

type CodeJournalState struct {
	Fingerprint, Scope string
	OwnerID            string
	Start              json.RawMessage
	State              map[string]string
	StoreRevision      uint64
	Length             int
	Outcome            json.RawMessage `json:",omitempty"`
	CommittedStore     *CodeStore      `json:",omitempty"`
}
type CodeStore struct {
	Revision uint64
	Values   map[string]string
}
