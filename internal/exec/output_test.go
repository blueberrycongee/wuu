package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

type deltaWriter struct {
	started chan struct{}
	write   func([]byte) (int, error)
}

func (w *deltaWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"agent_message_delta"`)) {
		close(w.started)
		return w.write(p)
	}
	return len(p), nil
}

func TestRunCancelsWithStalledOutput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &deltaWriter{started: make(chan struct{}), write: writer.Write}
	controller := newFakeController(notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{
		ThreadID: "thread-1", TurnID: "turn-1", Delta: "hello",
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Prompt: "work", JSON: true, Stdout: output, Controller: controller})
	}()
	select {
	case <-output.started:
	case <-time.After(5 * time.Second):
		t.Fatal("output write did not start")
	}
	cancel()
	select {
	case err := <-done:
		if ExitCode(err) != ExitInterrupted || controller.interruptReason != "interrupted" || !controller.shutdown {
			t.Fatalf("cancellation: err=%v interrupt=%q shutdown=%v", err, controller.interruptReason, controller.shutdown)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled consumer prevented cancellation")
	}
}

func TestRunStopsWhenOutputFailsMidRun(t *testing.T) {
	broken := errors.New("consumer disconnected")
	output := &deltaWriter{started: make(chan struct{}), write: func([]byte) (int, error) { return 0, broken }}
	controller := newFakeController(notification(appserver.NotificationAgentMessageDelta, appserver.AgentMessageDeltaNotification{
		ThreadID: "thread-1", TurnID: "turn-1", Delta: "hello",
	}))
	err := Run(context.Background(), Options{Prompt: "work", JSON: true, Stdout: output, Controller: controller})
	if ExitCode(err) != ExitProtocol || !errors.Is(err, broken) || controller.interruptReason == "" || !controller.shutdown {
		t.Fatalf("output failure: err=%v interrupt=%q shutdown=%v", err, controller.interruptReason, controller.shutdown)
	}
}
