package tools

import (
	"strings"
	"testing"
)

func TestRedactToolOutput(t *testing.T) {
	input := strings.Join([]string{
		"API_KEY=secret-value-1234567890",
		"TOKEN=purpose-secret-value-1234567890",
		"Authorization: Bearer abcdefghijklmnop",
		"sk-testsecret123456",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturepart",
	}, "\n")

	got := redactToolOutput(input)
	for _, leaked := range []string{"secret-value", "purpose-secret", "abcdefghijklmnop", "sk-testsecret", "eyJhbGci"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, got)
		}
	}
	if strings.Count(got, "[REDACTED]") < 4 {
		t.Fatalf("expected redaction markers, got: %s", got)
	}
}

func TestRedactToolOutput_PEMPrivateKey(t *testing.T) {
	input := strings.Join([]string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMw",
		"-----END OPENSSH PRIVATE KEY-----",
	}, "\n")

	got := redactToolOutput(input)
	if strings.Contains(got, "b3BlbnNzaC1rZXktdjE") {
		t.Fatalf("redacted output leaked PEM body: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected PEM block redaction marker, got: %s", got)
	}
}

func TestRedactToolOutputPreservesDottedIdentifiers(t *testing.T) {
	input := "test_manifest_replay (parityctl.test_parity.IntegrationTests.test_manifest_replay) ... ok\n" +
		"package_name.module_name.function_name\n"
	if got := redactToolOutput(input); got != input {
		t.Fatalf("ordinary test identifiers were redacted: %s", got)
	}
}
