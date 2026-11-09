package session

import "encoding/json"

// Admission fences are invocation metadata, not persisted history fields.
type historyWire struct {
	HistoryRecord
	ExpectedHeldMessage  []byte               `json:"expected_held_message_bytes,omitempty"`
	AcceptedClientInput  *AcceptedClientInput `json:"accepted_client_input,omitempty"`
	AcceptedClientTurnID string               `json:"accepted_client_turn_id,omitempty"`
}
type receiptWire struct {
	OperationID     string
	ContractVersion int
	DriverID        string
	DriverVersion   string
	Payload         []byte
	CreatedAt       json.RawMessage
}

func marshalDomainValue(value any) ([]byte, error) {
	switch v := value.(type) {
	case HistoryRecord:
		return json.Marshal(historyWire{v, []byte(v.ExpectedHeldMessage), v.AcceptedClientInput, v.AcceptedClientTurnID})
	case []HistoryRecord:
		if v == nil {
			return []byte("null"), nil
		}
		out := make([]historyWire, len(v))
		for i, r := range v {
			out[i] = historyWire{r, []byte(r.ExpectedHeldMessage), r.AcceptedClientInput, r.AcceptedClientTurnID}
		}
		return json.Marshal(out)
	case ModelInputReceiptRecord:
		at, err := json.Marshal(v.CreatedAt)
		if err != nil {
			return nil, err
		}
		return json.Marshal(receiptWire{v.OperationID, v.ContractVersion, v.DriverID, v.DriverVersion, []byte(v.Payload), at})
	case []ModelInputReceiptRecord:
		if v == nil {
			return []byte("null"), nil
		}
		out := make([]json.RawMessage, len(v))
		for i, r := range v {
			raw, err := marshalDomainValue(r)
			if err != nil {
				return nil, err
			}
			out[i] = raw
		}
		return json.Marshal(out)
	case map[SubscriptionActivityKey]SubscriptionActivity:
		out := make([]SubscriptionActivity, 0, len(v))
		for k, r := range v {
			r.Key = k
			out = append(out, r)
		}
		return json.Marshal(out)
	default:
		return json.Marshal(value)
	}
}
func unmarshalDomainValue(raw []byte, target any) error {
	switch v := target.(type) {
	case *HistoryRecord:
		var r historyWire
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		*v = r.HistoryRecord
		v.ExpectedHeldMessage = json.RawMessage(r.ExpectedHeldMessage)
		v.AcceptedClientInput, v.AcceptedClientTurnID = r.AcceptedClientInput, r.AcceptedClientTurnID
		return nil
	case *[]HistoryRecord:
		var rows []historyWire
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		if rows == nil {
			*v = nil
			return nil
		}
		*v = make([]HistoryRecord, len(rows))
		for i, r := range rows {
			(*v)[i] = r.HistoryRecord
			(*v)[i].ExpectedHeldMessage = json.RawMessage(r.ExpectedHeldMessage)
			(*v)[i].AcceptedClientInput, (*v)[i].AcceptedClientTurnID = r.AcceptedClientInput, r.AcceptedClientTurnID
		}
		return nil
	case *ModelInputReceiptRecord:
		var r receiptWire
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		*v = ModelInputReceiptRecord{OperationID: r.OperationID, ContractVersion: r.ContractVersion, DriverID: r.DriverID, DriverVersion: r.DriverVersion, Payload: json.RawMessage(r.Payload)}
		return json.Unmarshal(r.CreatedAt, &v.CreatedAt)
	case *[]ModelInputReceiptRecord:
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		if rows == nil {
			*v = nil
			return nil
		}
		*v = make([]ModelInputReceiptRecord, len(rows))
		for i, r := range rows {
			if err := unmarshalDomainValue(r, &(*v)[i]); err != nil {
				return err
			}
		}
		return nil
	case *map[SubscriptionActivityKey]SubscriptionActivity:
		var rows []SubscriptionActivity
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		*v = make(map[SubscriptionActivityKey]SubscriptionActivity, len(rows))
		for _, r := range rows {
			(*v)[r.Key] = r
		}
		return nil
	default:
		return json.Unmarshal(raw, target)
	}
}
