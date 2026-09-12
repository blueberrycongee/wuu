package appserver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/channels"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const namedAgentRoomsContextSource = "runtime.named_agent_rooms"

func (s *Server) attachNamedAgentRoomContext(threadRuntime *runtime.ThreadRuntime, agentID string) {
	if s == nil || s.channelService == nil || threadRuntime == nil || threadRuntime.StreamRunner == nil {
		return
	}
	base := threadRuntime.StreamRunner.BeforeRequestContext
	threadRuntime.StreamRunner.BeforeRequestContext = func() []agent.ContextSegment {
		var segments []agent.ContextSegment
		if base != nil {
			segments = append(segments, base()...)
		}
		segments = append(segments, agent.RequestOnlyContextBlocks(s.namedAgentRoomContextBlocks(agentID))...)
		return segments
	}
}

func (s *Server) namedAgentRoomContextBlocks(agentID string) []wuucontext.Block {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.channelService == nil || agentID == "" {
		return nil
	}
	ctx := context.Background()
	rooms, err := s.channelService.ListRooms(ctx)
	if err != nil {
		providers.DebugLogf("read named agent room context for %q: %v", agentID, err)
		return nil
	}
	agents, err := s.channelService.ListNamedAgents(ctx)
	if err != nil {
		providers.DebugLogf("read named agent identities for room context %q: %v", agentID, err)
		return nil
	}
	agentDirectory := make(map[string]channels.NamedAgent, len(agents))
	for _, namedAgent := range agents {
		agentDirectory[namedAgent.ID] = namedAgent
	}

	principal, _ := s.channelService.GetAgentRuntime(ctx, agentID)
	memberRooms := make([]channels.Room, 0, len(rooms))
	for _, room := range rooms {
		if roomContainsAgent(room, agentID) || (principal.IsRoomRuntime() && principal.RoomID == room.ID) {
			memberRooms = append(memberRooms, room)
		}
	}
	sort.Slice(memberRooms, func(i, j int) bool {
		if memberRooms[i].Name != memberRooms[j].Name {
			return memberRooms[i].Name < memberRooms[j].Name
		}
		return memberRooms[i].ID < memberRooms[j].ID
	})

	var content strings.Builder
	content.WriteString("Current rooms and members for this named agent. This is room structure, not unread-message state.\n")
	if len(memberRooms) == 0 {
		content.WriteString("- No room memberships.")
	} else {
		for roomIndex, room := range memberRooms {
			if roomIndex > 0 {
				content.WriteByte('\n')
			}
			roomName := strings.TrimSpace(room.Name)
			if roomName == "" {
				roomName = "Unnamed room"
			}
			fmt.Fprintf(&content, "- %s (%s, room_id: %s, membership_revision: %d)\n", roomName, room.Kind, room.ID, room.MembershipRevision)
			members := append([]channels.RoomMember(nil), room.Members...)
			sort.Slice(members, func(i, j int) bool {
				if members[i].MemberType != members[j].MemberType {
					return members[i].MemberType < members[j].MemberType
				}
				return members[i].MemberID < members[j].MemberID
			})
			for _, member := range members {
				name := "User"
				role := ""
				if member.MemberType == channels.MemberAgent {
					name = "Unnamed agent"
					if namedAgent, ok := agentDirectory[member.MemberID]; ok {
						if named := strings.TrimSpace(namedAgent.Name); named != "" {
							name = named
						}
						role = strings.TrimSpace(namedAgent.Role)
					}
				}
				you := ""
				if member.MemberType == channels.MemberAgent && member.MemberID == agentID {
					you = ", you"
				}
				fmt.Fprintf(&content, "  - %s (%s%s, member_id: %s", name, member.MemberType, you, member.MemberID)
				if role != "" {
					fmt.Fprintf(&content, ", role: %s", role)
				}
				content.WriteString(")\n")
			}
		}
	}
	return []wuucontext.Block{{
		Kind:    wuucontext.BlockEnvironment,
		Title:   "Named agent room membership",
		Source:  namedAgentRoomsContextSource,
		Content: strings.TrimSpace(content.String()),
	}}
}

func roomContainsAgent(room channels.Room, agentID string) bool {
	for _, member := range room.Members {
		if member.MemberType == channels.MemberAgent && member.MemberID == agentID {
			return true
		}
	}
	return false
}
