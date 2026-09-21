package externalengine

import (
	"encoding/json"
	"testing"
)

func TestRPCResponseIDMatches(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: `"wuu-4"`, want: "wuu-4", ok: true},
		{raw: `"wuu-p1"`, want: "wuu-4", ok: false},
		{raw: `"unrelated"`, want: "wuu-4", ok: false},
		{raw: `null`, want: "wuu-4", ok: false},
		{raw: ``, want: "wuu-4", ok: false},
		{raw: `4`, want: "wuu-4", ok: false},
	}
	for _, test := range tests {
		if got := rpcResponseIDMatches(json.RawMessage(test.raw), test.want); got != test.ok {
			t.Errorf("rpcResponseIDMatches(%s, %q) = %v, want %v", test.raw, test.want, got, test.ok)
		}
	}
}
