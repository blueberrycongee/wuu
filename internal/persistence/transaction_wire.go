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
	decoder := jsontext.NewDecoder(bytes.NewBuffer(raw), json.DefaultOptionsV1())
	values := jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *json.RawMessage) error {
		encoded, err := dec.ReadValue()
		if err != nil {
			return err
		}
		end := dec.InputOffset()
		// ReadValue's buffer expires on the next read. Offset into the original
		// immutable request instead, whose lifetime belongs to the service call.
		*value = raw[end-int64(len(encoded)) : end]
		return nil
	})
	if err := jsonv2.UnmarshalDecode(decoder, &tx, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(true), jsonv2.WithUnmarshalers(values)); err != nil {
		return Transaction{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := decoder.ReadToken(); err != io.EOF {
		return Transaction{}, fmt.Errorf("%w: expected one request object", ErrInvalid)
	}
	return tx, nil
}

type transactionByteCount int

func (n *transactionByteCount) Write(p []byte) (int, error) {
	*n += transactionByteCount(len(p))
	return len(p), nil
}

func marshalTransaction(tx Transaction) (json.RawMessage, error) {
	// The service contract needs one contiguous request. Count first so growth
	// and a final Marshal clone do not temporarily retain additional full copies.
	var size transactionByteCount
	if err := WriteTransactionJSON(&size, tx); err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	encoded.Grow(int(size))
	if err := WriteTransactionJSON(&encoded, tx); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
