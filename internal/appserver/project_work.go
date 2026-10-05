package appserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

var errWorkInputNotConsumed = errors.New("this assignment has not been consumed")

// The public view excludes persisted outbox prompts and runtime snapshots.
// Models list compact summaries; get and the desktop reveal the contract.
type projectWorkView struct {
	ID               string              `json:"id"`
	ProjectID        string              `json:"project_id"`
	Version          int                 `json:"version"`
	Revision         int                 `json:"revision"`
	Title            string              `json:"title"`
	Phase            string              `json:"phase"`
	LeadID           string              `json:"lead_id"`
	ExecutorID       string              `json:"executor_id,omitempty"`
	Workspace        string              `json:"workspace"`
	Brief            string              `json:"brief,omitempty"`
	Acceptance       string              `json:"acceptance,omitempty"`
	Authority        string              `json:"authority,omitempty"`
	SourceRefs       string              `json:"source_refs,omitempty"`
	Summary          string              `json:"summary,omitempty"`
	Evidence         string              `json:"evidence,omitempty"`
	CodeRef          string              `json:"code_ref,omitempty"`
	Review           string              `json:"review,omitempty"`
	ReviewTurnID     string              `json:"review_turn_id,omitempty"`
	SubmissionTurnID string              `json:"submission_turn_id,omitempty"`
	Delivery         string              `json:"delivery,omitempty"`
	Blocker          string              `json:"blocker,omitempty"`
	LeadDispatch     *projectSessionView `json:"lead_dispatch,omitempty"`
	ExecutorDispatch *projectSessionView `json:"executor_dispatch,omitempty"`
	Usage            []projectRoleUsage  `json:"usage,omitempty"`
}

type projectRoleUsage struct {
	Role                string `json:"role"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
}

func (s *Server) workForMember(projectID, memberID string) (session.ProjectWork, bool, error) {
	works, err := session.ListProjectWork(s.rt.SessionDir, projectID)
	if err != nil {
		return session.ProjectWork{}, false, err
	}
	for _, w := range works {
		if w.LeadID == memberID || w.ExecutorID == memberID {
			return w, true, nil
		}
	}
	return session.ProjectWork{}, false, nil
}

func (s *Server) projectWork(ctx context.Context, actorID, callID string, r tools.ProjectWorkRequest) (any, error) {
	project, actor, _, err := s.projectActor(actorID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	coordinator := actor.ID == project.ID
	operationID := "project-work:" + actor.ID + ":" + callID
	if r.Operation == "list" {
		return s.listProjectWork(project.ID, false)
	}
	if r.Operation != "get" {
		if prior, ok, err := session.ProjectWorkOperation(s.rt.SessionDir, operationID); err != nil {
			return nil, err
		} else if ok {
			// Drain the current outbox, never the stale receipt's earlier instruction.
			current, err := session.ReadProjectWork(s.rt.SessionDir, prior.ID)
			if err != nil {
				return nil, err
			}
			if err = s.reconcileProjectWork(current); err != nil {
				return nil, err
			}
			return s.projectWorkView(current, true)
		}
	}
	var w session.ProjectWork
	if r.Operation == "create" {
		if !coordinator {
			return nil, errors.New("only the project coordinator creates work")
		}
		if strings.TrimSpace(r.Brief) == "" || strings.TrimSpace(r.Acceptance) == "" || strings.TrimSpace(r.Authority) == "" {
			return nil, errors.New("create requires brief, acceptance and actual user authority")
		}
		leadModel, err := s.projectWorkModel(project, s.rt.ProjectModels.TechnicalLead, r.ModelAlias)
		if err != nil {
			return nil, err
		}
		executorDefault := s.rt.ProjectModels.Executor
		if executorDefault.Provider == "" {
			executorDefault = s.rt.ProjectModels.Worker
		}
		executorModel, err := s.projectWorkModel(project, executorDefault, "")
		if err != nil {
			return nil, err
		}
		workspace := "shared"
		if worktree.IsGitRepo(project.CWD) {
			workspace = "worktree"
		}
		w = session.ProjectWork{ID: session.NewID(), ProjectID: project.ID, LeadID: session.NewID(), Revision: 1, Title: firstNonEmpty(strings.TrimSpace(r.Title), excerpt(r.Brief, 60)), Brief: r.Brief, Acceptance: r.Acceptance, Authority: r.Authority, SourceRefs: r.SourceRefs, Phase: "planning", Workspace: workspace, LeadModel: leadModel, ExecutorModel: executorModel}
		w.LeadInput = s.workInput(w, operationID, project.ID, workContract(w))
	} else {
		w, err = session.ReadProjectWork(s.rt.SessionDir, r.WorkID)
		if err != nil {
			return nil, err
		}
		if w.ProjectID != project.ID {
			return nil, errors.New("work belongs to another project")
		}
		if r.Operation == "get" {
			return s.projectWorkView(w, true)
		}
		if !coordinator && actor.ID != w.LeadID && actor.ID != w.ExecutorID {
			return nil, errors.New("only this work's participants can change it")
		}
		if r.Revision != w.Revision {
			return nil, session.ErrProjectWorkChanged
		}
		if w.Phase == "stopped" && r.Operation != "resume" && r.Operation != "stop" {
			return nil, errors.New("work is stopped; only an explicit coordinator resume can restart it")
		}
		switch r.Operation {
		case "update", "resume":
			if !coordinator {
				return nil, errors.New("only the coordinator changes requirements or resumes work")
			}
			if strings.TrimSpace(r.Brief) == "" || strings.TrimSpace(r.Acceptance) == "" || strings.TrimSpace(r.Authority) == "" {
				return nil, errors.New("update/resume requires the complete brief, acceptance and authority")
			}
			if r.Operation == "resume" && w.Phase != "stopped" && w.Phase != "blocked" {
				return nil, errors.New("only stopped or blocked work needs resume")
			}
			w.Revision++
			w.Brief, w.Acceptance, w.Authority, w.SourceRefs = r.Brief, r.Acceptance, r.Authority, r.SourceRefs
			w.Phase, w.Summary, w.Evidence, w.CodeRef, w.Review, w.ReviewTurnID, w.Delivery, w.Blocker, w.SubmissionTurnID = "planning", "", "", "", "", "", "", "", ""
			w.ExecutorInput = nil
			w.LeadInput = s.workInput(w, operationID, actor.ID, workContract(w))
		case "execute":
			if actor.ID != w.LeadID {
				return nil, errors.New("only the technical lead dispatches this work's executor")
			}
			if strings.TrimSpace(r.Brief) == "" {
				return nil, errors.New("execute requires a scoped technical brief")
			}
			if active, err := s.workMemberActive(w.ExecutorID); err != nil {
				return nil, err
			} else if active {
				return nil, errors.New("executor is still running; message it for a correction or stop the work before taking over")
			}
			if w.ExecutorID == "" {
				w.ExecutorID = session.NewID()
				if r.ModelAlias != "" {
					w.ExecutorModel, err = s.projectWorkModel(project, config.ModelRoleConfig{}, r.ModelAlias)
					if err != nil {
						return nil, err
					}
				}
			} else if r.ModelAlias != "" {
				return nil, errors.New("existing executors retain their saved model; change the session model explicitly")
			}
			w.Phase, w.Summary, w.Evidence, w.CodeRef, w.Review, w.ReviewTurnID, w.Delivery, w.Blocker, w.SubmissionTurnID = "executing", "", "", "", "", "", "", "", ""
			w.ExecutorInput = s.workInput(w, operationID, actor.ID, workContract(w)+"\n\nTechnical execution brief:\n"+r.Brief)
		case "submit":
			if actor.ID != w.ExecutorID || w.Phase != "executing" {
				return nil, errors.New("only the assigned executor can submit executing work")
			}
			if err = s.requireWorkEvidence(r); err != nil {
				return nil, err
			}
			if w.ExecutorInput == nil {
				return nil, errors.New("executor has no assignment")
			}
			turnID, err := s.consumedWorkTurn(w.ExecutorID, w.ExecutorInput.ClientID)
			if err != nil {
				return nil, err
			}
			w.Phase, w.Summary, w.Evidence, w.CodeRef, w.SubmissionTurnID = "verifying", r.Summary, r.Evidence, r.CodeRef, turnID
		case "review":
			if actor.ID != w.LeadID {
				return nil, errors.New("only the technical lead can accept the result")
			}
			if err = s.requireWorkEvidence(r); err != nil {
				return nil, err
			}
			if active, err := s.workMemberActive(w.ExecutorID); err != nil {
				return nil, err
			} else if active {
				return nil, errors.New("executor has not exited; inspect its exact terminal result before accepting")
			}
			if w.ExecutorInput != nil {
				if w.Phase != "reviewing" || w.CodeRef != r.CodeRef || w.SubmissionTurnID == "" {
					return nil, errors.New("review must match a successfully completed submission and its exact code_ref")
				}
			} else if w.Phase != "planning" {
				return nil, errors.New("work is not ready for direct technical review")
			}
			if w.LeadInput == nil {
				return nil, errors.New("technical lead has no current brief")
			}
			consumed, err := s.consumedWorkTurn(w.LeadID, w.LeadInput.ClientID)
			if err != nil {
				return nil, err
			}
			turnID, err := s.currentProjectWorkTurn(w.LeadID)
			if consumed != turnID {
				return nil, errors.New("technical lead has not consumed the current review brief")
			}
			if err != nil {
				return nil, err
			}
			w.Phase, w.Summary, w.CodeRef, w.Review, w.ReviewTurnID, w.Blocker = "accepted", r.Summary, r.CodeRef, r.Evidence, turnID, ""
			if w.ExecutorInput == nil {
				w.Evidence = r.Evidence
			}
		case "dispatch_delivery":
			if !coordinator || w.Phase != "accepted" || strings.TrimSpace(r.Brief) == "" {
				return nil, errors.New("the coordinator can dispatch authorized delivery only after technical acceptance")
			}
			w.Phase = "delivering"
			w.LeadInput = s.workInput(w, operationID, actor.ID, workContract(w)+"\n\nAuthorized delivery instruction:\n"+r.Brief+"\nAccepted code/content: "+w.CodeRef+"\nPreserve the accepted content. If changes are needed, start a new execution phase and review it. Call complete_delivery with the actual destination only after delivery succeeds.")
		case "complete_delivery":
			if actor.ID != w.LeadID || w.Phase != "delivering" || r.CodeRef != w.CodeRef || strings.TrimSpace(r.Delivery) == "" {
				return nil, errors.New("the technical lead must report the actual delivery of the accepted code_ref")
			}
			consumed, err := s.consumedWorkTurn(w.LeadID, w.LeadInput.ClientID)
			if err != nil {
				return nil, err
			}
			current, err := s.currentProjectWorkTurn(w.LeadID)
			if err != nil {
				return nil, err
			}
			if consumed != current {
				return nil, errors.New("delivery instruction has not been consumed by this turn")
			}
			w.Phase, w.Delivery, w.DeliveryBy = "delivered", r.Delivery, actor.ID

		case "deliver":
			if !coordinator || w.Phase != "accepted" || w.Review == "" {
				return nil, errors.New("only the coordinator can deliver reviewed work for the current revision")
			}
			if strings.TrimSpace(r.Delivery) == "" {
				return nil, errors.New("delivery needs the actual authorized destination and result")
			}
			w.Phase, w.Delivery, w.DeliveryBy = "delivered", r.Delivery, actor.ID
		case "block":
			if actor.ID != w.LeadID || strings.TrimSpace(r.Summary) == "" {
				return nil, errors.New("the technical lead must describe the actionable blocker")
			}
			w.Phase, w.Blocker = "blocked", r.Summary
		case "stop":
			if !coordinator && actor.ID != w.LeadID {
				return nil, errors.New("only the coordinator or technical lead stops the complete work")
			}
			w.Phase, w.Blocker = "stopped", ""
			w.Revision++
			w.LeadInput, w.ExecutorInput = nil, nil
		default:
			return nil, errors.New("unknown work operation")
		}
	}
	w, err = session.SaveProjectWork(s.rt.SessionDir, w, w.Version, operationID)
	if err != nil {
		return nil, err
	}
	s.startBackground(func() { s.kickProjectInboxDrain(w.ProjectID) })

	// Requirement corrections fence writes immediately and interrupt stale execution.
	// Execution leases remain held until the owning processes really exit.
	if r.Operation == "update" || r.Operation == "resume" {
		if err = s.interruptWorkMember(w.ExecutorID); err != nil {
			return nil, err
		}
	}
	if err = s.reconcileProjectWork(w); err != nil {
		return nil, err
	}
	s.publishProjectWork(w)
	return s.projectWorkView(w, true)
}

func (s *Server) listProjectWork(projectID string, details bool) (any, error) {
	works, err := session.ListProjectWork(s.rt.SessionDir, projectID)
	if err != nil {
		return nil, err
	}
	views := make([]projectWorkView, 0, len(works))
	roles := map[string]string{projectID: "coordinator"}
	for _, w := range works {
		v, err := s.projectWorkView(w, false)
		if err != nil {
			return nil, err
		}
		if !details {
			v.Brief, v.Acceptance, v.Authority, v.SourceRefs, v.Evidence, v.Review = "", "", "", "", "", ""
		}
		views = append(views, v)
		roles[w.LeadID] = "technical_lead"
		if w.ExecutorID != "" {
			roles[w.ExecutorID] = "executor"
		}
	}
	if members, err := s.projectSessions(projectID); err != nil {
		return nil, err
	} else {
		for _, member := range members {
			if _, known := roles[member.ID]; !known {
				roles[member.ID] = "legacy_" + projectRoleForSession(member)
			}
		}
	}

	usage, err := s.projectWorkUsage(roles)
	return map[string]any{"project_id": projectID, "works": views, "usage": usage}, err
}

func (s *Server) workInput(w session.ProjectWork, id, sender, brief string) *session.ProjectWorkInput {
	return &session.ProjectWorkInput{ClientID: id, SenderID: sender, Brief: fmt.Sprintf("Work %s; requirement revision %d.\n%s", w.ID, w.Revision, brief), Revision: w.Revision}
}
func workContract(w session.ProjectWork) string {
	return fmt.Sprintf("Outcome: %s\nUser requirements:\n%s\nAcceptance:\n%s\nUser authority and limits:\n%s\nSource references and dependencies:\n%s", w.Title, w.Brief, w.Acceptance, w.Authority, w.SourceRefs)
}
func (s *Server) requireWorkEvidence(r tools.ProjectWorkRequest) error {
	if strings.TrimSpace(r.Summary) == "" || strings.TrimSpace(r.Evidence) == "" || strings.TrimSpace(r.CodeRef) == "" {
		return errors.New("summary, verification/review evidence and exact code_ref are required")
	}
	return nil
}
func (s *Server) workMemberActive(id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	return session.ThreadExecutionActive(s.rt.SessionDir, id)
}
func (s *Server) interruptWorkMember(id string) error {
	if id == "" {
		return nil
	}
	s.revokeSessionInputs(id)
	return s.interruptOwnedThreadExecution(id)
}
func (s *Server) currentProjectWorkTurn(id string) (string, error) {
	th, err := s.ensureThreadLoaded(id)
	if err != nil {
		return "", err
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if len(th.Turns) == 0 {
		return "", errors.New("a technical decision requires an attributable turn")
	}
	return th.Turns[len(th.Turns)-1].ID, nil
}
func (s *Server) consumedWorkTurn(id, clientID string) (string, error) {
	turns, err := s.loadDurableProjectTurns(id)
	if err != nil {
		return "", err
	}
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.SourceID == clientID {
				return turn.ID, nil
			}
		}
	}
	return "", errWorkInputNotConsumed
}

func (s *Server) projectWorkModel(project session.Session, selection config.ModelRoleConfig, alias string) (session.RuntimeSelection, error) {
	model := runtimeSelectionFromSession(project)
	if selection.Provider != "" {
		model.Provider, model.Model, model.Variant, model.Effort = selection.Provider, selection.Model, selection.Variant, selection.Effort
	}
	if alias != "" {
		resolved := s.resolveSubagentModelAlias(alias)
		if resolved.Err != nil {
			return model, resolved.Err
		}
		if !resolved.Found {
			return model, fmt.Errorf("unknown model alias %q", alias)
		}
		model.Provider, model.Model, model.Variant, model.Effort = resolved.Runtime.Provider, resolved.Runtime.Model, resolved.Runtime.Variant, resolved.Runtime.Effort
	}
	return model, nil
}

func (s *Server) reconcileProjectWork(w session.ProjectWork) (resultErr error) {
	current, err := session.ReadProjectWork(s.rt.SessionDir, w.ID)
	if err != nil {
		return err
	}
	if current.Phase == "stopped" {
		if err := errors.Join(s.interruptWorkMember(current.ExecutorID), s.interruptWorkMember(current.LeadID)); err != nil {
			return err
		}
		return session.ReconcileProjectWork(s.rt.SessionDir, current.ID, current.Version)
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(s.rt.SessionDir, "project-work-reconcile:"+w.ID)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer lease.Release()
	w, err = session.ReadProjectWork(s.rt.SessionDir, w.ID)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr == nil {
			resultErr = session.ReconcileProjectWork(s.rt.SessionDir, w.ID, w.Version)
		}
	}()

	if w.Phase == "stopped" {
		return errors.Join(s.interruptWorkMember(w.ExecutorID), s.interruptWorkMember(w.LeadID))
	}
	if _, live := s.projectCoordinator(w.ProjectID); !live {
		return nil
	}
	if err := s.ensureWorkMember(w, w.LeadID, "technical_lead", w.LeadModel, ""); err != nil {
		return err
	}
	if w.ExecutorID != "" {
		if err := s.ensureWorkMember(w, w.ExecutorID, "executor", w.ExecutorModel, w.LeadID); err != nil {
			return err
		}
	}
	// Old assignments are retained as evidence but never restarted after acceptance.
	if w.Phase == "planning" && w.ExecutorInput == nil {
		if err := s.interruptWorkMember(w.ExecutorID); err != nil {
			return err
		}
	}
	if w.Phase == "planning" || w.Phase == "reviewing" || w.Phase == "delivering" {
		if err := s.enqueueWorkBrief(w, w.LeadID, w.LeadInput); err != nil {
			return err
		}
	}
	if w.Phase == "executing" {
		if err := s.enqueueWorkBrief(w, w.ExecutorID, w.ExecutorInput); err != nil {
			return err
		}
	}
	if w.Phase == "accepted" || w.Phase == "blocked" || (w.Phase == "delivered" && w.DeliveryBy == w.LeadID) {
		message := fmt.Sprintf("Work %q (%s), requirement revision %d: %s.\n%s\nTechnical review: %s\nCode/content version: %s\nBlocker: %s\nDelivery: %s", w.Title, w.ID, w.Revision, w.Phase, w.Summary, w.Review, w.CodeRef, w.Blocker, w.Delivery)
		if err := session.EnqueueInbox(s.rt.SessionDir, session.InboxMessage{ClientID: fmt.Sprintf("project-work-result:%s:%d", w.ID, w.Version), SessionID: w.ProjectID, RelatedSessionID: w.LeadID, Cause: projectCauseResult, Content: message, Wake: true, WorkID: w.ID, WorkRevision: w.Revision, WorkVersion: w.Version}); err != nil {
			return err
		}
		s.startBackground(func() { s.drainSessionInbox(w.ProjectID) })
	}
	return nil
}

func (s *Server) ensureWorkMember(w session.ProjectWork, id, role string, model session.RuntimeSelection, sharedWith string) error {
	s.projectCreateMu.Lock()
	defer s.projectCreateMu.Unlock()
	if metadata, found, err := session.Find(s.rt.SessionDir, id); err != nil {
		return err
	} else if found {
		if metadata.Source != projectSessionSource || metadata.ParentID != w.ProjectID || metadata.ArchivedAt != nil {
			return errors.New("work member was removed or archived; it cannot be silently replaced")
		}
		return nil
	}
	project, live := s.projectCoordinator(w.ProjectID)
	if !live {
		return errors.New("project is no longer active")
	}
	input := w.LeadInput
	if role == "executor" {
		input = w.ExecutorInput
	}
	if input == nil {
		return errors.New("missing durable assignment for work member")
	}
	initial := session.InboxMessage{ClientID: input.ClientID, RelatedSessionID: input.SenderID, Cause: "project", Content: input.Brief, Wake: true, WorkID: w.ID, WorkRevision: input.Revision}
	workspace := w.Workspace
	if sharedWith != "" {
		workspace = "shared"
	}
	_, err := s.createHostSessionThread(projectSessionOwner, projectSessionSource, id, hostSessionCreateParams{
		RequestID: "project-work-member:" + id, Name: w.Title + " · " + role, Visibility: sessionVisibilityUser, ContextSource: sessionContextFresh,
		ParentSessionID: w.ProjectID, Workspace: workspace, WorkspaceID: project.WorkspaceID, SharedWorkspaceSessionID: sharedWith,
		Provider: model.Provider, Model: model.Model, Variant: model.Variant, Effort: model.Effort, PermissionMode: project.PermissionMode, ProjectRole: role, InitialInput: &initial,
	})
	return err
}

func (s *Server) enqueueWorkBrief(w session.ProjectWork, target string, input *session.ProjectWorkInput) error {
	if input == nil || target == "" {
		return nil
	}
	control, ok, err := session.ReadControl(s.rt.SessionDir, target)
	if err != nil {
		return err
	}
	if !ok || control.ManagerID != w.ProjectID || control.State != session.ControlActive {
		return session.ErrControlChanged
	}
	message := session.InboxMessage{ClientID: input.ClientID, SessionID: target, RelatedSessionID: input.SenderID, Cause: "project", Content: input.Brief, Wake: true, Controls: []session.Control{control}, WorkID: w.ID, WorkRevision: input.Revision}
	if err := session.EnqueueInbox(s.rt.SessionDir, message); err != nil {
		return err
	}
	s.startBackground(func() { s.drainSessionInbox(target) })
	return nil
}

func (s *Server) projectWorkView(w session.ProjectWork, includeUsage bool) (projectWorkView, error) {
	view := projectWorkView{ID: w.ID, ProjectID: w.ProjectID, Version: w.Version, Revision: w.Revision, Title: w.Title, Phase: w.Phase, LeadID: w.LeadID, ExecutorID: w.ExecutorID, Workspace: w.Workspace,
		Brief: w.Brief, Acceptance: w.Acceptance, Authority: w.Authority, SourceRefs: w.SourceRefs, Summary: w.Summary, Evidence: w.Evidence, CodeRef: w.CodeRef, Review: w.Review, ReviewTurnID: w.ReviewTurnID, SubmissionTurnID: w.SubmissionTurnID, Delivery: w.Delivery, Blocker: w.Blocker}
	for _, entry := range []struct {
		id    string
		input *session.ProjectWorkInput
		dst   **projectSessionView
	}{{w.LeadID, w.LeadInput, &view.LeadDispatch}, {w.ExecutorID, w.ExecutorInput, &view.ExecutorDispatch}} {
		if entry.input == nil {
			continue
		}
		metadata, found, err := session.Find(s.rt.SessionDir, entry.id)
		if err != nil {
			return view, err
		}
		if !found {
			continue
		}
		receipt, err := s.projectDispatchReceipt(metadata, entry.input.ClientID)
		if err != nil {
			return view, err
		}
		*entry.dst = &receipt
	}
	var err error
	if includeUsage {
		view.Usage, err = s.projectWorkUsage(map[string]string{w.LeadID: "technical_lead", w.ExecutorID: "executor"})
	}
	return view, err
}

func (s *Server) projectWorkUsage(roles map[string]string) ([]projectRoleUsage, error) {
	rows, err := session.ListTokenUsage(s.rt.SessionDir)
	if err != nil {
		return nil, err
	}
	usage := make([]projectRoleUsage, 0)
	indices := map[string]int{}
	for _, row := range rows {
		role, ok := roles[row.SessionID]
		if !ok || row.SessionID == "" {
			continue
		}
		key := role + ":" + row.Provider + ":" + row.Model
		i, ok := indices[key]
		if !ok {
			i = len(usage)
			indices[key] = i
			usage = append(usage, projectRoleUsage{Role: role, Provider: row.Provider, Model: row.Model})
		}
		u := &usage[i]
		u.InputTokens += row.InputTokens
		u.OutputTokens += row.OutputTokens
		u.CacheReadTokens += row.CacheReadTokens
		u.CacheCreationTokens += row.CacheCreationTokens
	}
	return usage, nil
}

func (s *Server) publishProjectWork(w session.ProjectWork) {
	if err := s.writeNotification("project/work/updated", map[string]any{"project_id": w.ProjectID, "work_id": w.ID}); err != nil {
		providers.DebugLogf("publish project work: %v", err)
	}
}

// recordWorkTurn suppresses member-level reports to the coordinator. Only the
// owning technical lead receives executor detail; turn completion is not review.
func (s *Server) recordWorkTurn(th *threadState, turn Turn) (bool, error) {
	w, found, err := s.workForMember(th.ProjectID, th.ID)
	if err != nil || !found {
		return found, err
	}
	if w.Phase == "stopped" {
		return true, nil
	}
	if th.ID == w.LeadID {
		if w.LeadInput == nil {
			return true, nil
		}
		assigned, err := s.consumedWorkTurn(th.ID, w.LeadInput.ClientID)
		if errors.Is(err, errWorkInputNotConsumed) {
			return true, nil
		}
		if err != nil || assigned != turn.ID {
			return true, err
		}
	}
	if th.ID == w.ExecutorID {
		if w.ExecutorInput == nil {
			return true, nil
		}
		assigned, err := s.consumedWorkTurn(th.ID, w.ExecutorInput.ClientID)
		if err != nil || assigned != turn.ID {
			return true, err
		}
		if w.ExecutorInput.Revision != w.Revision {
			return true, nil
		}
		if turn.Status == TurnStatusCompleted && w.Phase == "verifying" && w.SubmissionTurnID == turn.ID {
			w.Phase = "reviewing"
		} else if turn.Status == TurnStatusInterrupted {
			w.Phase, w.Blocker = "stopped", "Execution interrupted; explicit resume required"
			w.Revision++
			w.LeadInput, w.ExecutorInput = nil, nil
		} else if w.Phase == "executing" || w.Phase == "verifying" {
			w.Phase, w.Blocker = "reviewing", "Executor ended without successful verified submission; inspect before retrying"
		} else {
			return true, nil
		}
		if w.Phase == "reviewing" {
			message := fmt.Sprintf("Work %s revision %d requires technical review. Executor turn %s: %s.\n%s\nEvidence: %s\nCode/content: %s\n%s", w.ID, w.Revision, turn.ID, turn.Status, finalAnswerText(turn), w.Evidence, w.CodeRef, w.Blocker)
			w.LeadInput = s.workInput(w, projectResultClientID(th.ID, turn.ID), th.ID, message)
		}
		saved, err := session.SaveProjectWork(s.rt.SessionDir, w, w.Version, projectResultClientID(th.ID, turn.ID)+":work")
		if err != nil {
			return true, err
		}
		s.publishProjectWork(saved)
		if saved.Phase == "stopped" {
			return true, nil
		}
		return true, s.enqueueWorkBrief(saved, w.LeadID, saved.LeadInput)
	}
	if th.ID == w.LeadID && (turn.Status == TurnStatusFailed || turn.Status == TurnStatusInterrupted || w.Phase == "planning" || w.Phase == "reviewing" || w.Phase == "delivering") && w.Phase != "accepted" && w.Phase != "delivered" {
		w.Phase, w.Blocker = "blocked", "Technical lead did not complete its turn; inspect and explicitly resume"
		if turn.Status == TurnStatusInterrupted {
			w.Phase = "stopped"
			w.Revision++
			w.LeadInput, w.ExecutorInput = nil, nil
		}
		saved, err := session.SaveProjectWork(s.rt.SessionDir, w, w.Version, projectResultClientID(th.ID, turn.ID)+":work")
		if err != nil {
			return true, err
		}
		s.publishProjectWork(saved)
		return true, s.reconcileProjectWork(saved)
	}
	return true, nil
}

func (s *Server) recoverProjectWork(projectID string) error {
	works, err := session.ListProjectWork(s.rt.SessionDir, projectID)
	if err != nil {
		return err
	}
	for _, w := range works {
		for _, memberID := range []string{w.LeadID, w.ExecutorID} {
			if memberID == "" || w.Phase == "stopped" || w.Phase == "accepted" || w.Phase == "delivered" || w.Phase == "blocked" {
				continue
			}
			active, err := s.workMemberActive(memberID)
			if err != nil {
				return err
			}
			if active {
				continue
			}
			turns, err := s.loadDurableProjectTurns(memberID)
			if err != nil {
				continue
			}
			if len(turns) > 0 && turns[len(turns)-1].Status == TurnStatusInProgress {
				w.Phase, w.Blocker = "blocked", "Execution ended without durable terminal evidence; inspect and explicitly resume"
				w, err = session.SaveProjectWork(s.rt.SessionDir, w, w.Version, fmt.Sprintf("work-recovery:%s:%d", w.ID, w.Version))
				if err != nil {
					return err
				}
			}
		}
		if err := s.reconcileProjectWork(w); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// The desktop exposes only user status and explicit control actions. Technical
// lifecycle transitions remain attributed to the responsible model session.
func (s *Server) handleProjectWork(req Request) error {
	var p struct {
		ProjectID string `json:"project_id"`
		RequestID string `json:"request_id"`
		tools.ProjectWorkRequest
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	switch p.Operation {
	case "list", "get":
	case "stop", "resume":
		if p.RequestID == "" {
			return s.writeResponse(req.ID, nil, errors.New("request_id is required for work control"))
		}
	default:
		return s.writeResponse(req.ID, nil, errors.New("desktop work control supports list, get, stop and resume"))
	}
	if p.Operation == "list" {
		if _, _, _, err := s.projectActor(p.ProjectID); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		result, err := s.listProjectWork(p.ProjectID, true)
		return s.writeResponse(req.ID, result, err)
	}

	result, err := s.projectWork(context.Background(), p.ProjectID, "desktop:"+p.RequestID, p.ProjectWorkRequest)
	return s.writeResponse(req.ID, result, err)
}
