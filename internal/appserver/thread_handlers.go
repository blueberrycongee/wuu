package appserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// workspaceKindForCWD classifies a thread by where its working directory lives.
// Threads whose cwd sits under <wuuHome>/scratch belong to the scratch
// (no-project) workspace; everything else is treated as a project
// thread. The scratch root mirrors the layout produced by the desktop
// ProjectManager.selectNoProject helper, so threads created from a scratch
// chat round-trip with the correct kind after a desktop restart.
func workspaceKindForCWD(wuuHome, cwd string) WorkspaceKind {
	if wuuHome == "" || cwd == "" {
		return WorkspaceKindProject
	}
	scratchRoot := filepath.Clean(filepath.Join(wuuHome, "scratch"))
	cleanCWD := filepath.Clean(cwd)
	if cleanCWD == scratchRoot {
		return WorkspaceKindScratch
	}
	if strings.HasPrefix(cleanCWD, scratchRoot+string(filepath.Separator)) {
		return WorkspaceKindScratch
	}
	return WorkspaceKindProject
}

func (s *Server) handleThreadStart(req Request) error {
	var params ThreadStartParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := session.NewID()
	persistHistory := !params.Ephemeral
	threadCWD := strings.TrimSpace(params.CWD)
	if threadCWD == "" {
		threadCWD = s.rt.RootDir
	} else {
		canonical, err := canonicalWorkspaceDirectory(threadCWD)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		threadCWD = canonical
	}
	workspaceID := strings.TrimSpace(params.WorkspaceID)
	if workspaceID == "" {
		workspaceID = s.rt.WorkspaceID
	}
	// Engine selection is a thread-creation decision; threads never silently
	// switch engines afterwards. The registry is the source of truth: the
	// built-in wuu engine plus any external engines this build hosts.
	// Empty request means the settings default engine.
	engineID := agentengine.NormalizeEngineID(params.Engine)
	if strings.TrimSpace(params.Engine) == "" && s.rt.DefaultEngine != "" {
		engineID = s.rt.DefaultEngine
	}
	if !s.rt.EngineAvailable(engineID) {
		return s.writeResponse(req.ID, nil, agentengine.CheckEngine(engineID))
	}
	selection := s.currentSessionRuntimeSelection()
	if model := strings.TrimSpace(params.Model); model != "" {
		selection.Model = model
	}
	if effort := strings.TrimSpace(params.Effort); effort != "" {
		selection.Effort = effort
		if engineID == agentengine.EngineWuu {
			// The desktop mirrors one level choice into both columns before
			// the first send, and the runtime update path stores the level
			// in the variant column (see handleThreadModelSelection). Keep
			// that mirror here; clearing variant would make every UI that
			// prefers model_variant read the brand-new thread as "Default"
			// even though the explicit effort was stored and will run.
			selection.Variant = effort
		} else {
			// External engines own the effort field exclusively; their
			// display and execution read the effort column first.
			selection.Variant = ""
		}
	}
	if engineID != agentengine.EngineWuu {
		selection.Provider = string(engineID)
	}
	if provider := strings.TrimSpace(params.Provider); provider != "" {
		selection.Provider = provider
	}
	if mode := strings.TrimSpace(params.PermissionMode); mode != "" {
		selection.PermissionMode = config.NormalizePermissionMode(mode)
	} else if engineID != agentengine.EngineWuu {
		// External agents initially run without interactive approval friction.
		// The persisted generic mode is later mapped to each engine's native
		// full-access setting and remains visible in the composer.
		selection.PermissionMode = config.PermissionModeUnconfined
	}
	if params.Handoff != nil {
		th, err := s.startHandoffThread(selection, params)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		th.mu.Lock()
		thread := th.snapshotLocked()
		th.mu.Unlock()
		if err := s.writeResponse(req.ID, ThreadStartResult{Thread: thread}, nil); err != nil {
			return err
		}
		if err := s.notifyThreadStarted(thread); err != nil {
			return err
		}
		s.pruneCachedThreads(thread.ID)
		return nil
	}
	workspaceKind := workspaceKindForCWD(s.rt.WuuHome, threadCWD)
	threadSource := ""
	if !params.Ephemeral {
		if _, err := session.CreateWithMetadata(s.rt.SessionDir, id, threadCWD); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if err := session.WritePluginGenerationSnapshot(s.rt.SessionDir, id, s.rt.PluginGenerationSnapshot()); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if _, err := session.SetRuntimeSelection(s.rt.SessionDir, id, selection); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if _, err := session.SetEngine(s.rt.SessionDir, id, string(engineID)); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		// Bind project threads to the active workspace's stable id so their
		// state and listing survive the project moving. Scratch threads carry
		// no project id.
		if wsID := strings.TrimSpace(workspaceID); wsID != "" {
			if _, err := session.SetWorkspaceID(s.rt.SessionDir, id, wsID); err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
		}
	} else {
		id = "ephemeral-" + id
	}
	history := make([]providers.ChatMessage, 0, 1)
	if prompt := strings.TrimSpace(s.rt.StreamRunner.SystemPrompt); prompt != "" {
		history = append(history, providers.ChatMessage{Role: "system", Content: prompt})
	}
	th := newThreadState(id, history, s.rt.ProviderName, s.rt.Model, threadCWD, persistHistory, time.Now().UTC())
	th.EngineID = string(engineID)
	th.Source = threadSource
	applyThreadRuntimeSelection(th, selection)
	th.WorkspaceID = workspaceID
	th.WorkspaceKind = workspaceKind
	th.Ephemeral = params.Ephemeral

	s.mu.Lock()
	s.threads[id] = th
	s.mu.Unlock()
	s.startThreadPrewarm(th)

	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	if err := s.writeResponse(req.ID, ThreadStartResult{Thread: thread}, nil); err != nil {
		return err
	}
	if err := s.notifyThreadStarted(thread); err != nil {
		return err
	}
	s.pruneCachedThreads(thread.ID)
	return nil
}

func (s *Server) startHandoffThread(selection session.RuntimeSelection, params ThreadStartParams) (*threadState, error) {
	if params.Handoff == nil {
		return nil, errors.New("handoff params are required")
	}
	if params.Ephemeral {
		return nil, errors.New("handoff cannot create an ephemeral session")
	}
	parentID := strings.TrimSpace(params.Handoff.ParentSessionID)
	if parentID == "" {
		return nil, errors.New("handoff requires a source session")
	}
	if strings.TrimSpace(selection.Provider) == "" || strings.TrimSpace(selection.Model) == "" {
		return nil, errors.New("handoff requires provider and model")
	}
	createParams := pluginhost.SessionCreateParams{
		RequestID:       strings.TrimSpace(params.Handoff.RequestID),
		Visibility:      pluginhost.SessionVisibilityUser,
		ParentSessionID: parentID,
		ContextSource:   pluginhost.SessionContextSourceSeed,
		Workspace:       "shared",
		WorkspaceID:     strings.TrimSpace(params.WorkspaceID),
		Provider:        selection.Provider,
		Model:           selection.Model,
		Variant:         selection.Variant,
		Effort:          selection.Effort,
		PermissionMode:  selection.PermissionMode,
		Seed: &pluginhost.SessionContextSeed{
			Version: session.ContextSeedVersionV1,
			Source:  pluginhost.SessionHistorySnapshot{SessionID: parentID},
			Provenance: pluginhost.SessionSeedProvenance{
				Producer:    "desktop:handoff",
				SourceModel: strings.TrimSpace(s.rt.Model),
			},
		},
		Launch: &pluginhost.SessionLaunchParams{
			Revision: params.Handoff.Revision,
			Kind:     session.SessionLaunchKindHandoff,
			Intent:   strings.TrimSpace(params.Handoff.Intent),
			Prompt:   strings.TrimSpace(params.Handoff.Intent),
		},
	}
	result, err := s.createPluginSession(context.Background(), "handoff", createParams)
	if err != nil {
		return nil, err
	}
	if th := s.thread(result.SessionID); th != nil {
		s.startThreadPrewarm(th)
		return th, nil
	}
	return nil, fmt.Errorf("handoff session %q was not loaded", result.SessionID)
}

const threadPrewarmTimeout = 30 * time.Second

// startThreadPrewarm moves provider auth and transport setup into the gap
// between opening a blank thread and sending its first message. It deliberately
// avoids constructing the thread runtime; that would start tool/worker
// lifecycle state just to warm a connection. A failed transport prewarm leaves
// a short-lived fallback result for the normal request path.
func (s *Server) startThreadPrewarm(th *threadState) {
	if s == nil || th == nil {
		return
	}
	s.startBackground(func() {
		ctx, cancel := context.WithTimeout(context.Background(), threadPrewarmTimeout)
		defer cancel()
		th.mu.Lock()
		selection := runtime.ThreadModelSelection{
			Provider:       th.ModelProvider,
			Model:          th.Model,
			Variant:        th.ModelVariant,
			Effort:         th.ModelEffort,
			PermissionMode: th.PermissionMode,
		}
		engineID := agentengine.NormalizeEngineID(th.EngineID)
		threadID := th.ID
		th.mu.Unlock()
		if err := s.rt.PrewarmThreadProvider(ctx, threadID, engineID, selection); err != nil && !errors.Is(err, context.Canceled) {
			providers.DebugLogf("thread provider prewarm %q: %v", th.ID, err)
		}
	})
}

func (s *Server) handleThreadResume(req Request) error {
	var params ThreadResumeParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.SessionID)
	var err error
	if id == "" {
		id, err = s.mostRecentVisibleThreadID()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if id == "" {
			return s.writeResponse(req.ID, nil, errors.New("no sessions found"))
		}
	}
	if th := s.thread(id); th != nil {
		th.mu.Lock()
		s.restorePluginToolLabels(th.Turns)
		thread := th.resumeSnapshotLocked(params.HistoryPage)
		th.mu.Unlock()
		thread, err = s.threadWithChildAgents(thread)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		return s.writeThreadResumeResult(req, thread, params.ResponseOnly || params.HistoryPage)
	}
	th, err := s.loadPersistedThreadState(id, time.Now().UTC())
	if err != nil {
		if !errors.Is(err, session.ErrSessionNotFound) {
			return s.writeResponse(req.ID, nil, err)
		}
		thread, ok, agentErr := s.agentSessionThread(id)
		if agentErr != nil {
			return s.writeResponse(req.ID, nil, agentErr)
		}
		if !ok {
			return s.writeResponse(req.ID, nil, session.ErrSessionNotFound)
		}
		if params.HistoryPage {
			thread = pageThreadSnapshot(thread)
		}
		return s.writeThreadResumeResult(req, thread, params.ResponseOnly || params.HistoryPage)
	}
	th = s.addLoadedThread(th)
	if th == nil {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}

	th.mu.Lock()
	thread := th.resumeSnapshotLocked(params.HistoryPage)
	th.mu.Unlock()
	thread, err = s.threadWithChildAgents(thread)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeThreadResumeResult(req, thread, params.ResponseOnly || params.HistoryPage)
}

func (s *Server) writeThreadResumeResult(req Request, thread Thread, responseOnly bool) error {
	held, err := s.loadHeldUserTurns(thread.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if th := s.thread(thread.ID); th != nil {
		s.startThreadPrewarm(th)
	}
	heldMessages := heldUserMessageSummaries(thread.ID, held)
	pendingMessages := s.pendingUserMessageSummaries(thread.ID)
	result := ThreadResumeResult{
		Thread: thread, HeldUserMessages: heldMessages, PendingUserMessages: pendingMessages,
	}
	if err := s.writeResponse(req.ID, result, nil); err != nil {
		return err
	}
	if !responseOnly {
		if err := s.writeNotification(NotificationThreadResumed, ThreadResumedNotification{
			Thread: thread, HeldUserMessages: heldMessages, PendingUserMessages: pendingMessages,
		}); err != nil {
			return err
		}
	}
	if s.thread(thread.ID) != nil {
		s.restorePendingProcessCompletionsOnThreadResume(thread.ID)
	}
	s.pruneCachedThreads(thread.ID)
	return nil
}

func (s *Server) ensureThreadLoaded(id string) (*threadState, error) {
	if s == nil || s.closed.Load() {
		return nil, errServerClosed
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("thread_id is required")
	}
	if th := s.thread(id); th != nil {
		return th, nil
	}
	th, err := s.loadPersistedThreadState(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	th = s.addLoadedThread(th)
	if th == nil {
		return nil, errServerClosed
	}
	s.pruneCachedThreads(id)
	return th, nil
}

func (s *Server) addLoadedThread(th *threadState) *threadState {
	if s == nil || th == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil
	}
	if existing := s.threads[th.ID]; existing != nil {
		return existing
	}
	s.threads[th.ID] = th
	return th
}

func (s *Server) loadPersistedThreadState(id string, now time.Time) (*threadState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("thread_id is required")
	}
	loaded, err := s.loadPersistedThreadSnapshot(id)
	if err != nil {
		return nil, err
	}
	threadCWD := firstNonEmpty(loaded.metadata.CWD, s.rt.RootDir)
	th := newThreadState(id, loaded.history, s.rt.ProviderName, s.rt.Model, threadCWD, true, now)
	th.historyHeadSeq = loaded.baselineSeq
	th.Turns = turnsFromPersistedHistory(id, loaded.displayHistory, now, s.resolveParticipantSummary)
	s.restorePluginToolLabels(th.Turns)
	th.Turns = applyTokenUsageMetasToTurns(th.Turns, loaded.tokenMetas)
	th.WorkspaceKind = workspaceKindForCWD(s.rt.WuuHome, threadCWD)
	applySessionMetadata(th, loaded.metadata)
	if th.NamedAgentID != "" && s.channelService != nil {
		binding, err := s.channelService.LookupCollaborationSession(context.Background(), id)
		if err == nil {
			if binding.PrincipalID != th.NamedAgentID {
				return nil, channels.ErrUnauthorized
			}
			th.CollaborationSessionRef = binding.SessionRef
		} else if !errors.Is(err, channels.ErrNotFound) {
			return nil, err
		}
	}
	return th, nil
}

type persistedThreadSnapshot struct {
	metadata         session.Session
	history          []providers.ChatMessage
	repairedHistory  []providers.ChatMessage
	repairNeeded     bool
	baselineSeq      int
	displayHistory   []persistedMessage
	rawHistory       []persistedMessage
	tokenMetas       []persistedMessage
	pluginGeneration session.PluginGenerationSnapshot
}

// loadPersistedThreadSnapshot is deliberately read-only. Loading or resuming a
// thread may race with the app-server that currently owns its execution lease,
// so even a deterministic history repair must not be written back here. The
// lease admission paths persist repairNeeded under exclusive ownership.
func (s *Server) loadPersistedThreadSnapshot(id string) (persistedThreadSnapshot, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return persistedThreadSnapshot{}, errors.New("thread_id is required")
	}
	metadata, ok, err := session.Find(s.rt.SessionDir, id)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	if !ok {
		return persistedThreadSnapshot{}, session.ErrSessionNotFound
	}
	pluginGeneration, _, err := session.ReadPluginGenerationSnapshot(s.rt.SessionDir, id)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	providerRecords, historyHeadSeq, err := loadProviderPersistedMessages(s.rt.SessionDir, id, true)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	providerHistory := make([]persistedMessage, 0, len(providerRecords))
	for _, rec := range providerRecords {
		if strings.EqualFold(strings.TrimSpace(rec.Role), "meta") {
			continue
		}
		providerHistory = append(providerHistory, rec)
	}
	history := chatMessagesFromPersistedMessages(providerHistory)
	repaired, err := providers.RepairAndValidateToolCallHistory(history)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	displayHistory, err := loadPersistedMessages(s.rt.SessionDir, id, true)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	rawHistory := append([]persistedMessage(nil), displayHistory...)
	displayHistory = displayHistoryAcrossProviderCheckpoint(displayHistory, providerRecords)
	tokenMetas, err := loadMetaMessages(s.rt.SessionDir, id)
	if err != nil {
		return persistedThreadSnapshot{}, err
	}
	if strings.HasPrefix(metadata.Source, namedAgentSessionSource) {
		// Legacy wakes were hidden even though they establish a durable turn
		// boundary. Project them for stable IDs without changing model history.
		for i := range displayHistory {
			if displayHistory[i].Phase == "channel_wake" {
				displayHistory[i].Hidden = false
			}
		}
	}
	loaded := persistedThreadSnapshot{
		metadata:         metadata,
		repairedHistory:  repaired,
		repairNeeded:     !reflect.DeepEqual(repaired, history),
		baselineSeq:      historyHeadSeq,
		displayHistory:   displayHistory,
		rawHistory:       rawHistory,
		tokenMetas:       tokenMetas,
		pluginGeneration: pluginGeneration,
	}
	systemPrompt := s.rt.StreamRunner.SystemPrompt
	// The active runtime prompt is configuration, not conversation data. Use it
	// in memory without rewriting the thread during a read-only load.
	if strings.HasPrefix(metadata.Source, namedAgentSessionSource) {
		loaded.history = repaired
	} else {
		loaded.history = replaceBaseSystemPrompt(repaired, systemPrompt)
	}
	return loaded, nil
}

type forkSourceThread struct {
	history        []providers.ChatMessage
	displayHistory []providers.ChatMessage
	rawHistory     []persistedMessage
	modelProvider  string
	model          string
	modelVariant   string
	modelEffort    string
	permissionMode string
	cwd            string
	thread         Thread
}

func (s *Server) handleThreadFork(req Request) error {
	var params ThreadForkParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sourceID := strings.TrimSpace(params.ThreadID)
	if sourceID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	mode := strings.TrimSpace(params.Mode)
	if mode == "" {
		mode = "local"
	}
	if mode != "local" && mode != "worktree" {
		return s.writeResponse(req.ID, nil, fmt.Errorf("unsupported fork mode %q", mode))
	}

	now := time.Now().UTC()
	source, err := s.loadForkSourceThread(sourceID, now)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	target := ThreadItem{}
	if params.Target != nil {
		target.Seq = params.Target.Seq
		target.Type = params.Target.Type
		target.SourceID = strings.TrimSpace(params.Target.SourceID)
	}
	// The provider checkpoint is only the active model context. Fork from the
	// durable transcript first so earlier conversation is not silently lost.
	var history []providers.ChatMessage
	err = errForkTargetNotFound
	if len(source.rawHistory) > 0 {
		history, err = forkPersistedHistoryAtTarget(source.rawHistory, source.thread.ID, source.thread.Turns, params.TurnID, params.ItemID, target)
	}
	if errors.Is(err, errForkTargetNotFound) && len(source.displayHistory) > 0 {
		history, err = forkHistoryAtTargetWithIdentity(source.displayHistory, source.thread.ID, source.thread.Turns, params.TurnID, params.ItemID, target)
	}
	if errors.Is(err, errForkTargetNotFound) {
		history, err = forkHistoryAtTargetWithIdentity(source.history, source.thread.ID, source.thread.Turns, params.TurnID, params.ItemID, target)
	}
	if errors.Is(err, errForkTargetNotFound) {
		if liveTurn, ok := turnByID(source.thread.Turns, strings.TrimSpace(params.TurnID)); ok {
			base := source.history
			if len(source.rawHistory) > 0 {
				// Streaming tools may already have been archived. Materialize
				// the live turn only after its admitted prompt, not after those
				// tools, otherwise their call/result pairs would be duplicated.
				for _, item := range liveTurn.Items {
					if item.Type != ThreadItemUserMessage {
						continue
					}
					if prefix, prefixErr := forkPersistedHistoryAtTarget(source.rawHistory, source.thread.ID, source.thread.Turns, liveTurn.ID, item.ID, item); prefixErr == nil {
						base = prefix
					}
					break
				}
			}
			history, err = forkLiveAnswerHistory(base, liveTurn, params.ItemID)
		}
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}

	id := session.NewID()
	fork := session.ForkMetadata{
		ForkedFromID:     source.thread.ID,
		ForkedFromTurnID: strings.TrimSpace(params.TurnID),
		ForkedFromItemID: strings.TrimSpace(params.ItemID),
	}
	forkCWD := source.cwd
	var createdWorktree *worktree.Worktree
	var forkWorktree session.WorktreeInfo
	if mode == "worktree" {
		manager, mgrErr := s.worktreeManager(source.cwd)
		if mgrErr != nil {
			return s.writeResponse(req.ID, nil, mgrErr)
		}
		createdWorktree, err = manager.Create(id, "fork", "")
		if err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("worktree create: %w", err))
		}
		forkCWD = createdWorktree.Path
		forkWorktree = session.WorktreeInfo{
			Path:     createdWorktree.Path,
			BaseHEAD: createdWorktree.HEAD,
			BaseRepo: firstNonEmpty(worktreeBaseRepo(source.thread.Worktree), source.cwd),
		}
	}
	cleanupWorktree := func() {
		if createdWorktree == nil {
			return
		}
		if manager, mgrErr := s.worktreeManager(source.cwd); mgrErr == nil {
			_ = manager.Cleanup(createdWorktree)
		}
	}

	var sess *session.Session
	if mode == "worktree" {
		sess, err = session.CreateWithWorktree(s.rt.SessionDir, id, forkCWD, fork, forkWorktree)
	} else {
		sess, err = session.CreateForkWithMetadata(s.rt.SessionDir, id, forkCWD, fork)
	}
	if err != nil {
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}
	updatedSession, err := session.SetRuntimeSelection(s.rt.SessionDir, sess.ID, session.RuntimeSelection{
		Provider:       source.modelProvider,
		Model:          source.model,
		Variant:        source.modelVariant,
		Effort:         source.modelEffort,
		PermissionMode: source.permissionMode,
	})
	if err != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}
	// A fork inherits its source's engine binding; threads never silently
	// switch engines.
	if _, err := session.SetEngine(s.rt.SessionDir, sess.ID, source.thread.EngineID); err != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}
	sess = &updatedSession
	// A fork belongs to the same workspace as its source, so it inherits the
	// active workspace's stable id (empty for scratch/DM/group runtimes).
	if wsID := strings.TrimSpace(s.rt.WorkspaceID); wsID != "" {
		if _, err := session.SetWorkspaceID(s.rt.SessionDir, id, wsID); err != nil {
			cleanupWorktree()
			return s.writeResponse(req.ID, nil, err)
		}
	}
	stateDir, stateDirErr := s.workspaceStateDir()
	if stateDirErr != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, stateDirErr)
	}
	if err := preserveForkArtifacts(stateDir, source.thread.ID, sess.ID, history); err != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		_ = os.RemoveAll(statepath.SessionArtifactDir(stateDir, sess.ID))
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}
	if err := rewriteChatHistory(s.rt.SessionDir, sess.ID, history); err != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		_ = os.RemoveAll(statepath.SessionArtifactDir(stateDir, sess.ID))
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}
	if err := session.UpdateIndex(s.rt.SessionDir, sess.ID, persistableMessageCount(history), threadPreview(history)); err != nil {
		_, _ = session.Delete(s.rt.SessionDir, sess.ID)
		_ = os.RemoveAll(statepath.SessionArtifactDir(stateDir, sess.ID))
		cleanupWorktree()
		return s.writeResponse(req.ID, nil, err)
	}

	th := newThreadState(sess.ID, history, source.modelProvider, source.model, forkCWD, true, now)
	applySessionMetadata(th, *sess)
	s.mu.Lock()
	s.threads[th.ID] = th
	s.mu.Unlock()

	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	thread = s.threadWithWorktreeStatus(thread)
	if err := s.writeResponse(req.ID, ThreadForkResult{Thread: thread, Worktree: thread.Worktree}, nil); err != nil {
		return err
	}
	if err := s.notifyThreadStarted(thread); err != nil {
		return err
	}
	s.pruneCachedThreads(thread.ID, source.thread.ID)
	return nil
}

func (s *Server) handleThreadEditMessage(req Request) error {
	var params ThreadEditMessageParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	th, err := s.ensureThreadLoaded(threadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.hasQueuedUserTurns(threadID) {
		return s.writeResponse(req.ID, nil, errors.New("queued messages must be sent or removed before editing history"))
	}

	th.mu.Lock()
	if th.ReadOnly {
		th.mu.Unlock()
		return s.writeResponse(req.ID, nil, errors.New("thread is read-only"))
	}
	if th.running {
		th.mu.Unlock()
		return s.writeResponse(req.ID, nil, errors.New("thread is running"))
	}
	var mutationLease *session.ThreadExecutionLease
	if th.PersistHistory {
		mutationLease, err = s.tryAcquireThreadMutationLease(th.ID)
		if err != nil {
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, err)
		}
		if err := s.refreshDurableThreadHistoryLocked(th); err != nil {
			releaseThreadMutationLease(th.ID, mutationLease)
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, err)
		}
		if th.ReadOnly {
			releaseThreadMutationLease(th.ID, mutationLease)
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, errors.New("thread is read-only"))
		}
	}
	current := th.snapshotLocked()
	historyBaselineSeq := th.historyHeadSeq
	nextHistory, draft, err := editHistoryBeforeUserMessage(th.History, th.ID, current.Turns, params.TurnID, params.ItemID)
	if err != nil {
		releaseThreadMutationLease(th.ID, mutationLease)
		th.mu.Unlock()
		return s.writeResponse(req.ID, nil, err)
	}
	committedHistory := nextHistory
	if th.PersistHistory {
		if err := rewriteChatHistoryAtBaseline(s.rt.SessionDir, th.ID, nextHistory, historyBaselineSeq); err != nil {
			releaseThreadMutationLease(th.ID, mutationLease)
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, err)
		}
		committedRecords, committedHeadSeq, loadErr := loadProviderPersistedMessages(s.rt.SessionDir, th.ID, false)
		if loadErr != nil {
			releaseThreadMutationLease(th.ID, mutationLease)
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, loadErr)
		}
		committedHistory = chatMessagesFromPersistedMessages(committedRecords)
		th.historyHeadSeq = committedHeadSeq
		if err := session.UpdateIndex(s.rt.SessionDir, th.ID, persistableMessageCount(committedHistory), threadPreview(committedHistory)); err != nil {
			releaseThreadMutationLease(th.ID, mutationLease)
			th.mu.Unlock()
			return s.writeResponse(req.ID, nil, err)
		}
	}
	now := time.Now().UTC()
	th.History = committedHistory
	th.Turns = turnsFromHistory(th.ID, committedHistory, now)
	th.UpdatedAt = now
	th.currentTurn = ""
	th.currentExecutionRunID = ""
	th.currentTurnResumed = false
	th.nextItemIndex = 0
	th.activeAgentItemID = ""
	th.activeReasoningItemID = ""
	th.toolItems = make(map[string]string)
	thread := th.snapshotLocked()
	releaseThreadMutationLease(th.ID, mutationLease)
	th.mu.Unlock()

	if err := s.writeResponse(req.ID, ThreadEditMessageResult{Thread: thread, Draft: draft}, nil); err != nil {
		return err
	}
	return s.notifyThreadUpdated(thread)
}

func (s *Server) loadForkSourceThread(id string, now time.Time) (forkSourceThread, error) {
	if th := s.thread(id); th != nil {
		th.mu.Lock()
		s.restorePluginToolLabels(th.Turns)
		source := forkSourceThread{
			history:        cloneHistory(th.History),
			displayHistory: cloneHistory(th.History),
			modelProvider:  th.ModelProvider,
			model:          th.Model,
			modelVariant:   th.ModelVariant,
			modelEffort:    th.ModelEffort,
			permissionMode: th.PermissionMode,
			cwd:            th.CWD,
			thread:         th.snapshotLocked(),
		}
		persisted := th.PersistHistory
		th.mu.Unlock()
		if persisted {
			loaded, err := s.loadPersistedThreadSnapshot(id)
			if err != nil {
				return forkSourceThread{}, err
			}
			source.displayHistory = chatMessagesFromPersistedMessages(loaded.displayHistory)
			source.rawHistory = loaded.rawHistory
		}
		return source, nil
	}

	if _, ok, err := session.Find(s.rt.SessionDir, id); err != nil {
		return forkSourceThread{}, err
	} else if !ok {
		return forkSourceThread{}, session.ErrSessionNotFound
	}
	history, err := loadChatMessages(s.rt.SessionDir, id)
	if err != nil {
		return forkSourceThread{}, err
	}
	repaired, err := providers.RepairAndValidateToolCallHistory(history)
	if err != nil {
		return forkSourceThread{}, err
	}
	// Forking reads the source snapshot but never mutates it. Any repair is
	// persisted only by an execution/mutation admission that owns the source.
	history = replaceBaseSystemPrompt(repaired, s.rt.StreamRunner.SystemPrompt)
	th := newThreadState(id, history, s.rt.ProviderName, s.rt.Model, s.rt.RootDir, true, now)
	displayHistory := cloneHistory(history)
	var rawHistory []persistedMessage
	if loaded, loadErr := s.loadPersistedThreadSnapshot(id); loadErr != nil {
		return forkSourceThread{}, loadErr
	} else {
		displayHistory = chatMessagesFromPersistedMessages(loaded.displayHistory)
		rawHistory = loaded.rawHistory
		th.Turns = turnsFromPersistedHistory(id, loaded.displayHistory, now, s.resolveParticipantSummary)
		s.restorePluginToolLabels(th.Turns)
	}
	if metas, err := loadMetaMessages(s.rt.SessionDir, id); err != nil {
		return forkSourceThread{}, err
	} else {
		th.Turns = applyTokenUsageMetasToTurns(th.Turns, metas)
	}
	if metadata, ok, err := session.Find(s.rt.SessionDir, id); err != nil {
		return forkSourceThread{}, err
	} else if ok {
		applySessionMetadata(th, metadata)
	}
	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	return forkSourceThread{
		history:        cloneHistory(th.History),
		displayHistory: displayHistory,
		rawHistory:     rawHistory,
		modelProvider:  th.ModelProvider,
		model:          th.Model,
		modelVariant:   th.ModelVariant,
		modelEffort:    th.ModelEffort,
		permissionMode: th.PermissionMode,
		cwd:            th.CWD,
		thread:         thread,
	}, nil
}

func isNamedAgentSessionSource(source string) bool {
	return strings.HasPrefix(strings.TrimSpace(source), namedAgentSessionSource)
}

func (s *Server) handleThreadList(req Request) error {
	var params ThreadListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	targetCWD := firstNonEmpty(params.CWD, s.rt.RootDir)
	// Trust the active workspace's stable id only when the caller didn't pin a
	// specific cwd; an explicit params.CWD falls back to pure cwd matching.
	targetWorkspaceID := ""
	if strings.TrimSpace(params.CWD) == "" {
		targetWorkspaceID = s.rt.WorkspaceID
	}
	sessions, err := session.ListForCWD(s.rt.SessionDir, targetCWD, targetWorkspaceID, 0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// Agent worker sessions are persisted alongside regular conversations, but
	// their worker-only parent/path metadata lives in the agent thread store
	// rather than the session index. Build this set once before constructing
	// entries so a restarted server cannot leak workers into the root rail
	// without turning a list request into an N² metadata scan.
	agentThreadIDs := make(map[string]struct{})
	rootIDs, err := s.rootThreadIDsForSessions(sessions)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	for _, rootID := range rootIDs {
		store := s.agentThreadStore(rootID)
		if store == nil {
			continue
		}
		threads, err := store.ListThreads()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		for _, meta := range threads {
			if meta.Source.Kind == agentthread.SourceThreadSpawn {
				agentThreadIDs[meta.ID] = struct{}{}
			}
		}
	}

	entries := make(map[string]threadListEntry, len(sessions))
	for _, sess := range sessions {
		if sess.Visibility == pluginhost.SessionVisibilityPlugin {
			continue
		}
		if sess.ArchivedAt != nil {
			continue
		}
		if isNamedAgentSessionSource(sess.Source) {
			continue
		}
		if _, isAgentThread := agentThreadIDs[sess.ID]; isAgentThread {
			continue
		}
		entries[sess.ID] = threadEntryFromSession(sess, s.rt.ProviderName, s.rt.Model)
	}

	s.mu.Lock()
	for _, th := range s.threads {
		th.mu.Lock()
		thread := th.snapshotLocked()
		visibility := th.Visibility
		entry := threadListEntry{thread: thread, pinnedAt: th.PinnedAt}
		th.mu.Unlock()
		if visibility == pluginhost.SessionVisibilityPlugin {
			delete(entries, thread.ID)
			continue
		}
		if thread.Ephemeral {
			continue
		}
		if thread.ReadOnly {
			continue
		}
		if isNamedAgentSessionSource(thread.Source) {
			delete(entries, thread.ID)
			continue
		}
		if thread.Archived {
			delete(entries, thread.ID)
			continue
		}
		if persisted, ok := entries[thread.ID]; ok {
			entry.thread.Pinned = persisted.thread.Pinned
			entry.thread.FolderID = persisted.thread.FolderID
			entry.thread.WorkspaceID = persisted.thread.WorkspaceID
			entry.pinnedAt = persisted.pinnedAt
			entries[thread.ID] = entry
			continue
		}
		if sameThreadListCWD(thread.CWD, targetCWD) || sameThreadListCWD(worktreeBaseRepo(thread.Worktree), targetCWD) {
			entries[thread.ID] = entry
		}
	}
	s.mu.Unlock()

	threads := make([]threadListEntry, 0, len(entries))
	for _, entry := range entries {
		threads = append(threads, entry)
	}
	sortThreadListEntries(threads)
	result := make([]Thread, 0, len(threads))
	for _, entry := range threads {
		thread, err := s.threadWithChildAgents(entry.thread)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if params.SummaryOnly {
			thread.Turns = []Turn{}
		}
		result = append(result, thread)
	}
	return s.writeResponse(req.ID, ThreadListResult{Threads: result}, nil)
}

// handleThreadListAll returns active root conversations across every workspace.
// Global folders must not depend on a project section having been expanded.
func (s *Server) handleThreadListAll(req Request) error {
	var params ThreadListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sessions, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	agentThreadIDs := make(map[string]struct{})
	rootIDs, err := s.rootThreadIDs()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	for _, rootID := range rootIDs {
		store := s.agentThreadStore(rootID)
		if store == nil {
			continue
		}
		threads, err := store.ListThreads()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		for _, meta := range threads {
			if meta.Source.Kind == agentthread.SourceThreadSpawn {
				agentThreadIDs[meta.ID] = struct{}{}
			}
		}
	}
	entries := make(map[string]threadListEntry, len(sessions))
	for _, sess := range sessions {
		if sess.Visibility == pluginhost.SessionVisibilityPlugin || sess.ArchivedAt != nil || isNamedAgentSessionSource(sess.Source) {
			continue
		}
		if _, isAgentThread := agentThreadIDs[sess.ID]; isAgentThread {
			continue
		}
		entries[sess.ID] = threadEntryFromSession(sess, s.rt.ProviderName, s.rt.Model)
	}
	s.mu.Lock()
	for _, th := range s.threads {
		th.mu.Lock()
		thread := th.snapshotLocked()
		visibility := th.Visibility
		entry := threadListEntry{thread: thread, pinnedAt: th.PinnedAt}
		th.mu.Unlock()
		if persisted, ok := entries[thread.ID]; ok {
			entry.thread.Pinned = persisted.thread.Pinned
			entry.thread.FolderID = persisted.thread.FolderID
			entry.thread.WorkspaceID = persisted.thread.WorkspaceID
			entry.pinnedAt = persisted.pinnedAt
			thread = entry.thread
		}
		if visibility == pluginhost.SessionVisibilityPlugin || thread.Ephemeral || thread.ReadOnly || thread.Archived || isNamedAgentSessionSource(thread.Source) {
			delete(entries, thread.ID)
			continue
		}
		if _, isAgentThread := agentThreadIDs[thread.ID]; isAgentThread {
			delete(entries, thread.ID)
			continue
		}
		entries[thread.ID] = entry
	}
	s.mu.Unlock()
	ordered := make([]threadListEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sortThreadListEntries(ordered)
	result := make([]Thread, 0, len(ordered))
	for _, entry := range ordered {
		thread, err := s.threadWithChildAgents(entry.thread)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if params.SummaryOnly {
			thread.Turns = []Turn{}
		}
		result = append(result, thread)
	}
	return s.writeResponse(req.ID, ThreadListResult{Threads: result}, nil)
}

// handleThreadListArchived returns every archived session the running server
// knows about, regardless of cwd. The Settings → Archive panel is global —
// it must surface threads the user has archived across every workspace and
// scratch directory, not just the active one. After an app restart the
// archive page reconstructs itself exclusively from this handler, so we
// merge disk-backed sessions with in-memory threads (in-memory wins when
// both exist, mirroring handleThreadList's policy) and ignore the caller's
// cwd argument.
func (s *Server) handleThreadListArchived(req Request) error {
	var params ThreadListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}

	sessions, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	entries := make(map[string]threadListEntry, len(sessions))
	for _, sess := range sessions {
		if sess.Visibility == pluginhost.SessionVisibilityPlugin {
			continue
		}
		if sess.ArchivedAt == nil {
			continue
		}
		if isNamedAgentSessionSource(sess.Source) {
			continue
		}
		entries[sess.ID] = threadEntryFromSession(sess, s.rt.ProviderName, s.rt.Model)
	}

	s.mu.Lock()
	for _, th := range s.threads {
		th.mu.Lock()
		thread := th.snapshotLocked()
		visibility := th.Visibility
		entry := threadListEntry{thread: thread, pinnedAt: th.PinnedAt}
		th.mu.Unlock()
		if visibility == pluginhost.SessionVisibilityPlugin {
			delete(entries, thread.ID)
			continue
		}
		if thread.Ephemeral {
			continue
		}
		if thread.ReadOnly {
			continue
		}
		if isNamedAgentSessionSource(thread.Source) {
			delete(entries, thread.ID)
			continue
		}
		if !thread.Archived {
			continue
		}
		entries[thread.ID] = entry
	}
	s.mu.Unlock()

	threads := make([]threadListEntry, 0, len(entries))
	for _, entry := range entries {
		threads = append(threads, entry)
	}
	sortThreadListEntries(threads)
	result := make([]Thread, 0, len(threads))
	for _, entry := range threads {
		thread, err := s.threadWithChildAgents(entry.thread)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if params.SummaryOnly {
			thread.Turns = []Turn{}
		}
		result = append(result, thread)
	}
	return s.writeResponse(req.ID, ThreadListResult{Threads: result}, nil)
}

func sameThreadListCWD(left, right string) bool {
	return cleanThreadListCWD(left) == cleanThreadListCWD(right)
}

func cleanThreadListCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cwd)
}

func (s *Server) handleThreadPin(req Request) error {
	var params ThreadPinParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	metadata, err := session.UpdatePinned(s.rt.SessionDir, id, params.Pinned)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	thread, err := s.threadAfterMetadataUpdate(metadata)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.writeResponse(req.ID, ThreadPinResult{Thread: thread}, nil); err != nil {
		return err
	}
	return s.notifyThreadUpdated(thread)
}

func (s *Server) handleThreadArchive(req Request) error {
	var params ThreadArchiveParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	if params.Archived && params.Force {
		s.settleThreadExecutionForForcedArchive(id)
	}
	if params.Archived {
		if th := s.thread(id); th != nil {
			th.mu.Lock()
			running := th.running
			th.mu.Unlock()
			if running {
				return s.writeResponse(req.ID, nil, errors.New("cannot archive a running thread"))
			}
		}
	}
	var mutationLease *session.ThreadExecutionLease
	if params.Archived {
		var leaseErr error
		mutationLease, leaseErr = s.tryAcquireThreadMutationLease(id)
		if leaseErr != nil {
			return s.writeResponse(req.ID, nil, leaseErr)
		}
	}
	metadata, err := session.UpdateArchived(s.rt.SessionDir, id, params.Archived)
	if err != nil {
		releaseThreadMutationLease(id, mutationLease)
		return s.writeResponse(req.ID, nil, err)
	}
	thread, err := s.threadAfterMetadataUpdate(metadata)
	if err != nil {
		releaseThreadMutationLease(id, mutationLease)
		return s.writeResponse(req.ID, nil, err)
	}
	releaseThreadMutationLease(id, mutationLease)
	return s.writeResponse(req.ID, ThreadArchiveResult{Thread: thread}, nil)
}

var errArchivedWhileRunning = errors.New("archived while the conversation was still running")

// settleThreadExecutionForForcedArchive is the escape hatch behind
// thread/archive force: it makes a thread whose execution state can no longer
// settle on its own idle so the durable archive mutation can be admitted.
// The canonical stuck shape is a runner that died without clearing the
// running flag; nothing ever settles the turn again, so the thread would stay
// un-archivable forever.
//
// A live turn is interrupted with the usual interrupt semantics first (queued
// work is held, the worker tree freezes, observers are notified); the
// synchronous settlement below then clears the running state immediately, and
// the runner's own settlement no-ops once currentTurn is cleared. A dead turn
// is settled in place. Cross-process ownership is unaffected: a live owner in
// another app-server still holds the OS-level execution lock, so the mutation
// lease acquisition that follows keeps rejecting the archive.
func (s *Server) settleThreadExecutionForForcedArchive(threadID string) {
	th := s.thread(threadID)
	if th == nil {
		return
	}
	th.mu.Lock()
	live := th.cancel != nil
	th.mu.Unlock()
	if live {
		if _, err := s.interruptThreadExecution(threadID, "", ""); err != nil {
			// The escape hatch must still work when a side-path of the full
			// interrupt fails: cancel the turn directly so its runner exits.
			providers.DebugLogf("interrupt thread %q for forced archive: %v", threadID, err)
			th.mu.Lock()
			cancel := th.cancel
			th.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
	now := time.Now().UTC()
	th.mu.Lock()
	turnID := strings.TrimSpace(th.currentTurn)
	turnKind := th.currentTurnKind
	settledTurnID := ""
	var reconnectItem *ThreadItem
	switch {
	case turnID != "":
		th.completeTurnLocked(turnID, TurnStatusInterrupted, errArchivedWhileRunning, now, "", "", false)
		settledTurnID = turnID
		if item, ok := streamReconnectItemForPersistLocked(th, turnID); ok {
			reconnectItem = &item
		}
	case th.executionLease != nil || th.admissionReserved || th.running:
		// Stuck without an active turn record: a leaked admission or a
		// running flag whose runner is gone. Release the execution lease so
		// the archive mutation can be admitted.
		th.releaseThreadExecutionLeaseLocked()
		th.running = false
	}
	th.mu.Unlock()
	if settledTurnID == "" {
		return
	}
	if err := s.persistTurnTerminal(th, settledTurnID, turnKind, TurnStatusInterrupted, errArchivedWhileRunning, now, reconnectItem); err != nil {
		providers.DebugLogf("persist forced archive settlement for thread %q: %v", threadID, err)
	}
}

func (s *Server) handleThreadRename(req Request) error {
	var params ThreadRenameParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	metadata, err := session.UpdateTitle(s.rt.SessionDir, id, params.Title)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	thread, err := s.threadAfterMetadataUpdate(metadata)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.writeResponse(req.ID, ThreadRenameResult{Thread: thread}, nil); err != nil {
		return err
	}
	return s.notifyThreadUpdated(thread)
}

type threadListEntry struct {
	thread   Thread
	pinnedAt *time.Time
}

func applySessionMetadata(th *threadState, metadata session.Session) {
	if !metadata.CreatedAt.IsZero() {
		th.CreatedAt = metadata.CreatedAt
	}
	if !metadata.UpdatedAt.IsZero() {
		th.UpdatedAt = metadata.UpdatedAt
	} else if !metadata.CreatedAt.IsZero() {
		th.UpdatedAt = metadata.CreatedAt
	}
	if strings.TrimSpace(metadata.CWD) != "" {
		th.CWD = metadata.CWD
	}
	th.Title = metadata.Title
	th.Source = metadata.Source
	if strings.HasPrefix(metadata.Source, namedAgentSessionSource) {
		th.NamedAgentID = strings.TrimPrefix(metadata.Source, namedAgentSessionSource)
	}
	th.Owner = metadata.Owner
	th.Visibility = metadata.Visibility
	if selection := runtimeSelectionFromSession(metadata); selection.Provider != "" && selection.Model != "" {
		applyThreadRuntimeSelection(th, selection)
	}
	th.EngineID = string(agentengine.NormalizeEngineID(metadata.EngineID))
	th.EngineRef = strings.TrimSpace(metadata.EngineRef)
	th.ForkedFromID = metadata.ForkedFromID
	th.ForkedFromTurnID = metadata.ForkedFromTurnID
	th.ForkedFromItemID = metadata.ForkedFromItemID
	th.WorktreePath = metadata.WorktreePath
	th.WorktreeBaseHEAD = metadata.WorktreeBaseHEAD
	th.WorktreeBaseRepo = metadata.WorktreeBaseRepo
	th.WorkspaceID = metadata.WorkspaceID
	th.PinnedAt = metadata.PinnedAt
	th.FolderID = metadata.FolderID
	th.ArchivedAt = metadata.ArchivedAt
}

// persistThreadEngineRef stores the engine's native session reference for a
// thread (for example a codex thread id) in the session row and the live
// thread state. It is invoked by engine sessions after they create their
// native thread on first use, so later turns resume it.
func (s *Server) persistThreadEngineRef(threadID, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if _, err := session.SetEngineRef(s.rt.SessionDir, threadID, ref); err != nil {
		return err
	}
	if th := s.thread(threadID); th != nil {
		th.mu.Lock()
		th.EngineRef = ref
		th.mu.Unlock()
	}
	return nil
}

// runtimeSelectionFromSession is the one conversion from a persisted session
// row to its runtime selection. List entries, loaded threads, and legacy
// migration all read the row through it so they cannot diverge on which
// selection fields a session carries.
func runtimeSelectionFromSession(sess session.Session) session.RuntimeSelection {
	return session.RuntimeSelection{
		Provider:       strings.TrimSpace(sess.Provider),
		Model:          strings.TrimSpace(sess.Model),
		Variant:        strings.TrimSpace(sess.Variant),
		Effort:         strings.TrimSpace(sess.Effort),
		PermissionMode: strings.TrimSpace(sess.PermissionMode),
	}
}

func applyThreadRuntimeSelection(th *threadState, selection session.RuntimeSelection) {
	if th == nil {
		return
	}
	th.ModelProvider = strings.TrimSpace(selection.Provider)
	th.Model = strings.TrimSpace(selection.Model)
	th.ModelVariant = strings.TrimSpace(selection.Variant)
	th.ModelEffort = strings.TrimSpace(selection.Effort)
	if mode := strings.TrimSpace(selection.PermissionMode); mode != "" {
		th.PermissionMode = config.NormalizePermissionMode(mode)
	}
}

func threadEntryFromSession(sess session.Session, provider, model string) threadListEntry {
	updatedAt := sess.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = sess.CreatedAt
	}
	selection := runtimeSelectionFromSession(sess)
	permissionMode := ""
	if selection.PermissionMode != "" {
		permissionMode = config.NormalizePermissionMode(selection.PermissionMode)
	}
	return threadListEntry{
		thread: Thread{
			ID:                    sess.ID,
			Source:                sess.Source,
			Preview:               firstNonEmpty(sess.Title, sess.Summary),
			Title:                 sess.Title,
			ModelProvider:         firstNonEmpty(selection.Provider, provider),
			Model:                 firstNonEmpty(selection.Model, model),
			ModelVariant:          selection.Variant,
			ModelEffort:           selection.Effort,
			PermissionMode:        permissionMode,
			EngineID:              string(agentengine.NormalizeEngineID(sess.EngineID)),
			CWD:                   sess.CWD,
			WorkspaceID:           sess.WorkspaceID,
			Status:                ThreadStatusIdle,
			Pinned:                sess.PinnedAt != nil,
			FolderID:              sess.FolderID,
			Archived:              sess.ArchivedAt != nil,
			ForkedFromID:          sess.ForkedFromID,
			ForkedFromTurnID:      sess.ForkedFromTurnID,
			ForkedFromItemID:      sess.ForkedFromItemID,
			Worktree:              threadWorktreeInfo(sess.WorktreePath, sess.WorktreeBaseHEAD, sess.WorktreeBaseRepo),
			CreatedAt:             sess.CreatedAt,
			UpdatedAt:             updatedAt,
			LatestCompletedTurnID: sess.LatestCompletedTurnID,
			Turns:                 []Turn{},
		},
		pinnedAt: sess.PinnedAt,
	}
}

func (s *Server) threadWithChildAgents(thread Thread) (Thread, error) {
	agents, err := s.childAgentsForThread(thread.ID)
	if err != nil {
		return thread, err
	}
	thread.ChildAgents = agents
	return s.threadWithWorktreeStatus(thread), nil
}

func (s *Server) threadWithWorktreeStatus(thread Thread) Thread {
	if thread.Worktree == nil || strings.TrimSpace(thread.Worktree.Path) == "" {
		return thread
	}
	info := *thread.Worktree
	// A worktree that no longer exists on disk (moved or deleted since the
	// session was created) cannot report status. Spawning git against the
	// stale path costs a process start per stale thread on every listing,
	// which dominates thread/list once worktrees are removed.
	if stat, statErr := os.Stat(info.Path); statErr != nil || !stat.IsDir() {
		return thread
	}
	manager, err := s.worktreeManager(firstNonEmpty(info.BaseRepo, thread.CWD, s.rt.RootDir))
	if err == nil {
		if status, statusErr := manager.Status(info.Path); statusErr == nil {
			info.Dirty = status.Dirty
			info.ChangedFiles = append([]string(nil), status.ChangedFiles...)
		}
	}
	thread.Worktree = &info
	return thread
}

func (s *Server) worktreeManager(parentRepo string) (*worktree.Manager, error) {
	if s == nil || s.rt == nil {
		return nil, errors.New("runtime session is required")
	}
	parentRepo = firstNonEmpty(parentRepo, s.rt.RootDir)
	stateDir, err := s.workspaceStateDir()
	if err != nil {
		return nil, err
	}
	return worktree.NewManager(parentRepo, statepath.WorktreeRoot(stateDir))
}

func worktreeBaseRepo(info *WorktreeInfo) string {
	if info == nil {
		return ""
	}
	return strings.TrimSpace(info.BaseRepo)
}

func (s *Server) childAgentsForThread(threadID string) ([]Agent, error) {
	store := s.agentThreadStore(threadID)
	if store == nil {
		return nil, nil
	}
	threads, err := store.ListThreads()
	if err != nil {
		return nil, err
	}
	threads, err = s.reconcilePersistedTerminalAgentThreads(threadID, store, threads)
	if err != nil {
		return nil, err
	}

	children := make([]Agent, 0)
	childIndexByPath := make(map[string]int)
	childIDs := make([]string, 0, len(threads))
	for _, meta := range threads {
		if !isDirectChildAgentThread(threadID, meta) {
			continue
		}
		childIDs = append(childIDs, meta.ID)
	}
	if len(childIDs) == 0 {
		return nil, nil
	}
	// Resolve pin/archive flags for every direct child in one store open.
	// Per-child session.Find opens and reconfigures the (potentially huge)
	// sessions database once per child, which dominates thread/list for
	// workspaces with many subagent sessions.
	childPinnedArchived, err := session.FindPinnedArchived(s.rt.SessionDir, childIDs)
	if err != nil {
		return nil, err
	}
	for _, meta := range threads {
		if !isDirectChildAgentThread(threadID, meta) {
			continue
		}
		flags := childPinnedArchived[meta.ID]
		childIndexByPath[meta.Path] = len(children)
		children = append(children, agentFromThreadMetadata(meta, flags.Pinned, flags.Archived))
	}
	if len(children) == 0 {
		return nil, nil
	}

	for _, meta := range threads {
		if meta.Source.Kind != agentthread.SourceThreadSpawn {
			continue
		}
		for path, index := range childIndexByPath {
			if meta.ID == children[index].ID || !strings.HasPrefix(meta.Path, path+"/") {
				continue
			}
			children[index].NestedCount++
			if isRunningAgentStatus(string(meta.Status)) {
				children[index].NestedRunningCount++
			}
			break
		}
	}

	sort.Slice(children, func(i, j int) bool {
		left := children[i].StartedAt
		right := children[j].StartedAt
		if !left.Equal(right) {
			return left.Before(right)
		}
		return children[i].AgentPath < children[j].AgentPath
	})
	return children, nil
}

// reconcilePersistedTerminalAgentThreads repairs a UI-facing thread index when
// the worker snapshot reached a terminal state before the previous process
// recorded the matching thread status. AgentControl performs the full durable
// task reconciliation when an execution runtime is created, but thread/list
// and thread/resume must also report terminal truth without requiring the user
// to start another turn first.
func (s *Server) reconcilePersistedTerminalAgentThreads(
	rootThreadID string,
	store *agentthread.Store,
	threads []agentthread.Metadata,
) ([]agentthread.Metadata, error) {
	if s == nil || store == nil || strings.TrimSpace(rootThreadID) == "" {
		return threads, nil
	}
	stateDir, err := s.workspaceStateDir()
	if err != nil {
		return nil, err
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, rootThreadID)
	historyDir := filepath.Join(artifactDir, "workers")
	harnessDir := filepath.Join(artifactDir, "harness")
	for index := range threads {
		meta := threads[index]
		if meta.Source.Kind != agentthread.SourceThreadSpawn ||
			!isRunningAgentStatus(string(meta.Status)) {
			continue
		}
		active, err := agentcontrol.WorkerExecutionActive(harnessDir, meta.ID)
		if err != nil {
			return nil, fmt.Errorf("inspect worker %q execution lease: %w", meta.ID, err)
		}
		if active {
			continue
		}
		run, err := subagent.LoadPersistedRun(filepath.Join(historyDir, meta.ID+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load worker %q snapshot: %w", meta.ID, err)
		}
		status, terminal := terminalAgentThreadStatus(run.Status)
		if !terminal {
			continue
		}
		meta.Status = status
		if !run.CompletedAt.IsZero() {
			meta.UpdatedAt = run.CompletedAt
		}
		if err := store.RecordStatus(meta); err != nil {
			return nil, fmt.Errorf("persist terminal worker %q thread status: %w", meta.ID, err)
		}
		threads[index] = meta
	}
	return threads, nil
}

func terminalAgentThreadStatus(status subagent.Status) (agentthread.Status, bool) {
	switch status {
	case subagent.StatusCompleted:
		return agentthread.StatusCompleted, true
	case subagent.StatusFailed, subagent.StatusInterrupted:
		return agentthread.StatusFailed, true
	case subagent.StatusCancelled:
		return agentthread.StatusCancelled, true
	default:
		return "", false
	}
}

func (s *Server) agentSessionThread(agentID string) (Thread, bool, error) {
	rootID, meta, ok, err := s.agentThreadMetadata(agentID)
	if err != nil || !ok {
		return Thread{}, ok, err
	}

	var rec persistedAgentHistory
	if control := s.liveAgentControl(rootID); control != nil && control.Manager() != nil {
		if sa := control.Manager().Get(meta.ID); sa != nil {
			snap := sa.Snapshot()
			var th *threadState
			if isRunningSubAgentStatus(snap.Status) {
				th, _, _ = s.ensureLiveAgentThread(rootID, control, snap, time.Now().UTC())
			} else {
				th, _, _, _ = s.syncFinalAgentThread(rootID, control, snap, time.Now().UTC())
			}
			if th != nil {
				th.mu.Lock()
				thread := th.snapshotLocked()
				th.mu.Unlock()
				return thread, true, nil
			}
		}
	}
	history, hasHistory := s.liveAgentHistory(rootID, meta.ID)
	if !hasHistory {
		path, err := s.agentHistoryPath(rootID, meta.ID)
		if err != nil {
			return Thread{}, true, err
		}
		if path != "" {
			loaded, err := loadAgentHistory(path)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return Thread{}, true, err
				}
			} else {
				rec = loaded
				history = cloneHistory(rec.Messages)
			}
		}
	}
	if len(history) == 0 {
		history = fallbackAgentHistory(meta, rec)
	}

	now := time.Now().UTC()
	createdAt := meta.CreatedAt
	if createdAt.IsZero() {
		createdAt = rec.StartedAt
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := meta.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = rec.CompletedAt
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	model := firstNonEmpty(rec.Model, meta.Model, s.rt.Model)
	cwd := firstNonEmpty(meta.CWD, s.rt.RootDir)

	return Thread{
		ID:            meta.ID,
		ParentID:      rootID,
		AgentPath:     meta.Path,
		Preview:       agentSessionPreview(meta, rec, history),
		ModelProvider: s.rt.ProviderName,
		Model:         model,
		CWD:           cwd,
		Status:        ThreadStatusIdle,
		ReadOnly:      true,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		Turns:         turnsFromHistory(meta.ID, history, now),
	}, true, nil
}

func (s *Server) agentThreadMetadata(agentID string) (string, agentthread.Metadata, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", agentthread.Metadata{}, false, nil
	}
	rootIDs, err := s.rootThreadIDs()
	if err != nil {
		return "", agentthread.Metadata{}, false, err
	}
	for _, rootID := range rootIDs {
		store := s.agentThreadStore(rootID)
		if store == nil {
			continue
		}
		threads, err := store.ListThreads()
		if err != nil {
			return "", agentthread.Metadata{}, false, err
		}
		for _, meta := range threads {
			if meta.ID == agentID && meta.Source.Kind == agentthread.SourceThreadSpawn {
				return rootID, meta, true, nil
			}
		}
	}
	return "", agentthread.Metadata{}, false, nil
}

func (s *Server) rootThreadIDs() ([]string, error) {
	if s == nil || s.rt == nil {
		return nil, nil
	}
	sessions, err := session.ListForCWD(s.rt.SessionDir, s.rt.RootDir, s.rt.WorkspaceID, 0)
	if err != nil {
		return nil, err
	}
	return s.rootThreadIDsForSessions(sessions)
}

// rootThreadIDsForSessions is rootThreadIDs with the workspace session list
// already loaded, so thread/list does not rescan the sessions table for the
// same rows it just fetched.
func (s *Server) rootThreadIDsForSessions(sessions []session.Session) ([]string, error) {
	if s == nil || s.rt == nil {
		return nil, nil
	}
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}

	for _, sess := range sessions {
		add(sess.ID)
	}

	s.mu.Lock()
	for id, th := range s.threads {
		if th == nil {
			continue
		}
		th.mu.Lock()
		cwd := th.CWD
		readOnly := th.ReadOnly
		th.mu.Unlock()
		if !readOnly && cwd == s.rt.RootDir {
			add(id)
		}
	}
	s.mu.Unlock()
	return ids, nil
}

func (s *Server) liveAgentHistory(rootID, agentID string) ([]providers.ChatMessage, bool) {
	control := s.liveAgentControl(rootID)
	if control == nil || control.Manager() == nil {
		return nil, false
	}
	return control.Manager().History(agentID)
}

func (s *Server) liveAgentControl(rootID string) *agentcontrol.AgentControl {
	th := s.thread(rootID)
	if th == nil {
		return nil
	}
	th.mu.Lock()
	threadRuntime := th.execRuntime
	th.mu.Unlock()
	if threadRuntime == nil {
		return nil
	}
	return threadRuntime.AgentControl
}

func (s *Server) agentHistoryPath(rootID, agentID string) (string, error) {
	rootID = strings.TrimSpace(rootID)
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.rt == nil || rootID == "" || agentID == "" {
		return "", nil
	}
	stateDir, err := s.workspaceStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(statepath.SessionArtifactDir(stateDir, rootID), "workers", agentID+".json"), nil
}

func (s *Server) agentThreadStore(threadID string) *agentthread.Store {
	if s == nil || s.rt == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	stateDir, err := s.workspaceStateDir()
	if err != nil {
		return nil
	}
	return agentthread.NewStore(filepath.Join(statepath.SessionArtifactDir(stateDir, threadID), "threads"))
}

func (s *Server) workspaceStateDir() (string, error) {
	if s == nil || s.rt == nil {
		return "", errors.New("runtime session is required")
	}
	if stateDir := strings.TrimSpace(s.rt.StateDir); stateDir != "" {
		return stateDir, nil
	}
	home, err := statepath.Home("")
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(s.rt.WorkspaceID); id != "" {
		return statepath.WorkspaceDirByID(home, id)
	}
	return statepath.WorkspaceDir(home, s.rt.RootDir)
}

func fallbackAgentHistory(meta agentthread.Metadata, rec persistedAgentHistory) []providers.ChatMessage {
	prompt := firstNonEmpty(rec.Prompt, meta.LastTaskMessage)
	result := strings.TrimSpace(rec.Result)
	errorText := strings.TrimSpace(rec.Error)
	history := make([]providers.ChatMessage, 0, 2)
	if prompt != "" {
		history = append(history, providers.ChatMessage{Role: "user", Content: prompt})
	}
	if result != "" {
		history = append(history, providers.ChatMessage{Role: "assistant", Content: result})
	} else if errorText != "" {
		history = append(history, providers.ChatMessage{Role: "assistant", Content: "Worker failed: " + errorText})
	}
	return history
}

func agentSessionPreview(meta agentthread.Metadata, rec persistedAgentHistory, history []providers.ChatMessage) string {
	if preview := firstNonEmpty(rec.Description, meta.TaskName, threadPreview(history), rec.Prompt, meta.LastTaskMessage, meta.ID); preview != "" {
		return preview
	}
	return "子任务"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isDirectChildAgentThread(threadID string, meta agentthread.Metadata) bool {
	if meta.Source.Kind != agentthread.SourceThreadSpawn || meta.ID == threadID {
		return false
	}
	if meta.Source.ParentPath == agentthread.RootPath {
		return true
	}
	return strings.TrimSpace(meta.ParentID) == strings.TrimSpace(threadID) && agentPathDepth(meta.Path) == 2
}

func agentFromThreadMetadata(meta agentthread.Metadata, pinArchive ...bool) Agent {
	startedAt := meta.CreatedAt
	if startedAt.IsZero() {
		startedAt = meta.UpdatedAt
	}
	var pinned, archived bool
	if len(pinArchive) > 0 {
		pinned = pinArchive[0]
	}
	if len(pinArchive) > 1 {
		archived = pinArchive[1]
	}
	return Agent{
		ID:           meta.ID,
		Type:         meta.Role,
		TaskName:     meta.TaskName,
		AgentProfile: meta.AgentProfile,
		AgentPath:    meta.Path,
		ParentID:     meta.ParentID,
		Description:  meta.TaskName,
		Status:       string(meta.Status),
		Pinned:       pinned,
		Archived:     archived,
		StartedAt:    startedAt,
	}
}

func isRunningAgentStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case string(agentthread.StatusPending), string(agentthread.StatusRunning):
		return true
	default:
		return false
	}
}

func agentPathDepth(path string) int {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "/") + 1
}

func sortThreadListEntries(entries []threadListEntry) {
	sort.Slice(entries, func(i, j int) bool {
		leftPinned := entries[i].pinnedAt != nil
		rightPinned := entries[j].pinnedAt != nil
		if leftPinned != rightPinned {
			return leftPinned
		}
		leftTime := entries[i].thread.UpdatedAt
		if leftTime.IsZero() {
			leftTime = entries[i].thread.CreatedAt
		}
		rightTime := entries[j].thread.UpdatedAt
		if rightTime.IsZero() {
			rightTime = entries[j].thread.CreatedAt
		}
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return entries[i].thread.ID > entries[j].thread.ID
	})
}

func (s *Server) mostRecentVisibleThreadID() (string, error) {
	sessions, err := session.ListForCWD(s.rt.SessionDir, s.rt.RootDir, s.rt.WorkspaceID, 0)
	if err != nil {
		return "", err
	}
	entries := make([]threadListEntry, 0, len(sessions))
	for _, sess := range sessions {
		if sess.ArchivedAt != nil {
			continue
		}
		entries = append(entries, threadEntryFromSession(sess, s.rt.ProviderName, s.rt.Model))
	}
	sortThreadListEntries(entries)
	if len(entries) == 0 {
		return "", nil
	}
	return entries[0].thread.ID, nil
}

func (s *Server) threadAfterMetadataUpdate(metadata session.Session) (Thread, error) {
	if th := s.thread(metadata.ID); th != nil {
		th.mu.Lock()
		applySessionMetadata(th, metadata)
		thread := th.snapshotLocked()
		th.mu.Unlock()
		return s.threadWithChildAgents(thread)
	}
	return s.threadWithChildAgents(threadEntryFromSession(metadata, s.rt.ProviderName, s.rt.Model).thread)
}

func (s *Server) hasRunningThread() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, th := range s.threads {
		th.mu.Lock()
		running := th.running
		th.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}
