package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func waitForProviderCoordinator(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func receiveProviderCoordinatorResult(t *testing.T, results <-chan string) string {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider coordinator result")
		return ""
	}
}

func testProviderCoordinator() *ProviderCoordinator {
	return NewProviderCoordinator(ProviderCoordinatorConfig{
		MaxInFlight:               1,
		CircuitFailureThreshold:   2,
		CircuitOpenDuration:       25 * time.Millisecond,
		DefaultRateLimitDelay:     20 * time.Millisecond,
		PriorityAgingInterval:     5 * time.Millisecond,
		RecoveryAdmissionInterval: 5 * time.Millisecond,
	})
}

func TestProviderScopeUsesKeyedCredentialFingerprint(t *testing.T) {
	first := NewProviderScope("HTTPS://API.EXAMPLE.TEST/", "sk-secret", "org-1")
	second := NewProviderScope("https://api.example.test", "sk-secret", "org-1")
	sensitiveURL := NewProviderScope("https://user:password@API.EXAMPLE.TEST:443/v1/CaseSensitive?access_token=query-secret#fragment", "sk-secret", "org-1")
	different := NewProviderScope("https://api.example.test", "sk-other", "org-1")
	if first != second || first != sensitiveURL || first == different {
		t.Fatalf("scopes = %q / %q / %q / %q", first, second, sensitiveURL, different)
	}
	for _, secret := range []string{"sk-secret", "org-1", "user", "password", "query-secret", "CaseSensitive"} {
		if strings.Contains(string(sensitiveURL), secret) {
			t.Fatalf("scope leaked %q: %q", secret, sensitiveURL)
		}
	}
}

func TestProviderCoordinatorPrioritizesInteractiveWaiter(t *testing.T) {
	coordinator := testProviderCoordinator()
	scope := NewProviderScope("https://api.example.test", "key", "")
	held, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}

	order := make(chan string, 2)
	acquire := func(label string, priority ProviderPriority) {
		lease, err := coordinator.Acquire(context.Background(), scope, priority)
		if err != nil {
			order <- "error:" + label
			return
		}
		order <- label
		lease.Release()
	}
	go acquire("background", ProviderPriorityBackground)
	go acquire("interactive", ProviderPriorityInteractive)
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 2
	}, "both prioritized waiters to queue")
	held.Release()
	if got := receiveProviderCoordinatorResult(t, order); got != "interactive" {
		t.Fatalf("first admitted waiter = %q", got)
	}
	if got := receiveProviderCoordinatorResult(t, order); got != "background" {
		t.Fatalf("second admitted waiter = %q", got)
	}
}

func TestProviderCoordinatorHonorsSharedRetryAfter(t *testing.T) {
	coordinator := testProviderCoordinator()
	scope := NewProviderScope("https://api.example.test", "key", "")
	failed, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	failed.Fail(NormalizedFailure{Category: FailureRateLimit, RetryAfter: 25 * time.Millisecond})
	started := time.Now()
	lease, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Fatalf("coordinator ignored Retry-After: waited %s", waited)
	}
}

func TestProviderCoordinatorCircuitAndScopeIsolation(t *testing.T) {
	coordinator := testProviderCoordinator()
	blocked := NewProviderScope("https://api.example.test", "key-a", "")
	independent := NewProviderScope("https://api.example.test", "key-b", "")
	failure := NormalizedFailure{Category: FailureServer}
	for range 2 {
		lease, err := coordinator.Acquire(context.Background(), blocked, ProviderPriorityInteractive)
		if err != nil {
			t.Fatal(err)
		}
		lease.Fail(failure)
	}
	if coordinator.Snapshot(blocked).CircuitUntil.IsZero() {
		t.Fatal("circuit did not open")
	}

	started := time.Now()
	lease, err := coordinator.Acquire(context.Background(), independent, ProviderPriorityBackground)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if waited := time.Since(started); waited > 10*time.Millisecond {
		t.Fatalf("independent credential scope was blocked for %s", waited)
	}

	started = time.Now()
	lease, err = coordinator.Acquire(context.Background(), blocked, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Fatalf("open circuit did not delay admission: %s", waited)
	}
}

func TestProviderCoordinatorCancellationDoesNotLeakCapacity(t *testing.T) {
	coordinator := testProviderCoordinator()
	scope := NewProviderScope("https://api.example.test", "key", "")
	held, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.Acquire(ctx, scope, ProviderPriorityInteractive)
		done <- err
	}()
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 1
	}, "cancellable waiter to queue")
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled acquire returned nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled acquire")
	}
	held.Release()
	lease, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestProviderCoordinatorLateSuccessDoesNotCloseNewCircuit(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{
		MaxInFlight:             2,
		CircuitFailureThreshold: 1,
		CircuitOpenDuration:     100 * time.Millisecond,
	})
	scope := NewProviderScope("https://api.example.test", "key", "")
	failed, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	lateSuccess, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	failed.Fail(NormalizedFailure{Category: FailureServer})
	opened := coordinator.Snapshot(scope).CircuitUntil
	if !opened.After(time.Now()) {
		t.Fatal("circuit did not open")
	}

	lateSuccess.Succeed()
	snapshot := coordinator.Snapshot(scope)
	if !snapshot.CircuitUntil.Equal(opened) || snapshot.ConsecutiveFailure != 1 {
		t.Fatalf("late success closed newer circuit: %+v", snapshot)
	}
}

func TestProviderCoordinatorAgesWaitingPriorities(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{
		MaxInFlight:           1,
		PriorityAgingInterval: 5 * time.Millisecond,
	})
	scope := NewProviderScope("https://api.example.test", "key", "")
	held, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityCritical)
	if err != nil {
		t.Fatal(err)
	}

	order := make(chan string, 2)
	acquire := func(label string, priority ProviderPriority) {
		lease, acquireErr := coordinator.Acquire(context.Background(), scope, priority)
		if acquireErr != nil {
			order <- "error:" + label
			return
		}
		order <- label
		lease.Release()
	}
	go acquire("background", ProviderPriorityBackground)
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 1
	}, "background waiter to queue")
	time.Sleep(12 * time.Millisecond)
	go acquire("critical", ProviderPriorityCritical)
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 2
	}, "critical waiter to queue")

	held.Release()
	if got := receiveProviderCoordinatorResult(t, order); got != "background" {
		t.Fatalf("aged waiter was starved by newer critical work: %q", got)
	}
	if got := receiveProviderCoordinatorResult(t, order); got != "critical" {
		t.Fatalf("second admitted waiter = %q", got)
	}
}

func TestProviderCoordinatorPacesRecoveryAdmissions(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{
		MaxInFlight:               3,
		ReservedInteractiveSlots:  -1,
		DefaultRateLimitDelay:     15 * time.Millisecond,
		RecoveryAdmissionInterval: 200 * time.Millisecond,
	})
	scope := NewProviderScope("https://api.example.test", "key", "")
	failed, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	failed.Fail(NormalizedFailure{Category: FailureRateLimit})

	leases := make(chan *ProviderLease, 3)
	for range 3 {
		go func() {
			lease, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
			if err == nil {
				leases <- lease
			}
		}()
	}
	waitForProviderCoordinator(t, func() bool {
		snapshot := coordinator.Snapshot(scope)
		return snapshot.InFlight == 1 && snapshot.Recovering
	}, "first paced recovery admission")
	time.Sleep(50 * time.Millisecond)
	if got := coordinator.Snapshot(scope).InFlight; got != 1 {
		t.Fatalf("recovery released a burst of %d requests", got)
	}
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).InFlight == 3
	}, "remaining paced recovery admissions")
	for range 3 {
		select {
		case lease := <-leases:
			lease.Release()
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for paced lease")
		}
	}
}

func TestProviderCoordinatorReservesForegroundCapacity(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{MaxInFlight: 3})
	scope := NewProviderScope("https://api.example.test", "key", "")
	first, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityBackground)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityBackground)
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan *ProviderLease, 1)
	go func() {
		lease, acquireErr := coordinator.Acquire(context.Background(), scope, ProviderPriorityBackground)
		if acquireErr == nil {
			queued <- lease
		}
	}()
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 1
	}, "background waiter to reach reserved slot")

	foreground, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	foreground.Release()
	select {
	case lease := <-queued:
		lease.Release()
		t.Fatal("background work consumed the reserved foreground slot")
	case <-time.After(10 * time.Millisecond):
	}

	first.Release()
	select {
	case lease := <-queued:
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("background waiter was not admitted after capacity became available")
	}
	second.Release()
}

func TestProviderCoordinatorUsesAttemptWorkloadProfile(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{MaxInFlight: 1})
	scope := NewProviderScope("https://api.example.test", "key", "")
	held, err := coordinator.Acquire(context.Background(), scope, ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}

	background := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileBackgroundAgent)).BeginAttempt()
	interactive := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive)).BeginAttempt()
	order := make(chan string, 2)
	acquire := func(label string, attempt InferenceAttempt) {
		lease, acquireErr := coordinator.AcquireForAttempt(context.Background(), scope, attempt)
		if acquireErr != nil {
			order <- "error:" + label
			return
		}
		order <- label
		lease.Release()
	}
	go acquire("background", background)
	go acquire("interactive", interactive)
	waitForProviderCoordinator(t, func() bool {
		return coordinator.Snapshot(scope).Waiting == 2
	}, "profile-derived waiters to queue")
	held.Release()
	if got := receiveProviderCoordinatorResult(t, order); got != "interactive" {
		t.Fatalf("first admitted profile = %q", got)
	}
	if got := receiveProviderCoordinatorResult(t, order); got != "background" {
		t.Fatalf("second admitted profile = %q", got)
	}
}

func TestProviderLeaseSettlesBoundSubmissionWithoutCoordinator(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAuxiliary, InferenceProfileInteractive))
	attempt := execution.BeginAttempt()
	lease := &ProviderLease{}
	lease.RecordSubmission(attempt, InferenceSubmissionMeta{Provider: "test", Transport: "memory"})
	lease.ObserveOutput("answer")
	lease.SucceedWithUsage(&TokenUsage{InputTokens: 2, OutputTokens: 1})
	lease.Release()

	submission := execution.Snapshot().Submissions[0]
	if submission.Outcome != InferenceSubmissionSucceeded || submission.CostState != InferenceCostKnown || submission.ReportedUsage == nil || submission.ReportedUsage.OutputTokens != 1 {
		t.Fatalf("submission = %+v", submission)
	}
}

func TestProviderLeaseTransportFallbackDoesNotOpenAccountCircuit(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{
		MaxInFlight:             1,
		CircuitFailureThreshold: 1,
		CircuitOpenDuration:     time.Second,
	})
	scope := NewProviderScope("https://api.example.test", "key", "")
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	attempt := execution.BeginAttempt()
	lease, err := coordinator.AcquireForAttempt(context.Background(), scope, attempt)
	if err != nil {
		t.Fatal(err)
	}
	lease.RecordSubmission(attempt, InferenceSubmissionMeta{Transport: "websocket"})
	lease.FallbackError(errors.New("websocket transport failed"))

	coordinatorState := coordinator.Snapshot(scope)
	submission := execution.Snapshot().Submissions[0]
	if coordinatorState.ConsecutiveFailure != 0 || !coordinatorState.CircuitUntil.IsZero() || submission.Outcome != InferenceSubmissionFallback || submission.CostState != InferenceCostUnknownBillable {
		t.Fatalf("coordinator=%+v submission=%+v", coordinatorState, submission)
	}
}

func TestProviderCoordinatorCloseStopsQueuedAdmission(t *testing.T) {
	coordinator := NewProviderCoordinator(ProviderCoordinatorConfig{MaxInFlight: 1})
	lease, err := coordinator.Acquire(context.Background(), "account", ProviderPriorityInteractive)
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() {
		_, err := coordinator.Acquire(context.Background(), "account", ProviderPriorityInteractive)
		queued <- err
	}()
	waitForProviderCoordinator(t, func() bool { return coordinator.Snapshot("account").Waiting == 1 }, "queued admission")
	coordinator.Close()
	select {
	case err := <-queued:
		if err == nil {
			t.Fatal("closed coordinator admitted queued request")
		}
	case <-time.After(time.Second):
		t.Fatal("close stranded queued admission")
	}
	lease.Release()
	if _, err := coordinator.Acquire(context.Background(), "account", ProviderPriorityInteractive); err == nil {
		t.Fatal("closed coordinator accepted new request")
	}
}
