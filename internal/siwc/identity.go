package siwc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

type jwk struct {
	KeyType    string   `json:"kty"`
	ID         string   `json:"kid"`
	Algorithm  string   `json:"alg"`
	Use        string   `json:"use"`
	Operations []string `json:"key_ops"`
	N          string   `json:"n"`
	E          string   `json:"e"`
	Curve      string   `json:"crv"`
	X          string   `json:"x"`
	Y          string   `json:"y"`
}
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}
type identityClaims struct {
	Issuer          string          `json:"iss"`
	Subject         string          `json:"sub"`
	Audience        json.RawMessage `json:"aud"`
	AuthorizedParty string          `json:"azp"`
	Expiration      int64           `json:"exp"`
	IssuedAt        int64           `json:"iat"`
	NotBefore       int64           `json:"nbf"`
	Nonce           string          `json:"nonce"`
	Email           string          `json:"email"`
}

var errInvalidIdentity = errors.New("ChatGPT ID token could not be verified")

func (m *Manager) verifyIdentity(ctx context.Context, token, clientID, nonce string, requireNonce bool) (identityClaims, error) {
	var claims identityClaims
	if len(token) > 128*1024 {
		return claims, errInvalidIdentity
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, errInvalidIdentity
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, errInvalidIdentity
	}
	var header struct {
		Algorithm string   `json:"alg"`
		KeyID     string   `json:"kid"`
		Critical  []string `json:"crit"`
		Base64    *bool    `json:"b64"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.KeyID == "" || len(header.Critical) != 0 || (header.Base64 != nil && !*header.Base64) || (header.Algorithm != "RS256" && header.Algorithm != "ES256") {
		return claims, errInvalidIdentity
	}
	key, err := m.signingKey(ctx, header.KeyID, header.Algorithm)
	if err != nil {
		return claims, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return claims, errInvalidIdentity
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch pub := key.(type) {
	case *rsa.PublicKey:
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) != nil {
			return claims, errInvalidIdentity
		}
	case *ecdsa.PublicKey:
		if len(signature) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
			return claims, errInvalidIdentity
		}
	default:
		return claims, errInvalidIdentity
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return claims, errInvalidIdentity
	}
	now := m.now().Unix()
	if claims.Issuer != m.config.Issuer || claims.Subject == "" || len(claims.Subject) > 4096 || claims.Expiration <= now-5 || claims.IssuedAt <= 0 || claims.IssuedAt > now+5 || claims.NotBefore > now+5 {
		return claims, errInvalidIdentity
	}
	var audience []string
	var single string
	if json.Unmarshal(claims.Audience, &single) == nil {
		audience = []string{single}
	} else if json.Unmarshal(claims.Audience, &audience) != nil {
		return claims, errInvalidIdentity
	}
	found := false
	for _, a := range audience {
		if a == clientID {
			found = true
		}
	}
	if !found || (len(audience) > 1 && claims.AuthorizedParty != clientID) || (claims.AuthorizedParty != "" && claims.AuthorizedParty != clientID) {
		return claims, errInvalidIdentity
	}
	if (requireNonce || claims.Nonce != "") && subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return claims, errInvalidIdentity
	}
	return claims, nil
}

func (m *Manager) signingKey(ctx context.Context, id, algorithm string) (any, error) {
	doc, err := m.discover(ctx)
	if err != nil {
		return nil, err
	}
	m.keysMu.Lock()
	defer m.keysMu.Unlock()
	fresh := len(m.keys.Keys) > 0 && m.now().Sub(m.keysFetched) < time.Hour
	if fresh {
		if key := findKey(m.keys, id, algorithm); key != nil {
			return key, nil
		}
	}
	// Refresh an expired set or an unfamiliar kid once per verification. All
	// key URLs come from pinned discovery, never JWT-controlled jku/x5u fields.
	var keys jwksDocument
	if err = m.getJSON(ctx, doc.JWKSURI, &keys); err != nil {
		return nil, err
	}
	if len(keys.Keys) > 100 {
		return nil, errInvalidIdentity
	}
	m.keys = keys
	m.keysFetched = m.now()
	if key := findKey(keys, id, algorithm); key != nil {
		return key, nil
	}
	return nil, errInvalidIdentity
}
func findKey(doc jwksDocument, id, algorithm string) any {
	var result any
	for _, key := range doc.Keys {
		if key.ID != id || (key.Algorithm != "" && key.Algorithm != algorithm) || (key.Use != "" && key.Use != "sig") || (len(key.Operations) > 0 && !hasScope(key.Operations, "verify")) {
			continue
		}
		var parsed any
		switch {
		case algorithm == "RS256" && key.KeyType == "RSA":
			n, err := base64.RawURLEncoding.DecodeString(key.N)
			if err != nil {
				continue
			}
			e, err := base64.RawURLEncoding.DecodeString(key.E)
			if err != nil || len(e) > 4 {
				continue
			}
			modulus := new(big.Int).SetBytes(n)
			exponent := new(big.Int).SetBytes(e).Int64()
			if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent < 3 || exponent > 2147483647 || exponent%2 == 0 {
				continue
			}
			parsed = &rsa.PublicKey{N: modulus, E: int(exponent)}
		case algorithm == "ES256" && key.KeyType == "EC" && key.Curve == "P-256":
			x, err := base64.RawURLEncoding.DecodeString(key.X)
			if err != nil || len(x) != 32 {
				continue
			}
			y, err := base64.RawURLEncoding.DecodeString(key.Y)
			if err != nil || len(y) != 32 {
				continue
			}
			pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
			if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
				continue
			}
			parsed = pub
		}
		if parsed != nil {
			if result != nil {
				return nil
			}
			result = parsed
		}
	}
	return result
}
