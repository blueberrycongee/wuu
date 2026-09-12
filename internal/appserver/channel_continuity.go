package appserver

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/channels"
)

const MethodChannelContinuity = "channel/continuity"

type ChannelContinuityParams struct {
	Action   string                  `json:"action"`
	RoomID   string                  `json:"roomId"`
	OwnerID  string                  `json:"ownerId"`
	ID       string                  `json:"id"`
	State    string                  `json:"state"`
	Revision int                     `json:"revision"`
	After    string                  `json:"after"`
	Limit    int                     `json:"limit"`
	Memory   channels.NotebookParams `json:"memory"`
}

func (s *Server) handleChannelContinuity(ctx context.Context, req Request) error {
	if s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("collaboration is unavailable"))
	}
	var p ChannelContinuityParams
	if err := decodeParams(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	switch p.Action {
	case "list":
		r, e := s.channelService.QueryRoomFollowups(ctx, p.RoomID, p.After, p.Limit)
		return s.writeResponse(req.ID, r, e)
	case "control":
		r, e := s.channelService.ControlRoomFollowup(ctx, p.RoomID, p.ID, p.State, p.Revision)
		return s.writeResponse(req.ID, channels.FollowupPage{Arrangements: []channels.Followup{r}}, e)
	case "memory":
		r, e := s.channelService.RoomNotebook(ctx, p.RoomID, p.OwnerID, p.Memory)
		return s.writeResponse(req.ID, r, e)
	default:
		return s.writeResponse(req.ID, nil, errors.New("unsupported continuity action"))
	}
}
