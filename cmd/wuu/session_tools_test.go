package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionToolsProxyPreservesRPCAndSkipsNotifications(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/capability" || r.Method != http.MethodPost {
			t.Errorf("unexpected destination: %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		methods = append(methods, request.Method)
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"ok": true}})
	}))
	defer server.Close()
	input := "{\"jsonrpc\":\"2.0\",\"id\":\"init\",\"method\":\"initialize\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":42,\"method\":\"tools/list\"}\n"
	var out bytes.Buffer
	if err := proxySessionTools(context.Background(), server.URL+"/capability", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || len(methods) != 3 {
		t.Fatalf("methods=%v output=%s", methods, out.String())
	}
	for i, want := range []string{`"init"`, "42"} {
		var response struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &response); err != nil {
			t.Fatal(err)
		}
		if string(response.ID) != want {
			t.Fatalf("id=%s want=%s", response.ID, want)
		}
	}
}

func TestSessionToolsProxyRejectsWrongDestinationAndRedactsCapability(t *testing.T) {
	for _, endpoint := range []string{"", "https://127.0.0.1:1234/secret", "http://localhost:1234/secret", "http://example.com/secret", "http://user:pass@127.0.0.1:1234/secret"} {
		if err := proxySessionTools(context.Background(), endpoint, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	endpoint := server.URL + "/secret-capability"
	defer server.Close()
	err := proxySessionTools(context.Background(), endpoint, strings.NewReader(`{"id":1,"method":"tools/list"}`), &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "secret-capability") {
		t.Fatalf("error=%v", err)
	}
	server.Close()
	err = proxySessionTools(context.Background(), endpoint, strings.NewReader(`{"id":1,"method":"tools/list"}`), &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "secret-capability") {
		t.Fatalf("error=%v", err)
	}
}
