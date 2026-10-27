package toolbatch

import (
	"context"
	"errors"
	"sync"
)

// V1Adapter explicitly adapts existing agent.tools@1 per-call providers. It has
// no native toolkit dependency and uses the same resources as external batches.
type V1Adapter struct{}

func (V1Adapter) ExecuteBatch(ctx context.Context, req Request, resources Resources) error {
	resolved := make([]Resolution, len(req.Calls))
	for i := range req.Calls {
		var err error
		resolved[i], err = resources.Resolve(ctx, i)
		if err != nil {
			return err
		}
	}
	capacity := max(1, req.MaxParallel)
	if req.Serial {
		capacity = 1
	}
	for start := 0; start < len(req.Calls); {
		end := start + 1
		if !req.Serial && resolved[start].ConcurrencySafe {
			for end < len(req.Calls) && resolved[end].ConcurrencySafe {
				end++
			}
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var failures []error
		slots := make(chan struct{}, capacity)
		for i := start; i < end; i++ {
			if err := ctx.Err(); err != nil {
				wg.Wait()
				return errors.Join(append(failures, err)...)
			}
			slots <- struct{}{}
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				defer func() { <-slots }()
				_, err := resources.Execute(ctx, index)
				if err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
		start = end
	}
	return nil
}

func (V1Adapter) SupportsStreaming() bool { return true }
func (V1Adapter) AdmitStream(ctx context.Context, request StreamRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return request.ReadOnly && request.ConcurrencySafe, nil
}
