// Package context provides typed model context for the agent loop. Stable
// session context can be rendered into the system prompt, while transient
// runtime information gets assembled into request-only <system-reminder>
// blocks without becoming durable conversation history.
//
// The prompt path stays split:
//   - System prompt = static role, rules, instructions, and session environment
//   - Request context = dynamic runtime state such as plans, active files, and tool summaries
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SystemReminderMessageName marks legacy hidden context injections that bundled
// multiple runtime blocks into one model-visible message. They are
// model-visible for the current request but not durable user conversation
// history.
const SystemReminderMessageName = "wuu_system_reminder"

const systemReminderBlockMessageNamePrefix = "wuu_ctx_"

// DynamicContextProjectionEnvVar controls compact request-only context
// projection. It defaults to active; set it to "off" for the legacy baseline.
const DynamicContextProjectionEnvVar = "WUU_DYNAMIC_CONTEXT_PROJECTION"

// DerivedContextLedgersEnvVar gates the legacy derived context ledgers
// (plan TASK_STATE, ACTIVE_FILES, TOOL_RESULT_SUMMARY, WEB_EVIDENCE). They
// are excluded from the default model projection; set it to "on" for the
// A/B baseline.
const DerivedContextLedgersEnvVar = "WUU_DERIVED_CONTEXT_LEDGERS"

// TaskContractMessageName marks legacy hidden task-contract context derived
// from recent user directives. New requests no longer synthesize this
// context; older persisted histories can still carry this message name and
// must be filtered from provider requests.
const TaskContractMessageName = "wuu_task_contract"

// AgentNotificationMessageName marks internal sub-agent completion handoffs.
// They are model-visible user-role messages, but they are not user intent.
const AgentNotificationMessageName = "wuu_agent_notification"

// ProcessNotificationMessageName marks internal background-process completion
// notifications. They are model-visible user-role messages, but they are not
// user intent.
const ProcessNotificationMessageName = "wuu_process_notification"

// EnvInfo holds a lightweight environment snapshot. CWD and Date are stable
// session prompt inputs; optional git fields are volatile and are not collected
// by default.
type EnvInfo struct {
	CWD       string
	Date      string
	GitBranch string
	GitStatus string // optional short summary, not collected by default
}

type BlockKind string

const (
	BlockSystemContract    BlockKind = "SYSTEM_CONTRACT"
	BlockProjectRules      BlockKind = "PROJECT_RULES"
	BlockTaskState         BlockKind = "TASK_STATE"
	BlockActiveFiles       BlockKind = "ACTIVE_FILES"
	BlockTestFailures      BlockKind = "TEST_FAILURES"
	BlockPlanReminder      BlockKind = "PLAN_REMINDER"
	BlockWebEvidence       BlockKind = "WEB_EVIDENCE"
	BlockMemory            BlockKind = "MEMORY"
	BlockToolPolicy        BlockKind = "TOOL_POLICY"
	BlockAvailableDeferred BlockKind = "AVAILABLE_DEFERRED_TOOLS"
	BlockToolResultSummary BlockKind = "TOOL_RESULT_SUMMARY"
	BlockEnvironment       BlockKind = "ENVIRONMENT"
	BlockAdditionalContext BlockKind = "ADDITIONAL_CONTEXT"
)

type Block struct {
	Kind        BlockKind
	Title       string
	Source      string
	Content     string
	TokenBudget int
}

// Snapshot captures the current environment state. Safe to call from
// any goroutine. Keep this snapshot lightweight: volatile repository state is
// available through tools when the task needs it, but default request context
// should not spend tokens or subprocesses on git status every model round.
func Snapshot(cwd string) EnvInfo {
	return EnvInfo{
		CWD:  cwd,
		Date: time.Now().Format("2006-01-02"),
	}
}

func CompileBlocks(blocks []Block) string {
	return compileBlocks(blocks, false)
}

// CompileRequestBlocks renders request-only blocks without model-visible
// metadata that is already carried by the block and request telemetry.
func CompileRequestBlocks(blocks []Block) string {
	return compileBlocks(blocks, true)
}

func compileBlocks(blocks []Block, compact bool) string {
	var b strings.Builder
	for _, block := range blocks {
		rendered := renderBlock(block, compact)
		if rendered == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(rendered)
	}
	return b.String()
}

// DynamicContextProjectionEnabled returns whether request-only context uses
// compact projection. Empty and unknown values intentionally resolve active so
// the feature remains on by default; "off" is the A/B baseline.
func DynamicContextProjectionEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv(DynamicContextProjectionEnvVar)), "off")
}

// DerivedContextLedgersEnabled returns whether the legacy derived context
// ledgers ride in request-only context. They default to off: ordinary
// requests read the same facts from their causal source (read_file results,
// web tool results, and the tool transcript itself).
// "on" restores the ledger projection as the A/B baseline.
func DerivedContextLedgersEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(DerivedContextLedgersEnvVar)), "on")
}

// derivedLedgerBlockIdentities are the (kind, source) pairs removed from the
// default model projection. Other blocks sharing a kind — session-memory
// recovery, subagent status, frozen worker trees — are not ledgers and stay.
var derivedLedgerBlockIdentities = []struct {
	kind   BlockKind
	source string
}{
	{BlockActiveFiles, "read_file"},
	{BlockToolResultSummary, "tool_telemetry"},
	{BlockWebEvidence, "web_tools"},
}

// IsDerivedLedgerBlockName reports whether a system-reminder block message
// name belongs to a derived ledger excluded from the default projection, so
// retained request state can drop stale copies instead of re-splicing them
// into every later request.
func IsDerivedLedgerBlockName(name string) bool {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, systemReminderBlockMessageNamePrefix) {
		return false
	}
	for _, identity := range derivedLedgerBlockIdentities {
		slug := sanitizeMessageNameSlug(string(identity.kind) + "_" + identity.source)
		if slug == "" {
			continue
		}
		if strings.HasPrefix(name, systemReminderBlockMessageNamePrefix+slug+"_") {
			return true
		}
	}
	return false
}

func FormatSystemReminderBlocks(blocks ...Block) string {
	return "<system-reminder>\n" + CompileBlocks(blocks) + "\n</system-reminder>"
}

// SystemReminderBlockMessageName returns a stable provider-safe message name
// for one hidden runtime context block. Keeping distinct blocks on distinct
// names lets replay skip unchanged blocks instead of re-appending the whole
// context bundle when only one block changes.
func SystemReminderBlockMessageName(block Block, ordinal int) string {
	if ordinal < 0 {
		ordinal = 0
	}
	identity := systemReminderBlockIdentity(block, ordinal)
	slug := sanitizeMessageNameSlug(string(block.Kind) + "_" + block.Source)
	if slug == "" {
		slug = "additional_context"
	}
	sum := sha256.Sum256([]byte(identity))
	hash := hex.EncodeToString(sum[:4])

	const maxNameLen = 64
	const separatorLen = 1
	maxSlugLen := maxNameLen - len(systemReminderBlockMessageNamePrefix) - separatorLen - len(hash)
	if maxSlugLen < 1 {
		maxSlugLen = 1
	}
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
	}
	return systemReminderBlockMessageNamePrefix + slug + "_" + hash
}

func systemReminderBlockIdentity(block Block, ordinal int) string {
	return strings.Join([]string{
		strings.TrimSpace(string(block.Kind)),
		strings.TrimSpace(block.Source),
		strings.TrimSpace(block.Title),
		strconv.Itoa(ordinal),
	}, "\n")
}

func sanitizeMessageNameSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func renderBlock(block Block, compact bool) string {
	content := strings.TrimSpace(block.Content)
	if content == "" {
		return ""
	}
	content = enforceBlockTokenBudget(content, block.TokenBudget)
	kind := block.Kind
	if strings.TrimSpace(string(kind)) == "" {
		kind = BlockAdditionalContext
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", kind)
	if compact {
		b.WriteString(content)
		return strings.TrimRight(b.String(), "\n")
	}
	if title := strings.TrimSpace(block.Title); title != "" {
		fmt.Fprintf(&b, "title: %s\n", title)
	}
	if source := strings.TrimSpace(block.Source); source != "" {
		fmt.Fprintf(&b, "source: %s\n", source)
	}
	if block.TokenBudget > 0 {
		fmt.Fprintf(&b, "token_budget: %d\n", block.TokenBudget)
	}
	if b.Len() > len(kind)+3 {
		b.WriteString("\n")
	}
	b.WriteString(content)
	return strings.TrimRight(b.String(), "\n")
}

func enforceBlockTokenBudget(content string, tokenBudget int) string {
	if tokenBudget <= 0 || estimateBlockContentTokens(content) <= tokenBudget {
		return content
	}

	originalTokens := estimateBlockContentTokens(content)
	note := fmt.Sprintf("\ntruncated: block content exceeded token_budget %d; original approx %d tokens", tokenBudget, originalTokens)
	contentBudget := tokenBudget - estimateBlockContentTokens(note)
	if contentBudget < 1 {
		contentBudget = 1
	}
	return strings.TrimRight(truncateBlockContent(content, contentBudget), "\n") + note
}

func truncateBlockContent(content string, tokenBudget int) string {
	if tokenBudget <= 0 {
		return ""
	}
	if estimateBlockContentTokens(content) <= tokenBudget {
		return content
	}

	lines := strings.SplitAfter(content, "\n")
	var b strings.Builder
	used := 0
	for _, line := range lines {
		lineTokens := estimateBlockContentTokens(line)
		if used+lineTokens > tokenBudget {
			break
		}
		b.WriteString(line)
		used += lineTokens
	}
	if b.Len() > 0 {
		return b.String()
	}
	return truncateBlockContentLine(content, tokenBudget)
}

func truncateBlockContentLine(content string, tokenBudget int) string {
	runes := []rune(content)
	if len(runes) == 0 || tokenBudget <= 0 {
		return ""
	}
	limit := min(len(runes), tokenBudget*4)
	for limit > 1 && estimateBlockContentTokens(string(runes[:limit])) > tokenBudget {
		limit = max(1, limit*9/10)
	}
	return string(runes[:limit])
}

func estimateBlockContentTokens(text string) int {
	if text == "" {
		return 0
	}
	var cjkCount, totalChars int
	for _, r := range text {
		totalChars++
		if isCJK(r) {
			cjkCount++
		}
	}
	nonCJK := totalChars - cjkCount
	return (nonCJK / 4) + (cjkCount / 2) + 1
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x309F) ||
		(r >= 0x30A0 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func EnvironmentBlock(env EnvInfo) Block {
	var b strings.Builder
	b.WriteString("# Environment\n")
	b.WriteString(fmt.Sprintf("- CWD: %s\n", env.CWD))
	b.WriteString(fmt.Sprintf("- Date: %s\n", env.Date))
	if env.GitBranch != "" {
		b.WriteString(fmt.Sprintf("- Git branch: %s\n", env.GitBranch))
	}
	if env.GitStatus != "" {
		b.WriteString(fmt.Sprintf("- Git status: %s\n", env.GitStatus))
	}
	return Block{
		Kind:    BlockEnvironment,
		Title:   "Runtime environment",
		Source:  "runtime.snapshot",
		Content: strings.TrimRight(b.String(), "\n"),
	}
}

// FormatSystemReminder formats environment info and optional extra
// context sections (instruction files, skills) into a <system-reminder> block
// suitable for injection into a user message.
func FormatSystemReminder(env EnvInfo, sections ...string) string {
	blocks := []Block{EnvironmentBlock(env)}
	for i, sec := range sections {
		sec = strings.TrimSpace(sec)
		if sec != "" {
			blocks = append(blocks, Block{
				Kind:    BlockAdditionalContext,
				Title:   fmt.Sprintf("Additional context %d", i+1),
				Source:  "runtime.injector",
				Content: sec,
			})
		}
	}

	return FormatSystemReminderBlocks(blocks...)
}

// IsSystemReminder reports whether the given metadata/content belongs to an
// internal system-reminder block rather than a durable conversation turn.
func IsSystemReminder(name, content string) bool {
	name = strings.TrimSpace(name)
	if name == SystemReminderMessageName || strings.HasPrefix(name, systemReminderBlockMessageNamePrefix) {
		return true
	}
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "<system-reminder>") &&
		strings.HasSuffix(trimmed, "</system-reminder>")
}

// IsAgentNotification reports whether the given metadata/content belongs to an
// internal sub-agent handoff rather than a durable user directive.
func IsAgentNotification(name, content string) bool {
	if strings.TrimSpace(name) == AgentNotificationMessageName {
		return true
	}
	trimmed := strings.TrimSpace(content)
	if isSubagentNotificationContent(trimmed) {
		return true
	}
	var envelope struct {
		Author    string `json:"author"`
		Recipient string `json:"recipient"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return false
	}
	return isSubagentNotificationContent(envelope.Content) ||
		(isAgentPath(envelope.Author) && isAgentPath(envelope.Recipient))
}

// IsProcessNotification reports whether the message is an internal
// background-process completion rather than a durable user directive.
func IsProcessNotification(name, content string) bool {
	if strings.TrimSpace(name) == ProcessNotificationMessageName {
		return true
	}
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "<process_notification>") &&
		strings.HasSuffix(trimmed, "</process_notification>")
}

func isSubagentNotificationContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "<subagent_notification>") &&
		strings.HasSuffix(trimmed, "</subagent_notification>")
}

func isAgentPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/root" || strings.HasPrefix(path, "/root/")
}

// ── git helpers ────────────────────────────────────────────────────

func gitBranch(cwd string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "(detached)", nil
	}
	return branch, nil
}

func gitStatusSummary(cwd string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--short")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "clean", nil
	}
	if len(lines) > 10 {
		return fmt.Sprintf("%d changed files", len(lines)), nil
	}
	// For small diffs, show the summary
	modified, added, deleted, other := 0, 0, 0, 0
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		switch {
		case line[0] == '?' || line[1] == '?':
			added++
		case line[0] == 'M' || line[1] == 'M':
			modified++
		case line[0] == 'D' || line[1] == 'D':
			deleted++
		case line[0] == 'A' || line[1] == 'A':
			added++
		default:
			other++
		}
	}
	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("%d other", other))
	}
	if len(parts) == 0 {
		return "clean", nil
	}
	return strings.Join(parts, ", "), nil
}
