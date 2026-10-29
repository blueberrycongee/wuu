package pluginhost

const (
	SecurityAuthorizeService = "security.authorize"
	ProcessSandboxService    = "sandbox.process"
	SecurityServiceVersion   = "1.0.0"
	SecurityServiceMajor     = 1

	SecurityAuthorizeMethod = "authorize"
	ProcessSandboxMethod    = "confine"
)

type AuthorizationRequest struct {
	SessionID      string            `json:"session_id,omitempty"`
	ActorID        string            `json:"actor_id,omitempty"`
	CWD            string            `json:"cwd"`
	PermissionMode string            `json:"permission_mode"`
	Tool           AuthorizationTool `json:"tool"`
}

type AuthorizationTool struct {
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Arguments       string `json:"arguments,omitempty"`
	ReadOnly        bool   `json:"read_only"`
	ConcurrencySafe bool   `json:"concurrency_safe"`
	Destructive     bool   `json:"destructive"`
	Risk            string `json:"risk,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type AuthorizationDecision struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// Authorization leases bind one exact request to an authenticated registry caller.
// A selected provider must keep its verdict valid through explicit release;
// absence of these optional methods never permits a snapshot-only fallback.
type AuthorizationAcquireRequest struct {
	ID      string               `json:"id"`
	Request AuthorizationRequest `json:"request"`
}
type AuthorizationLease struct {
	ID       string                `json:"id"`
	Decision AuthorizationDecision `json:"decision"`
}
type AuthorizationReleaseRequest struct {
	ID string `json:"id"`
}

type ProcessSandboxRequest struct {
	Argv   []string             `json:"argv"`
	Policy ProcessSandboxPolicy `json:"policy"`
}

type ProcessSandboxPolicy struct {
	// ProtectedPaths must remain inaccessible regardless of broader writable roots.
	ProtectedPaths []string `json:"protected_paths,omitempty"`
	Mode           string   `json:"mode"`
	WritableRoots  []string `json:"writable_roots,omitempty"`
}

type ProcessSandboxResult struct {
	// ProtectedPathsEnforced attests that every requested protected path is enforced.
	// False or absence cannot satisfy a request containing protected paths.
	ProtectedPathsEnforced  bool     `json:"protected_paths_enforced,omitempty"`
	Argv                    []string `json:"argv"`
	Enforcement             string   `json:"enforcement"`
	DenialSignatures        []string `json:"denial_signatures,omitempty"`
	RunnerFailureSignatures []string `json:"runner_failure_signatures,omitempty"`
}

func SecurityAuthorizeDescriptor() ServiceDescriptor {
	return ServiceDescriptor{Name: SecurityAuthorizeService, Version: SecurityServiceVersion, Methods: []ServiceMethodDescriptor{{
		Name: SecurityAuthorizeMethod, InputSchema: "security.authorize.input.v1", OutputSchema: "security.authorize.output.v1",
	}}}
}

func ProcessSandboxDescriptor() ServiceDescriptor {
	return ServiceDescriptor{Name: ProcessSandboxService, Version: SecurityServiceVersion, Methods: []ServiceMethodDescriptor{{
		Name: ProcessSandboxMethod, InputSchema: "sandbox.process.input.v1", OutputSchema: "sandbox.process.output.v1",
	}}}
}
