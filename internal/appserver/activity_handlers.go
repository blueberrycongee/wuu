package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) handleActivityList(req Request) error {
	var params ActivityListParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("parse activity/list params: %w", err))
		}
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	if params.ThreadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("activity thread_id is required"))
	}
	registry, err := s.activityRegistry()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sessions := registry.List(params.ThreadID)
	activities := make([]ActivitySession, 0, len(sessions))
	for _, session := range sessions {
		activities = append(activities, activitySessionFromRuntime(session))
	}
	return s.writeResponse(req.ID, ActivityListResult{Activities: activities}, nil)
}

func (s *Server) handleActivityTakeover(req Request) error {
	params, err := parseActivityActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	registry, err := s.activityRegistry()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	current, err := registry.Takeover(params.ThreadID, params.ActivityID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if current.Kind == activity.KindBrowser {
		if err := s.takeHarnessControl(params.ThreadID, session.ControlPaused); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if _, err := s.interruptThreadExecution(params.ThreadID, "", ""); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	return s.writeResponse(req.ID, ActivityActionResult{Activity: activitySessionFromRuntime(current)}, nil)
}

func (s *Server) handleActivityRelease(req Request) error {
	params, err := parseActivityActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	registry, err := s.activityRegistry()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	session, lease, err := registry.Release(params.ThreadID, params.ActivityID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ActivityReleaseResult{Activity: activitySessionFromRuntime(session), LeaseToken: lease.Token}, nil)
}

func (s *Server) handleActivityStop(req Request) error {
	params, err := parseActivityActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	registry, err := s.activityRegistry()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	session, err := registry.Stop(params.ThreadID, params.ActivityID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ActivityActionResult{Activity: activitySessionFromRuntime(session)}, nil)
}

func parseActivityActionParams(req Request) (ActivityActionParams, error) {
	var params ActivityActionParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return params, fmt.Errorf("parse activity action params: %w", err)
		}
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.ActivityID = strings.TrimSpace(params.ActivityID)
	if params.ThreadID == "" || params.ActivityID == "" {
		return params, errors.New("activity thread_id and activity_id are required")
	}
	return params, nil
}

func (s *Server) activityRegistry() (*activity.Registry, error) {
	if s == nil || s.rt == nil || s.rt.ActivityRegistry == nil {
		return nil, errors.New("activity registry is unavailable")
	}
	return s.rt.ActivityRegistry, nil
}

func (s *Server) notifyActivityEvent(event activity.Event) {
	method := ""
	switch event.Type {
	case activity.EventStarted:
		method = NotificationActivityStarted
	case activity.EventUpdated:
		method = NotificationActivityUpdated
	case activity.EventControlChanged:
		method = NotificationActivityControlChanged
	case activity.EventStopped:
		method = NotificationActivityStopped
	}
	if method != "" {
		_ = s.writeNotification(method, activitySessionFromRuntime(event.Activity))
	}
}

func activitySessionFromRuntime(session activity.Session) ActivitySession {
	return ActivitySession{
		ID:          session.ID,
		Kind:        string(session.Kind),
		ThreadID:    session.ThreadID,
		Workdir:     session.Workdir,
		PluginID:    session.PluginID,
		Target:      session.Target,
		ProcessID:   session.ProcessID,
		WindowID:    session.WindowID,
		State:       string(session.State),
		Controller:  string(session.Controller),
		Preview:     session.Preview,
		Error:       session.Error,
		Interaction: session.Interaction,
		CreatedAt:   session.CreatedAt,
		UpdatedAt:   session.UpdatedAt,
	}
}
