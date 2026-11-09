package appserver

import (
	"errors"
	"fmt"
)

// PushRegistrar is supplied by remote-host transports that can bind app-server
// push preferences to a specific paired device. Local desktop app-server
// sessions leave this nil, so device push RPCs fail explicitly.
type PushRegistrar interface {
	RegisterDevicePush(DevicePushRegisterParams) error
	UnregisterDevicePush(DevicePushUnregisterParams) error
}

type DevicePushRegisterParams struct {
	Token    string `json:"token,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type DevicePushUnregisterParams struct {
	Token    string `json:"token,omitempty"`
	Platform string `json:"platform,omitempty"`
}

func (s *Server) handleDevicePushRegister(req Request) error {
	if s.pushRegistrar == nil {
		return s.writeResponse(req.ID, nil, errors.New("device push registration is only available from a remote device session"))
	}
	var params DevicePushRegisterParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Token == "" {
		return s.writeResponse(req.ID, nil, errors.New("token is required"))
	}
	if err := s.pushRegistrar.RegisterDevicePush(params); err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("register device push: %w", err))
	}
	return s.writeResponse(req.ID, OKResult{OK: true}, nil)
}

func (s *Server) handleDevicePushUnregister(req Request) error {
	if s.pushRegistrar == nil {
		return s.writeResponse(req.ID, nil, errors.New("device push unregistration is only available from a remote device session"))
	}
	var params DevicePushUnregisterParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Token == "" {
		return s.writeResponse(req.ID, nil, errors.New("token is required"))
	}
	if err := s.pushRegistrar.UnregisterDevicePush(params); err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("unregister device push: %w", err))
	}
	return s.writeResponse(req.ID, OKResult{OK: true}, nil)
}
