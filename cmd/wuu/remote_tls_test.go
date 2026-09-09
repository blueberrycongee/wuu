package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteRelayDeployment(t *testing.T) {
	for _, tc := range []struct {
		origin, address, want string
		tls                   bool
	}{
		{"", "100.64.0.1:8787", "ws://100.64.0.1:8787/v1/connect", false},
		{"", "[::1]:8787", "wss://[::1]:8787/v1/connect", true},
		{"https://wuu.example/", "127.0.0.1:8787", "wss://wuu.example/v1/connect", false},
	} {
		got, err := remoteRelayURL(tc.origin, tc.address, tc.tls)
		if err != nil || got != tc.want {
			t.Fatalf("got %q, %v; want %q", got, err, tc.want)
		}
	}
	for _, origin := range []string{"https://user:secret@example.com", "https://example.com/subpath", "file:///tmp", "https://example.com/?pair=secret"} {
		if _, err := remoteRelayURL(origin, "", false); err == nil {
			t.Fatalf("accepted invalid origin %q", origin)
		}
	}
}

func TestRemoteRelayTLSProtectsWebDelivery(t *testing.T) {
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	defer seed.Close()
	cert := seed.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := remoteRelayTLS(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("workbench")) }))
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.TLS == nil || response.StatusCode != 200 {
		t.Fatal("web delivery was not protected by TLS")
	}
	if _, err := remoteRelayTLS(certFile, ""); err == nil {
		t.Fatal("accepted missing private key")
	}
}
