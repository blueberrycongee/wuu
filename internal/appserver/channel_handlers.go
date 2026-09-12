package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/session"
)

const localChannelHumanID = "local-user"

func (s *Server) handleChannelBootstrap(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	result, err := s.channelService.EnsureBootstrap(ctx, localChannelHumanID)
	if err == nil {
		err = s.attachLocalHumanUnreadCounts(ctx, result.Rooms)
	}
	return s.writeResponse(req.ID, ChannelBootstrapResult(result), err)
}

func (s *Server) handleChannelAgentList(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	agents, err := s.channelService.ListNamedAgents(ctx)
	if err == nil {
		for i := range agents {
			capacity, capacityErr := s.channelService.NamedAgentCapacity(ctx, agents[i].ID)
			if capacityErr != nil {
				err = capacityErr
				break
			}
			agents[i].SessionCapacity = capacity
			agents[i].ActivityStatus = "idle"
			thinking, roomIDs, activityErr := s.namedAgentActivity(ctx, agentRuntimeFromNamed(agents[i]))
			if activityErr != nil {
				err = activityErr
				break
			}
			if thinking {
				agents[i].ActivityStatus = "thinking"
				agents[i].ActivityRoomIDs = roomIDs
			}
		}
	}
	return s.writeResponse(req.ID, ChannelAgentListResult{Agents: agents}, err)
}

func (s *Server) handleChannelAgentCreate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentCreateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.validateNamedAgentCreation(&params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	credential, err := s.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{
		RequestID: params.RequestID, Name: params.Name, Role: params.Role, AvatarKey: params.AvatarKey, AvatarImage: params.AvatarImage, EngineOverride: params.EngineOverride, ProviderOverride: params.ProviderOverride, ModelOverride: params.ModelOverride, EffortOverride: params.EffortOverride, Autostart: true,
	})
	if err == nil {
		s.invalidateChannelAgentInsights()
	}
	return s.writeResponse(req.ID, ChannelAgentCreateResult{Agent: credential.Agent}, err)
}

func (s *Server) handleChannelAgentUpdate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	engineID := agentengine.NormalizeEngineID(params.EngineOverride)
	if s.rt == nil || !s.rt.EngineAvailable(engineID) {
		return s.writeResponse(req.ID, nil, agentengine.ErrUnknownEngine)
	}
	agent, err := s.channelService.UpdateNamedAgent(ctx, channels.UpdateNamedAgentParams{
		ID: params.AgentID, Name: params.Name, Role: params.Role, AvatarKey: params.AvatarKey, AvatarImage: params.AvatarImage, EngineOverride: string(engineID), ProviderOverride: params.ProviderOverride, ModelOverride: params.ModelOverride, EffortOverride: params.EffortOverride,
	})
	// Existing sessions keep their own context, title and model selection.
	// These identity defaults are used by subsequent session creation.
	if err == nil {
		s.invalidateChannelAgentInsights()
	}
	return s.writeResponse(req.ID, ChannelAgentUpdateResult{Agent: agent}, err)
}

func (s *Server) handleChannelAgentDelete(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentDeleteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	err := s.channelService.DeleteNamedAgent(ctx, params.AgentID)
	if err == nil {
		s.invalidateChannelAgentInsights()
	}
	return s.writeResponse(req.ID, ChannelAgentDeleteResult{Deleted: err == nil}, err)
}

func (s *Server) handleChannelAgentStart(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentStartParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	agent, err := s.channelService.GetNamedAgent(ctx, params.AgentID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.namedAgentMu.Lock()
	defer s.namedAgentMu.Unlock()
	thread, err := s.ensureNamedAgentThreadLocked(agent)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	state, err := s.channelService.WakeState(ctx, agent.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	started := false
	if state.Outstanding {
		if err := s.dispatchNamedAgentWakeLocked(ctx, agentRuntimeFromNamed(agent), true); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		started, _, err = s.namedAgentActivity(ctx, agentRuntimeFromNamed(agent))
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	return s.writeResponse(req.ID, ChannelAgentStartResult{Agent: agent, WakeState: state, Started: started, ThreadID: thread.ID}, nil)
}

func (s *Server) handleChannelAgentReset(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil || s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentResetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	agent, err := s.channelService.GetNamedAgent(ctx, params.AgentID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	threadID := namedAgentSessionID(agent)
	refs, _, err := s.namedAgentSessionRefs(ctx, agentRuntimeFromNamed(agent))
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	requested := false
	for _, ref := range refs {
		oneRequested, resetErr := session.RequestThreadExecutionReset(s.rt.SessionDir, ref)
		if resetErr != nil {
			return s.writeResponse(req.ID, nil, resetErr)
		}
		requested = requested || oneRequested
		if !oneRequested {
			_, _, _, _ = s.removeHeldUserTurn(ref, namedAgentWakeID(agent.ID, ref))
		}
	}
	if !requested {
		// No owner will complete this wake. Normalize stale queue ownership while
		// retaining inbox rows; the next explicit start or mention can consume them.
		if err := s.channelService.ClearWakeOnCheck(ctx, agent.ID); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	state, err := s.channelService.WakeState(ctx, agent.ID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, ChannelAgentResetResult{
		Agent: agent, WakeState: state, Requested: requested, ThreadID: threadID,
	}, nil)
}

func (s *Server) handleChannelAgentCreationResolve(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelAgentCreationResolveParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	proposal, err := s.channelService.ResolveAgentCreationProposal(ctx, channels.ResolveAgentCreationProposalParams{
		ProposalID: params.ProposalID,
		HumanID:    localChannelHumanID,
		Approve:    params.Approve,
		Provider:   params.Provider,
		Model:      params.Model,
	})
	return s.writeResponse(req.ID, ChannelAgentCreationResolveResult{Proposal: proposal}, err)
}

func (s *Server) handleChannelRoomList(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	rooms, err := s.channelService.ListRooms(ctx)
	if err == nil {
		err = s.attachLocalHumanUnreadCounts(ctx, rooms)
	}
	return s.writeResponse(req.ID, ChannelRoomListResult{Rooms: rooms}, err)
}

func (s *Server) attachLocalHumanUnreadCounts(ctx context.Context, rooms []channels.Room) error {
	counts, err := s.channelService.HumanRoomUnreadStatus(ctx, localChannelHumanID)
	if err != nil {
		return err
	}
	byRoom := make(map[string]int, len(counts))
	for _, count := range counts {
		byRoom[count.RoomID] = count.UnreadCount
	}
	activityByRoom := make(map[string]bool)
	agents, err := s.channelService.ListAgentRuntimes(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		thinking, roomIDs, err := s.namedAgentActivity(ctx, agent)
		if err != nil {
			return err
		}
		if thinking {
			for _, roomID := range roomIDs {
				activityByRoom[roomID] = true
			}
		}
	}
	for index := range rooms {
		rooms[index].UnreadCount = byRoom[rooms[index].ID]
		rooms[index].ActivityStatus = "idle"
		if activityByRoom[rooms[index].ID] {
			rooms[index].ActivityStatus = "thinking"
		}
	}
	return nil
}

func (s *Server) handleChannelRoomCreate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelRoomCreateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	members := []channels.RoomMember{{MemberType: channels.MemberHuman, MemberID: localChannelHumanID}}
	seen := make(map[string]struct{}, len(params.AgentIDs))
	for _, rawID := range params.AgentIDs {
		agentID := strings.TrimSpace(rawID)
		if agentID == "" {
			return s.writeResponse(req.ID, nil, errors.New("agent_ids cannot contain an empty id"))
		}
		if _, duplicate := seen[agentID]; duplicate {
			continue
		}
		seen[agentID] = struct{}{}
		members = append(members, channels.RoomMember{MemberType: channels.MemberAgent, MemberID: agentID})
	}
	room, err := s.channelService.CreateRoom(ctx, channels.CreateRoomParams{
		Name: params.Name, AvatarImage: params.AvatarImage, Kind: channels.RoomChannel, CreatedBy: localChannelHumanID, Members: members,
	})
	return s.writeResponse(req.ID, ChannelRoomCreateResult{Room: room}, err)
}

func (s *Server) handleChannelDirectMessageOpen(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelDirectMessageOpenParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	room, err := s.channelService.OpenDirectMessage(ctx, localChannelHumanID, params.AgentID)
	if err == nil {
		rooms := []channels.Room{room}
		err = s.attachLocalHumanUnreadCounts(ctx, rooms)
		room = rooms[0]
	}
	return s.writeResponse(req.ID, ChannelDirectMessageOpenResult{Room: room}, err)
}

func (s *Server) handleChannelRoomUpdate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelRoomUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var room channels.Room
	var err error
	switch {
	case params.AvatarImage != nil && (params.Name != nil || params.AgentIDs != nil):
		err = errors.New("room avatar cannot be updated with other fields")
	case params.AvatarImage != nil:
		room, err = s.channelService.UpdateRoomAvatar(ctx, params.RoomID, *params.AvatarImage)
	case params.Name != nil || params.AgentIDs != nil:
		var members *[]channels.RoomMember
		if params.AgentIDs != nil {
			value := make([]channels.RoomMember, 0, len(*params.AgentIDs))
			for _, agentID := range *params.AgentIDs {
				value = append(value, channels.RoomMember{MemberType: channels.MemberAgent, MemberID: agentID})
			}
			members = &value
		}
		room, err = s.channelService.UpdateRoom(ctx, channels.UpdateRoomParams{
			RoomID:  params.RoomID,
			Name:    params.Name,
			Members: members,
		})
	default:
		err = errors.New("room update requires name, avatar_image, or agent_ids")
	}
	return s.writeResponse(req.ID, ChannelRoomUpdateResult{Room: room}, err)
}

func (s *Server) handleChannelRoomDelete(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelRoomDeleteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	err := s.channelService.DeleteRoom(ctx, params.RoomID)
	return s.writeResponse(req.ID, ChannelRoomDeleteResult{Deleted: err == nil}, err)
}

func (s *Server) handleChannelRoomRead(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelRoomReadParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	err := s.channelService.MarkHumanRoomRead(ctx, params.RoomID, localChannelHumanID)
	return s.writeResponse(req.ID, ChannelRoomReadResult{Read: err == nil}, err)
}

func (s *Server) handleChannelMessageList(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelMessageListParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// Read previews first: a concurrently settled reply is then either still a
	// preview or present in the durable list, without an empty transition.
	responses, err := s.channelResponses(ctx, params.RoomID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	coordinator, err := s.channelCoordinatorStatus(ctx, params.RoomID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	messages, err := s.channelService.ListMessages(ctx, params.RoomID, params.AfterSeq, params.Limit)
	return s.writeResponse(req.ID, ChannelMessageListResult{Messages: messages, Responses: responses, Coordinator: coordinator}, err)
}

func (s *Server) handleChannelMessageSend(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelMessageSendParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	images, err := normalizeTurnStartImages(params.Images)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	files, err := normalizeTurnStartFiles(params.Files)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	messageImages := make([]channels.MessageImage, 0, len(images))
	for _, image := range images {
		messageImages = append(messageImages, channels.MessageImage{
			MediaType: image.MediaType, Data: image.Data, Width: image.Width, Height: image.Height,
		})
	}
	messageFiles := make([]channels.MessageFile, 0, len(files))
	for _, file := range files {
		messageFiles = append(messageFiles, channels.MessageFile{
			MediaType: file.MediaType, Data: file.Data, Filename: file.Filename,
		})
	}
	result, err := s.channelService.SendHuman(ctx, channels.HumanSendParams{
		RoomID: params.RoomID, HumanID: localChannelHumanID, Body: params.Body,
		Images: messageImages, Files: messageFiles,
	})
	return s.writeResponse(req.ID, ChannelMessageSendResult{Message: result.Message}, err)
}

func (s *Server) handleChannelTaskCreate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelTaskCreateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	task, err := s.channelService.CreateTaskHuman(ctx, channels.TaskCreateParams{
		RoomID: params.RoomID, Title: params.Title, OwnerID: params.OwnerID, HumanID: localChannelHumanID,
	})
	return s.writeResponse(req.ID, ChannelTaskCreateResult{Task: task}, err)
}

func (s *Server) handleChannelTaskUpdate(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	var params ChannelTaskUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	task, err := s.channelService.UpdateTaskHuman(ctx, channels.TaskUpdateParams{
		TaskID: params.TaskID, State: channels.TaskState(params.State), OwnerID: params.OwnerID, HumanID: localChannelHumanID,
	})
	return s.writeResponse(req.ID, ChannelTaskUpdateResult{Task: task}, err)
}

func (s *Server) handleChannelHumanMentionStatus(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	counts, err := s.channelService.HumanMentionStatus(ctx, localChannelHumanID)
	total := 0
	for _, count := range counts {
		total += count.UnreadCount
	}
	return s.writeResponse(req.ID, ChannelHumanMentionStatusResult{Count: total}, err)
}

func (s *Server) handleChannelHumanMentionAck(ctx context.Context, req Request) error {
	if s == nil || s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("channels service is unavailable"))
	}
	counts, err := s.channelService.HumanMentionStatus(ctx, localChannelHumanID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	total := 0
	for _, count := range counts {
		if err := s.channelService.AckHumanMentions(ctx, count.RoomID, localChannelHumanID); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		total += count.UnreadCount
	}
	return s.writeResponse(req.ID, ChannelHumanMentionAckResult{Acknowledged: total}, nil)
}
