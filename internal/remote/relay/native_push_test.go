package relay

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/account"
)

type pushTransport func(*http.Request) (*http.Response, error)

func (f pushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func pushResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestNativePushProviderSignaturesAndTokenReuse(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &NativePusher{apnsKey: ec, fcmKey: rsaKey, fcmEmail: "sender@example.invalid", fcmProject: "test-project"}
	if err = json.Unmarshal([]byte(`{"apns":{"key_id":"KEY","team_id":"TEAM","topic":"com.example.wuu","sandbox":true}}`), &p.config); err != nil {
		t.Fatal(err)
	}
	authCalls, apnsCalls, fcmCalls := 0, 0, 0
	lastAPNs := ""
	verify := func(token string, ecKey bool) {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatal("invalid JWT")
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		if ecKey {
			if len(signature) != 64 || !ecdsa.Verify(&ec.PublicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
				t.Fatal("invalid APNs JOSE signature")
			}
		} else if rsa.VerifyPKCS1v15(&rsaKey.PublicKey, crypto.SHA256, digest[:], signature) != nil {
			t.Fatal("invalid FCM JWT signature")
		}
	}
	p.client = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "oauth2.googleapis.com":
			authCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			verify(r.Form.Get("assertion"), false)
			return pushResponse(`{"access_token":"oauth-test","expires_in":3600}`), nil
		case "api.sandbox.push.apple.com":
			apnsCalls++
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "bearer ")
			verify(token, true)
			if lastAPNs != "" && lastAPNs != token {
				t.Fatal("APNs signing token refreshed on every notification")
			}
			lastAPNs = token
			if r.Header.Get("apns-topic") != "com.example.wuu" || r.Header.Get("apns-push-type") != "alert" {
				t.Fatal("missing APNs delivery contract")
			}
		case "fcm.googleapis.com":
			fcmCalls++
			if r.URL.Path != "/v1/projects/test-project/messages:send" || r.Header.Get("Authorization") != "bearer oauth-test" {
				t.Fatal("invalid FCM authorization")
			}
		default:
			t.Fatalf("unexpected provider %s", r.URL.Host)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			t.Fatal("missing notification payload")
		}
		return pushResponse(`{}`), nil
	})}
	for range 2 {
		if err = p.deliver(context.Background(), PushEvent{Host: "host-public-key", Hint: "agent_done"}, account.PushRegistration{Platform: "ios", Token: strings.Repeat("ab", 32)}); err != nil {
			t.Fatal(err)
		}
		if err = p.deliver(context.Background(), PushEvent{Host: "host-public-key", Hint: "needs_input"}, account.PushRegistration{Platform: "android", Token: "fcm-device"}); err != nil {
			t.Fatal(err)
		}
	}
	if authCalls != 1 || apnsCalls != 2 || fcmCalls != 2 {
		t.Fatal("provider requests or OAuth caching incorrect")
	}
}
