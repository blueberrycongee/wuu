package sdk

import (
	"strings"
	"testing"
)

func TestAuthenticationHandshakeCannotSelectOwnerOrAudience(t *testing.T) {
	for _, raw := range []string{
		`{"id":1,"method":"initialize"}`,
		`{"id":1,"method":"authenticate","params":{"version":1,"scheme":"Bearer","credential":"opaque","owner":true}}`,
		`{"id":1,"method":"authenticate","params":{"version":1,"scheme":"Bearer","credential":"opaque","audience":"attacker"}}`,
		`{"id":1,"method":"authenticate","params":{"version":2,"scheme":"Bearer","credential":"opaque"}}`,
	} {
		if _, _, _, err := readAuthenticationHandshake(strings.NewReader(raw+"\n"), "receiver"); err == nil {
			t.Fatalf("unsafe handshake accepted: %s", raw)
		}
	}
	input, id, credential, err := readAuthenticationHandshake(strings.NewReader(`{"id":"auth","method":"authenticate","params":{"version":1,"scheme":"Bearer","credential":"opaque"}}`+"\n"+`{"id":2,"method":"initialize"}`+"\n"), "receiver")
	if err != nil || string(id) != `"auth"` || credential.Audience != "receiver" || credential.Token != "opaque" {
		t.Fatalf("handshake: %s %#v %v", id, credential, err)
	}
	next, err := input.ReadString('\n')
	if err != nil || !strings.Contains(next, "initialize") {
		t.Fatalf("buffered next RPC lost: %q %v", next, err)
	}
}
