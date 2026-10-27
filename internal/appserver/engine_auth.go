package appserver

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type EngineAuthParams struct {
	EngineID string `json:"engine_id"`
	MethodID string `json:"method_id,omitempty"`
}

func (s *Server) beginEngineAuth(ctx context.Context, id string) (context.Context, func(), error) {
	s.engineAuthMu.Lock()
	defer s.engineAuthMu.Unlock()
	if s.closed.Load() {
		return nil, nil, errServerClosed
	}
	if s.engineAuthCancels[id] != nil {
		return nil, nil, errors.New("an authentication operation is already running for this engine")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	if s.engineAuthCancels == nil {
		s.engineAuthCancels = make(map[string]context.CancelFunc)
	}
	s.engineAuthCancels[id] = cancel
	return ctx, func() {
		cancel()
		s.engineAuthMu.Lock()
		delete(s.engineAuthCancels, id)
		s.engineAuthMu.Unlock()
	}, nil
}

func (s *Server) cancelEngineAuth(id string) {
	s.engineAuthMu.Lock()
	defer s.engineAuthMu.Unlock()
	for engineID, cancel := range s.engineAuthCancels {
		if id == "" || id == engineID {
			cancel()
		}
	}
}

// Authentication is admitted before launching background work so cancellation
// and shutdown cannot miss a process between request receipt and startup.
func (s *Server) handleEngineAuth(ctx context.Context, req Request) error {
	var params EngineAuthParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.EngineID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("engine_id is required"))
	}
	if params.MethodID == "local-cli" && req.Method == MethodEngineAuthenticate {
		if s.remoteDevice {
			return s.writeResponse(req.ID, nil, errors.New("CLI sign-in must be started on the local desktop"))
		}
	}
	if req.Method == MethodEngineAuthCancel {
		s.cancelEngineAuth(id)
		return s.writeResponse(req.ID, map[string]bool{"ok": true}, nil)
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	if req.Method == MethodEngineAuthMethods {
		params.MethodID = ""
	} else if strings.TrimSpace(params.MethodID) == "" {
		return s.writeResponse(req.ID, nil, errors.New("method_id is required"))
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	authCtx, release, err := s.beginEngineAuth(ctx, id)
	if err != nil {
		binding.Close()
		return s.writeResponse(req.ID, nil, err)
	}
	if !s.startBackground(func() {
		defer binding.Close()
		var result pluginprotocol.EngineAuthResult
		err := binding.CallEngine(authCtx, agentengine.EngineID(id), "auth", pluginprotocol.EngineAuthRequest{MethodID: params.MethodID, Remote: s.remoteDevice}, &result)
		release()
		if writeErr := s.writeResponse(req.ID, result, err); writeErr != nil {
			log.Printf("wuu: engine authentication response: %v", writeErr)
		}
	}) {
		binding.Close()
		release()
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	return nil
}
