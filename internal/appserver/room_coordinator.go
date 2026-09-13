package appserver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/channels"
)

// ChannelCoordinatorStatus exposes operational state without adding a hidden
// identity or its private transcript to the room member/response namespaces.
type ChannelCoordinatorStatus struct {
	State      string   `json:"state"`
	SessionRef string   `json:"session_ref,omitempty"`
	AgentIDs   []string `json:"agent_ids,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (s *Server) channelCoordinatorStatus(ctx context.Context, roomID string) (*ChannelCoordinatorStatus, error) {
	room, err := s.channelService.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	for _, member := range room.Members {
		if member.MemberType == channels.MemberAgent {
			return nil, nil
		}
	}
	return &ChannelCoordinatorStatus{State: "needs_members"}, nil
}
