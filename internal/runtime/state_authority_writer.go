package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
)

// The checksum covers the exact payload bytes, excluding the envelope. Keep the
// legacy JSON format so existing heads and pending receipts remain recoverable.
func writeAuthorityEnvelope(dst io.Writer, record *authorityRecord) error {
	if _, err := io.WriteString(dst, `{"payload":`); err != nil {
		return err
	}
	digest := sha256.New()
	if err := jsonv2.MarshalWrite(io.MultiWriter(dst, digest), record, json.DefaultOptionsV1()); err != nil {
		return err
	}
	_, err := io.WriteString(dst, `,"digest":"`+hex.EncodeToString(digest.Sum(nil))+`"}`)
	return err
}
