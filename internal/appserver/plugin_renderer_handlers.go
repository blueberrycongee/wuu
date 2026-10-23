package appserver

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
)

type rendererClientScope struct {
	mu      sync.Mutex
	id      string
	closed  bool
	clients map[string]struct{}
}

type PluginDesktopActivationParams struct {
	ID              string `json:"id"`
	Fingerprint     string `json:"fingerprint"`
	GraphGeneration string `json:"graph_generation"`
	RendererID      string `json:"renderer_id"`
	Disconnected    bool   `json:"disconnected,omitempty"`
	pluginhost.RendererActivationReport
}

func (s *Server) handlePluginDesktopActivation(req Request) error {
	var params PluginDesktopActivationParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if len(params.RendererID) > 128 {
		return s.writeResponse(req.ID, nil, errors.New("invalid renderer identity"))
	}
	// A device-owned stdio connection has one renderer. Multiplexed desktop
	// transports supply their own owner IDs, still scoped to this connection.
	if params.RendererID == "" {
		params.RendererID = "primary"
	}
	s.rendererClients.mu.Lock()
	defer s.rendererClients.mu.Unlock()
	if s.rendererClients.closed {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	if s.rendererClients.id == "" {
		s.rendererClients.id = session.NewID()
		s.rendererClients.clients = make(map[string]struct{})
	}
	identity := s.rendererClients.id + ":" + params.RendererID
	if params.Disconnected {
		s.rt.ForgetRenderer(identity)
		delete(s.rendererClients.clients, identity)
		return s.writeResponse(req.ID, OKResult{OK: true}, nil)
	}
	if err := s.rt.ReportRendererActivation(params.GraphGeneration, params.ID, params.Fingerprint, identity, params.RendererActivationReport); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.rendererClients.clients[identity] = struct{}{}
	return s.writeResponse(req.ID, OKResult{OK: true}, nil)
}

func (s *Server) closeRendererClients() {
	s.rendererClients.mu.Lock()
	defer s.rendererClients.mu.Unlock()
	s.rendererClients.closed = true
	for identity := range s.rendererClients.clients {
		s.rt.ForgetRenderer(identity)
	}
	s.rendererClients.clients = nil
}
