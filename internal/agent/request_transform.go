package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type RequestTransform func(ctx context.Context, req *providers.ChatRequest) error

type RequestTransformProvider interface {
	TransformKey() string
	Transform() RequestTransform
	TransformPriority() int
}

type RequestTransformChain struct {
	mu         sync.RWMutex
	transforms map[string]RequestTransformProvider
	owners     map[string]string // key → pluginID
	order      []string
}

func NewRequestTransformChain() *RequestTransformChain {
	return &RequestTransformChain{
		transforms: make(map[string]RequestTransformProvider),
		owners:     make(map[string]string),
	}
}

func (c *RequestTransformChain) Add(p RequestTransformProvider) {
	c.AddWithOwner(p, "")
}

func (c *RequestTransformChain) AddWithOwner(p RequestTransformProvider, pluginID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := p.TransformKey()
	if _, exists := c.transforms[key]; !exists {
		c.order = append(c.order, key)
	}
	c.transforms[key] = p
	if pluginID != "" {
		c.owners[key] = pluginID
	}
}

func (c *RequestTransformChain) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.transforms, key)
	delete(c.owners, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *RequestTransformChain) RemoveByPlugin(pluginID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeByOwner(pluginID)
}

func (c *RequestTransformChain) removeByOwner(owner string) {
	var toRemove []string
	for key, entryOwner := range c.owners {
		if entryOwner == owner {
			toRemove = append(toRemove, key)
		}
	}
	for _, key := range toRemove {
		delete(c.transforms, key)
		delete(c.owners, key)
	}
	filtered := make([]string, 0, len(c.order))
	for _, key := range c.order {
		if _, ok := c.transforms[key]; ok {
			filtered = append(filtered, key)
		}
	}
	c.order = filtered
}

func (c *RequestTransformChain) Apply(ctx context.Context, req *providers.ChatRequest, beforeFn RequestTransform) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if beforeFn != nil {
		if err := beforeFn(ctx, req); err != nil {
			return fmt.Errorf("request transform (legacy before): %w", err)
		}
	}

	providers := make([]RequestTransformProvider, 0, len(c.transforms))
	for _, key := range c.order {
		if p, ok := c.transforms[key]; ok {
			providers = append(providers, p)
		}
	}

	sort.Slice(providers, func(i, j int) bool {
		pi, pj := providers[i].TransformPriority(), providers[j].TransformPriority()
		if pi != pj {
			return pi > pj
		}
		return providers[i].TransformKey() < providers[j].TransformKey()
	})

	for _, p := range providers {
		if err := p.Transform()(ctx, req); err != nil {
			return fmt.Errorf("request transform %s: %w", p.TransformKey(), err)
		}
	}

	return nil
}

func (c *RequestTransformChain) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.transforms)
}

type simpleRequestTransform struct {
	key      string
	fn       RequestTransform
	priority int
}

func (p *simpleRequestTransform) TransformKey() string        { return p.key }
func (p *simpleRequestTransform) Transform() RequestTransform { return p.fn }
func (p *simpleRequestTransform) TransformPriority() int      { return p.priority }

func NewRequestTransform(key string, fn RequestTransform, priority int) RequestTransformProvider {
	return &simpleRequestTransform{key: key, fn: fn, priority: priority}
}
