package session

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// ClientInputContextRef identifies an admitted input included in a model context.
type ClientInputContextRef struct {
	ClientID   string
	MessageSeq int
}

func (s *documentStore) MarkClientInputsRead(scope ClientInputScope, refs []ClientInputContextRef) error {
	if err := scope.validate(true); err != nil {
		return err
	}
	keys := make([]string, 0, len(refs))
	identities := make(map[string]map[int]struct{}, len(refs))
	for _, ref := range refs {
		if ref.ClientID == "" || ref.MessageSeq < 1 {
			continue
		}
		if identities[ref.ClientID] == nil {
			identities[ref.ClientID] = make(map[int]struct{})
			keys = append(keys, clientInputDocumentKey(scope.SessionID, ref.ClientID))
		}
		identities[ref.ClientID][ref.MessageSeq] = struct{}{}
	}
	if len(keys) == 0 {
		return nil
	}
	readAt := time.Now().UTC().Format(time.RFC3339Nano)
	return s.transaction(func(tx *persistence.Tx) error {
		sess, err := requireSessionDocument(tx, scope.SessionID)
		if err != nil {
			return err
		}
		if !scope.Binding.Matches(userspace.Binding{UserSpaceID: sess.UserSpaceID, ProductDomain: sess.ProductDomain}) {
			return errors.New("client receipt session binding changed")
		}
		documents, err := tx.Read(persistence.Query{Namespace: documentNamespace("client_input_receipts"), Keys: keys})
		if err != nil {
			return err
		}
		for _, document := range documents {
			var row clientInputDocument
			if err := json.Unmarshal(document.Value, &row); err != nil {
				return err
			}
			r := &row.Receipt
			_, included := identities[r.ClientID][r.MessageSeq]
			if !included || r.ReceiptState != "in_history" || r.ReadAt != "" || r.SessionID != scope.SessionID || r.UserSpaceID != scope.Binding.UserSpaceID || r.ProductDomain != scope.Binding.ProductDomain || (scope.PluginID != "" && r.PluginID != scope.PluginID) {
				continue
			}
			r.ReadAt, r.Revision = readAt, r.Revision+1
			if err := tx.Put(documentNamespace("client_input_receipts"), document.Key, row); err != nil {
				return err
			}
		}
		return nil
	})
}
