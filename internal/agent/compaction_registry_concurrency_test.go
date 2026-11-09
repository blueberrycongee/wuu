package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPluginCompactionRegistryAllowsConcurrentReloadAndResolution(t *testing.T) {
	compactions := NewCompactionRegistry()

	const workers = 16
	const iterations = 100
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				key := fmt.Sprintf("plugin-%d", worker)
				compactions.Register(concurrentCompactionProvider{key: key})
				_ = compactions.Resolve(nil)
				_ = compactions.Count()
				compactions.Unregister(key)
			}
		}()
	}
	wg.Wait()
}

type concurrentCompactionProvider struct{ key string }

func (p concurrentCompactionProvider) CompactionKey() string { return p.key }
func (concurrentCompactionProvider) Compact(_ context.Context, _ string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return messages, nil
}
func (concurrentCompactionProvider) CompactionPriority() int { return 0 }
