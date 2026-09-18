package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestActivityTakeoverCancelsRunningActionAndRetainsPartialEvidence(t *testing.T) {
	registry := activity.NewRegistry()
	kit := &Toolkit{activityRegistry: registry}
	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		result, err := kit.runActivityBoundAction(context.Background(), activitySpec{
			Kind: activity.KindCUA, ThreadID: "thread", Workdir: "/repo", PluginID: "driver", Target: "app",
		}, activityHooks{run: func(ctx context.Context, session activity.Session, _ activity.Lease) (toolresult.Result, string, error) {
			started <- session.ID
			<-ctx.Done()
			return toolresult.Result{StructuredContent: []byte(`{"delivery":"partial","completed_events":1}`)}, "", ctx.Err()
		}})
		if !result.IsError || string(result.StructuredContent) != `{"delivery":"partial","completed_events":1}` {
			done <- errors.New("partial execution evidence lost")
			return
		}
		done <- err
	}()
	var id string
	select {
	case id = <-started:
	case <-time.After(time.Second):
		t.Fatal("action did not start")
	}
	if _, err := registry.Takeover("thread", id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, activity.ErrControlRevoked) {
			t.Fatalf("action result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("takeover did not interrupt the action")
	}
	sessions := registry.List("thread")
	if sessions[0].Controller != activity.ControllerUser {
		t.Fatalf("action overwrote takeover: %+v", sessions[0])
	}
}
