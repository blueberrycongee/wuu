package toolctx

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
)

// Authorization callbacks retain the exact original host participants. The
// ledger stores their non-bearer settlement identity, not their arguments.
// Once the owner process or binding is gone the old result cannot be replayed.
var observationAuthorizations = struct {
	sync.Mutex
	entries map[SettlementIdentity]*observationAuthorization
}{entries: make(map[SettlementIdentity]*observationAuthorization)}

type observationAuthorization struct {
	check   func(context.Context) error
	expires time.Time
	owner   any
}

const observationAuthorizationLimit = 4096
const observationAuthorizationLifetime = 30 * time.Minute

func RegisterObservationAuthorization(id SettlementIdentity, owner any, check func(context.Context) error) error {
	if id.OperationID == "" || id.WorkerInstance == "" || id.Actor == "" || id.EnvironmentIdentity == "" || check == nil || owner == nil || !reflect.ValueOf(owner).Comparable() {
		return errors.New("observation authorization binding unavailable")
	}
	observationAuthorizations.Lock()
	defer observationAuthorizations.Unlock()
	now := time.Now()
	for key, entry := range observationAuthorizations.entries {
		if !entry.expires.After(now) {
			delete(observationAuthorizations.entries, key)
		}
	}
	if _, exists := observationAuthorizations.entries[id]; exists {
		return errors.New("duplicate observation authorization binding")
	}
	if len(observationAuthorizations.entries) >= observationAuthorizationLimit {
		return errors.New("observation authorization capacity reached")
	}
	observationAuthorizations.entries[id] = &observationAuthorization{check: check, expires: now.Add(observationAuthorizationLifetime), owner: owner}
	return nil
}
func AuthorizeObservation(ctx context.Context, id SettlementIdentity, owner any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	observationAuthorizations.Lock()
	entry, ok := observationAuthorizations.entries[id]
	if ok && !entry.expires.After(time.Now()) {
		delete(observationAuthorizations.entries, id)
		ok = false
	}
	observationAuthorizations.Unlock()
	if !ok || owner == nil || !reflect.ValueOf(owner).Comparable() || entry.owner != owner {
		return errors.New("original observation authorization is unavailable")
	}
	if err := entry.check(ctx); err != nil {
		return errors.New("observation authorization failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	observationAuthorizations.Lock()
	current, exists := observationAuthorizations.entries[id]
	valid := exists && current == entry && current.expires.After(time.Now())
	observationAuthorizations.Unlock()
	if !valid {
		return errors.New("observation authorization binding retired")
	}
	return nil
}
func ForgetObservationAuthorization(id SettlementIdentity, owner any) {
	observationAuthorizations.Lock()
	entry, ok := observationAuthorizations.entries[id]
	if ok && owner != nil && reflect.ValueOf(owner).Comparable() && entry.owner == owner {
		delete(observationAuthorizations.entries, id)
	}
	observationAuthorizations.Unlock()
}
