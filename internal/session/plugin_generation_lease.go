package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const pluginGenerationLeaseFile = ".plugin-generation.lock"
const pluginCatalogMutationLeaseFile = ".plugin-catalog.lock"

// PluginGenerationLease coordinates plugin execution and mutation across every
// app-server that shares a Wuu home. Executions hold shared leases; package or
// policy mutations hold the exclusive lease.
type PluginGenerationLease struct {
	mu        sync.Mutex
	file      *os.File
	exclusive bool
	epoch     uint64
}

func TryAcquirePluginGenerationExecutionLease(wuuHome string) (*PluginGenerationLease, bool, error) {
	return tryAcquirePluginGenerationLease(wuuHome, false)
}

func TryAcquirePluginGenerationMutationLease(wuuHome string) (*PluginGenerationLease, bool, error) {
	return tryAcquirePluginGenerationLease(wuuHome, true)
}

// ReadPluginGenerationEpoch reads the lightweight change signal without
// taking an execution lease. Watchers use it to avoid briefly blocking every
// package mutation while polling an unchanged generation. A concurrent write
// or transient read error is harmless to watchers: they retry on the next
// observation before acquiring the shared execution lease for refresh.
func ReadPluginGenerationEpoch(wuuHome string) (uint64, error) {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return 0, errors.New("Wuu home is required")
	}
	file, err := os.Open(filepath.Join(wuuHome, pluginGenerationLeaseFile))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open plugin generation epoch: %w", err)
	}
	defer file.Close()
	return readPluginGenerationEpoch(file)
}

func tryAcquirePluginGenerationLease(wuuHome string, exclusive bool) (*PluginGenerationLease, bool, error) {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return nil, false, errors.New("Wuu home is required")
	}
	if err := securefs.Mkdir(wuuHome); err != nil {
		return nil, false, fmt.Errorf("create Wuu home for plugin generation lease: %w", err)
	}
	file, err := securefs.OpenFile(filepath.Join(wuuHome, pluginGenerationLeaseFile), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, fmt.Errorf("open plugin generation lease: %w", err)
	}
	acquired, err := tryLockPluginGenerationFile(file, exclusive)
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("lock plugin generation lease: %w", err)
	}
	if !acquired {
		_ = file.Close()
		return nil, false, nil
	}
	epoch, err := readPluginGenerationEpoch(file)
	if err != nil {
		_ = unlockPluginGenerationFile(file)
		_ = file.Close()
		return nil, false, err
	}
	return &PluginGenerationLease{file: file, exclusive: exclusive, epoch: epoch}, true, nil
}

func (l *PluginGenerationLease) Epoch() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

// Advance marks the start of a mutation generation while the exclusive lock is
// held. Advancing before disk changes is conservative: even a failed mutation
// makes peers revalidate rather than risk running a stale generation.
func (l *PluginGenerationLease) Advance() (uint64, error) {
	if l == nil {
		return 0, errors.New("plugin generation lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || !l.exclusive {
		return 0, errors.New("exclusive plugin generation lease is required")
	}
	return l.advanceEpochLocked()
}

// advanceEpochLocked increments and persists the epoch without requiring the
// exclusive lock. Writers must serialize themselves (catalog mutations use
// the dedicated catalog lock) and hold the shared generation lock to exclude
// concurrent activations. The caller also holds l.mu.
func (l *PluginGenerationLease) advanceEpochLocked() (uint64, error) {
	if l == nil || l.file == nil {
		return 0, errors.New("plugin generation lease is not held")
	}
	// A catalog writer may have read its shared generation snapshot before
	// another writer committed. Re-read under the writer lock to retain each
	// completed mutation's change signal.
	epoch, err := readPluginGenerationEpoch(l.file)
	if err != nil {
		return 0, err
	}
	epoch++
	if err := writePluginGenerationEpoch(l.file, epoch); err != nil {
		return 0, err
	}
	l.epoch = epoch
	return epoch, nil
}

func writePluginGenerationEpoch(file *os.File, epoch uint64) error {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], epoch)
	if _, err := file.WriteAt(encoded[:], 0); err != nil {
		return fmt.Errorf("write plugin generation epoch: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plugin generation epoch: %w", err)
	}
	return nil
}

func (l *PluginGenerationLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(unlockPluginGenerationFile(file), file.Close())
}

func readPluginGenerationEpoch(file *os.File) (uint64, error) {
	var encoded [8]byte
	n, err := file.ReadAt(encoded[:], 0)
	if errors.Is(err, io.EOF) && n == 0 {
		return 0, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read plugin generation epoch: %w", err)
	}
	if n != len(encoded) {
		return 0, errors.New("plugin generation epoch is corrupt")
	}
	return binary.BigEndian.Uint64(encoded[:]), nil
}

// PluginCatalogMutationLease coordinates catalog-only package changes across
// app-servers sharing one Wuu home: compatible with in-flight executions
// (turns keep running), mutually exclusive with generation activations and
// with other catalog mutations. The epoch advances only after the caller's
// disk work completes, so peers revalidate complete state.
type PluginCatalogMutationLease struct {
	mu         sync.Mutex
	generation *PluginGenerationLease
	catalog    *os.File
}

func TryAcquirePluginCatalogMutationLease(wuuHome string) (*PluginCatalogMutationLease, bool, error) {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return nil, false, errors.New("Wuu home is required")
	}
	if err := securefs.Mkdir(wuuHome); err != nil {
		return nil, false, fmt.Errorf("create Wuu home for plugin catalog lease: %w", err)
	}
	generation, acquired, err := tryAcquirePluginGenerationLease(wuuHome, false)
	if err != nil {
		return nil, false, err
	}
	if !acquired {
		return nil, false, nil
	}
	catalog, err := securefs.OpenFile(filepath.Join(wuuHome, pluginCatalogMutationLeaseFile), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		_ = generation.Release()
		return nil, false, fmt.Errorf("open plugin catalog mutation lease: %w", err)
	}
	locked, err := tryLockPluginGenerationFile(catalog, true)
	if err != nil {
		_ = catalog.Close()
		_ = generation.Release()
		return nil, false, fmt.Errorf("lock plugin catalog mutation lease: %w", err)
	}
	if !locked {
		_ = catalog.Close()
		_ = generation.Release()
		return nil, false, nil
	}
	return &PluginCatalogMutationLease{generation: generation, catalog: catalog}, true, nil
}

func (l *PluginCatalogMutationLease) Epoch() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generation == nil {
		return 0
	}
	return l.generation.Epoch()
}

// Advance persists the next mutation epoch. Callers must advance after their
// disk changes are complete and while the lease is still held.
func (l *PluginCatalogMutationLease) Advance() (uint64, error) {
	if l == nil {
		return 0, errors.New("plugin catalog mutation lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generation == nil {
		return 0, errors.New("plugin catalog mutation lease is required")
	}
	l.generation.mu.Lock()
	defer l.generation.mu.Unlock()
	return l.generation.advanceEpochLocked()
}

func (l *PluginCatalogMutationLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.catalog != nil {
		err = errors.Join(err, unlockPluginGenerationFile(l.catalog), l.catalog.Close())
		l.catalog = nil
	}
	if l.generation != nil {
		err = errors.Join(err, l.generation.Release())
		l.generation = nil
	}
	return err
}
