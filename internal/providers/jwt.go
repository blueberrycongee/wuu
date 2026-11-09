package providers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// DecodeJWTClaims returns the claims map decoded from a JWT payload
// segment. The signature is NOT verified — use the result only for
// metadata such as expiry or account identifiers, never for
// authorization decisions.
func DecodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&claims); err != nil {
		return nil
	}
	return claims
}

// TokenExpiringWithin reports whether the token's exp claim falls before
// now+skew. A token without a usable exp claim is not treated as
// expiring.
func TokenExpiringWithin(token string, skew time.Duration) bool {
	claims := DecodeJWTClaims(token)
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return false
	}
	return time.Unix(int64(exp), 0).Before(time.Now().Add(skew))
}
