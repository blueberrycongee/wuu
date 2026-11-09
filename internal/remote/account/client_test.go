package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnauthorizedDistinguishesRevocationFromOutage(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"request rejected"}`))
		}))
		err := Request(context.Background(), server.URL, "token", "GET", "/devices", nil, nil)
		server.Close()
		if err == nil || Unauthorized(err) != (status == http.StatusUnauthorized) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
