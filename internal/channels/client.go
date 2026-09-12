package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AgentClient struct {
	service       *Service
	agentID       string
	principalKind PrincipalKind
	token         string
	sessionRef    string
}

func (s *Service) BindAgent(ctx context.Context, agentID string) (*AgentClient, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("named agent id is required")
	}
	token, err := s.loadAgentToken(ctx, agentID)
	if err != nil {
		return nil, err
	}
	_, err = s.GetNamedAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return &AgentClient{service: s, agentID: agentID, principalKind: PrincipalNamedAgent, token: token}, nil
}

func (s *Service) BindRuntime(ctx context.Context, runtimeID string) (*AgentClient, error) {
	return nil, fmt.Errorf("%w: room runtimes have been retired", ErrUnauthorized)
}

func (s *Service) BindAgentSession(ctx context.Context, agentID, sessionRef string) (*AgentClient, error) {
	client, err := s.BindAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	binding, err := s.GetCollaborationSession(ctx, client.agentID, client.token, strings.TrimSpace(sessionRef))
	if err != nil {
		return nil, err
	}
	if binding.PrincipalID != client.agentID {
		return nil, ErrUnauthorized
	}
	client.sessionRef = binding.SessionRef
	return client, nil
}

func (c *AgentClient) AgentID() string {
	if c == nil {
		return ""
	}
	return c.agentID
}

func (c *AgentClient) IsRoomRuntime() bool {
	if c == nil {
		return false
	}
	return c.principalKind == PrincipalRoomRuntime
}

func (c *AgentClient) SessionRef() string {
	if c == nil {
		return ""
	}
	return c.sessionRef
}

func (c *AgentClient) RoomRoster(ctx context.Context, roomID string) (RoomRoster, error) {
	if c == nil || c.service == nil {
		return RoomRoster{}, ErrUnauthorized
	}
	return c.service.RoomRoster(ctx, c.agentID, c.token, roomID)
}

func (c *AgentClient) InviteRoomAgent(ctx context.Context, roomID, agentID string) (Room, error) {
	if c == nil || c.service == nil {
		return Room{}, ErrUnauthorized
	}
	return c.service.InviteRoomAgent(ctx, c.agentID, c.token, roomID, agentID)
}

func (c *AgentClient) ProposeRoomAgent(ctx context.Context, roomID, name, role string) (AgentCreationProposal, error) {
	if c == nil || c.service == nil {
		return AgentCreationProposal{}, ErrUnauthorized
	}
	return c.service.ProposeRoomAgent(ctx, c.agentID, c.token, roomID, name, role)
}

func (c *AgentClient) Check(ctx context.Context) (CheckResult, error) {
	if c == nil || c.service == nil {
		return CheckResult{}, errors.New("chat agent is not bound")
	}
	if c.sessionRef != "" {
		return c.service.CheckSession(ctx, c.agentID, c.token, c.sessionRef)
	}
	return c.service.Check(ctx, c.agentID, c.token)
}

func (c *AgentClient) ReadInbox(ctx context.Context, itemIDs []string) ([]Message, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.ReadInboxMessages(ctx, c.agentID, c.token, itemIDs)
}

func (c *AgentClient) ReadRoom(ctx context.Context, roomID string, afterSeq int64, limit int) ([]Message, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.ReadAgentMessages(ctx, c.agentID, c.token, roomID, afterSeq, limit)
}

func (c *AgentClient) Send(ctx context.Context, params AgentSendParams) (SendResult, error) {
	if c == nil || c.service == nil {
		return SendResult{}, errors.New("chat agent is not bound")
	}
	if params.BasisSeq < 0 {
		return SendResult{}, errors.New("message basis sequence cannot be negative")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	params.SessionRef = c.sessionRef
	return c.service.SendAgent(ctx, params)
}

func (c *AgentClient) SendCollaboration(ctx context.Context, params CollaborationSendParams) (CollaborationMessage, error) {
	if c == nil || c.service == nil {
		return CollaborationMessage{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	if c.sessionRef != "" && params.FromSessionRef != "" && params.FromSessionRef != c.sessionRef {
		return CollaborationMessage{}, ErrUnauthorized
	}
	if params.FromSessionRef == "" {
		params.FromSessionRef = c.sessionRef
	}
	return c.service.SendCollaboration(ctx, params)
}

func (c *AgentClient) BindCollaborationSession(ctx context.Context, params CollaborationSessionBindParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.BindCollaborationSession(ctx, params)
}

func (c *AgentClient) GetCollaborationSession(ctx context.Context, sessionRef string) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, errors.New("chat agent is not bound")
	}
	return c.service.GetCollaborationSession(ctx, c.agentID, c.token, sessionRef)
}

func (c *AgentClient) ListCollaborationSessions(ctx context.Context, params CollaborationSessionListParams) ([]CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.ListCollaborationSessions(ctx, params)
}

func (c *AgentClient) UpdateCollaborationSessionState(ctx context.Context, params CollaborationSessionStateParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.UpdateCollaborationSessionState(ctx, params)
}

func (c *AgentClient) ListDrafts(ctx context.Context) ([]Draft, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.listDrafts(ctx, c.agentID, c.token, c.sessionRef)
}

func (c *AgentClient) CreateTask(ctx context.Context, params TaskCreateParams) (Message, error) {
	if c == nil || c.service == nil {
		return Message{}, errors.New("chat agent is not bound")
	}
	params.SourceSessionRef = c.sessionRef
	params.AgentID = c.agentID
	params.Token = c.token
	return c.service.CreateTask(ctx, params)
}

func (c *AgentClient) UpdateTask(ctx context.Context, params TaskUpdateParams) (Message, error) {
	if c == nil || c.service == nil {
		return Message{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	params.SessionRef = c.sessionRef
	return c.service.UpdateTask(ctx, params)
}

func (c *AgentClient) ListTasks(ctx context.Context, roomID string) ([]Message, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.ListTasks(ctx, TaskListParams{RoomID: roomID, AgentID: c.agentID, Token: c.token})
}

func (c *AgentClient) SubmitTaskVerification(ctx context.Context, params TaskVerificationSubmitParams) (TaskVerificationSubmitResult, error) {
	if c == nil || c.service == nil {
		return TaskVerificationSubmitResult{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	return c.service.SubmitTaskVerification(ctx, params)
}

func (c *AgentClient) SetReminder(ctx context.Context, params ReminderSetParams) (Reminder, error) {
	if c == nil || c.service == nil {
		return Reminder{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	return c.service.SetReminder(ctx, params)
}

func (c *AgentClient) SetReminderAfter(ctx context.Context, delay time.Duration, params ReminderSetParams) (Reminder, error) {
	if c == nil || c.service == nil {
		return Reminder{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	return c.service.SetReminderAfter(ctx, params, delay)
}

func (c *AgentClient) ListReminders(ctx context.Context, state ReminderState) ([]Reminder, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.ListReminders(ctx, ReminderListParams{AgentID: c.agentID, Token: c.token, State: state})
}

func (c *AgentClient) CancelReminder(ctx context.Context, reminderID string) (Reminder, error) {
	if c == nil || c.service == nil {
		return Reminder{}, errors.New("chat agent is not bound")
	}
	return c.service.CancelReminder(ctx, ReminderCancelParams{AgentID: c.agentID, Token: c.token, ReminderID: reminderID})
}

func (c *AgentClient) ResolveDraft(ctx context.Context, params ResolveDraftParams) (DraftResult, error) {
	if c == nil || c.service == nil {
		return DraftResult{}, errors.New("chat agent is not bound")
	}
	params.AgentID = c.agentID
	params.Token = c.token
	params.SessionRef = c.sessionRef
	return c.service.ResolveDraft(ctx, params)
}

func (c *AgentClient) GetWork(ctx context.Context, workID string) (Work, error) {
	if c == nil || c.service == nil {
		return Work{}, errors.New("chat agent is not bound")
	}
	work, err := c.service.GetWork(ctx, workID)
	if err != nil {
		return Work{}, err
	}
	if err := c.service.requireRoomPrincipalAccess(ctx, work.RoomID, c.agentID); err != nil {
		return Work{}, err
	}
	return work, nil
}

func (c *AgentClient) ListWorks(ctx context.Context, roomID string) ([]Work, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("chat agent is not bound")
	}
	return c.service.ListWorks(ctx, roomID, c.agentID, c.token)
}

func (c *AgentClient) StartWorkRun(ctx context.Context, params WorkRunStartParams) (WorkRun, error) {
	if c == nil || c.service == nil {
		return WorkRun{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	params.SourceSessionRef = c.sessionRef
	if params.SessionRef == "" && c.sessionRef != "" {
		binding, err := c.service.LookupCollaborationSession(ctx, c.sessionRef)
		if err != nil {
			return WorkRun{}, err
		}
		if binding.WorkID == params.WorkID && (params.NamedAgentID == "" || params.NamedAgentID == c.agentID) && params.Kind != WorkRunVerifier {
			params.SessionRef = c.sessionRef
		}
	}
	if params.NamedAgentID == "" && params.Kind != WorkRunVerifier {
		params.NamedAgentID = c.agentID
	}
	return c.service.StartWorkRun(ctx, params)
}

func (c *AgentClient) FinishWorkRun(ctx context.Context, params WorkRunFinishParams) (WorkRun, error) {
	if c == nil || c.service == nil {
		return WorkRun{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.FinishWorkRun(ctx, params)
}

func (c *AgentClient) AttachWorkRunTurn(ctx context.Context, params WorkRunTurnParams) (WorkRun, error) {
	if c == nil || c.service == nil {
		return WorkRun{}, errors.New("chat agent is not bound")
	}
	if c.sessionRef == "" {
		return WorkRun{}, errors.New("work run turn requires a bound collaboration session")
	}
	params.AgentID, params.Token = c.agentID, c.token
	if params.SessionRef == "" {
		params.SessionRef = c.sessionRef
	}
	return c.service.AttachWorkRunTurn(ctx, params)
}

func (c *AgentClient) AddWorkArtifact(ctx context.Context, params WorkArtifactAddParams) (WorkArtifact, error) {
	if c == nil || c.service == nil {
		return WorkArtifact{}, errors.New("chat agent is not bound")
	}
	if params.RunID == "" && c.sessionRef != "" {
		work, err := c.GetWork(ctx, params.WorkID)
		if err != nil {
			return WorkArtifact{}, err
		}
		for _, run := range work.Runs {
			if run.SessionRef == c.sessionRef && (run.State == WorkRunRunning || run.State == WorkRunQueued) {
				params.RunID = run.ID
				break
			}
		}
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.AddWorkArtifact(ctx, params)
}

func (c *AgentClient) PromoteWorkCandidate(ctx context.Context, params WorkCandidatePromoteParams) (Work, error) {
	if c == nil || c.service == nil {
		return Work{}, errors.New("chat agent is not bound")
	}
	if params.RunID == "" && params.ArtifactRef != "" {
		work, err := c.GetWork(ctx, params.WorkID)
		if err != nil {
			return Work{}, err
		}
		for _, artifact := range work.Artifacts {
			if artifact.ID == params.ArtifactRef {
				params.RunID = artifact.RunID
				break
			}
		}
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.PromoteWorkCandidate(ctx, params)
}

func (c *AgentClient) CancelWork(ctx context.Context, workID, reason string) (Work, error) {
	if c == nil || c.service == nil {
		return Work{}, errors.New("chat agent is not bound")
	}
	return c.service.CancelWork(ctx, workID, reason, c.agentID, c.token)
}

func (c *AgentClient) UpdateWorkPolicy(ctx context.Context, params WorkPolicyUpdateParams) (Work, error) {
	if c == nil || c.service == nil {
		return Work{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.UpdateWorkPolicy(ctx, params)
}

func (c *AgentClient) UpdateWorkEvidence(ctx context.Context, params WorkEvidenceUpdateParams) (Work, error) {
	if c == nil || c.service == nil {
		return Work{}, errors.New("chat agent is not bound")
	}
	params.AgentID, params.Token = c.agentID, c.token
	return c.service.UpdateWorkEvidence(ctx, params)
}

func (s *Service) BindRuntimeSession(ctx context.Context, runtimeID, sessionRef string) (*AgentClient, error) {
	client, err := s.BindRuntime(ctx, runtimeID)
	if err != nil {
		return nil, err
	}
	binding, err := s.GetCollaborationSession(ctx, client.agentID, client.token, strings.TrimSpace(sessionRef))
	if err != nil {
		return nil, err
	}
	if binding.PrincipalID != client.agentID {
		return nil, ErrUnauthorized
	}
	client.sessionRef = binding.SessionRef
	return client, nil
}

func (c *AgentClient) RoomPeers(ctx context.Context, roomID string) ([]AgentCapabilitySummary, error) {
	if c == nil || c.service == nil {
		return nil, ErrUnauthorized
	}
	return c.service.RoomPeers(ctx, c.agentID, c.token, roomID)
}
