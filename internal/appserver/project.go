package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A project is its coordinator conversation. The coordinator investigates and
// delegates; the ordinary sessions it manages make every change.
const (
	projectSource        = "project"
	projectSessionSource = "project-session"
	// projectSessionOwner keeps managed sessions user-owned: the user can open,
	// continue, archive and delete them like any conversation.
	projectSessionOwner = "user"
)

var errProjectCoordinatorReadOnly = errors.New("a project coordinator is read-only; its sessions make changes")

// The instructions are static so renaming a project never makes them stale.
const projectCoordinatorInstructions = `You coordinate this project. You never edit files, run commands that change anything, or operate a browser: your permission mode is read-only. Read, search and run read-only commands to understand the workspace, keep the user's goals and decisions in view, and delegate every change to a managed session with the session tool.

- Start one session per independent stream of work. Its brief must stand alone: the goal, constraints, acceptance checks, and what to leave alone. Sessions do not see this conversation.
- Correct an existing session instead of starting a replacement; a changed goal is a correction.
- Sessions that change files work in their own Git worktree by default. Give concurrent writers separate scopes, and use a shared session only for a single writer.
- When a session's turn ends, its result arrives here. Treat it as evidence, not proof that the goal is met: decide the next instruction, or tell the user what is ready to review.
- Changes reach the workspace only when the user applies a candidate. Never say work is delivered before that.
- For an independent check, start a session from a candidate and ask it to test and report findings without changing the candidate.
- When the user takes over a session, stop instructing it until they return it.
- Keep a short checklist of open work and decisions in your notes.
- Answer small, self-contained questions yourself; suggest an ordinary conversation when delegation adds nothing.`

const projectSessionInstructions = `A project coordinator manages this session; your first message is its brief. The coordinator reads your final answer each time a turn ends, and the user can read or take over this session at any time. End each turn with a plain report: what you did, choices you made that the brief did not settle, the evidence (commands run and their results), and open questions. In a session with its own worktree, your changes reach the workspace only when the user applies them.`

func (s *Server) startProjectThread(selection session.RuntimeSelection, engineID agentengine.EngineID, params ThreadStartParams) (*threadState, error) {
	name := strings.TrimSpace(params.Project.Name)
	if name == "" {
		return nil, errors.New("a project needs a name")
	}
	if params.Ephemeral {
		return nil, errors.New("a project cannot be ephemeral")
	}
	if engineID != agentengine.EngineWuu {
		return nil, errors.New("a project coordinator runs on the Wuu engine")
	}
	workspaceID := firstNonEmpty(strings.TrimSpace(params.WorkspaceID), s.rt.WorkspaceID)
	if workspaceID == "" {
		return nil, errors.New("a project needs a registered workspace")
	}
	return s.createHostSessionThread(projectSessionOwner, projectSource, "", pluginhost.SessionCreateParams{
		Name: name, Visibility: pluginhost.SessionVisibilityUser, ContextSource: pluginhost.SessionContextFresh,
		Workspace: "shared", WorkspaceID: workspaceID, WorkspaceRoot: strings.TrimSpace(params.CWD),
		Provider: selection.Provider, Model: selection.Model, Variant: selection.Variant, Effort: selection.Effort,
		PermissionMode: config.PermissionModeReadOnly, Instructions: projectCoordinatorInstructions,
	})
}

// projectCoordinator resolves a live project. Archived and deleted projects
// no longer manage their sessions.
func (s *Server) projectCoordinator(id string) (session.Session, bool) {
	metadata, found, err := session.Find(s.rt.SessionDir, strings.TrimSpace(id))
	if err != nil || !found || metadata.Source != projectSource || metadata.ArchivedAt != nil {
		return session.Session{}, false
	}
	return metadata, true
}

// projectManagedSession resolves one of a project's live managed sessions.
func (s *Server) projectManagedSession(projectID, sessionID string) (session.Session, error) {
	metadata, found, err := session.Find(s.rt.SessionDir, strings.TrimSpace(sessionID))
	if err != nil {
		return session.Session{}, err
	}
	if !found || metadata.Source != projectSessionSource || metadata.ParentID != projectID || metadata.ArchivedAt != nil {
		return session.Session{}, fmt.Errorf("session %q is not managed by this project", sessionID)
	}
	return metadata, nil
}

// pendingCandidates reads a project thread's undecided candidate count from
// counts returned by session.PendingCandidateCounts.
func pendingCandidates(source, id string, bySession, byProject map[string]int) int {
	switch source {
	case projectSource:
		return byProject[id]
	case projectSessionSource:
		return bySession[id]
	}
	return 0
}

func projectIDForSession(metadata session.Session) string {
	if metadata.Source == projectSessionSource {
		return metadata.ParentID
	}
	return ""
}

// afterProjectTurn reports a managed session's finished turn to its project
// and lets a coordinator pick up input that arrived while it was busy. An
// interrupted coordinator turn does not immediately restart on queued input.
func (s *Server) afterProjectTurn(th *threadState, turn Turn, compactOnly bool) {
	th.mu.Lock()
	source := th.Source
	th.mu.Unlock()
	switch {
	case source == projectSessionSource && !compactOnly:
		s.startBackground(func() { s.recordProjectResult(th, turn) })
	case source == projectSource && turn.Status != TurnStatusInterrupted:
		s.startBackground(func() { s.drainSessionInbox(th.ID) })
	}
}

// recordProjectResult freezes a managed turn's changes for the user's review
// and tells the coordinator once that the turn ended. Turns that end while
// the user holds control are theirs: their changes are frozen, but they are
// not reported.
func (s *Server) recordProjectResult(th *threadState, turn Turn) {
	if turn.Status == TurnStatusInProgress {
		return
	}
	th.mu.Lock()
	projectID, title := th.ProjectID, th.Title
	th.mu.Unlock()
	if _, live := s.projectCoordinator(projectID); !live {
		return
	}
	candidate, err := s.captureCandidate(th, turn)
	if err != nil {
		providers.DebugLogf("capture candidate for session %q turn %q: %v", th.ID, turn.ID, err)
	}
	control, ok, err := session.ReadControl(s.rt.SessionDir, th.ID)
	if err != nil || !ok || control.State != session.ControlActive || control.ManagerID != projectID {
		return
	}
	clientID := projectResultClientID(th.ID, turn.ID)
	if recorded, err := session.InboxHas(s.rt.SessionDir, clientID); err != nil || recorded {
		return
	}
	var report strings.Builder
	fmt.Fprintf(&report, "Session %q finished a turn: %s.", title, turn.Status)
	if turn.Error != nil && turn.Error.Message != "" {
		fmt.Fprintf(&report, "\nError: %s", turn.Error.Message)
	}
	if answer := finalAnswerText(turn); answer != "" {
		fmt.Fprintf(&report, "\n\n%s", excerpt(answer, 2400))
	}
	if candidate != nil {
		fmt.Fprintf(&report, "\n\nProposal awaiting the user's review (every change of the session not yet delivered): %s", strings.Join(candidate.ChangedFiles, ", "))
	}
	s.enqueueProjectInput(projectID, th.ID, clientID, report.String())
}

func projectResultClientID(sessionID, turnID string) string {
	return "project-result:" + sessionID + ":" + turnID
}

// settleUserControlledTurn marks the session's latest finished turn as handled
// when the user returns it, so recovery never reports a turn the user ran.
func (s *Server) settleUserControlledTurn(projectID, sessionID string) error {
	th, err := s.ensureOwnedThreadLoaded(sessionID)
	if err != nil || th == nil {
		return err
	}
	th.mu.Lock()
	turnID := latestCompletedTurnID(th.Turns)
	th.mu.Unlock()
	if turnID == "" {
		return nil
	}
	return session.SettleInbox(s.rt.SessionDir, projectResultClientID(sessionID, turnID), projectID)
}

func finalAnswerText(turn Turn) string {
	for index := len(turn.Items) - 1; index >= 0; index-- {
		if item := turn.Items[index]; item.Type == ThreadItemAgentMessage && strings.TrimSpace(item.Text) != "" {
			return strings.TrimSpace(item.Text)
		}
	}
	return ""
}

// noticeProjectControl tells the coordinator the user took over, paused or
// returned one of its sessions.
func (s *Server) noticeProjectControl(sessionID string, control session.Control) {
	if _, live := s.projectCoordinator(control.ManagerID); !live {
		return
	}
	th := s.thread(sessionID)
	title := sessionID
	if th != nil {
		th.mu.Lock()
		title = th.Title
		th.mu.Unlock()
	}
	var notice string
	switch control.State {
	case session.ControlTakenOver:
		notice = fmt.Sprintf("The user took over session %q. Do not instruct it until they return it.", title)
	case session.ControlPaused:
		notice = fmt.Sprintf("The user paused session %q. Do not instruct it until they return it.", title)
	case session.ControlActive:
		notice = fmt.Sprintf("The user returned session %q to you. Inspect what changed before continuing.", title)
	default:
		return
	}
	s.enqueueProjectInput(control.ManagerID, sessionID, fmt.Sprintf("project-control:%s:%d", sessionID, control.Revision), notice)
}

func (s *Server) enqueueProjectInput(projectID, relatedSessionID, clientID, content string) {
	if err := session.EnqueueInbox(s.rt.SessionDir, session.InboxMessage{
		ClientID: clientID, SessionID: projectID, RelatedSessionID: relatedSessionID, Content: content,
	}); err != nil {
		providers.DebugLogf("queue project input %q: %v", clientID, err)
		return
	}
	s.startBackground(func() { s.drainSessionInbox(projectID) })
}

// startProjectRecovery checks at start-up whether any project work may need
// recovery and replays it in the background; an idle store costs one read.
func (s *Server) startProjectRecovery() {
	controls, err := session.ListControls(s.rt.SessionDir)
	if err != nil {
		providers.DebugLogf("recover project sessions: %v", err)
		return
	}
	pending := false
	for _, control := range controls {
		pending = pending || control.State != session.ControlReleased && !strings.HasPrefix(control.ManagerID, "plugin:")
	}
	if !pending {
		targets, err := session.InboxTargets(s.rt.SessionDir)
		if err != nil {
			providers.DebugLogf("recover project inbox: %v", err)
			return
		}
		pending = len(targets) > 0
	}
	if pending {
		s.startBackground(s.recoverProjectInbox)
	}
}

// recoverProjectInbox freezes and reports managed turns that ended while no
// host was running, then retries undelivered coordinator input. Each step is
// idempotent, so running them again never duplicates a delivery.
func (s *Server) recoverProjectInbox() {
	controls, err := session.ListControls(s.rt.SessionDir)
	if err != nil {
		providers.DebugLogf("recover project sessions: %v", err)
		return
	}
	for sessionID, control := range controls {
		if control.State == session.ControlReleased {
			continue
		}
		if _, err := s.projectManagedSession(control.ManagerID, sessionID); err != nil {
			continue
		}
		th, err := s.ensureOwnedThreadLoaded(sessionID)
		if err != nil || th == nil {
			continue
		}
		th.mu.Lock()
		var last *Turn
		if !th.running && len(th.Turns) > 0 {
			turn := th.Turns[len(th.Turns)-1]
			last = &turn
		}
		th.mu.Unlock()
		if last != nil {
			s.recordProjectResult(th, *last)
		}
	}
	targets, err := session.InboxTargets(s.rt.SessionDir)
	if err != nil {
		providers.DebugLogf("recover project inbox: %v", err)
		return
	}
	for _, target := range targets {
		s.drainSessionInbox(target)
	}
}

// ensureOwnedThreadLoaded loads a session only in the host that executes its
// workspace; another project's host leaves it alone.
func (s *Server) ensureOwnedThreadLoaded(id string) (*threadState, error) {
	metadata, found, err := session.Find(s.rt.SessionDir, id)
	if err != nil || !found || metadata.ArchivedAt != nil {
		return nil, err
	}
	root, workspaceID, err := s.sessionWorkspace(metadata)
	if err != nil || !s.ownsSessionWorkspace(root, workspaceID) {
		return nil, err
	}
	return s.ensureThreadLoaded(id)
}

// drainSessionInbox hands pending host input to its session: it steers a
// running turn or starts one. Input that cannot be admitted now stays pending
// for the session's next turn end, the next delivery, or the next start-up.
func (s *Server) drainSessionInbox(target string) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	th, err := s.ensureOwnedThreadLoaded(target)
	if err != nil || th == nil {
		return
	}
	pending, err := session.PendingInbox(s.rt.SessionDir, target)
	if err != nil {
		providers.DebugLogf("read inbox for %q: %v", target, err)
		return
	}
	for _, message := range pending {
		msg := providers.ChatMessage{
			Role: "user", Content: message.Content, ClientID: message.ClientID,
			Origin: pluginhost.SessionInputPlugin, Cause: "project",
			PresentationKind: pluginhost.SessionPresentationSessionMessage, RelatedSessionID: message.RelatedSessionID, ReadOnly: true,
		}
		if related := s.thread(message.RelatedSessionID); related != nil {
			related.mu.Lock()
			msg.Name = related.Title
			related.mu.Unlock()
		}
		permissions, err := s.resolveThreadTurnPermissions(th, nil)
		if err != nil {
			providers.DebugLogf("resolve inbox permissions for %q: %v", target, err)
			return
		}
		if _, ok, err := s.trySubmitSessionInput(context.Background(), th, msg, pluginhost.SessionIfRunningSteer, turnRuntimeSnapshot{}.withPermissions(permissions)); err != nil || !ok {
			if err != nil {
				providers.DebugLogf("deliver inbox %q: %v", message.ClientID, err)
			}
			return
		}
	}
}
