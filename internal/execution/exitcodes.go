package execution

import "strings"

// Exit codes are the shared contract between a settled Run and CLI-style
// callers. The app-server records them in Result.ExitCode at settlement
// time, and `wuu exec` maps its process exit status directly onto them, so
// the persisted audit record and the CLI behavior can never drift apart.
const (
	ExitOK                 = 0
	ExitTurnFailed         = 1
	ExitInvalidInput       = 2
	ExitPermissionDenied   = 3
	ExitTimeout            = 4
	ExitInterrupted        = 5
	ExitProtocol           = 6
	ExitProviderModelError = 7
	ExitToolFailed         = 8
	ExitConflict           = 9
)

// ExitCodeForSettlement maps a terminal Run status and its structured error
// to the exit code recorded in Result.ExitCode. Classification trusts the
// structured code/category produced by the app-server's turn-error builder;
// unknown categories fall back to ExitTurnFailed rather than guessing from
// message text.
func ExitCodeForSettlement(status Status, runErr *Error) int {
	switch status {
	case StatusCompleted:
		return ExitOK
	case StatusTimedOut:
		return ExitTimeout
	case StatusInterrupted, StatusCancelled:
		return ExitInterrupted
	}
	if runErr == nil {
		return ExitTurnFailed
	}
	code := strings.ToLower(strings.TrimSpace(runErr.Code))
	category := strings.ToLower(strings.TrimSpace(runErr.Category))
	if code == "permission_denied" || category == "permission_denied" {
		return ExitPermissionDenied
	}
	switch category {
	case "provider", "auth", "network", "invalid_request":
		return ExitProviderModelError
	case "local":
		return ExitToolFailed
	case "cancelled", "canceled":
		return ExitInterrupted
	case "timeout":
		return ExitTimeout
	}
	return ExitTurnFailed
}
