package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/channels"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

func currentExecutionPath(env *Env) string {
	if env == nil || strings.TrimSpace(env.AgentPath) == "" {
		return agentthread.RootPath
	}
	return strings.TrimSpace(env.AgentPath)
}

// ReadFileEntry tracks file content known to the session for read deduplication
// and active-file context freshness.
type ReadFileEntry struct {
	MtimeUnix     int64
	MtimeUnixNano int64
	Size          int64
	ContentSHA256 string
	Offset        int
	Limit         int
	WrittenByTool bool
}

// readFileState is a thread-safe record of read_file calls.
type readFileState struct {
	mu    sync.RWMutex
	state map[string]ReadFileEntry
}

func (r *readFileState) record(absPath string, entry ReadFileEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil {
		r.state = make(map[string]ReadFileEntry)
	}
	r.state[absPath] = entry
}

func (r *readFileState) delete(absPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.state, absPath)
}

func (r *readFileState) hasBeenRead(absPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.state[absPath]
	return ok
}

func (r *readFileState) getEntry(absPath string) (ReadFileEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.state[absPath]
	return entry, ok
}

func (r *readFileState) snapshot() map[string]ReadFileEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]ReadFileEntry, len(r.state))
	for path, entry := range r.state {
		out[path] = entry
	}
	return out
}

type testRunEntry struct {
	CommandHash    string
	Revision       string
	Failed         bool
	Command        string
	Scope          string
	Purpose        string
	ExitCode       int
	TimedOut       bool
	DurationMS     int64
	FailureSummary testFailureSummary
	FullLogRef     string
	CreatedAt      time.Time
}

type testRunState struct {
	mu      sync.RWMutex
	records []testRunEntry
}

func (s *testRunState) record(commandHash, revision string, failed bool) {
	if commandHash == "" || revision == "" {
		return
	}
	s.recordEntry(testRunEntry{
		CommandHash: commandHash,
		Revision:    revision,
		Failed:      failed,
		CreatedAt:   time.Now().UTC(),
	})
}

func (s *testRunState) recordEntry(entry testRunEntry) {
	if entry.CommandHash == "" || entry.Revision == "" {
		return
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, entry)
}

func (s *testRunState) consecutiveFailures(commandHash, revision string) int {
	if commandHash == "" || revision == "" {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if record.CommandHash != commandHash || record.Revision != revision {
			continue
		}
		if !record.Failed {
			break
		}
		count++
	}
	return count
}

func (s *testRunState) latestFailure() (testRunEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].Failed {
			return s.records[i], true
		}
	}
	return testRunEntry{}, false
}

type webEvidenceEntry struct {
	ToolName    string
	Evidence    webEvidence
	Error       string
	ResultCount int
	StatusCode  int
	ContentType string
	Size        int
	Truncated   bool
	CreatedAt   time.Time
}

type webEvidenceState struct {
	mu      sync.RWMutex
	entries []webEvidenceEntry
}

func (s *webEvidenceState) record(entry webEvidenceEntry) {
	if entry.Evidence.ID == "" {
		return
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

func (s *webEvidenceState) snapshot() []webEvidenceEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]webEvidenceEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Env holds shared runtime state that individual tools receive at
// construction time. It replaces the old approach of making every
// handler a method on *Toolkit.
type Env struct {
	RootDir     string
	WorkspaceID string
	StateDir    string
	// Unconfined is the explicit escape hatch for lifting path confinement.
	// Default false means file tools stay inside FileScopeRoots/RootDir.
	Unconfined     bool
	PermissionMode string

	// AllowMutations mirrors WorkspaceBoundary.AllowMutations for the active
	// runtime. Set by Toolkit.SetBoundary. False in read-only mode even when
	// Unconfined is also false; tools use it to allow per-path exemptions
	// (such as the agent's own runtime metadata directory) without
	// re-deriving the boundary state.
	AllowMutations bool
	// boundaryConfigured distinguishes an explicitly installed read-only
	// boundary from a lightweight Env literal used by internal callers and
	// tests. The historical zero-value Env behaves as the standard boundary.
	boundaryConfigured bool

	// Optional dependencies — nil means the feature is unavailable.
	// Tools check for nil and return a clear error rather than panic.
	SessionID  string
	SessionDir string // absolute session artifact path for result budgeting
	// SessionsDir overrides the user-level SQLite session store location for
	// tools that read conversations by ID. Empty keeps the canonical WUU_HOME
	// lookup used by ordinary runtimes.
	SessionsDir string
	// ToolResultProjectionMode selects stable tool-result projection behavior
	// ("off"/"shadow"/"active"); empty resolves to active (on by default). The
	// WUU_TOOL_RESULT_PROJECTION environment variable overrides it.
	ToolResultProjectionMode string
	AgentID                  string
	AgentPath                string
	// ToolSearchEnabled means deferred tools are loaded through the
	// model-visible tool_search entrypoint. When false, the active surface is
	// flattened and tool_search guidance must not be emitted.
	ToolSearchEnabled bool
	// NativeDeferredToolDiscovery means the active provider can receive
	// schemas discovered by ordinary tool results without requiring an
	// explicit tool_search call first.
	NativeDeferredToolDiscovery bool
	// ImageInputSupported is the active model's resolved image-input
	// capability. Nil preserves the legacy behavior for standalone toolkits
	// that have not been configured by a runtime.
	ImageInputSupported *bool
	// GitAttributionDisabled is the opt-out bit for WUU's commit trailer.
	// The zero value intentionally means enabled so ordinary toolkits inherit
	// the product default without extra initialization.
	GitAttributionDisabled bool
	// GitWrapperExecutable overrides the WUU executable used by the private
	// bash git launcher. Production resolves os.Executable when this is empty.
	GitWrapperExecutable   string
	gitAttributionShell    gitAttributionShellState
	ProcessMgr             *proc.Manager
	ProcessSandboxProvider processsandbox.Provider
	AgentControl           *agentcontrol.AgentControl
	ChatAgent              *channels.AgentClient
	// BrowserBridge routes the browser tool's actions to the desktop host that
	// owns the hidden WebContentsView + CDP session. Nil means no embedded
	// browser backend is attached (for example the CLI/headless runtime), and
	// the browser tool returns a clear execute-time error rather than panicking.
	BrowserBridge BrowserBridge
	// BrowserTabs persists this thread's tab records (url/title/status/activity)
	// so tabs survive core restarts and can be rebuilt on the first observe.
	// Nil means tab state is not durable in this environment.
	BrowserTabs BrowserTabStore
	// FileScopeRoots, when non-empty, replaces the single-RootDir file
	// boundary with a whitelist: file tools (read/write/edit/glob/grep/…)
	// may only touch paths inside one of these roots — the agent home,
	// the user's registered workspaces, and the system temp directory.
	// Reads are rejected the same as writes. Empty keeps the ordinary
	// workspace-confinement behavior for named-agent runs.
	FileScopeRoots []string
	Skills         []skills.Skill
	// ActiveSurface is the compiled model profile surface currently
	// governing this tool environment. Tools with secondary catalogs
	// such as load_skill use it to avoid exposing instructions that
	// require unavailable capabilities.
	ActiveSurface capability.Surface
	// OnFileChanged is called after write_file/edit_file successfully
	// modifies a file. Enables FileChanged hook dispatch without
	// coupling the tools package to the hooks package.
	OnFileChanged func(absPath string)
	// OnSessionWorkspaceChanged persists and broadcasts an explicit main-agent
	// workspace move before subsequent tools start resolving paths there.
	OnSessionWorkspaceChanged func(root string) error

	readState *readFileState
	testState testRunState
	webState  webEvidenceState

	toolTelemetry toolTelemetry
}

// prepareSessionWorkspaceChange constructs every fallible runtime dependency
// before persistence. Its commit function only applies prepared state and is
// called after the app-server has durably accepted the new workspace.
func (e *Env) prepareSessionWorkspaceChange(root string) (func(), error) {
	root = filepath.Clean(root)
	fileScopeRoots := rebaseRuntimeFileScope(e.FileScopeRoots, e.RootDir, root)
	commitAgentControl := func() {}
	if e.AgentControl != nil {
		var err error
		commitAgentControl, err = e.AgentControl.PrepareWorkspaceRebind(root)
		if err != nil {
			return nil, err
		}
	}
	return func() {
		e.RootDir = root
		e.FileScopeRoots = fileScopeRoots
		commitAgentControl()
	}, nil
}

func rebaseRuntimeFileScope(roots []string, oldRoot, newRoot string) []string {
	if len(roots) == 0 {
		return nil
	}
	out := append([]string(nil), roots...)
	for i, root := range out {
		if sameRuntimeFileScopePath(root, oldRoot) {
			out[i] = newRoot
			return out
		}
	}
	return append([]string{newRoot}, out...)
}

func sameRuntimeFileScopePath(left, right string) bool {
	canonical := func(path string) string {
		path = strings.TrimSpace(path)
		if path == "" {
			return ""
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return filepath.Clean(path)
	}
	return canonical(left) == canonical(right)
}

// BrowserScreenshotResult reports the persisted screenshot geometry and on-disk
// path returned by BrowserBridge.Screenshot. Declared in the tools package so
// tool_browser never imports the appserver wire types; the bridge implementation
// translates the protocol result into this shape.
type BrowserScreenshotResult struct {
	Width  int
	Height int
	Path   string
}

// BrowserBridge is the transport the browser tool uses to reach the desktop
// host process that owns each tab's hidden WebContentsView. Every method blocks
// on a server-initiated request and honors ctx (the turn context); a nil bridge
// on Env means the backend is unavailable and the tool must surface a clear
// error. Call is the general CDP escape hatch; the typed methods cover the
// lifecycle operations the tool needs without hand-rolling CDP for each.
type BrowserBridge interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Screenshot(ctx context.Context, tabID, destPath, format string) (BrowserScreenshotResult, error)
	OpenTab(ctx context.Context, tabID, url string) error
	CloseTab(ctx context.Context, tabID string) error
	SetVisibility(ctx context.Context, tabID string, visible bool) error
	ListTabs(ctx context.Context) ([]string, error)
}

// BrowserTabRecord is the durable per-tab state the tool persists between turns
// and across core restarts. Dead marks a record whose backing WebContentsView
// was lost (core restart) and that must be rebuilt by initial_url on next use.
type BrowserTabRecord struct {
	TabID      string    `json:"tab_id"`
	URL        string    `json:"url,omitempty"`
	Title      string    `json:"title,omitempty"`
	Status     string    `json:"status,omitempty"`
	ActivityID string    `json:"activity_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	Dead       bool      `json:"dead,omitempty"`
}

// BrowserTabStore is the minimal CRUD contract the browser tool needs over the
// thread's tab records. The concrete atomic-file store lands in a later layer;
// this interface freezes the shape the tool codes against. A nil store means
// tab persistence is unavailable and the tool degrades to in-memory addressing.
type BrowserTabStore interface {
	List() ([]BrowserTabRecord, error)
	Get(tabID string) (BrowserTabRecord, bool, error)
	Put(rec BrowserTabRecord) error
	Delete(tabID string) error
}

// RecordRead records a successful read_file invocation.
func (e *Env) RecordRead(absPath string, entry ReadFileEntry) {
	if e.readState == nil {
		e.readState = &readFileState{}
	}
	e.readState.record(absPath, entry)
}

// RecordWriteState records content just written by a mutating file tool without
// claiming that read_file already returned the new full body to the model.
func (e *Env) RecordWriteState(absPath string, content []byte) {
	if e == nil || strings.TrimSpace(absPath) == "" {
		return
	}
	entry := ReadFileEntry{
		Size:          int64(len(content)),
		ContentSHA256: sha256Hex(content),
		WrittenByTool: true,
	}
	if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
		entry.MtimeUnix = info.ModTime().Unix()
		entry.MtimeUnixNano = info.ModTime().UnixNano()
		entry.Size = info.Size()
	}
	e.RecordRead(absPath, entry)
}

func (e *Env) ForgetRead(absPath string) {
	if e == nil || e.readState == nil {
		return
	}
	e.readState.delete(absPath)
}

// HasBeenRead reports whether a file has been read via read_file.
func (e *Env) HasBeenRead(absPath string) bool {
	if e.readState == nil {
		return false
	}
	return e.readState.hasBeenRead(absPath)
}

// GetReadEntry returns the read state for a file, if any.
func (e *Env) GetReadEntry(absPath string) (ReadFileEntry, bool) {
	if e.readState == nil {
		return ReadFileEntry{}, false
	}
	return e.readState.getEntry(absPath)
}

func (e *Env) ReadEntries() map[string]ReadFileEntry {
	if e.readState == nil {
		return nil
	}
	return e.readState.snapshot()
}

func (e *Env) RecordTestRun(commandHash, revision string, failed bool) {
	e.testState.record(commandHash, revision, failed)
}

func (e *Env) RecordTestRunResult(entry testRunEntry) {
	e.testState.recordEntry(entry)
}

func (e *Env) ConsecutiveTestFailures(commandHash, revision string) int {
	return e.testState.consecutiveFailures(commandHash, revision)
}

func (e *Env) LatestTestFailure() (testRunEntry, bool) {
	return e.testState.latestFailure()
}

func (e *Env) RecordWebEvidence(entry webEvidenceEntry) {
	e.webState.record(entry)
}

func (e *Env) WebEvidenceEntries() []webEvidenceEntry {
	return e.webState.snapshot()
}

func (e *Env) BypassToolHardProtections() bool {
	return e != nil && e.Unconfined
}

// RedactToolOutput masks common credential patterns in text returned to the
// model. It stays on in every permission mode, including unconfined: lifting
// the path boundary does not lift secret redaction.
func (e *Env) RedactToolOutput(text string) string {
	return redactToolOutput(text)
}

// ResolvePath resolves a user-supplied relative or absolute path. Path
// confinement is always enforced unless the runtime is explicitly unconfined.
func (e *Env) ResolvePath(input string) (string, error) {
	candidate := strings.TrimSpace(input)
	if candidate == "" {
		candidate = "."
	}

	var abs string
	if filepath.IsAbs(candidate) {
		abs = filepath.Clean(candidate)
	} else {
		abs = filepath.Join(e.RootDir, candidate)
	}

	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if e.BypassToolHardProtections() {
		return resolved, nil
	}
	if len(e.FileScopeRoots) > 0 {
		for _, root := range e.FileScopeRoots {
			if pathWithinRoot(root, resolved) {
				return resolved, nil
			}
		}
		return "", fmt.Errorf("path %q is outside the allowed file scope (agent home directory, registered workspaces, and the system temp directory): 该路径不在工作区内，请用户在侧栏添加该目录为工作区后重试", input)
	}

	evalRoot := e.RootDir
	if ev, err := filepath.EvalSymlinks(e.RootDir); err == nil {
		evalRoot = ev
	}
	evalResolved := resolved
	if ev, err := filepath.EvalSymlinks(resolved); err == nil {
		evalResolved = ev
	}

	if _, ok := relInsideRoot(evalRoot, evalResolved); !ok {
		return "", fmt.Errorf("path %q escapes workspace", input)
	}
	return resolved, nil
}

// relInsideRoot reports whether path stays inside root, returning the
// relative remainder when it does. Both inputs must already be absolute
// and cleaned. Windows filesystems compare names case-insensitively, so
// the containment decision folds case there while the returned remainder
// keeps the caller's casing.
func relInsideRoot(root, path string) (string, bool) {
	cmpRoot, cmpPath := root, path
	if runtime.GOOS == "windows" {
		cmpRoot = strings.ToLower(cmpRoot)
		cmpPath = strings.ToLower(cmpPath)
	}
	rel, err := filepath.Rel(cmpRoot, cmpPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return ".", true
	}
	if cmpRoot == root && cmpPath == path {
		return rel, true
	}
	// Case was folded for the decision; slice the remainder out of the
	// original path so its casing survives. Inside-ness guarantees path
	// extends root.
	remainder := strings.TrimLeft(path[len(root):], string(filepath.Separator))
	if remainder == "" {
		return ".", true
	}
	return remainder, true
}

// ResolveReadPath resolves paths for read-only tools. In addition to ordinary
// workspace files, it allows files under the current session artifact directory
// so compact tool and agent results can point at Wuu-managed artifacts.
func (e *Env) ResolveReadPath(input string) (absPath, displayPath string, managed bool, err error) {
	candidate := strings.TrimSpace(input)
	if candidate == "" {
		candidate = "."
	}

	if expanded, ok, err := e.expandSessionPathRef(candidate); ok || err != nil {
		if err != nil {
			return "", "", false, err
		}
		display := e.normalizeSessionDisplayPath(expanded)
		return expanded, display, true, nil
	}

	if filepath.IsAbs(candidate) && strings.TrimSpace(e.SessionDir) != "" {
		resolved, err := filepath.Abs(filepath.Clean(candidate))
		if err != nil {
			return "", "", false, fmt.Errorf("resolve path: %w", err)
		}
		if pathWithinRoot(e.SessionDir, resolved) {
			display := e.normalizeSessionDisplayPath(resolved)
			return resolved, display, true, nil
		}
	}

	resolved, err := e.ResolvePath(candidate)
	if err != nil {
		return "", "", false, err
	}
	return resolved, e.NormalizeDisplayPath(resolved), false, nil
}

func (e *Env) expandSessionPathRef(input string) (string, bool, error) {
	sessionDir := strings.TrimSpace(e.SessionDir)
	if sessionDir == "" {
		return "", false, nil
	}
	const prefix = "$SESSION_DIR"
	if input != prefix && !strings.HasPrefix(input, prefix+"/") && !strings.HasPrefix(input, prefix+string(filepath.Separator)) {
		return "", false, nil
	}
	suffix := strings.TrimPrefix(input, prefix)
	suffix = strings.TrimPrefix(suffix, "/")
	suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
	resolved, err := filepath.Abs(filepath.Join(sessionDir, filepath.FromSlash(suffix)))
	if err != nil {
		return "", true, fmt.Errorf("resolve path: %w", err)
	}
	if !pathWithinRoot(sessionDir, resolved) {
		return "", true, fmt.Errorf("path %q escapes session artifact directory", input)
	}
	return resolved, true, nil
}

func (e *Env) normalizeSessionDisplayPath(absPath string) string {
	sessionDir := strings.TrimSpace(e.SessionDir)
	if sessionDir == "" {
		return absPath
	}
	rel, err := filepath.Rel(sessionDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return absPath
	}
	if rel == "." {
		return "$SESSION_DIR"
	}
	return "$SESSION_DIR/" + filepath.ToSlash(rel)
}

func pathWithinRoot(root, path string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	evalRoot := absRoot
	if ev, err := filepath.EvalSymlinks(evalRoot); err == nil {
		evalRoot = ev
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	evalPath := absPath
	if ev, err := filepath.EvalSymlinks(evalPath); err == nil {
		evalPath = ev
	} else if rel, ok := relInsideRoot(absRoot, absPath); ok {
		evalPath = filepath.Join(evalRoot, rel)
	}
	_, ok := relInsideRoot(evalRoot, evalPath)
	return ok
}

// NormalizeDisplayPath returns a relative path for display.
func (e *Env) NormalizeDisplayPath(absPath string) string {
	return normalizeDisplayPath(e.RootDir, absPath)
}

// ---------------------------------------------------------------------------
// Worktree execution root (fork-to-worktree step 5)
//
// A thread forked with mode "worktree" persists the checkout path in its
// session metadata; the turn entry injects it into the tool execution
// context via toolctx.WithWorktreePath. The helpers below apply that
// binding STRICTLY AFTER the ordinary sandbox / whitelist checks:
//
//   - the sandbox keeps judging the model-visible workspace paths against
//     RootDir / FileScopeRoots exactly as before (nothing is loosened, and
//     the checkout — which usually lives under the wuu state directory —
//     is never fed into those checks where it would look out-of-bounds);
//   - only a path that already passed is then rebased onto the checkout,
//     so relative-path resolution, bash cwd, and search roots all switch
//     consistently to the isolated copy;
//   - whitelisted roots outside the workspace (the user memory notebook,
//     the system temp dir) are not mirrored by the checkout and pass
//     through unchanged.
//
// When the toolkit is already rooted at the checkout (the normal thread
// runtime path), the binding equals RootDir and every helper is a no-op.
// ---------------------------------------------------------------------------

// worktreeExecRoot returns the ctx-bound worktree checkout when one is
// bound and differs from RootDir. A bound checkout that is missing on disk
// is an error — tools must fail loudly instead of silently falling back to
// the parent repo the user believes is isolated.
func (e *Env) worktreeExecRoot(ctx context.Context) (string, bool, error) {
	path, ok := toolctx.WorktreePath(ctx)
	if !ok {
		return "", false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve worktree checkout %q: %w", path, err)
	}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		abs = ev
	}
	root := strings.TrimSpace(e.RootDir)
	if root != "" {
		evalRoot := root
		if absRoot, err := filepath.Abs(root); err == nil {
			evalRoot = absRoot
		}
		if ev, err := filepath.EvalSymlinks(evalRoot); err == nil {
			evalRoot = ev
		}
		if filepath.Clean(evalRoot) == filepath.Clean(abs) {
			return "", false, nil
		}
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", false, fmt.Errorf("worktree checkout %q is not ready (missing or not a directory); this thread is bound to an isolated worktree and refuses to fall back to the parent repository", path)
	}
	return abs, true, nil
}

// ExecRootDir returns the directory command-style tools (bash cwd, git,
// search roots) execute in: the bound worktree checkout when present,
// otherwise RootDir.
func (e *Env) ExecRootDir(ctx context.Context) (string, error) {
	wt, ok, err := e.worktreeExecRoot(ctx)
	if err != nil {
		return "", err
	}
	if ok {
		return wt, nil
	}
	return e.RootDir, nil
}

// ExecPath maps a sandbox-approved absolute path onto the bound worktree
// checkout. Call it only AFTER ResolvePath / ResolveReadPath and the
// sensitive-path checks have accepted the workspace path. Paths outside
// RootDir (whitelisted roots such as the user memory notebook or the
// system temp dir) are returned unchanged.
func (e *Env) ExecPath(ctx context.Context, resolved string) (string, error) {
	wt, ok, err := e.worktreeExecRoot(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return resolved, nil
	}
	rel, ok := workspaceRelativePath(e.RootDir, resolved)
	if !ok {
		return resolved, nil
	}
	if rel == "." {
		return wt, nil
	}
	return filepath.Join(wt, rel), nil
}

// NormalizeDisplayPathExec keeps the existing display convention for
// execution paths: paths under the bound worktree checkout display
// relative to the checkout (exactly what the same file would display as in
// the parent workspace), everything else falls back to NormalizeDisplayPath.
func (e *Env) NormalizeDisplayPathExec(ctx context.Context, absPath string) string {
	if wt, ok, err := e.worktreeExecRoot(ctx); err == nil && ok && pathWithinRoot(wt, absPath) {
		return normalizeDisplayPath(wt, absPath)
	}
	return e.NormalizeDisplayPath(absPath)
}

// RevisionRoot is the directory workspace_revision telemetry should be
// computed from: the bound worktree checkout when present and ready,
// otherwise RootDir. Telemetry-only; never errors.
func (e *Env) RevisionRoot(ctx context.Context) string {
	if wt, ok, err := e.worktreeExecRoot(ctx); err == nil && ok {
		return wt
	}
	return e.RootDir
}

// workspaceRelativePath returns path relative to root when path resolves
// inside root (symlink-tolerant, missing paths allowed), mirroring the
// pathWithinRoot rules.
func workspaceRelativePath(root, path string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	evalRoot := absRoot
	if ev, err := filepath.EvalSymlinks(evalRoot); err == nil {
		evalRoot = ev
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	evalPath := absPath
	if ev, err := filepath.EvalSymlinks(evalPath); err == nil {
		evalPath = ev
	} else if rel, ok := relInsideRoot(absRoot, absPath); ok {
		evalPath = filepath.Join(evalRoot, rel)
	}
	return relInsideRoot(evalRoot, evalPath)
}

// ProcessManager returns the process manager, creating a default one
// if none was injected.
func (e *Env) ProcessManager() (*proc.Manager, error) {
	if e.ProcessMgr != nil {
		return e.ProcessMgr, nil
	}
	stateDir, err := e.WorkspaceStateDir()
	if err != nil {
		return nil, err
	}
	return proc.NewManager(e.RootDir, statepath.RuntimeDir(stateDir))
}

// WorkspaceStateDir returns the user-level state directory for this workspace.
func (e *Env) WorkspaceStateDir() (string, error) {
	if strings.TrimSpace(e.StateDir) != "" {
		return e.StateDir, nil
	}
	wuuHome, err := statepath.Home("")
	if err != nil {
		return "", err
	}
	stateDir, err := statepath.WorkspaceDir(wuuHome, e.RootDir)
	if err != nil {
		return "", err
	}
	e.StateDir = stateDir
	return stateDir, nil
}

// FindSkill looks up a skill by name, returning it and true if found.
func (e *Env) FindSkill(name string) (skills.Skill, bool) {
	return skills.Find(e.VisibleSkills(), name)
}

// SkillNames returns all available skill names.
func (e *Env) SkillNames() []string {
	visible := e.VisibleSkills()
	out := make([]string, 0, len(visible))
	for _, s := range visible {
		out = append(out, s.Name)
	}
	return out
}

// VisibleSkills returns the skills allowed by the active model surface.
func (e *Env) VisibleSkills() []skills.Skill {
	if e == nil {
		return nil
	}
	return FilterSkillsForSurface(e.Skills, e.ActiveSurface)
}

// ProcessSkillBody processes a skill body with variable substitution. Inline
// shell stays disabled here: loading a skill should expose its instructions and
// resources, not execute code as a side effect.
func (e *Env) ProcessSkillBody(ctx context.Context, skill skills.Skill, arguments string) string {
	return skills.ProcessSkillBody(ctx, skill.Content, skills.ProcessOptions{
		Arguments:        arguments,
		SkillDir:         skill.Dir,
		SessionID:        e.SessionID,
		Shell:            skill.Shell,
		AllowInlineShell: false,
	})
}
