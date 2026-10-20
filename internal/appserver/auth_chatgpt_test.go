package appserver

// This protocol integration test deliberately uses a fake issuer and inference
// server. It covers the complete registration-to-inference path without an
// installed CLI, browser login, actual credentials, or paid model requests.
import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/siwc"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type chatGPTFixtureTransport struct {
	target    *url.URL
	transport http.RoundTripper
}

func (r chatGPTFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	if u.Host != "api.openai.com" || u.Scheme != "https" {
		return nil, fmt.Errorf("unexpected inference destination")
	}
	clone.URL.Scheme, clone.URL.Host = r.target.Scheme, r.target.Host
	return r.transport.RoundTrip(clone)
}

func TestChatGPTRegistrationConnectionDiscoveryInferenceSignOut(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer *httptest.Server
	var nonce, challenge, redirect string
	var manager *siwc.Manager
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/jwks", "revocation_endpoint": issuer.URL + "/revoke"})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("client_id") != "oaiapp_fixture" || r.Form.Get("redirect_uri") != redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				t.Error("exchange did not bind registration, redirect and PKCE")
			}
			if _, err := manager.Account(r.Context(), "oaiapp_fixture"); err != nil {
				t.Error("registration not persisted before exchange")
			}
			claims, _ := json.Marshal(map[string]any{"iss": issuer.URL, "sub": "fixture-user", "aud": "oaiapp_fixture", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "fixture@example.test"})
			input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fixture"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
			sum = sha256.Sum256([]byte(input))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "id_token": input + "." + base64.RawURLEncoding.EncodeToString(sig), "token_type": "Bearer", "expires_in": 3600, "scope": "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"})
		case "/revoke":
			r.ParseForm()
			if r.Form.Get("token") != "fixture-refresh" {
				t.Error("wrong revocation token")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	root := t.TempDir()
	manager, err = siwc.New(siwc.Config{StateDir: filepath.Join(root, "siwc"), Credentials: credentialstore.NewFileStore(filepath.Join(root, "credentials.json")), Issuer: issuer.URL, HTTPClient: issuer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Error("wrong account access token")
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"models":[{"slug":"second","display_name":"Second","visibility":"list","capabilities":{"fixture_capability":true}},{"slug":"first","display_name":"First","visibility":"list"},{"slug":"hidden","visibility":"hidden"}]}`)
		case "/v1/responses":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["store"] != false || body["stream"] != true || body["model"] != "second" {
				t.Errorf("wrong inference policy: %v", body)
			}
			if _, ok := body["temperature"]; ok {
				t.Error("unsupported inference field")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"fixture complete\"}]}]}}\n\n")
		default:
			t.Errorf("unsupported destination %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer inference.Close()
	endpoint, _ := url.Parse(inference.URL)
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	out := &lockedBuffer{}
	srv := New(rt, out)
	srv.chatGPTManager = manager
	srv.chatGPTHTTP = &http.Client{Transport: chatGPTFixtureTransport{endpoint, inference.Client().Transport}}
	call := func(id, method, params string) map[string]any {
		t.Helper()
		if err := srv.handleLine(context.Background(), []byte(`{"id":"`+id+`","method":"`+method+`","params":`+params+`}`)); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	started := call("start", "auth/chatgpt/login/start", `{}`)
	if started["error"] != nil {
		t.Fatal(started["error"])
	}
	attempt := remarshal[AuthChatGPTLoginResult](t, started["result"])
	u, _ := url.Parse(attempt.AuthorizationURL)
	q := u.Query()
	nonce, challenge, redirect = q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")
	callback, _ := url.Parse(redirect)
	callback.RawQuery = url.Values{"state": {q.Get("state")}, "code": {"fixture-code"}, "client_id": {"oaiapp_fixture"}}.Encode()
	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	poll := call("poll", "auth/chatgpt/login/poll", `{"login_id":"`+attempt.LoginID+`"}`)
	result := remarshal[AuthChatGPTLoginResult](t, poll["result"])
	if result.Status != "success" || result.AccountID != "oaiapp_fixture" {
		t.Fatalf("login result: %+v", result)
	}
	if strings.Contains(fmt.Sprint(poll), "fixture-access") || strings.Contains(fmt.Sprint(poll), "fixture-refresh") {
		t.Fatal("credentials crossed account protocol")
	}
	models := call("models", "auth/chatgpt/models", `{"account_id":"oaiapp_fixture"}`)
	if models["error"] != nil {
		t.Fatal(models["error"])
	}
	bad := requestProviderSettings(t, srv, out, "hidden", `{"provider":"chatgpt-personal","type":"chatgpt","create_provider":true,"chatgpt_account_id":"oaiapp_fixture","model":"hidden","keep_selection":true}`)
	if bad["error"] == nil {
		t.Fatal("hidden account model accepted")
	}
	connected := requestProviderSettings(t, srv, out, "connect", `{"provider":"chatgpt-personal","type":"chatgpt","create_provider":true,"chatgpt_account_id":"oaiapp_fixture","model":"second","keep_selection":true}`)
	if connected["error"] != nil {
		t.Fatal(connected["error"])
	}
	cfg := readProviderSettingsConfig(t, rt)
	p := cfg.Providers["chatgpt-personal"]
	if p.ChatGPTAccountID != "oaiapp_fixture" || cfg.DefaultProvider != "fake-provider" {
		t.Fatal("provider connection changed default or lost account")
	}
	srv.threads["fixture-thread"] = newThreadState("fixture-thread", nil, "fake-provider", "fake-model", rt.RootDir, false, time.Now().UTC())
	rejected := requestProviderSettings(t, srv, out, "thread-hidden", `{"thread_id":"fixture-thread","provider":"chatgpt-personal","model":"hidden"}`)
	if rejected["error"] == nil {
		t.Fatal("conversation selected a model absent from account catalog")
	}
	p.ContextWindow = 16000
	cfg.Providers["chatgpt-personal"] = p
	configured, _ := json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, configured, 0600); err != nil {
		t.Fatal(err)
	}
	summaries := srv.providerSummaries()
	summary := providerSummaryNamed(t, summaries, "chatgpt-personal")
	if len(summary.Models) != 2 || summary.Models[0].ID != "second" || summary.AuthStatus != "connected" {
		t.Fatalf("account model order/status: %+v", summary)
	}
	// Reconstruct the server and credential manager: composer choices must not
	// require opening Settings after an app restart.
	restartedManager, err := siwc.New(siwc.Config{StateDir: filepath.Join(root, "siwc"), Credentials: credentialstore.NewFileStore(filepath.Join(root, "credentials.json")), Issuer: issuer.URL, HTTPClient: issuer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer restartedManager.Close()
	restarted := New(rt, &lockedBuffer{})
	restarted.chatGPTManager = restartedManager
	restored := providerSummaryNamed(t, restarted.providerSummaries(), "chatgpt-personal")
	if len(restored.Models) != 2 || restored.Models[0].ID != "second" || restored.Models[1].ID != "first" {
		t.Fatalf("restart lost account catalog: %+v", restored.Models)
	}
	if !strings.Contains(string(restored.Models[0].AccountDescriptor), "fixture_capability") {
		t.Fatal("account descriptor lost across persistence and initialization")
	}
	for _, view := range []ProviderSummary{summary, restored} {
		facts := view.Models[0]
		if !facts.Capabilities.Responses || facts.Capabilities.SystemRole || !facts.Capabilities.DeveloperRole || facts.Capabilities.ProtocolFamily != "openai_responses" || facts.Capabilities.ContextWindow != 16000 {
			t.Fatalf("serialized account route facts missing: %+v", facts)
		}
	}
	client, err := srv.chatGPTClient(p.ChatGPTAccountID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), providers.ChatRequest{Provider: "chatgpt-personal", Model: p.Model, Messages: []providers.ChatMessage{{Role: "user", Content: "fixture"}}, Temperature: .7})
	if err != nil || response.Content != "fixture complete" {
		t.Fatalf("inference: %+v %v", response, err)
	}
	signedOut := call("out", "auth/chatgpt/signout", `{"account_id":"oaiapp_fixture"}`)
	if signedOut["error"] != nil {
		t.Fatal(signedOut["error"])
	}
	if _, err := client.Chat(context.Background(), providers.ChatRequest{Model: p.Model}); err == nil {
		t.Fatal("signed-out credentials still usable")
	}
}
