package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/persistence"
)

type failingRootDocuments struct{ persistence.Documents }

func (d failingRootDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	for _, write := range tx.Writes {
		if write.Namespace == agentthread.DocumentNamespace {
			return persistence.Receipt{}, errors.New("injected root registration failure")
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestRootRegistrationFailureDrainsNativeToolScopes(t *testing.T) {
	owner, observer := newTemporaryScopeRuntime(t)
	owner.Documents = failingRootDocuments{owner.Documents}
	for i := 0; i < 3; i++ {
		thread, err := owner.NewThreadRuntimeForRoot("failed-root", owner.RootDir)
		if thread != nil || err == nil {
			t.Fatalf("failure was hidden: thread=%v err=%v", thread, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := owner.waitNativeCleanups(ctx); err != nil {
		t.Fatal(err)
	}
	calls := observer.snapshot()
	if len(calls) < 3 {
		t.Fatalf("native provider was not reached for each failed construction: %d", len(calls))
	}
	for _, call := range calls {
		observer.assertClosed(t, call)
	}
}
