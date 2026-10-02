// Package processsandbox confines subprocess filesystem effects to the same
// preselected workspace boundary used by Wuu's in-process tools.
package processsandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Mode string

const (
	ModeReadOnly       Mode = "read-only"
	ModeWorkspaceWrite Mode = "workspace-write"
)

var ErrUnavailable = errors.New("filesystem process sandbox is unavailable")

var denialSignatures = []string{
	"operation not permitted",
	"permission denied",
	"read-only file system",
}

// Policy is the complete filesystem-effect policy for one subprocess. Network
// access and process visibility are deliberately outside this contract.
type Policy struct {
	Mode          Mode
	WritableRoots []string
}

type Enforcement string

const (
	EnforcementFull    Enforcement = "full"
	EnforcementPartial Enforcement = "partial"
)

type ConfinedCommand struct {
	Argv                    []string
	Enforcement             Enforcement
	DenialSignatures        []string
	RunnerFailureSignatures []string
}

// ResultClassifier carries the diagnostic dialect of the backend that wrapped
// one command. These strings classify results; they are not a security boundary.
type ResultClassifier struct {
	DenialSignatures        []string
	RunnerFailureSignatures []string
}

// Provider is the stable seam for alternate same-world process sandboxes.
// Implementations return the exact argv to execute and must never return the
// original command as a successful no-op.
type Provider interface {
	Confine(ctx context.Context, argv []string, policy Policy) (ConfinedCommand, error)
}

// Apply rewrites cmd so the original command runs under the host filesystem
// sandbox. Implementations must either install confinement or return an error;
// silently running the original command is forbidden.
func Apply(cmd *exec.Cmd, policy Policy) error {
	if cmd == nil {
		return errors.New("filesystem process sandbox requires a command")
	}
	if err := validatePolicy(policy); err != nil {
		return err
	}
	return applyPlatform(cmd, normalizedPolicy(policy))
}

// ApplyWithProvider applies an alternate provider when configured, otherwise
// it uses Wuu's built-in platform backend. Provider failures never fall back to
// unconfined execution, and partial enforcement is rejected by this consumer.
func ApplyWithProvider(ctx context.Context, cmd *exec.Cmd, policy Policy, provider Provider) (ResultClassifier, error) {
	if provider == nil {
		return builtInResultClassifier(), Apply(cmd, policy)
	}
	if cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return ResultClassifier{}, errors.New("filesystem process sandbox requires a complete command argv")
	}
	if err := validatePolicy(policy); err != nil {
		return ResultClassifier{}, err
	}
	argv := append([]string(nil), cmd.Args...)
	argv[0] = cmd.Path
	confined, err := provider.Confine(ctx, argv, normalizedPolicy(policy))
	if err != nil {
		return ResultClassifier{}, fmt.Errorf("custom filesystem process sandbox failed: %w", err)
	}
	if len(confined.Argv) == 0 || strings.TrimSpace(confined.Argv[0]) == "" {
		return ResultClassifier{}, errors.New("custom filesystem process sandbox returned an empty argv")
	}
	if !filepath.IsAbs(confined.Argv[0]) {
		return ResultClassifier{}, errors.New("custom filesystem process sandbox must return an absolute executable path")
	}
	if confined.Enforcement != EnforcementFull {
		return ResultClassifier{}, fmt.Errorf("custom filesystem process sandbox reported %q enforcement; full enforcement is required", confined.Enforcement)
	}
	cmd.Path = confined.Argv[0]
	cmd.Args = append([]string(nil), confined.Argv...)
	return ResultClassifier{
		DenialSignatures:        normalizedSignatures(confined.DenialSignatures),
		RunnerFailureSignatures: normalizedSignatures(confined.RunnerFailureSignatures),
	}, nil
}

// Supported reports whether this build has a filesystem process sandbox
// backend available on this host. Linux requires Landlock ABI 3 or later.
func Supported() bool { return platformSupported() }

// IsDenied classifies a failed confined execution using the diagnostic dialect
// produced by the shipped backends. It is a result fact, not an approval
// trigger.
func IsDenied(exitCode int, output string) bool {
	return builtInResultClassifier().IsDenied(exitCode, output)
}

func (c ResultClassifier) IsDenied(exitCode int, output string) bool {
	if exitCode == 0 {
		return false
	}
	lower := strings.ToLower(output)
	for _, signature := range c.DenialSignatures {
		if strings.Contains(lower, signature) {
			return true
		}
	}
	return false
}

// IsRunnerFailure distinguishes a failed sandbox launcher from a command that
// ran and was denied. The original command did not execute on this path.
func IsRunnerFailure(exitCode int, output string) bool {
	return builtInResultClassifier().IsRunnerFailure(exitCode, output)
}

func (c ResultClassifier) IsRunnerFailure(exitCode int, output string) bool {
	if exitCode == 0 {
		return false
	}
	lower := strings.ToLower(output)
	for _, signature := range c.RunnerFailureSignatures {
		if strings.Contains(lower, signature) {
			return true
		}
	}
	return false
}

func builtInResultClassifier() ResultClassifier {
	return ResultClassifier{
		DenialSignatures:        append([]string(nil), denialSignatures...),
		RunnerFailureSignatures: []string{"sandbox-exec:", "wuu-filesystem-sandbox:"},
	}
}

func normalizedSignatures(signatures []string) []string {
	result := make([]string, 0, len(signatures))
	for _, signature := range signatures {
		if signature = strings.ToLower(strings.TrimSpace(signature)); signature != "" {
			result = append(result, signature)
		}
	}
	return result
}

func validatePolicy(policy Policy) error {
	switch policy.Mode {
	case ModeReadOnly, ModeWorkspaceWrite:
		return nil
	default:
		return fmt.Errorf("unknown filesystem process sandbox mode %q", policy.Mode)
	}
}

func normalizedPolicy(policy Policy) Policy {
	if policy.Mode != ModeWorkspaceWrite {
		policy.WritableRoots = nil
		return policy
	}

	seen := make(map[string]struct{}, len(policy.WritableRoots))
	roots := make([]string, 0, len(policy.WritableRoots))
	for _, root := range policy.WritableRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
			abs = evaluated
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	sort.Strings(roots)
	policy.WritableRoots = roots
	return policy
}

func seatbeltString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + replacer.Replace(value) + `"`
}

func seatbeltProfile(policy Policy) string {
	forms := []string{
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		`(allow file-write* (literal "/dev/null"))`,
		`(allow file-write* (literal "/dev/ptmx"))`,
		`(allow file-write* (regex #"^/dev/ttys[0-9]+$"))`,
	}
	if policy.Mode == ModeWorkspaceWrite {
		for _, root := range policy.WritableRoots {
			forms = append(forms, fmt.Sprintf("(allow file-write* (subpath %s))", seatbeltString(root)))
		}
	}
	return strings.Join(forms, "\n")
}
