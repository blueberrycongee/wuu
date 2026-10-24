package persistence

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
)

// DecodeTransaction validates one request while borrowing its document values.
// The caller must keep raw immutable for the returned transaction's lifetime.
// Authority and provider dispatch both finish using it before returning the call.
func DecodeTransaction(raw json.RawMessage) (Transaction, error) {
	var tx Transaction
	if err := decodeBorrowedValues(raw, &tx, true); err != nil {
		return Transaction{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return tx, nil
}

func decodeBorrowedValues(raw json.RawMessage, target any, rejectUnknown bool) error {
	decoder := jsontext.NewDecoder(bytes.NewBuffer(raw), json.DefaultOptionsV1())
	values := jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *json.RawMessage) error {
		encoded, err := dec.ReadValue()
		if err != nil {
			return err
		}
		end := dec.InputOffset()
		// ReadValue's buffer expires on the next read. Offset into the original
		// immutable bytes instead, which remain alive through these slices.
		*value = raw[end-int64(len(encoded)) : end : end]
		return nil
	})
	if err := jsonv2.UnmarshalDecode(decoder, target, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(rejectUnknown), jsonv2.WithUnmarshalers(values)); err != nil {
		return err
	}
	if _, err := decoder.ReadToken(); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

type wireByteCount int

func (n *wireByteCount) Write(p []byte) (int, error) {
	*n += wireByteCount(len(p))
	return len(p), nil
}

func marshalTransaction(tx Transaction) (json.RawMessage, error) {
	return marshalWire(func(w io.Writer) error { return WriteTransactionJSON(w, tx) })
}

// MarshalSnapshot encodes the export response without retaining a second
// complete serialization buffer. Its bytes match json.Marshal(snapshot).
func MarshalSnapshot(snapshot Snapshot) (json.RawMessage, error) {
	return marshalWire(func(w io.Writer) error { return jsonv2.MarshalWrite(w, snapshot, json.DefaultOptionsV1()) })
}

func marshalWire(write func(io.Writer) error) (json.RawMessage, error) {
	// The service contract needs one contiguous message. Count first so growth
	// and a final Marshal clone do not temporarily retain additional full copies.
	var size wireByteCount
	if err := write(&size); err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	encoded.Grow(int(size))
	if err := write(&encoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
