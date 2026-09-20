package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSubscriptionOverflowIsolatedFromOtherConsumers(t *testing.T) {
	for _, limit := range []SubscriptionOptions{
		{MaxPendingEvents: 2},
		{MaxPendingBytes: 1},
	} {
		t.Run(fmt.Sprintf("events=%d,bytes=%d", limit.MaxPendingEvents, limit.MaxPendingBytes), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := &Client{
				rpc:       &protocolClient{events: make(chan Event)},
				eventDone: make(chan struct{}), subs: make(map[*eventSubscriber]struct{}),
			}
			go client.forwardEvents()
			defer func() { close(client.rpc.events); <-client.eventDone }()
			slow := client.Subscribe(ctx, limit)
			defer slow.Close()
			healthy := client.Subscribe(ctx, SubscriptionOptions{})
			defer healthy.Close()
			for i := 0; i < 10; i++ {
				event := Event{Method: "test/event", Params: json.RawMessage(fmt.Sprintf(`{"index":%d}`, i))}
				select {
				case client.rpc.events <- event:
				case <-ctx.Done():
					t.Fatal("slow subscriber blocked dispatch")
				}
				select {
				case got, ok := <-healthy.Events:
					if !ok || string(got.Params) != string(event.Params) {
						t.Fatalf("healthy stream: got %+v, want %+v", got, event)
					}
				case <-ctx.Done():
					t.Fatal("healthy subscriber stalled")
				}
			}
		drain:
			for {
				select {
				case _, ok := <-slow.Events:
					if !ok {
						break drain
					}
				case <-ctx.Done():
					t.Fatal("overflow did not close subscription")
				}
			}
			if !errors.Is(slow.Err(), ErrSubscriptionOverflow) || healthy.Err() != nil {
				t.Fatalf("errors: slow=%v healthy=%v", slow.Err(), healthy.Err())
			}
		})
	}
}
