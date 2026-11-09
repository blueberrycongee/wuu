package plugin

import (
	"strings"
	"testing"
)

func TestCheckMinimumWuuVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		minimum string
		current string
		wantErr string
	}{
		{name: "empty minimum imposes no constraint", minimum: "", current: "0.15.0"},
		{name: "equal minor satisfies", minimum: "0.15.0", current: "0.15.0"},
		{name: "minor upgrade keeps compatibility", minimum: "0.14.0", current: "0.15.0"},
		{name: "patch above minimum satisfies", minimum: "0.15.1", current: "0.15.2"},
		{name: "current below minimum blocks", minimum: "0.16.0", current: "0.15.0", wantErr: "requires Wuu >= 0.16.0"},
		{name: "patch below minimum blocks", minimum: "0.15.1", current: "0.15.0", wantErr: "requires Wuu >= 0.15.1"},
		{name: "v-prefixed minimum accepted", minimum: "v0.15.0", current: "0.15.0"},
		{name: "v-prefixed current accepted", minimum: "0.15.0", current: "v0.15.0"},
		{name: "dev prerelease satisfies at base version", minimum: "0.15.0", current: "v0.15.0-dev"},
		{name: "dev prerelease below base still blocks", minimum: "0.16.0", current: "v0.15.0-dev", wantErr: "requires Wuu >= 0.16.0"},
		{name: "module pseudo-version satisfies at base", minimum: "0.18.1", current: "v0.18.1-0.20261101050653-675d99db5a4e"},
		{name: "module pseudo-version block explains itself", minimum: "2026.10.19", current: "v0.18.1-0.20261101050653-675d99db5a4e+dirty", wantErr: "pseudo-version"},
		{name: "no-base-tag pseudo-version block explains itself", minimum: "2026.10.19", current: "v0.0.0-20261101050653-675d99db5a4e", wantErr: "pseudo-version"},
		{name: "invalid minimum fails closed", minimum: "soon", current: "0.15.0", wantErr: "not a valid semantic version"},
		{name: "empty current fails closed", minimum: "0.15.0", current: "", wantErr: "host version is unknown"},
		{name: "invalid current fails closed", minimum: "0.15.0", current: "nightly", wantErr: "not a valid semantic version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckMinimumWuuVersion(tc.minimum, tc.current)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckMinimumWuuVersion(%q, %q) = %v, want nil", tc.minimum, tc.current, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckMinimumWuuVersion(%q, %q) = nil, want error containing %q", tc.minimum, tc.current, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckMinimumWuuVersion(%q, %q) = %q, want substring %q", tc.minimum, tc.current, err.Error(), tc.wantErr)
			}
		})
	}
}
