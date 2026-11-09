package agentthread

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestStorePersistsThreadIndexAndEvents(t *testing.T) {
	store := NewStore(t.TempDir())
	meta := Metadata{
		ID:        "worker-1",
		Path:      "/root/check_tests",
		TaskName:  "check_tests",
		Status:    StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Source:    Source{Kind: SourceThreadSpawn, ParentPath: RootPath, Depth: 2, EdgeStatus: EdgeOpen},
	}
	if err := store.UpsertThread(meta); err != nil {
		t.Fatalf("UpsertThread: %v", err)
	}
	meta.Status = StatusCompleted
	if err := store.RecordStatus(meta); err != nil {
		t.Fatalf("RecordStatus: %v", err)
	}

	threads, err := store.ListThreads()
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 1 || threads[0].Status != StatusCompleted {
		t.Fatalf("unexpected thread index: %+v", threads)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != EventThreadUpsert || events[1].Type != EventStatusChange {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
}

func TestStoreRecordsEdgeStatus(t *testing.T) {
	store := NewStore(t.TempDir())
	meta := Metadata{
		ID:        "worker-1",
		Path:      "/root/check_tests",
		TaskName:  "check_tests",
		Status:    StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Source:    Source{Kind: SourceThreadSpawn, ParentPath: RootPath, Depth: 2, EdgeStatus: EdgeOpen},
	}
	if err := store.UpsertThread(meta); err != nil {
		t.Fatalf("UpsertThread: %v", err)
	}
	meta.Source.EdgeStatus = EdgeClosed
	if err := store.RecordEdgeStatus(meta); err != nil {
		t.Fatalf("RecordEdgeStatus: %v", err)
	}
	threads, err := store.ListThreads()
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 1 || threads[0].Source.EdgeStatus != EdgeClosed {
		t.Fatalf("edge status not persisted: %+v", threads)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 || events[1].Type != EventEdgeChange || events[1].EdgeStatus != EdgeClosed {
		t.Fatalf("unexpected edge event: %+v", events)
	}
}

func TestStoreRecordsInterAgentCommunication(t *testing.T) {
	store := NewStore(t.TempDir())
	communication := NewInterAgentCommunication(
		AgentPath("/root/research"),
		AgentPath("/root"),
		"done",
		false,
	)
	if err := store.RecordCommunication("root-thread", communication); err != nil {
		t.Fatalf("RecordCommunication: %v", err)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %+v", events)
	}
	event := events[0]
	if event.Type != EventMessage || event.ThreadID != "root-thread" || event.AuthorPath != "/root/research" || event.RecipientPath != "/root" || event.Message != "done" {
		t.Fatalf("unexpected communication event: %+v", event)
	}
}

func TestStoreResultCommunicationAndConsumerTransitionAreIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	communication := NewInterAgentCommunication(AgentPath("/root/child"), AgentPath("/root/parent"), "child done", false)
	for i := 0; i < 2; i++ {
		if _, err := store.RecordResultCommunication("parent", "result-1", communication); err != nil {
			t.Fatalf("RecordResultCommunication %d: %v", i, err)
		}
	}
	if err := store.AppendEvent(Event{Type: EventResultReady, ResultID: "result-1"}); err != nil {
		t.Fatalf("append result ready: %v", err)
	}
	if claimed, consumedBy, err := store.ClaimResultDelivery(Event{ResultID: "result-1", Consumer: "nested_followup_pending"}); err != nil || !claimed || consumedBy != "" {
		t.Fatalf("pending claim = %t, %q, %v", claimed, consumedBy, err)
	}
	if transitioned, consumedBy, err := store.TransitionResultDelivery(Event{ResultID: "result-1", Consumer: "nested_followup"}, "nested_followup_pending"); err != nil || !transitioned || consumedBy != "nested_followup" {
		t.Fatalf("consumer transition = %t, %q, %v", transitioned, consumedBy, err)
	}
	if transitioned, consumedBy, err := store.TransitionResultDelivery(Event{ResultID: "result-1", Consumer: "unexpected"}, "nested_followup_pending"); err != nil || transitioned || consumedBy != "nested_followup" {
		t.Fatalf("repeated consumer transition = %t, %q, %v", transitioned, consumedBy, err)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	messageCount := 0
	for _, event := range events {
		if event.Type == EventMessage && event.ResultID == "result-1" {
			messageCount++
		}
	}
	if messageCount != 1 {
		t.Fatalf("durable parent inbox messages = %d, want 1", messageCount)
	}
}

func TestStoreConcurrentInstancesPreserveThreadUpdates(t *testing.T) {
	dir := t.TempDir()
	stores := []*Store{NewStore(dir), NewStore(dir)}
	const perStore = 32
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	for storeIndex, store := range stores {
		wg.Add(1)
		go func(storeIndex int, store *Store) {
			defer wg.Done()
			<-start
			for i := 0; i < perStore; i++ {
				id := fmt.Sprintf("worker-%d-%02d", storeIndex, i)
				if err := store.UpsertThread(Metadata{
					ID:     id,
					Path:   "/root/" + id,
					Status: StatusRunning,
				}); err != nil {
					errs <- err
					return
				}
			}
		}(storeIndex, store)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent UpsertThread: %v", err)
	}

	threads, err := NewStore(dir).ListThreads()
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != len(stores)*perStore {
		t.Fatalf("thread count = %d, want %d", len(threads), len(stores)*perStore)
	}
	events, err := NewStore(dir).ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != len(stores)*perStore {
		t.Fatalf("event count = %d, want %d", len(events), len(stores)*perStore)
	}
}

func TestStoreResultClaimIsAtomicAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	first := NewStore(dir)
	second := NewStore(dir)
	if err := first.AppendEvent(Event{Type: EventResultReady, ResultID: "result-1"}); err != nil {
		t.Fatalf("AppendEvent ready: %v", err)
	}

	type outcome struct {
		consumer   string
		claimed    bool
		consumedBy string
		err        error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	for i, store := range []*Store{first, second} {
		consumer := fmt.Sprintf("consumer-%d", i)
		go func(store *Store, consumer string) {
			<-start
			claimed, consumedBy, err := store.ClaimResultDelivery(Event{
				ResultID: "result-1",
				Consumer: consumer,
			})
			outcomes <- outcome{consumer: consumer, claimed: claimed, consumedBy: consumedBy, err: err}
		}(store, consumer)
	}
	close(start)

	results := []outcome{<-outcomes, <-outcomes}
	winner := ""
	claimedCount := 0
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("ClaimResultDelivery: %v", result.err)
		}
		if result.claimed {
			claimedCount++
			winner = result.consumer
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want 1: %+v", claimedCount, results)
	}
	for _, result := range results {
		if !result.claimed && result.consumedBy != winner {
			t.Fatalf("loser observed consumer %q, want %q", result.consumedBy, winner)
		}
	}
	if released, err := second.ReleaseResultDelivery(Event{ResultID: "result-1", Consumer: "not-owner"}); err != nil || released {
		t.Fatalf("non-owner release = %v, err=%v", released, err)
	}
	if released, err := first.ReleaseResultDelivery(Event{ResultID: "result-1", Consumer: winner}); err != nil || !released {
		t.Fatalf("owner release = %v, err=%v", released, err)
	}
	if claimed, consumedBy, err := second.ClaimResultDelivery(Event{ResultID: "result-1", Consumer: "after-release"}); err != nil || !claimed || consumedBy != "" {
		t.Fatalf("claim after release = %v consumedBy=%q err=%v", claimed, consumedBy, err)
	}

	events, err := first.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	claimCount := 0
	for _, event := range events {
		if event.Type == EventResultClaim {
			claimCount++
		}
	}
	if claimCount != 2 {
		t.Fatalf("claim event count = %d, want initial winner plus post-release claim", claimCount)
	}
}
