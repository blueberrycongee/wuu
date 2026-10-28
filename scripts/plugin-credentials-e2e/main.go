// Command plugin-credentials-e2e exercises the host's credential store and HTTPS
// transport against a synthetic keyring backend and a loopback TLS API. It never
// accesses the system keyring, real accounts or an external network endpoint.
// Run: go run ./scripts/plugin-credentials-e2e
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincredentials"
	keyring "github.com/zalando/go-keyring"
)

type syntheticKeyring struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *syntheticKeyring) Get(service, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[service+":"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (s *syntheticKeyring) Set(service, account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[service+":"+account] = value
	return nil
}
func (s *syntheticKeyring) Delete(service, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, service+":"+account)
	return nil
}

type report struct {
	Status      string   `json:"status"`
	Checks      []string `json:"checks"`
	Limitations []string `json:"limitations"`
	Error       string   `json:"error,omitempty"`
}

func run(result *report) error {
	ctx := context.Background()
	temp, err := os.MkdirTemp("", "wuu-plugin-credential-fixture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	// The production keyring's revision/lock protocol still runs, isolated from
	// the OS user's home. Only the backend that stores bytes is synthetic.
	if err := os.Setenv("HOME", temp); err != nil {
		return err
	}
	store := credentialstore.NewKeyringStore("wuu-fixture", &syntheticKeyring{values: map[string]string{}})
	const token = "synthetic-credential-one"
	var expected atomic.Value
	expected.Store(token)
	var calls atomic.Int64
	started := make(chan struct{}, 1)
	cleanup := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+expected.Load().(string) {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/document")
			w.WriteHeader(302)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
		case "/wait":
			started <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-cleanup:
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "7")
			w.Header().Set("Set-Cookie", "session="+token)
			w.Header().Set("X-Request-Id", expected.Load().(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Synthetic document", "echo": r.Header.Get("Authorization"), "method": r.Method})
		}
	}))
	defer func() { close(cleanup); server.Close() }()
	// Use the fixture server's CA trust, not InsecureSkipVerify. Production uses
	// the default system-root HTTPS transport without a test hook or override.
	http.DefaultTransport = server.Client().Transport
	binding, err := plugincredentials.BindingFor(temp, "project-a", "plugin:user:fixture", "token", []string{server.URL})
	if err != nil {
		return err
	}
	check := func(ok bool, name string) error {
		if !ok {
			return errors.New(name)
		}
		result.Checks = append(result.Checks, name)
		return nil
	}
	configured, err := plugincredentials.Configured(ctx, store, binding)
	if err != nil {
		return err
	}
	if err = check(!configured, "missing credential reports unconfigured"); err != nil {
		return err
	}
	if err = plugincredentials.Set(ctx, store, binding, token); err != nil {
		return err
	}
	configured, err = plugincredentials.Configured(ctx, store, binding)
	if err != nil {
		return err
	}
	if err = check(configured, "explicit save reports configured"); err != nil {
		return err
	}
	request := plugincredentials.RequestParams{Credential: "token", URL: server.URL + "/document", Method: "POST", Headers: map[string]string{"Content-Type": "application/json", "Notion-Version": "2026-03-11"}, Body: `{"title":"fixture"}`}
	response, err := plugincredentials.Request(ctx, store, binding, request)
	if err != nil {
		return err
	}
	wire, _ := json.Marshal(response)
	if err = check(response.Status == 200 && strings.Contains(response.Body, "Synthetic document") && !strings.Contains(string(wire), token) && response.Headers["retry-after"] == "7" && response.Headers["set-cookie"] == "", "authenticated API response retains content and retry metadata but removes credentials and cookies"); err != nil {
		return err
	}
	before := calls.Load()
	for _, address := range []string{"http://example.test/document", "https://example.test/document", strings.Replace(server.URL, "https://", "https://user:pass@", 1) + "/document", server.URL + "/document#fragment"} {
		denied := request
		denied.URL = address
		_, err = plugincredentials.Request(ctx, store, binding, denied)
		if err == nil {
			return errors.New("unsafe destination was accepted")
		}
	}
	for _, header := range []string{"Host", "Cookie", "Authorization", "Proxy-Authorization", "Connection", "Content-Length"} {
		denied := request
		denied.Headers = map[string]string{header: "forbidden"}
		_, err = plugincredentials.Request(ctx, store, binding, denied)
		if err == nil {
			return errors.New("host-controlled header was accepted")
		}
	}
	if err = check(calls.Load() == before, "undeclared origins, userinfo, non-HTTPS, fragments and host-controlled headers never reach API"); err != nil {
		return err
	}
	redirect := request
	redirect.URL = server.URL + "/redirect"
	_, err = plugincredentials.Request(ctx, store, binding, redirect)
	if err = check(err != nil && calls.Load() == before+1, "redirect is rejected without following Location"); err != nil {
		return err
	}
	oversized := request
	oversized.Body = strings.Repeat("x", (4<<20)+1)
	_, err = plugincredentials.Request(ctx, store, binding, oversized)
	if err = check(err != nil && calls.Load() == before+1, "oversized request is rejected before network I/O"); err != nil {
		return err
	}
	oversized = request
	oversized.URL = server.URL + "/large"
	_, err = plugincredentials.Request(ctx, store, binding, oversized)
	if err = check(err != nil, "oversized response is rejected"); err != nil {
		return err
	}
	other := binding
	other.Subject = "plugin:user:other"
	configured, err = plugincredentials.Configured(ctx, store, other)
	if err != nil {
		return err
	}
	if err = check(!configured, "another plugin cannot reuse the credential"); err != nil {
		return err
	}
	other = binding
	other.Origins = []string{"https://example.test"}
	configured, err = plugincredentials.Configured(ctx, store, other)
	if err != nil {
		return err
	}
	if err = check(!configured, "changed origin set requires a fresh credential"); err != nil {
		return err
	}
	projectA, _ := plugincredentials.BindingFor(temp, "project-a", "plugin:project:fixture", "token", []string{server.URL})
	projectB, _ := plugincredentials.BindingFor(temp, "project-b", "plugin:project:fixture", "token", []string{server.URL})
	if err = plugincredentials.Set(ctx, store, projectA, token); err != nil {
		return err
	}
	configured, err = plugincredentials.Configured(ctx, store, projectB)
	if err != nil {
		return err
	}
	if err = check(!configured, "same-ID project plugins have separate credential scopes"); err != nil {
		return err
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waiting := request
	waiting.URL = server.URL + "/wait"
	waiting.Method = "GET"
	waiting.Body = ""
	completed := make(chan error, 1)
	go func() { _, err := plugincredentials.Request(cancelCtx, store, binding, waiting); completed <- err }()
	select {
	case <-started:
		cancel()
	case <-time.After(5 * time.Second):
		return errors.New("cancellation fixture never started")
	}
	select {
	case err = <-completed:
		if err == nil {
			return errors.New("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		return errors.New("request ignored cancellation")
	}
	result.Checks = append(result.Checks, "cancellation stops in-flight API request")
	const replacement = "synthetic-credential-two"
	if err = plugincredentials.Set(ctx, store, binding, replacement); err != nil {
		return err
	}
	expected.Store(replacement)
	response, err = plugincredentials.Request(ctx, store, binding, request)
	if err != nil {
		return err
	}
	if err = check(response.Status == 200 && !strings.Contains(response.Body, replacement), "replacement token is used and redacted"); err != nil {
		return err
	}
	if err = plugincredentials.Set(ctx, store, binding, ""); err != nil {
		return err
	}
	configured, err = plugincredentials.Configured(ctx, store, binding)
	if err != nil {
		return err
	}
	if err = check(!configured, "explicit removal reports unconfigured"); err != nil {
		return err
	}
	before = calls.Load()
	_, err = plugincredentials.Request(ctx, store, binding, request)
	if err = check(err != nil && calls.Load() == before, "removed credential cannot authenticate later requests"); err != nil {
		return err
	}
	manifestDir := filepath.Join(temp, "manifest-fixture")
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		return err
	}
	for _, definition := range []map[string]any{
		{"type": "secret", "title": "Token", "default": "must-not-ship", "scope": "user", "apply": "live", "credential_origins": []string{server.URL}},
		{"type": "secret", "title": "Token", "default": "", "scope": "workspace", "apply": "live", "credential_origins": []string{server.URL}},
		{"type": "secret", "title": "Token", "default": "", "scope": "user", "apply": "live", "credential_origins": []string{"http://example.test"}},
		{"type": "secret", "title": "Token", "default": "", "scope": "user", "apply": "live", "credential_origins": []string{"https://example.test/path"}},
		{"type": "secret", "title": "Token", "default": "", "scope": "user", "apply": "live", "credential_origins": []string{"https://user:pass@example.test"}},
	} {
		manifest := map[string]any{"schemaVersion": 1, "id": "credential-fixture", "name": "Fixture", "version": "1.0.0", "contributes": map[string]any{"settings": map[string]any{"token": definition}}}
		data, _ := json.Marshal(manifest)
		path := filepath.Join(manifestDir, "plugin.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		if _, err := plugin.LoadManifest(path, "user"); err == nil {
			return errors.New("unsafe secret manifest was accepted")
		}
	}
	validManifest := map[string]any{"schemaVersion": 1, "id": "credential-fixture", "name": "Fixture", "version": "1.0.0", "contributes": map[string]any{"settings": map[string]any{"token": map[string]any{"type": "secret", "title": "Token", "default": "", "scope": "user", "apply": "live", "credential_origins": []string{server.URL}}}}}
	validData, _ := json.Marshal(validManifest)
	validPath := filepath.Join(manifestDir, "plugin.json")
	if err := os.WriteFile(validPath, validData, 0o600); err != nil {
		return err
	}
	if _, err := plugin.LoadManifest(validPath, "user"); err != nil {
		return fmt.Errorf("valid secret manifest: %w", err)
	}
	result.Checks = append(result.Checks, "secret manifests reject embedded credentials, workspace scope and unsafe audience declarations")
	for _, id := range []string{"chatgpt-workspace", "feishu-workspace", "notion-workspace"} {
		manifest := filepath.Join("internal", "plugin", "bundled", id, "plugin.json")
		if _, err := os.Stat(manifest); errors.Is(err, os.ErrNotExist) {
			continue
		}
		item, err := plugin.LoadManifest(manifest, "bundled")
		if err != nil {
			return fmt.Errorf("%s manifest: %w", id, err)
		}
		if item.EnabledByDefault() {
			return fmt.Errorf("%s unexpectedly enabled by default", id)
		}
		result.Checks = append(result.Checks, id+" manifest validates and stays disabled by default")
	}
	return nil
}

func main() {
	output := os.Getenv("WUU_CREDENTIAL_E2E_OUTPUT")
	if output == "" {
		output = ".tmp/e2e/plugin-credentials"
	}
	_ = os.MkdirAll(output, 0o755)
	result := report{Status: "passed", Checks: []string{}, Limitations: []string{"Synthetic keyring backend and loopback TLS API; no real Keychain/Secret Service, provider account or desktop UI acceptance."}}
	if err := run(&result); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join(output, "results.json"), append(data, '\n'), 0o600)
	fmt.Println(string(data))
	if result.Status != "passed" {
		os.Exit(1)
	}
}
