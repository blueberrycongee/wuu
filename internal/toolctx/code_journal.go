package toolctx

import (
	"context"
	"encoding/json"
)

// CodeJournal is a host-bound capability, available only to a durably admitted
// orchestrator. The remote execution worker forwards it through its existing
// nested-call bridge. Requests cannot choose a parent, owner, or attempt.
type CodeJournal interface {
	CodeJournal(context.Context, CodeJournalRequest) (CodeJournalResponse, error)
}

type CodeJournalRequest struct {
	Operation   string            `json:"operation"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Scope       string            `json:"scope,omitempty"`
	Start       json.RawMessage   `json:"start,omitempty"`
	Index       int               `json:"index,omitempty"`
	Entry       json.RawMessage   `json:"entry,omitempty"`
	Outcome     json.RawMessage   `json:"outcome,omitempty"`
	State       map[string]string `json:"state"`
}
type CodeJournalResponse struct {
	Start   json.RawMessage   `json:"start,omitempty"`
	State   map[string]string `json:"state,omitempty"`
	Length  int               `json:"length"`
	Entry   json.RawMessage   `json:"entry,omitempty"`
	Outcome json.RawMessage   `json:"outcome,omitempty"`
}
