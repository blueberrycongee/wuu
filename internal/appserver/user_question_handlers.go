package appserver

import (
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type UserQuestionListParams struct {
	ThreadID string `json:"thread_id,omitempty"`
}

type UserQuestionListResult struct {
	Questions []pluginhost.UserQuestionRequest `json:"questions"`
}

type UserQuestionRespondParams struct {
	RequestID string                        `json:"request_id"`
	Answer    pluginhost.UserQuestionAnswer `json:"answer"`
}

type UserQuestionCancelParams struct {
	RequestID string `json:"request_id"`
}

type UserQuestionHoldParams struct {
	RequestID string `json:"request_id"`
}

type UserQuestionResolveResult struct {
	RequestID string `json:"request_id"`
	Resolved  bool   `json:"resolved"`
}

func (s *Server) bindUserQuestions() {
	if s == nil || s.rt == nil || s.rt.UserQuestions == nil {
		return
	}
	events, unsubscribe := s.rt.UserQuestions.Subscribe(64)
	s.userQuestionUnbind = unsubscribe
	s.userQuestionStop = make(chan struct{})
	s.userQuestionDone = make(chan struct{})
	go func() {
		defer close(s.userQuestionDone)
		for {
			select {
			case event := <-events:
				method := NotificationUserQuestionRequested
				if event.Type == pluginhost.UserQuestionResolved {
					method = NotificationUserQuestionResolved
				}
				_ = s.writeNotification(method, event)
			case <-s.userQuestionStop:
				return
			}
		}
	}()
}

func (s *Server) handleUserQuestionList(req Request) error {
	var params UserQuestionListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.rt == nil || s.rt.UserQuestions == nil {
		return s.writeResponse(req.ID, nil, errors.New("user interaction is unavailable"))
	}
	return s.writeResponse(req.ID, UserQuestionListResult{Questions: s.rt.UserQuestions.List(params.ThreadID)}, nil)
}

func (s *Server) handleUserQuestionRespond(req Request) error {
	var params UserQuestionRespondParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	requestID := strings.TrimSpace(params.RequestID)
	if requestID == "" {
		return s.writeResponse(req.ID, nil, errors.New("request_id is required"))
	}
	if s.rt == nil || s.rt.UserQuestions == nil {
		return s.writeResponse(req.ID, nil, errors.New("user interaction is unavailable"))
	}
	if err := s.rt.UserQuestions.Respond(requestID, params.Answer); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, UserQuestionResolveResult{RequestID: requestID, Resolved: true}, nil)
}

func (s *Server) handleUserQuestionCancel(req Request) error {
	var params UserQuestionCancelParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	requestID := strings.TrimSpace(params.RequestID)
	if requestID == "" {
		return s.writeResponse(req.ID, nil, errors.New("request_id is required"))
	}
	if s.rt == nil || s.rt.UserQuestions == nil {
		return s.writeResponse(req.ID, nil, errors.New("user interaction is unavailable"))
	}
	if err := s.rt.UserQuestions.Cancel(requestID); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, UserQuestionResolveResult{RequestID: requestID, Resolved: true}, nil)
}

func (s *Server) handleUserQuestionHold(req Request) error {
	var params UserQuestionHoldParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	requestID := strings.TrimSpace(params.RequestID)
	if requestID == "" {
		return s.writeResponse(req.ID, nil, errors.New("request_id is required"))
	}
	if s.rt == nil || s.rt.UserQuestions == nil {
		return s.writeResponse(req.ID, nil, errors.New("user interaction is unavailable"))
	}
	if err := s.rt.UserQuestions.Hold(requestID); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, UserQuestionResolveResult{RequestID: requestID, Resolved: false}, nil)
}
