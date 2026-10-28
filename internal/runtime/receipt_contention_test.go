package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestConcurrentCompletedModelReceiptsSettleWithinOwnerDeadline(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.SQLiteModule, "wuu.session.default"}
	state, err := OpenState(context.Background(), StateOptions{RootDir: t.TempDir(), HomeDir: t.TempDir(), Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}}})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var wg sync.WaitGroup
	ready := make(chan struct{})
	var prepared sync.WaitGroup
	prepared.Add(8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, complete, err := state.BeginModelReceipts(context.Background())
			if err != nil {
				t.Error(err)
				prepared.Done()
				return
			}
			journal := state.InferenceJournalRuntime.ForOwner(fmt.Sprintf("worker-%d", worker))
			for round := 0; round < 7; round++ {
				req, err := providers.EnsureInferenceAttemptContext(providers.WithInferenceJournal(ctx, journal), providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "fixture"}}}, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
				if err != nil {
					t.Error(err)
					break
				}
				sub, err := req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Transport: "http", Mode: "stream"})
				if err != nil {
					t.Error(err)
					break
				}
				sub.CompleteSuccess(&providers.TokenUsage{InputTokens: 10, OutputTokens: 5})
				if err = req.Attempt.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
					t.Error(err)
					break
				}
				if err = req.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
					t.Error(err)
					break
				}
			}
			prepared.Done()
			<-ready
			start := time.Now()
			err = complete()
			t.Logf("worker=%d finish=%s err=%v", worker, time.Since(start), err)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	prepared.Wait()
	close(ready)
	wg.Wait()
}
