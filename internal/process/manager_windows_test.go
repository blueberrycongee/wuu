//go:build windows

package process

import (
	"os"
	"strings"
	"testing"
)

func TestVerifyProcessGroupRejectsMismatchedRecords(t *testing.T) {
	if _, err := verifyProcessGroup(1234, 5678); err == nil || !strings.Contains(err.Error(), "refusing to signal") {
		t.Fatalf("want refusal for pgid != pid, got %v", err)
	}
	ok, err := verifyProcessGroup(1234, 1234)
	if err != nil || !ok {
		t.Fatalf("matching pgid rejected: ok=%t err=%v", ok, err)
	}
}

func TestProcessExistsSelf(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatal("current process reported dead")
	}
}
