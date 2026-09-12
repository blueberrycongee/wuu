package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestCollaborationContinuityToolsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := channels.Open(filepath.Join(t.TempDir(), "channels"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Peer"})
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Team", CreatedBy: "human", Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: owner.Agent.ID}, {MemberType: channels.MemberAgent, MemberID: peer.Agent.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.Agent.ID, room.RuntimeID} {
		c, err := s.BindAgent(ctx, id)
		if id == room.RuntimeID {
			c, err = s.BindRuntime(ctx, id)
		}
		if err != nil {
			t.Fatal(err)
		}
		ref := "tool-session-" + id
		if _, err = c.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{RoomID: room.ID, SessionRef: ref, Purpose: func() channels.CollaborationSessionPurpose {
			if id == room.RuntimeID {
				return channels.CollaborationSessionCoordination
			}
			return channels.CollaborationSessionWork
		}()}); err != nil {
			t.Fatal(err)
		}
		c, err = s.BindAgentSession(ctx, id, ref)
		if id == room.RuntimeID {
			c, err = s.BindRuntimeSession(ctx, id, ref)
		}
		if err != nil {
			t.Fatal(err)
		}
		kit, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		kit.SetChatAgent(c)
		execute := func(name string, args any) string {
			t.Helper()
			data, _ := json.Marshal(args)
			result, e := kit.Execute(ctx, providers.ToolCall{Name: name, Arguments: string(data)})
			if e != nil {
				t.Fatal(e)
			}
			return result
		}
		raw := execute("chat_wake", map[string]any{"action": "set", "request_id": "later", "after": "1h", "note": "Review the evidence"})
		var f channels.Followup
		if err = json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		execute("chat_wake", map[string]any{"action": "cancel", "id": f.ID, "revision": f.Revision})
		execute("chat_memory", map[string]any{"action": "read", "scope": "room", "name": "MEMORY.md"})
		execute("chat_memory", map[string]any{"action": "write", "scope": "room", "name": id + ".md", "revision": "missing", "content": "Source: room task. Review on Friday."})
		if id == room.RuntimeID {
			if _, err = kit.Execute(ctx, providers.ToolCall{Name: "write_file", Arguments: `{"path":"escaped.txt","content":"no"}`}); err == nil {
				t.Fatal("coordinator gained general writes")
			}
		}
	}
}
