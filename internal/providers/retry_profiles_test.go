package providers

import "testing"

func TestRetryConfigForUnknownProfileFailsClosed(t *testing.T) {
	if got := RetryConfigForProfile("new-unregistered-workload"); got.MaxRetries != 0 {
		t.Fatalf("unknown workload max retries = %d, want 0", got.MaxRetries)
	}
}
