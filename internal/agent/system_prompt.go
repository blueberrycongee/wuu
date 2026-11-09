package agent

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SystemPromptProvider contributes a named section to the assembled system
// prompt. Each provider returns a stable key, the section text, and whether
// the section is static (same for every request) or dynamic (may change
// between requests).
//
// Providers are called in priority order (highest first). When two sections
// share the same key, the higher-priority section wins and the other is
// silently dropped.
type SystemPromptProvider interface {
	// SystemPromptKey returns a stable identifier for this section.
	// Keys must be unique within a single assembly; duplicates are
	// resolved by priority.
	SystemPromptKey() string

	// SystemPromptSection returns the section text and whether it is
	// static (unchanging across requests) or dynamic. Dynamic sections
	// are called on every request assembly; static sections are called
	// once and cached.
	SystemPromptSection() (text string, static bool)

	// SystemPromptPriority returns the ordering priority. Higher values
	// come first in the assembled prompt. Sections with the same
	// priority are ordered by key for determinism.
	SystemPromptPriority() int
}

// SystemPromptAssembler collects sections from registered providers and
// builds the complete system prompt. It tracks plugin ownership so that
// deactivating a plugin generation atomically withdraws all its sections.
//
// It is safe for concurrent use.
type SystemPromptAssembler struct {
	mu        sync.RWMutex
	providers map[string]SystemPromptProvider // keyed by provider key
	owners    map[string]string               // key → pluginID
	order     []string                        // insertion order
}

// NewSystemPromptAssembler creates an empty assembler.
func NewSystemPromptAssembler() *SystemPromptAssembler {
	return &SystemPromptAssembler{
		providers: make(map[string]SystemPromptProvider),
		owners:    make(map[string]string),
	}
}

// Add registers a system prompt provider. If a provider with the same
// key already exists, it is replaced.
func (a *SystemPromptAssembler) Add(p SystemPromptProvider) {
	a.AddWithOwner(p, "")
}

// AddWithOwner registers a system prompt provider and records which
// plugin owns it. When the plugin is deactivated, RemoveByPlugin
// atomically withdraws all sections registered by that plugin.
func (a *SystemPromptAssembler) AddWithOwner(p SystemPromptProvider, pluginID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := p.SystemPromptKey()
	if _, exists := a.providers[key]; !exists {
		a.order = append(a.order, key)
	}
	a.providers[key] = p
	if pluginID != "" {
		a.owners[key] = pluginID
	}
}

// Remove unregisters a provider by its key.
func (a *SystemPromptAssembler) Remove(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.providers, key)
	delete(a.owners, key)
	for i, k := range a.order {
		if k == key {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
}

// RemoveByPlugin withdraws all sections registered by the given plugin.
// Used by the generation lifecycle to atomically clean up on deactivation.
func (a *SystemPromptAssembler) RemoveByPlugin(pluginID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removeByOwner(pluginID)
}

func (a *SystemPromptAssembler) removeByOwner(owner string) {
	var toRemove []string
	for key, entryOwner := range a.owners {
		if entryOwner == owner {
			toRemove = append(toRemove, key)
		}
	}
	for _, key := range toRemove {
		delete(a.providers, key)
		delete(a.owners, key)
	}
	// Rebuild order slice excluding removed keys.
	filtered := make([]string, 0, len(a.order))
	for _, key := range a.order {
		if _, ok := a.providers[key]; ok {
			filtered = append(filtered, key)
		}
	}
	a.order = filtered
}

// Clear removes all providers.
func (a *SystemPromptAssembler) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = make(map[string]SystemPromptProvider)
	a.owners = make(map[string]string)
	a.order = nil
}

// Assemble builds the complete system prompt from all registered sections.
// It returns the full prompt text and metadata for each section.
//
// Sections are sorted by priority (descending), then by key for determinism.
// The host's base system prompt is prepended as the first section.
func (a *SystemPromptAssembler) Assemble(basePrompt string) (fullPrompt string, sections []SystemPromptSectionInfo) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Collect providers.
	providers := make([]SystemPromptProvider, 0, len(a.providers))
	for _, key := range a.order {
		if p, ok := a.providers[key]; ok {
			providers = append(providers, p)
		}
	}

	// Sort by priority (descending), then by key.
	sort.Slice(providers, func(i, j int) bool {
		pi, pj := providers[i].SystemPromptPriority(), providers[j].SystemPromptPriority()
		if pi != pj {
			return pi > pj
		}
		return providers[i].SystemPromptKey() < providers[j].SystemPromptKey()
	})

	// Build sections.
	type section struct {
		key    string
		text   string
		static bool
	}
	var parts []section

	// Base prompt always comes first (highest priority implicitly).
	if strings.TrimSpace(basePrompt) != "" {
		parts = append(parts, section{key: "host.base", text: basePrompt, static: true})
	}

	for _, p := range providers {
		text, static := p.SystemPromptSection()
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, section{
			key:    p.SystemPromptKey(),
			text:   text,
			static: static,
		})
	}

	// Assemble.
	var b strings.Builder
	sections = make([]SystemPromptSectionInfo, 0, len(parts))
	for i, part := range parts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(part.text)
		hash := sha256.Sum256([]byte(part.text))
		sections = append(sections, SystemPromptSectionInfo{
			Key:    part.key,
			Static: part.static,
			Bytes:  len(part.text),
			Hash:   fmt.Sprintf("%x", hash[:8]),
		})
	}

	return b.String(), sections
}

// ProviderCount returns the number of registered providers.
func (a *SystemPromptAssembler) ProviderCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.providers)
}

// simplePromptProvider is a basic implementation of SystemPromptProvider
// for straightforward static sections.
type simplePromptProvider struct {
	key      string
	text     string
	priority int
}

func (p *simplePromptProvider) SystemPromptKey() string             { return p.key }
func (p *simplePromptProvider) SystemPromptSection() (string, bool) { return p.text, true }
func (p *simplePromptProvider) SystemPromptPriority() int           { return p.priority }

// NewStaticPromptSection creates a simple static system prompt section provider.
func NewStaticPromptSection(key, text string, priority int) SystemPromptProvider {
	return &simplePromptProvider{key: key, text: text, priority: priority}
}
