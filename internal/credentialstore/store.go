package credentialstore

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("credentialstore: not found")

type Store interface {
	Get(context.Context, string, string) ([]byte, error)
	Set(context.Context, string, string, []byte) error
	Delete(context.Context, string, string) error
	// Update atomically reads and replaces one entry across processes. Missing
	// entries are nil; returning nil deletes. A callback error leaves storage unchanged.
	// The callback may modify the supplied byte slice in place.
	// The callback must not perform network I/O or reenter the store.
	Update(context.Context, string, string, func([]byte) ([]byte, error)) error
}
