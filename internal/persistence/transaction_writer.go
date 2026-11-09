package persistence

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
)

// WriteTransactionJSON writes the same bytes as json.Marshal(tx), without a
// trailing newline or a complete transaction buffer. It does not validate commit
// semantics. On an encoding or output error, dst may contain a partial value.
func WriteTransactionJSON(dst io.Writer, tx Transaction) error {
	return jsonv2.MarshalWrite(dst, tx, json.DefaultOptionsV1())
}
