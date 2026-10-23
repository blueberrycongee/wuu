package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
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

func (s *Server) projectSessionHandlerForHost(actorID string, host *pluginhost.Host) tools.ProjectSessionHandler {
	return func(ctx context.Context, callID string, request tools.ProjectSessionRequest) (any, error) {
		var raw json.RawMessage
		err := callProjectCoordination(ctx, host, "session", ProjectCoordinationRequest{ActorID: actorID, CallID: callID, Session: request}, &raw)
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
func (s *Server) projectWork(ctx context.Context, actorID, callID string, request tools.ProjectWorkRequest) (any, error) {
	return s.projectSessionHandlerForHost(actorID, s.rt.PluginHost)(ctx, callID, tools.ProjectSessionRequest{Action: "work", Work: &request})
}
func (s *Server) workForMember(projectID, memberID string) (session.ProjectWork, bool, error) {
	var result struct {
		Work  session.ProjectWork `json:"work"`
		Found bool                `json:"found"`
	}
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "member-work", ProjectCoordinationRequest{ProjectID: projectID, SessionID: memberID}, &result)
	return result.Work, result.Found, err
}
func (s *Server) projectCoordinator(id string) (session.Session, bool) {
	var result struct {
		Project session.Session `json:"project"`
		Found   bool            `json:"found"`
	}
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "coordinator", ProjectCoordinationRequest{ProjectID: id}, &result)
	return result.Project, err == nil && result.Found
}

func (s *Server) adoptProjectSession(projectID, sessionID string) (session.Session, error) {
	var result session.Session
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "membership", ProjectCoordinationRequest{ProjectID: projectID, SessionID: sessionID, Action: "adopt"}, &result)
	return result, err
}
func (s *Server) releaseProjectSession(projectID, sessionID string) (session.Session, error) {
	var result session.Session
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "membership", ProjectCoordinationRequest{ProjectID: projectID, SessionID: sessionID, Action: "release"}, &result)
	return result, err
}
func (s *Server) startProjectThread(selection session.RuntimeSelection, engineID agentengine.EngineID, params ThreadStartParams) (*threadState, error) {
	var result struct {
		SessionID string `json:"session_id"`
	}
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "create", ProjectCoordinationRequest{Selection: selection, EngineID: engineID, Start: params}, &result)
	if err != nil {
		return nil, err
	}
	return s.ensureThreadLoaded(result.SessionID)
}
func (s *Server) projectEvent(host *pluginhost.Host, method string, input ProjectCoordinationRequest) {
	if !projectAgentEnabled {
		return
	}
	if err := callProjectCoordination(context.Background(), host, method, input, nil); err != nil {
		providers.DebugLogf("project coordination %s: %v", method, err)
	}
}
func (s *Server) afterProjectTurnForHost(th *threadState, turn Turn, compactOnly bool, host *pluginhost.Host) {
	s.projectEvent(host, "turn", ProjectCoordinationRequest{SessionID: th.ID, Turn: turn, CompactOnly: compactOnly})
}

func (s *Server) noticeProjectUserMessage(th *threadState, msg providers.ChatMessage) {
	s.projectEvent(s.rt.PluginHost, "user-input", ProjectCoordinationRequest{SessionID: th.ID, Message: msg})
}
func (s *Server) enqueueProjectInput(projectID, relatedSessionID, clientID, cause, content string, wake bool) {
	s.projectEvent(s.rt.PluginHost, "enqueue", ProjectCoordinationRequest{ProjectID: projectID, SessionID: relatedSessionID, ClientID: clientID, Cause: cause, Content: content, Wake: wake})
}
func (s *Server) startProjectRecovery() {
	s.projectEvent(s.rt.PluginHost, "recover", ProjectCoordinationRequest{})
}

func (s *Server) drainSessionInboxForHost(id string, host *pluginhost.Host) {
	s.projectEvent(host, "inbox", ProjectCoordinationRequest{SessionID: id})
}
func (s *Server) kickProjectInboxDrain(id string) {
	s.projectEvent(s.rt.PluginHost, "schedule", ProjectCoordinationRequest{ProjectID: id})
}
func (s *Server) closeProjectInboxDrains() {
	host := s.rt.PluginHost
	if host == nil || host.ServiceRegistry() == nil {
		return
	}
	if id, ok := host.ServiceRegistry().ProviderPluginID(ProjectCoordinationService, 1); ok {
		host.RetirePlugin(context.Background(), id, errors.New("app-server closed"))
	}
}
func (s *Server) listProjectWork(projectID string, details bool) (any, error) {
	var result any
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "work-list", struct {
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

func (s *Server) deferProjectCapacityRetry(threadID, kind string) bool {
	var deferred bool
	err := callProjectCoordination(context.Background(), s.rt.PluginHost, "capacity", ProjectCoordinationRequest{SessionID: threadID, Action: kind}, &deferred)
	return err == nil && deferred
}
func (s *Server) recoverProjectInbox() {
	s.projectEvent(s.rt.PluginHost, "recover", ProjectCoordinationRequest{})
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
