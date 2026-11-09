package execution

import "testing"

func TestExitCodeForSettlementMapsStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status Status
		err    *Error
		want   int
	}{
		{name: "completed", status: StatusCompleted, want: ExitOK},
		{name: "timed out", status: StatusTimedOut, want: ExitTimeout},
		{name: "timed out wins over error", status: StatusTimedOut, err: &Error{Code: "x"}, want: ExitTimeout},
		{name: "interrupted", status: StatusInterrupted, want: ExitInterrupted},
		{name: "cancelled", status: StatusCancelled, want: ExitInterrupted},
		{name: "failed without error", status: StatusFailed, want: ExitTurnFailed},
		{name: "permission by code", status: StatusFailed, err: &Error{Code: "permission_denied"}, want: ExitPermissionDenied},
		{name: "permission by category", status: StatusFailed, err: &Error{Category: "permission_denied"}, want: ExitPermissionDenied},
		{name: "provider", status: StatusFailed, err: &Error{Category: "provider"}, want: ExitProviderModelError},
		{name: "auth", status: StatusFailed, err: &Error{Category: "auth"}, want: ExitProviderModelError},
		{name: "network", status: StatusFailed, err: &Error{Category: "network"}, want: ExitProviderModelError},
		{name: "invalid request", status: StatusFailed, err: &Error{Category: "invalid_request"}, want: ExitProviderModelError},
		{name: "local tool failure", status: StatusFailed, err: &Error{Category: "local"}, want: ExitToolFailed},
		{name: "cancelled category under failed status", status: StatusFailed, err: &Error{Category: "cancelled"}, want: ExitInterrupted},
		{name: "timeout category under failed status", status: StatusFailed, err: &Error{Category: "timeout"}, want: ExitTimeout},
		{name: "unknown category", status: StatusFailed, err: &Error{Code: "turn_failed", Category: "unknown"}, want: ExitTurnFailed},
		{name: "internal category", status: StatusFailed, err: &Error{Category: "internal"}, want: ExitTurnFailed},
		{name: "mixed case category normalizes", status: StatusFailed, err: &Error{Category: " Provider "}, want: ExitProviderModelError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCodeForSettlement(tc.status, tc.err); got != tc.want {
				t.Fatalf("ExitCodeForSettlement(%s, %+v) = %d, want %d", tc.status, tc.err, got, tc.want)
			}
		})
	}
}
