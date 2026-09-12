package channels

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRoomKnowledgePaginationAndResultAccess(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner, peer, outsider := createTestAgent(t, s, "Owner"), createTestAgent(t, s, "Peer"), createTestAgent(t, s, "Outsider")
	room := createTestRoom(t, s, owner, peer)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "evidence-source")
	b := flexibleTestSession(t, s, peer.Agent.ID, room.ID, "evidence-reader")
	outside, _ := s.BindAgent(ctx, outsider.Agent.ID)
	for _, body := range []string{"Evidence one", "Unrelated", "Evidence two"} {
		if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := b.QueryRoomHistory(ctx, RoomHistoryQuery{RoomID: room.ID, Query: "evidence", Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := b.QueryRoomHistory(ctx, RoomHistoryQuery{RoomID: room.ID, Query: "evidence", AfterSeq: first[0].Seq, Limit: 1})
	if err != nil || len(second) != 1 || second[0].Body != "Evidence two" {
		t.Fatalf("next = %+v, %v", second, err)
	}
	body := strings.Repeat("证据", 10000)
	if _, err = s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: a.SessionRef(), TurnID: "final", State: CollaborationSessionCompleted, Result: body}); err != nil {
		t.Fatal(err)
	}
	results, err := b.ReadSessionResults(ctx, a.SessionRef(), 0, 1)
	if err != nil || len(results.Results) != 1 || !results.Results[0].Truncated {
		t.Fatalf("result index = %+v, %v", results, err)
	}
	id := results.Results[0].ID
	page, err := b.ReadSessionResultPage(ctx, a.SessionRef(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	next, err := b.ReadSessionResultPage(ctx, a.SessionRef(), id, page["next_offset"].(int))
	if err != nil {
		t.Fatal(err)
	}
	if page["body"].(string)+next["body"].(string) != body {
		t.Fatal("result pages lost text")
	}
	if _, err = outside.ReadSessionResults(ctx, a.SessionRef(), 0, 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outsider = %v", err)
	}
}
