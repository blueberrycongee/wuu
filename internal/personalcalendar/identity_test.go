package personalcalendar

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
	"time"
)

func TestIDTokenSignatureAndClaims(t *testing.T) {
	b, _ := fixture(t)
	private, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	keys, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kid": "fixture-key", "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(private.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes())}}})
	b.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://www.googleapis.com/oauth2/v3/certs" || r.Header.Get("Authorization") != "" {
			t.Fatal("unsafe JWKS request")
		}
		return response(200, string(keys)), nil
	})
	sign := func(change func(map[string]any)) string {
		claims := map[string]any{"iss": "https://accounts.google.com", "aud": b.config.ClientID, "sub": "fixture-sub", "nonce": "fixture-nonce", "iat": b.now().Unix(), "exp": b.now().Add(time.Hour).Unix()}
		change(claims)
		body, _ := json.Marshal(claims)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture-key"}`))
		value := header + "." + base64.RawURLEncoding.EncodeToString(body)
		hash := sha256.Sum256([]byte(value))
		signature, e := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, hash[:])
		if e != nil {
			t.Fatal(e)
		}
		return value + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	valid := sign(func(map[string]any) {})
	if subject, e := b.verifyIDToken(context.Background(), valid, "fixture-nonce"); e != nil || subject != "fixture-sub" {
		t.Fatal(subject, e)
	}
	for _, change := range []func(map[string]any){func(c map[string]any) { c["iss"] = "https://evil.invalid" }, func(c map[string]any) { c["aud"] = "foreign-client" }, func(c map[string]any) { c["azp"] = "foreign-client" }, func(c map[string]any) { c["nonce"] = "replayed" }, func(c map[string]any) { c["exp"] = b.now().Unix() - 1 }, func(c map[string]any) { c["iat"] = b.now().Add(time.Hour).Unix() }} {
		if _, e := b.verifyIDToken(context.Background(), sign(change), "fixture-nonce"); e == nil {
			t.Fatal("invalid claims accepted")
		}
	}
	if _, e := b.verifyIDToken(context.Background(), valid+"bad", "fixture-nonce"); e == nil {
		t.Fatal("invalid signature accepted")
	}
}
