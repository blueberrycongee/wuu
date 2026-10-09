package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginGenerationLeaseExcludesMutationFromExecutions(t *testing.T) {
	home := t.TempDir()
	first, acquired, err := TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire first execution lease: acquired=%v err=%v", acquired, err)
	}
	defer first.Release()
	second, acquired, err := TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire second execution lease: acquired=%v err=%v", acquired, err)
	}
	defer second.Release()
	mutation, acquired, err := TryAcquirePluginGenerationMutationLease(home)
	if err != nil {
		t.Fatal(err)
	}
	if acquired || mutation != nil {
		t.Fatal("mutation lease acquired while executions were active")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	mutation, acquired, err = TryAcquirePluginGenerationMutationLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire mutation lease: acquired=%v err=%v", acquired, err)
	}
	defer mutation.Release()
	epoch, err := mutation.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 1 {
		t.Fatalf("epoch = %d, want 1", epoch)
	}
	blocked, acquired, err := TryAcquirePluginGenerationExecutionLease(home)
	if err != nil {
		t.Fatal(err)
	}
	if acquired || blocked != nil {
		t.Fatal("execution lease acquired while mutation was active")
	}
	if err := mutation.Release(); err != nil {
		t.Fatal(err)
	}
	next, acquired, err := TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire execution after mutation: acquired=%v err=%v", acquired, err)
	}
	defer next.Release()
	if next.Epoch() != 1 {
		t.Fatalf("persisted epoch = %d, want 1", next.Epoch())
	}
}

func TestReadPluginGenerationEpochDoesNotTakeExecutionLease(t *testing.T) {
	home := t.TempDir()
	if epoch, err := ReadPluginGenerationEpoch(home); err != nil || epoch != 0 {
		t.Fatalf("initial epoch = %d, err = %v", epoch, err)
	}

	mutation, acquired, err := TryAcquirePluginGenerationMutationLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire mutation: acquired=%v err=%v", acquired, err)
	}
	defer mutation.Release()
	epoch, err := mutation.Advance()
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ReadPluginGenerationEpoch(home)
	if err != nil || observed != epoch {
		t.Fatalf("observed epoch = %d, want %d, err = %v", observed, epoch, err)
	}
}

func TestPluginCatalogMutationAdvancePreservesInterleavedWriterEpochs(t *testing.T) {
	home := t.TempDir()
	// Pause writer B after its shared lease read, before acquiring the catalog
	// lock. Writer A can complete in this window because both leases are shared.
	pending, acquired, err := TryAcquirePluginGenerationExecutionLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire pending writer generation lease: acquired=%v err=%v", acquired, err)
	}
	defer pending.Release()
	first, acquired, err := TryAcquirePluginCatalogMutationLease(home)
	if err != nil || !acquired {
		t.Fatalf("acquire first catalog mutation: acquired=%v err=%v", acquired, err)
	}
	defer first.Release()
	firstEpoch, err := first.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	observed, err := ReadPluginGenerationEpoch(home)
	if err != nil || observed != firstEpoch {
		t.Fatalf("watcher observed epoch=%d err=%v, want %d", observed, err, firstEpoch)
	}

	// Resume B at the actual catalog lock boundary, retaining its earlier
	// generation snapshot. Real file locks make this interleaving repeatable.
	catalog, err := os.OpenFile(filepath.Join(home, pluginCatalogMutationLeaseFile), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := tryLockPluginGenerationFile(catalog, true)
	if err != nil || !locked {
		_ = catalog.Close()
		t.Fatalf("resume pending writer: locked=%v err=%v", locked, err)
	}
	second := &PluginCatalogMutationLease{generation: pending, catalog: catalog}
	defer second.Release()
	for step := uint64(1); step <= 2; step++ {
		epoch, err := second.Advance()
		if err != nil || epoch != firstEpoch+step {
			t.Fatalf("pending writer Advance = %d err=%v, want %d", epoch, err, firstEpoch+step)
		}
		if got := second.Epoch(); got != epoch {
			t.Fatalf("writer Epoch = %d, want committed epoch %d", got, epoch)
		}
		current, err := ReadPluginGenerationEpoch(home)
		if err != nil || current != epoch || current == observed {
			t.Fatalf("watcher lost completed mutation: previous=%d current=%d err=%v", observed, current, err)
		}
		observed = current
	}
	other, acquired, err := TryAcquirePluginCatalogMutationLease(home)
	if err != nil || acquired || other != nil {
		t.Fatalf("catalog writer admitted concurrently: acquired=%v err=%v", acquired, err)
	}
	activation, acquired, err := TryAcquirePluginGenerationMutationLease(home)
	if err != nil || acquired || activation != nil {
		t.Fatalf("generation activation admitted concurrently: acquired=%v err=%v", acquired, err)
	}
}

func TestPluginCatalogMutationEpochConcurrentWithAdvanceAndRelease(t *testing.T) {
	lease, acquired, err := TryAcquirePluginCatalogMutationLease(t.TempDir())
	if err != nil || !acquired {
		t.Fatalf("acquire catalog mutation: acquired=%v err=%v", acquired, err)
	}
	defer lease.Release()
	start := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-start
		for i := 0; i < 1000; i++ {
			lease.Epoch()
		}
	}()
	close(start)
	for i := 0; i < 10; i++ {
		if _, err := lease.Advance(); err != nil {
			t.Error(err)
			break
		}
	}
	if err := lease.Release(); err != nil {
		t.Error(err)
	}
	<-done
	if _, err := lease.Advance(); err == nil {
		t.Fatal("released lease advanced the epoch")
	}
}
