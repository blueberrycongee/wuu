package executionworker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type blockedObservationCommit struct{ entered chan struct{} }

func (b blockedObservationCommit) Commit(ctx context.Context) (bool, error) {
	close(b.entered)
	<-ctx.Done()
	return false, ctx.Err()
}
func TestReadObservationCommitDoesNotBlockOtherActorOrClose(t *testing.T) {
	s := New()
	entered := make(chan struct{})
	s.readReceipts["slow"] = &readReceipt{actor: "a", mode: "read_only", ticket: blockedObservationCommit{entered}, expires: time.Now().Add(time.Minute)}
	s.readReceipts["other"] = &readReceipt{actor: "b", mode: "read_only", ticket: blockedObservationCommit{make(chan struct{})}, expires: time.Now().Add(time.Minute)}
	slow, _ := json.Marshal(map[string]any{"actor": "a", "permission_mode": "read_only", "receipt": "slow"})
	other, _ := json.Marshal(map[string]any{"actor": "b", "permission_mode": "read_only", "receipt": "other"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Handle(ctx, "read/commit", slow); done <- err }()
	<-entered
	statusDone := make(chan error, 1)
	go func() { _, err := s.Handle(context.Background(), "read/status", other); statusDone <- err }()
	select {
	case err := <-statusDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("slow actor blocked other receipt")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("close did not cancel slow commit")
	}
	require.Error(t, <-done)
}
