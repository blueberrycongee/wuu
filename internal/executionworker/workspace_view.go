package executionworker

import (
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/workspaceview"
)

func (s *Server) workspaceView(data json.RawMessage) (json.RawMessage, error) {
	var request executionenv.WorkspaceViewRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.init == nil {
		s.mu.Unlock()
		return nil, errors.New("execution worker is not initialized")
	}
	binding := *s.init
	s.mu.Unlock()
	if request.Session != binding.Session || request.Identity != binding.Identity {
		return nil, errors.New("execution workspace identity mismatch")
	}
	target := request.Params.Target
	if target == nil || target.ThreadID != binding.Session || target.EnvironmentID != binding.Identity {
		return nil, errors.New("execution workspace target mismatch")
	}
	result, err := workspaceview.Read(binding.Root, request.Method, request.Params, target)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
