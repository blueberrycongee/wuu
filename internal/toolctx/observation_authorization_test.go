package toolctx

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestObservationAuthorizationBindsOwnerAndCurrentPolicy(t *testing.T) {
	id := SettlementIdentity{OperationID: "op-authorization", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "environment"}
	owner := new(int)
	other := new(int)
	var denied atomic.Bool
	var calls atomic.Int32
	require.NoError(t, RegisterObservationAuthorization(id, owner, func(context.Context) error {
		calls.Add(1)
		if denied.Load() {
			return errors.New("denied")
		}
		return nil
	}))
	defer ForgetObservationAuthorization(id, owner)
	require.NoError(t, AuthorizeObservation(context.Background(), id, owner))
	require.Error(t, AuthorizeObservation(context.Background(), id, other))
	require.EqualValues(t, 1, calls.Load())
	denied.Store(true)
	require.Error(t, AuthorizeObservation(context.Background(), id, owner))
	denied.Store(false)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = AuthorizeObservation(context.Background(), id, owner) }()
	}
	wg.Wait()
	observationAuthorizations.Lock()
	entry := observationAuthorizations.entries[id]
	entry.expires = time.Now().Add(-time.Second)
	observationAuthorizations.entries[id] = entry
	observationAuthorizations.Unlock()
	require.Error(t, AuthorizeObservation(context.Background(), id, owner))
}

func TestObservationAuthorizationRetirementFencesInFlightCheck(t *testing.T) {
	id := SettlementIdentity{OperationID: "retire", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "environment"}
	owner := new(int)
	entered, release := make(chan struct{}), make(chan struct{})
	require.NoError(t, RegisterObservationAuthorization(id, owner, func(context.Context) error { close(entered); <-release; return nil }))
	done := make(chan error, 1)
	go func() { done <- AuthorizeObservation(context.Background(), id, owner) }()
	<-entered
	ForgetObservationAuthorization(id, owner)
	require.NoError(t, RegisterObservationAuthorization(id, owner, func(context.Context) error { return nil }))
	defer ForgetObservationAuthorization(id, owner)
	close(release)
	require.ErrorContains(t, <-done, "retired")
}
func TestObservationAuthorizationCapacityCanBeReclaimed(t *testing.T) {
	owner := new(int)
	ids := make([]SettlementIdentity, 0, observationAuthorizationLimit)
	defer func() {
		for _, id := range ids {
			ForgetObservationAuthorization(id, owner)
		}
	}()
	for i := 0; i < observationAuthorizationLimit; i++ {
		id := SettlementIdentity{OperationID: fmt.Sprintf("capacity-%d", i), WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
		require.NoError(t, RegisterObservationAuthorization(id, owner, func(context.Context) error { return nil }))
		ids = append(ids, id)
	}
	extra := SettlementIdentity{OperationID: "capacity-extra", WorkerInstance: "worker", Actor: "actor", EnvironmentIdentity: "env"}
	require.ErrorContains(t, RegisterObservationAuthorization(extra, owner, func(context.Context) error { return nil }), "capacity")
	ForgetObservationAuthorization(ids[0], owner)
	require.NoError(t, RegisterObservationAuthorization(extra, owner, func(context.Context) error { return nil }))
	ForgetObservationAuthorization(extra, owner)
}
