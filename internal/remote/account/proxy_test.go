package account

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestAuthenticationLimitBehindTrustedProxy(t *testing.T) {
	h := NewHTTP(nil, false)
	h.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("172.30.87.2/32")}
	request := func(peer, forwarded string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/account/login", strings.NewReader("invalid"))
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 10; i++ {
		if code := request("172.30.87.2:1234", "198.51.100.1"); code != 400 {
			t.Fatal(code)
		}
	}
	if code := request("172.30.87.2:1234", "198.51.100.1"); code != 429 {
		t.Fatal(code)
	}
	if code := request("172.30.87.2:1234", "198.51.100.2"); code != 400 {
		t.Fatalf("another client blocked: %d", code)
	}
	if code := request("172.30.87.2:1234", "203.0.113.99, 198.51.100.1"); code != 429 {
		t.Fatalf("forged prefix bypassed limit: %d", code)
	}
	for i := 0; i < 11; i++ {
		code := request("198.51.100.3:1234", "203.0.113."+strconv.Itoa(i+1))
		if i == 10 && code != 429 {
			t.Fatalf("untrusted peer bypassed limit: %d", code)
		}
	}
}

func TestClientAddressTrustBoundary(t *testing.T) {
	for _, tc := range []struct{ name, peer, forwarded, want string }{
		{"direct", "198.51.100.1:123", "203.0.113.1", "198.51.100.1"},
		{"missing", "172.30.87.2:123", "", "172.30.87.2"},
		{"malformed", "172.30.87.2:123", "198.51.100.1, invalid", "172.30.87.2"},
		{"ipv6", "172.30.87.2:123", "2001:db8::1", "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHTTP(nil, false)
			h.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("172.30.87.2/32")}
			r := httptest.NewRequest("POST", "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			if got := h.clientAddress(r); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
