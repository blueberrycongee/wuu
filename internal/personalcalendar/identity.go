package personalcalendar

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

// Validate the signed ID token from the code exchange. User-provided identity,
// email labels, and unsigned JWT payloads cannot choose a connection namespace.
func (b *Broker) verifyIDToken(ctx context.Context, token, nonce string) (string, error) {
	bad := fault("identity_invalid")
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 32768 {
		return "", bad
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	h, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(h, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return "", bad
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", bad
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return "", bad
	}
	var claims struct {
		Issuer          string `json:"iss"`
		Audience        string `json:"aud"`
		AuthorizedParty string `json:"azp"`
		Subject         string `json:"sub"`
		Nonce           string `json:"nonce"`
		Expires         int64  `json:"exp"`
		Issued          int64  `json:"iat"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" || claims.Audience != b.config.ClientID || claims.AuthorizedParty != "" && claims.AuthorizedParty != b.config.ClientID || claims.Subject == "" || len(claims.Subject) > 255 || claims.Expires <= b.now().Unix() || claims.Issued > b.now().Add(time.Minute).Unix() || claims.Issued < b.now().Add(-10*time.Minute).Unix() || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return "", bad
	}
	status, body, e := b.request(ctx, "GET", "https://www.googleapis.com/oauth2/v3/certs", "", nil, true)
	if e != nil || status != 200 {
		return "", bad
	}
	var keys struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			Type      string `json:"kty"`
			Use       string `json:"use"`
			Algorithm string `json:"alg"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(body, &keys) != nil {
		return "", bad
	}
	for _, key := range keys.Keys {
		if key.KeyID != header.KeyID || key.Type != "RSA" || key.Use != "sig" || key.Algorithm != "RS256" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil || len(n) < 256 || len(n) > 1024 {
			return "", bad
		}
		exponent, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(exponent) > 4 {
			return "", bad
		}
		exp := new(big.Int).SetBytes(exponent).Int64()
		if exp < 3 || exp > 2147483647 {
			return "", bad
		}
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp)}, crypto.SHA256, sum[:], signature) != nil {
			return "", bad
		}
		return claims.Subject, nil
	}
	return "", bad
}
