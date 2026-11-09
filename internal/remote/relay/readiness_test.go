package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
)

func checkProbe(t *testing.T, handler http.Handler, path string, status int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != status {
		t.Fatalf("%s: got %d (%s), want %d", path, response.Code, response.Body.String(), status)
	}
	if path == "/readyz" && response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("readiness must not be cached")
	}
}

func TestReadinessDuringShutdown(t *testing.T) {
	srv := New(Options{})
	defer srv.Close()
	handler := srv.Handler()
	checkProbe(t, handler, "/readyz", http.StatusOK)
	srv.Close()
	checkProbe(t, handler, "/readyz", http.StatusServiceUnavailable)
	checkProbe(t, handler, "/healthz", http.StatusOK)
}

func TestReadinessRequiresAccountDatabase(t *testing.T) {
	store, err := account.Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := New(Options{Accounts: store})
	defer srv.Close()
	handler := srv.Handler()
	checkProbe(t, handler, "/readyz", http.StatusOK)
	store.Close()
	checkProbe(t, handler, "/readyz", http.StatusServiceUnavailable)
	checkProbe(t, handler, "/healthz", http.StatusOK)
}
