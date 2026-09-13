package channels

import (
	"context"
	"fmt"
	"testing"
)

func TestMessageWindowBackwardPagingAndForwardRecovery(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	room := createTestRoom(t, s)
	other := createTestRoom(t, s)
	for i := 1; i <= 7; i++ {
		if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: other.ID, HumanID: "human-1", Body: "other room"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		query RoomHistoryQuery
		want  []string
	}{
		{"latest", RoomHistoryQuery{RoomID: room.ID, Limit: 3, Latest: true}, []string{"5", "6", "7"}},
		{"older", RoomHistoryQuery{RoomID: room.ID, BeforeSeq: 5, Limit: 3, Latest: true}, []string{"2", "3", "4"}},
		{"oldest", RoomHistoryQuery{RoomID: room.ID, BeforeSeq: 2, Limit: 3, Latest: true}, []string{"1"}},
		{"recovery", RoomHistoryQuery{RoomID: room.ID, AfterSeq: 5, Limit: 3}, []string{"6", "7"}},
		{"legacy", RoomHistoryQuery{RoomID: room.ID, Limit: 3}, []string{"1", "2", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListMessageWindow(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d messages; want %d", len(got), len(tc.want))
			}
			for i, body := range tc.want {
				if got[i].Body != body {
					t.Fatalf("message %d = %q; want %q", i, got[i].Body, body)
				}
			}
		})
	}
	if _, err := s.ListMessageWindow(ctx, RoomHistoryQuery{RoomID: room.ID, BeforeSeq: -1}); err == nil {
		t.Fatal("negative cursor accepted")
	}
}
