package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Stable tool-result projection.
//
// A large built-in tool result is projected ONCE, at execution time, into a
// bounded, tool-aware, artifact-backed form. Because the projection replaces
// the result before it is stored, the model sees the same bytes on the first
// request and on every later request in the same cache epoch; history is never
// rewritten retroactively. The full result stays recoverable through a session
// artifact reference.
//
// Tool-specific projection is more precise than generic settlement: a
// projector understands its tool's schema and can preserve whole records and
// line boundaries. Generic settlement still archives the complete omitted
// provider text and preserves rich media and structured metadata intact.

const (
	// defaultProjectionTokenBudget is the per-result estimated-token target
	// above which an eligible result is projected. It matches the shell/grep
	// tool caps' order of magnitude while cutting the long tail: on the local
	// corpus a 2,048-token budget touched ~13% of eligible results yet removed
	// ~24% of their tokens. It is an experiment starting point, not a proven
	// optimum, and is deliberately shared across tools to reduce variables.
	defaultProjectionTokenBudget = 2048

	// projectorVersion is recorded in diagnostics so telemetry can attribute a
	// projected result to the exact projector revision that produced it. Bump
	// on any change that alters projected bytes for the same input.
	projectorVersion = "3"
)

// projectionMode selects how the stable projection participates in a run.
//
//   - off:    no projection is computed; the generic result budget applies.
//   - shadow: the projection and its diagnostics are computed and recorded for
//     A/B measurement, but the model still sees the generic-budgeted result.
//   - active: an applicable projection replaces the result the model sees.
//
// Projection is on by default (an unset value resolves to active); operators opt
// out with "off" or into measurement-only "shadow".
type projectionMode string

const (
	projectionModeOff    projectionMode = "off"
	projectionModeShadow projectionMode = "shadow"
	projectionModeActive projectionMode = "active"
)

// projectionModeEnvVar overrides the configured mode for experimentation
// without threading config, mirroring other WUU_* runtime toggles.
const projectionModeEnvVar = "WUU_TOOL_RESULT_PROJECTION"

// resolveProjectionMode resolves the effective mode. An explicit environment
// override wins. Projection is ON by default: an unset value resolves to active,
// so eligible built-in results are bounded unless an operator opts out with
// "off" or into measurement-only "shadow".
func resolveProjectionMode(configured string) projectionMode {
	raw := strings.TrimSpace(configured)
	if v := strings.TrimSpace(os.Getenv(projectionModeEnvVar)); v != "" {
		raw = v
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "disable", "disabled", "false", "0":
		return projectionModeOff
	case "shadow":
		return projectionModeShadow
	default:
		return projectionModeActive
	}
}

// builtInProjectionAllowlist is the exact set of built-in tool names eligible
// for tool-specific projection.
//
// Matched by EXACT name only. MCP tools always carry an "mcp_<server>_" prefix
// (see internal/mcp.mcpToolName) and the registry resolves built-ins before MCP
// on a bare name, so a bare name can never resolve to an MCP tool. An exact
// allowlist therefore reliably excludes MCP, mutation (apply_patch/edit_file),
// and coordination (load_skill/...) results. Never switch this to a
// prefix/substring match: "mcp_x_bash" must not match "bash".
var builtInProjectionAllowlist = map[string]bool{
	"read_file":  true,
	"list_files": true,
	"bash":       true,
	"thread_get": true,
}

// Some tools use projection as their semantic read contract, not only as an
// oversized-result optimization. They must pass through the projector even
// when the raw envelope happens to fit the shared budget.
var builtInAlwaysProject = map[string]bool{
	"thread_get": true,
}

// projectionReason is a stable, low-cardinality label for why a result took a
// given projection path. Recorded in telemetry; safe to compare across runs.
type projectionReason string

const (
	reasonNotEligible projectionReason = "not_eligible" // not on the allowlist, or not text-only
	reasonUnderBudget projectionReason = "under_budget" // eligible but already within budget (identity)
	reasonNoProjector projectionReason = "no_projector" // eligible + over budget but no projector registered
	reasonProjected   projectionReason = "projected"    // tool-specific projection applied
	reasonFailOpen    projectionReason = "fail_open"    // projector/artifact failed; full result preserved
)

// ProjectionDiagnostics captures non-content facts about one projection
// attempt for telemetry and A/B shadow measurement. It never contains tool
// output text — only sizes, counts, hashes, and stable labels.
type ProjectionDiagnostics struct {
	Tool             string           `json:"tool"`
	CallID           string           `json:"call_id,omitempty"`
	BudgetTokens     int              `json:"budget_tokens"`
	Eligible         bool             `json:"eligible"`
	Applied          bool             `json:"applied"`
	Reason           projectionReason `json:"reason"`
	OriginalBytes    int              `json:"original_bytes"`
	OriginalTokens   int              `json:"original_tokens"`
	ProjectedBytes   int              `json:"projected_bytes"`
	ProjectedTokens  int              `json:"projected_tokens"`
	ArtifactRef      string           `json:"artifact_ref,omitempty"`
	ArtifactReused   bool             `json:"artifact_reused,omitempty"`
	ArtifactWritten  bool             `json:"artifact_written,omitempty"`
	ArtifactFailed   bool             `json:"artifact_failed,omitempty"`
	OmittedLines     int              `json:"omitted_lines,omitempty"`
	OmittedRecords   int              `json:"omitted_records,omitempty"`
	OmittedBytes     int              `json:"omitted_bytes,omitempty"`
	OriginalHash     string           `json:"original_hash,omitempty"`
	ProjectionHash   string           `json:"projection_hash,omitempty"`
	ProjectorVersion string           `json:"projector_version"`
}

// projectionOmission reports what a projector dropped, for diagnostics and for
// the model-facing envelope's omitted-evidence hints.
type projectionOmission struct {
	Lines   int
	Records int
	Bytes   int
}

// projectorContext carries everything a per-tool projector needs beyond the raw
// text: the token budget it must fit within and a recoverable artifact
// reference to the full result (already ensured by finalizeBuiltInToolResult).
type projectorContext struct {
	CallID       string
	BudgetTokens int
	ArtifactRef  string
}

// toolProjector reduces a tool's raw envelope text to a bounded form that fits
// budgetTokens while preserving the tool's structure. It returns ok=false to
// decline (fail open to the full result) when the payload is malformed or it
// cannot honor the budget without corrupting evidence. A projector MUST be
// deterministic: identical input yields identical output.
type toolProjector func(rawText string, pc projectorContext) (projectedText string, om projectionOmission, ok bool)

// toolProjectors is the per-tool registry, populated by result_projectors.go.
// A tool with no registered projector falls open to the full result.
var toolProjectors = map[string]toolProjector{}

// toolResultProjectionMode resolves the effective projection mode for this
// environment, honoring the runtime override.
func (e *Env) toolResultProjectionMode() projectionMode {
	if e == nil {
		return resolveProjectionMode("")
	}
	return resolveProjectionMode(e.ToolResultProjectionMode)
}

// estimateResultTokens returns the deterministic estimated-token cost of a
// result's model-visible text using the shared context budget estimator.
func estimateResultTokens(text string) int {
	return contextbudget.EstimateTokens(text)
}

// projectionHash returns a stable content hash of projected text so telemetry
// can verify a projected result never changes across requests in an epoch.
func projectionHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ensureProjectionArtifact guarantees a recoverable reference to the full raw
// result before any projection drops evidence.
//
//   - existingRef non-empty: reuse it (e.g. a shell full_log_ref); no second copy.
//   - otherwise persist rawText to the session artifact directory.
//   - sessionDir empty or persistence fails: return ok=false so the caller fails
//     open and keeps the full result rather than fabricating a dead reference.
func ensureProjectionArtifact(sessionDir, callID, rawText, existingRef string) (ref string, reused bool, ok bool) {
	if strings.TrimSpace(existingRef) != "" {
		return existingRef, true, true
	}
	if strings.TrimSpace(sessionDir) == "" {
		return "", false, false
	}
	path, err := persistResult(sessionDir, callID, rawText)
	if err != nil {
		return "", false, false
	}
	return path, false, true
}

// finalizeBuiltInToolResult produces the stable projection for one built-in
// tool result and the diagnostics describing the decision. The returned result
// is used verbatim for both the history Content and the rich ToolResult, so the
// projection is the single source of truth downstream.
//
// It is fail-safe by construction: a non-eligible tool, an under-budget result,
// a missing projector, a declining projector, or an unrecoverable artifact all
// return the input result unchanged. Only a successful projection with a
// recoverable artifact replaces the result.
func finalizeBuiltInToolResult(sessionDir, toolName, callID string, raw toolresult.Result, budgetTokens int) (toolresult.Result, ProjectionDiagnostics) {
	if budgetTokens <= 0 {
		budgetTokens = defaultProjectionTokenBudget
	}
	rawText := raw.TextProjection()
	diag := ProjectionDiagnostics{
		Tool:             toolName,
		CallID:           callID,
		BudgetTokens:     budgetTokens,
		OriginalBytes:    len(rawText),
		OriginalTokens:   estimateResultTokens(rawText),
		ProjectedBytes:   len(rawText),
		ProjectorVersion: projectorVersion,
		OriginalHash:     projectionHash(rawText),
	}
	diag.ProjectedTokens = diag.OriginalTokens
	diag.ProjectionHash = diag.OriginalHash

	if !builtInProjectionAllowlist[toolName] || !raw.IsTextOnly() {
		diag.Reason = reasonNotEligible
		return raw, diag
	}
	diag.Eligible = true

	if diag.OriginalTokens <= budgetTokens && !builtInAlwaysProject[toolName] {
		diag.Reason = reasonUnderBudget
		return raw, diag
	}

	projector := toolProjectors[toolName]
	if projector == nil {
		diag.Reason = reasonNoProjector
		return raw, diag
	}

	// Guarantee recoverability before dropping any evidence. bash carries its
	// own full_log_ref in the envelope; other tools persist the raw text once.
	ref, reused, artifactOK := ensureProjectionArtifact(sessionDir, callID, rawText, extractProjectionArtifactRef(toolName, rawText))
	if !artifactOK {
		diag.Reason = reasonFailOpen
		diag.ArtifactFailed = true
		return raw, diag
	}

	projected, om, ok := projector(rawText, projectorContext{CallID: callID, BudgetTokens: budgetTokens, ArtifactRef: ref})
	if !ok || projected == "" {
		diag.Reason = reasonFailOpen
		diag.ArtifactReused = reused
		diag.ArtifactWritten = !reused
		diag.ArtifactRef = ref
		return raw, diag
	}

	stable := toolresult.FromText(projected)
	stable.IsError = raw.IsError

	diag.Applied = true
	diag.Reason = reasonProjected
	diag.ProjectedBytes = len(projected)
	diag.ProjectedTokens = estimateResultTokens(projected)
	diag.ProjectionHash = projectionHash(projected)
	diag.ArtifactRef = ref
	diag.ArtifactReused = reused
	diag.ArtifactWritten = !reused
	diag.OmittedLines = om.Lines
	diag.OmittedRecords = om.Records
	diag.OmittedBytes = om.Bytes
	return stable, diag
}

// extractProjectionArtifactRef returns a pre-existing recoverable reference
// embedded in a tool's own envelope (currently only bash's full_log_ref) so the
// finalizer reuses it instead of persisting a duplicate copy. Populated per
// tool in Phase 2; returns "" when the tool has no embedded reference.
func extractProjectionArtifactRef(toolName, rawText string) string {
	if fn := projectionArtifactExtractors[toolName]; fn != nil {
		return fn(rawText)
	}
	return ""
}

// projectionArtifactExtractors maps a tool name to a function that pulls a
// recoverable reference out of its raw envelope. Populated in Phase 2.
var projectionArtifactExtractors = map[string]func(rawText string) string{}
