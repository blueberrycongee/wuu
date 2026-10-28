package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func callProjectCoordination(ctx context.Context, host *pluginhost.Host, method string, input, output any) error {
	if !projectAgentEnabled {
		return errProjectAgentDisabled
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, callErr := host.CallService(ctx, ProjectCoordinationConsumer, ProjectCoordinationService, method, raw)
	if callErr != nil {
		switch callErr.Code {
		case "project_control_changed":
			return errors.Join(session.ErrControlChanged, callErr)
		case "project_work_changed":
			return errors.Join(session.ErrProjectWorkChanged, callErr)
		}
		return callErr
	}
	if output == nil {
		return nil
	}
	return json.Unmarshal(raw, output)
}

func (s *Server) projectSessionHandlerForState(actorID string, binding *runtime.State) tools.ProjectSessionHandler {
	return func(ctx context.Context, callID string, request tools.ProjectSessionRequest) (any, error) {
		var raw json.RawMessage
		err := callProjectCoordination(s.projectServiceContext(ctx, binding), binding.Host, "session", ProjectCoordinationRequest{ActorID: actorID, CallID: callID, Session: request}, &raw)
		if err != nil {
			return nil, err
		}
		return decodeProjectResult(request, raw)
	}
}
func decodeProjectResult(request tools.ProjectSessionRequest, raw json.RawMessage) (any, error) {
	switch request.Action {
	case "create", "side", "send", "wait":
		var view projectSessionView
		err := json.Unmarshal(raw, &view)
		return view, err
	case "work":
		if request.Work != nil && request.Work.Operation != "list" {
			var view projectWorkView
			err := json.Unmarshal(raw, &view)
			return view, err
		}
	}
	var value any
	err := json.Unmarshal(raw, &value)
	return value, err
}
func (s *Server) projectWork(binding *runtime.State, ctx context.Context, actorID, callID string, request tools.ProjectWorkRequest) (any, error) {
	return s.projectSessionHandlerForState(actorID, binding)(ctx, callID, tools.ProjectSessionRequest{Action: "work", Work: &request})
}
func (s *Server) workForMember(binding *runtime.State, projectID, memberID string) (session.ProjectWork, bool, error) {
	var result struct {
		Work  session.ProjectWork `json:"work"`
		Found bool                `json:"found"`
	}
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "member-work", ProjectCoordinationRequest{ProjectID: projectID, SessionID: memberID}, &result)
	return result.Work, result.Found, err
}
func (s *Server) projectCoordinator(binding *runtime.State, id string) (session.Session, bool) {
	var result struct {
		Project session.Session `json:"project"`
		Found   bool            `json:"found"`
	}
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "coordinator", ProjectCoordinationRequest{ProjectID: id}, &result)
	return result.Project, err == nil && result.Found
}

func (s *Server) adoptProjectSession(binding *runtime.State, projectID, sessionID string) (session.Session, error) {
	var result session.Session
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "membership", ProjectCoordinationRequest{ProjectID: projectID, SessionID: sessionID, Action: "adopt"}, &result)
	return result, err
}
func (s *Server) releaseProjectSession(binding *runtime.State, projectID, sessionID string) (session.Session, error) {
	var result session.Session
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "membership", ProjectCoordinationRequest{ProjectID: projectID, SessionID: sessionID, Action: "release"}, &result)
	return result, err
}
func (s *Server) startProjectThread(binding *runtime.State, selection session.RuntimeSelection, engineID agentengine.EngineID, params ThreadStartParams) (*threadState, error) {
	var result struct {
		SessionID string `json:"session_id"`
	}
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "create", ProjectCoordinationRequest{Selection: selection, EngineID: engineID, Start: params}, &result)
	if err != nil {
		return nil, err
	}
	return s.ensureThreadLoaded(binding, result.SessionID)
}
func (s *Server) projectEvent(binding *runtime.State, method string, input ProjectCoordinationRequest, thread *threadState) {
	if !projectAgentEnabled {
		return
	}
	ctx := s.projectServiceContext(context.Background(), binding)
	ctx.Value(projectCallScopeKey{}).(*projectCallScope).thread = thread
	if err := callProjectCoordination(ctx, binding.Host, method, input, nil); err != nil {
		providers.DebugLogf("project coordination %s: %v", method, err)
	}
}
func (s *Server) afterProjectTurnForState(th *threadState, turn Turn, compactOnly bool, binding *runtime.State) {
	s.projectEvent(binding, "turn", ProjectCoordinationRequest{SessionID: th.ID, Turn: turn, CompactOnly: compactOnly}, th)
}

func (s *Server) noticeProjectUserMessage(binding *runtime.State, th *threadState, msg providers.ChatMessage) {
	s.projectEvent(binding, "user-input", ProjectCoordinationRequest{SessionID: th.ID, Message: msg}, th)
}
func (s *Server) enqueueProjectInput(binding *runtime.State, projectID, relatedSessionID, clientID, cause, content string, wake bool) {
	s.projectEvent(binding, "enqueue", ProjectCoordinationRequest{ProjectID: projectID, SessionID: relatedSessionID, ClientID: clientID, Cause: cause, Content: content, Wake: wake}, nil)
}
func (s *Server) startProjectRecovery(binding *runtime.State) {
	s.projectEvent(binding, "recover", ProjectCoordinationRequest{}, nil)
}

func (s *Server) drainSessionInboxForState(id string, binding *runtime.State) {
	s.projectEvent(binding, "inbox", ProjectCoordinationRequest{SessionID: id}, nil)
}
func (s *Server) kickProjectInboxDrain(binding *runtime.State, id string) {
	s.projectEvent(binding, "schedule", ProjectCoordinationRequest{ProjectID: id}, nil)
}
func (s *Server) closeProjectInboxDrains() {
	s.projectRegistration.Close()
}
func (s *Server) listProjectWork(binding *runtime.State, projectID string, details bool) (any, error) {
	var result any
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "work-list", struct {
		ProjectID string `json:"project_id"`
		Details   bool   `json:"details"`
	}{projectID, details}, &result)
	return result, err
}

func ProjectInstructions(ctx context.Context, host *pluginhost.Host, metadata session.Session) (string, error) {
	if metadata.Source != projectSource && metadata.Source != projectSessionSource {
		return metadata.Instructions, nil
	}
	var result string
	err := callProjectCoordination(ctx, host, "instructions", metadata, &result)
	return result, err
}

func (s *Server) deferProjectCapacityRetry(binding *runtime.State, threadID, kind string) bool {
	var deferred bool
	err := callProjectCoordination(s.projectServiceContext(context.Background(), binding), binding.Host, "capacity", ProjectCoordinationRequest{SessionID: threadID, Action: kind}, &deferred)
	return err == nil && deferred
}
func (s *Server) recoverProjectInbox(binding *runtime.State) {
	s.projectEvent(binding, "recover", ProjectCoordinationRequest{}, nil)
}

func ProjectToolConfiguration(ctx context.Context, host *pluginhost.Host, metadata session.Session) (pluginhost.SessionToolPolicy, error) {
	var result pluginhost.SessionToolPolicy
	err := callProjectCoordination(ctx, host, "tool-config", metadata, &result)
	return result, err
}
func ProjectAdmission(ctx context.Context, host *pluginhost.Host, sessionID string) (ProjectAdmissionPolicy, error) {
	var result ProjectAdmissionPolicy
	err := callProjectCoordination(ctx, host, "admission", ProjectCoordinationRequest{SessionID: sessionID}, &result)
	if err == nil && result.MaxParallel < 0 {
		return result, errors.New("project admission capacity cannot be negative")
	}
	return result, err
}
