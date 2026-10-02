package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	worktreepkg "github.com/blueberrycongee/wuu/internal/worktree"
)

const (
	sessionVisibilityUser   = "user"
	sessionVisibilityPlugin = "plugin"
	sessionContextFresh     = "fresh"
	sessionContextFork      = "fork"
	sessionContextSeed      = "seed"
)

// hostSessionCreateParams is the host's creation contract. Callers validate
// their entry-point requirements before creating an ordinary persisted session.
type hostSessionCreateParams struct {
	Speed           string
	RequestID       string
	Name            string
	Visibility      string
	ParentSessionID string
	ContextSource   string
	Workspace       string
	WorkspaceID     string
	WorkspaceRoot   string
	ModelAlias      string
	ProjectRole     string
	Provider        string
	Model           string
	Variant         string
	Effort          string
	PermissionMode  string
	Instructions    string
	ToolPolicy      *hostSessionToolPolicy
	Seed            *session.ContextSeed
	Launch          *hostSessionLaunchParams
	InitialInput    *session.InboxMessage
}

type hostSessionToolPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type hostSessionLaunchParams struct {
	Revision int
	Kind     string
	Intent   string
	Prompt   string
}

func (s *Server) createHostSessionThread(owner, source, id string, params hostSessionCreateParams) (*threadState, error) {
	if s.rt == nil || s.rt.StreamRunner == nil {
		return nil, errors.New("runtime session is required")
	}
	if id == "" {
		id = session.NewID()
	}
	threadCWD := s.rt.RootDir
	managed := session.ManagedMetadata{Owner: owner, Visibility: params.Visibility, ParentID: params.ParentSessionID, ContextSource: params.ContextSource, CreationRequestID: params.RequestID}
	var history []providers.ChatMessage
	var createdWorktreePath string
	cleanupWorktree := false
	fork := session.ForkMetadata{}
	if params.ContextSource == sessionContextFork {
		parent, loadErr := s.loadPersistedThreadSnapshot(params.ParentSessionID)
		if loadErr != nil {
			return nil, loadErr
		}
		threadCWD = firstNonEmpty(parent.metadata.CWD, threadCWD)
		history = cloneForkHistory(parent.history)
		fork = session.ForkMetadata{ForkedFromID: params.ParentSessionID}
	}
	if params.ContextSource == sessionContextSeed && params.ParentSessionID != "" {
		parent, loadErr := s.loadPersistedThreadSnapshot(params.ParentSessionID)
		if loadErr != nil {
			return nil, loadErr
		}
		threadCWD = firstNonEmpty(parent.metadata.CWD, threadCWD)
	}
	selection := s.currentSessionRuntimeSelection()
	if params.ParentSessionID != "" {
		parent, found, err := session.Find(s.rt.SessionDir, params.ParentSessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, session.ErrSessionNotFound
		}
		selection = runtimeSelectionFromSession(parent)
		threadCWD = firstNonEmpty(parent.CWD, threadCWD)
	}
	if params.Provider != "" || params.Model != "" {
		selection.Provider = params.Provider
		selection.Model = params.Model
		selection.Variant = params.Variant
		selection.Effort = params.Effort
		selection.Speed = params.Speed
		if params.PermissionMode != "" {
			selection.PermissionMode = params.PermissionMode
		}
	}
	if params.ModelAlias != "" {
		resolved := s.resolveSubagentModelAlias(params.ModelAlias)
		if resolved.Err != nil {
			return nil, resolved.Err
		}
		if !resolved.Found {
			return nil, fmt.Errorf("unknown model alias %q (available: %s)", params.ModelAlias, strings.Join(resolved.ValidAliases, ", "))
		}
		selection.Provider = resolved.Runtime.Provider
		selection.Model = resolved.Runtime.Model
		selection.Variant = resolved.Runtime.Variant
		selection.Effort = resolved.Runtime.Effort
		selection.Speed = params.Speed
	}
	if params.Speed != "" {
		selection.Speed = params.Speed
	}
	workspaceID := strings.TrimSpace(s.rt.WorkspaceID)
	if params.WorkspaceID != "" || params.WorkspaceRoot != "" {
		root, resolvedID, err := s.resolveSessionWorkspace(params.WorkspaceID, params.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		threadCWD, workspaceID = root, resolvedID
	}
	if len(history) == 0 {
		history = make([]providers.ChatMessage, 0, 1)
	}
	if params.ContextSource != sessionContextSeed {
		if prompt := strings.TrimSpace(s.rt.StreamRunner.SystemPrompt); prompt != "" && len(history) == 0 {
			history = append(history, providers.ChatMessage{Role: "system", Content: sessionSystemPrompt(prompt, params.Instructions)})
		}
	}
	toolPolicyJSON := ""
	if params.ToolPolicy != nil {
		encoded, err := json.Marshal(params.ToolPolicy)
		if err != nil {
			return nil, fmt.Errorf("encode tool_policy: %w", err)
		}
		toolPolicyJSON = string(encoded)
	}

	worktree := session.WorktreeInfo{}
	if params.Workspace == "worktree" {
		baseRepo := threadCWD
		manager, err := s.worktreeManager(baseRepo)
		if err != nil {
			return nil, err
		}
		createdWorktree, err := manager.OpenOrCreate(worktreepkg.OpenOrCreateOptions{SessionID: id, WorkerID: "session"})
		if err != nil {
			return nil, err
		}
		createdWorktreePath = createdWorktree.Path
		cleanupWorktree = true
		threadCWD = createdWorktree.Path
		worktree = session.WorktreeInfo{Path: createdWorktree.Path, BaseHEAD: createdWorktree.HEAD, BaseRepo: baseRepo}
		defer func() {
			if cleanupWorktree {
				_ = manager.Cleanup(createdWorktree)
			}
		}()
	}
	initial := session.Session{
		ID: id, Title: params.Name, CWD: threadCWD,
		WorkspaceID: workspaceID, Source: source,
		Owner: managed.Owner, Visibility: managed.Visibility, ParentID: managed.ParentID,
		ContextSource: managed.ContextSource, CreationRequestID: managed.CreationRequestID,
		ForkedFromID: fork.ForkedFromID,
		WorktreePath: worktree.Path, WorktreeBaseHEAD: worktree.BaseHEAD, WorktreeBaseRepo: worktree.BaseRepo,
		Provider: selection.Provider, Model: selection.Model, Variant: selection.Variant,
		Effort: selection.Effort, Speed: selection.Speed, PermissionMode: selection.PermissionMode, ApproveForMe: selection.ApproveForMe,
		ProjectRole: params.ProjectRole, Instructions: params.Instructions, ToolPolicyJSON: toolPolicyJSON,
	}
	var records []session.HistoryRecord
	artifactStateDir := ""
	if params.ContextSource == sessionContextFork {
		stateDir, stateErr := s.workspaceStateDir()
		if stateErr != nil {
			return nil, stateErr
		}
		artifactStateDir = stateDir
		if err := preserveForkArtifacts(artifactStateDir, params.ParentSessionID, id, history); err != nil {
			_ = os.RemoveAll(statepath.SessionArtifactDir(artifactStateDir, id))
			return nil, err
		}
		records = historyRecordsFromChatMessages(history)
	}
	var seed session.ContextSeed
	var launch session.SessionLaunchRecord
	if params.ContextSource == sessionContextSeed {
		seed = *params.Seed
		launch = session.SessionLaunchRecord{
			RequestID: params.RequestID, Revision: 1, Kind: session.SessionLaunchKindHandoff,
			SourceSession: seed.Source.SessionID, SourceCutoff: seed.Source.ThroughSeq,
			Owner: owner, Producer: seed.Provenance.Producer,
			Runtime: session.SessionRuntimeSelection{Provider: selection.Provider, Model: selection.Model, Variant: selection.Variant, Effort: selection.Effort, Speed: selection.Speed, PermissionMode: selection.PermissionMode},
		}
		if params.Launch != nil {
			if params.Launch.Revision > 0 {
				launch.Revision = params.Launch.Revision
			}
			if params.Launch.Kind != "" {
				launch.Kind = params.Launch.Kind
			}
			launch.Input.Intent = params.Launch.Intent
			launch.Input.Prompt = strings.TrimSpace(params.Launch.Prompt)
		}
		if launch.Input.Prompt == "" {
			launch.Input.Prompt = strings.TrimSpace(launch.Input.Intent)
		}
		if launch.Input.Prompt != "" {
			records = append(records, historyRecordFromPersistedMessage(persistedMessageFromChatMessage(handoffLaunchUserMessage(params.RequestID, launch.Input.Prompt))))
		}
	}
	if err := s.rt.PinExecutionEnvironment(id, params.ParentSessionID); err != nil {
		return nil, err
	}
	var created *session.Session
	var err error
	if params.InitialInput != nil {
		created, err = session.CreateInitializedWithInbox(s.rt.SessionDir, initial, records, seed, launch, *params.InitialInput)
	} else {
		created, err = session.CreateInitializedWithLaunch(s.rt.SessionDir, initial, records, seed, launch)
	}
	if err != nil {
		if artifactStateDir != "" {
			_ = os.RemoveAll(statepath.SessionArtifactDir(artifactStateDir, id))
		}
		return nil, err
	}
	cleanupWorktree = false
	if params.ContextSource == sessionContextSeed {
		if loaded, err := loadChatMessages(s.rt.SessionDir, id); err == nil {
			history = loaded
		}
	}
	th := newThreadState(id, history, s.rt.ProviderName, s.rt.Model, threadCWD, true, time.Now().UTC())
	applyThreadRuntimeSelection(th, selection)
	th.Source = source
	th.Title = params.Name
	th.Owner = owner
	th.Visibility = params.Visibility
	th.Instructions = effectiveSessionInstructions(initial)
	if source == projectSessionSource {
		th.ProjectID = params.ParentSessionID
		th.ProjectRole = projectRoleForSession(initial)
	}
	// Session lineage stays in persisted metadata for management and cancellation.
	// Thread.ParentID identifies internal agent workers, not ordinary sessions
	// created from another session; keep this consistent with applySessionMetadata.
	th.WorktreePath = createdWorktreePath
	if created != nil {
		th.WorktreeBaseHEAD = created.WorktreeBaseHEAD
		th.WorktreeBaseRepo = created.WorktreeBaseRepo
	}
	th.WorkspaceID = workspaceID
	th.WorkspaceKind = workspaceKindForCWD(s.rt.WuuHome, threadCWD)
	if workspaceID != "" {
		th.WorkspaceKind = WorkspaceKindProject
	}
	s.mu.Lock()
	s.threads[id] = th
	s.mu.Unlock()
	if params.ContextSource == sessionContextSeed {
		if err := s.dispatchHandoffLaunchTurn(th, params); err != nil {
			return nil, err
		}
	}
	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	if params.Visibility == sessionVisibilityUser && params.ContextSource != sessionContextSeed {
		if err := s.notifyThreadStarted(thread); err != nil {
			providers.DebugLogf("notify created thread %q: %v", id, err)
		}
	}
	s.pruneCachedThreads(id)
	return th, nil
}

func handoffLaunchUserMessage(requestID, prompt string) providers.ChatMessage {
	return providers.ChatMessage{
		Role:    "user",
		Content: strings.TrimSpace(prompt),
		// Keep the persisted client ID stable across handoff retries and reloads.
		ClientID: "plugin:handoff:" + strings.TrimSpace(requestID),
		Origin:   "user",
		Cause:    session.SessionLaunchKindHandoff,
	}
}

func handoffLaunchTurnAlreadyStarted(th *threadState, clientID string) bool {
	if th == nil {
		return false
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.running {
		return true
	}
	sawUser := false
	for _, existing := range th.History {
		if strings.TrimSpace(existing.ClientID) == clientID {
			sawUser = true
			continue
		}
		if sawUser && strings.EqualFold(strings.TrimSpace(existing.Role), "assistant") && !existing.Hidden {
			return true
		}
	}
	return false
}

func (s *Server) dispatchHandoffLaunchTurn(th *threadState, params hostSessionCreateParams) error {
	if th == nil || params.Launch == nil {
		return nil
	}
	prompt := strings.TrimSpace(params.Launch.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(params.Launch.Intent)
	}
	if prompt == "" {
		return nil
	}
	msg := handoffLaunchUserMessage(params.RequestID, prompt)
	if handoffLaunchTurnAlreadyStarted(th, msg.ClientID) {
		return nil
	}
	permissions, err := s.resolveThreadTurnPermissions(th, nil)
	if err != nil {
		return err
	}
	_, _, err = s.startSubmittedSessionTurn(context.Background(), th, msg, turnRuntimeSnapshot{}.withPermissions(permissions))
	return err
}

// sessionSystemPrompt appends create-time session instructions to the runtime
// prompt. The runtime prompt is configuration refreshed on every turn and
// reload; the instructions are session state that must survive each refresh.
func sessionSystemPrompt(prompt, instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return prompt
	}
	return strings.TrimSpace(prompt) + "\n\n# Session instructions\n\n" + instructions
}
