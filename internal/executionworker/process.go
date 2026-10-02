package executionworker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/process"
)

func (s *Server) process(ctx context.Context, method string, data json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	manager := s.processes
	session := s.init
	s.mu.Unlock()
	if manager == nil {
		return nil, errors.New("worker is not initialized")
	}
	var request struct {
		ID       string                    `json:"id"`
		Input    string                    `json:"input"`
		Cols     int                       `json:"cols"`
		Rows     int                       `json:"rows"`
		Minutes  int                       `json:"minutes"`
		Consumer string                    `json:"consumer"`
		Mode     process.CompletionMode    `json:"mode"`
		Options  process.OutputReadOptions `json:"options"`
	}
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
	}
	if request.ID != "" {
		p, err := manager.Get(request.ID)
		if err != nil {
			return nil, err
		}
		if p.RootThreadID != session.Session {
			return nil, errors.New("process belongs to another execution session")
		}
	}
	var result any
	var err error
	switch strings.TrimPrefix(method, "process/") {
	case "list":
		result, err = manager.List()
	case "get":
		result, err = manager.Get(request.ID)
	case "read":
		result, err = manager.ReadOutputSnapshot(ctx, request.ID, request.Options)
	case "write":
		result, err = manager.WriteStdin(request.ID, request.Input)
	case "resize":
		result, err = manager.ResizeTTY(request.ID, request.Cols, request.Rows)
	case "stop":
		result, err = manager.Stop(request.ID)
	case "input_available":
		result = manager.InputAvailable(request.ID)
	case "completion_pending":
		result, err = manager.CompletionPending(request.ID)
	case "completion_delivered":
		result, err = manager.MarkCompletionDelivered(request.ID, request.Consumer)
	case "recheck":
		result, err = manager.SetRecheck(request.ID, request.Minutes)
	case "completion_mode":
		result, err = manager.SetCompletionMode(request.ID, request.Mode)
	case "recheck_delivered":
		result, err = manager.MarkRecheckDelivered(request.ID)
	case "cleanup":
		result, err = manager.CleanupSessionWithResult()
	default:
		return nil, errors.New("unknown remote process operation")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
