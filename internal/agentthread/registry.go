package agentthread

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	mu     sync.Mutex
	byID   map[string]Metadata
	byPath map[string]string
}

func NewRegistry() *Registry {
	return &Registry{
		byID:   make(map[string]Metadata),
		byPath: make(map[string]string),
	}
}

func (r *Registry) RegisterRoot(threadID, sessionID, cwd, model string, now time.Time) Metadata {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := strings.TrimSpace(threadID)
	if id == "" {
		id = strings.TrimSpace(sessionID)
	}
	meta := Metadata{
		ID:        id,
		SessionID: strings.TrimSpace(sessionID),
		Path:      RootPath,
		TaskName:  "root",
		Role:      "root",
		CWD:       strings.TrimSpace(cwd),
		Model:     strings.TrimSpace(model),
		Source: Source{
			Kind: SourceRoot,
		},
		Status:    StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[id]; ok {
		existing.SessionID = meta.SessionID
		existing.CWD = meta.CWD
		existing.Model = meta.Model
		existing.UpdatedAt = now
		r.byID[id] = existing
		r.byPath[existing.Path] = id
		return existing
	}
	r.byID[id] = meta
	r.byPath[RootPath] = id
	return meta
}

func (r *Registry) RegisterSpawn(spec SpawnSpec) (Metadata, error) {
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		return Metadata{}, errors.New("thread id is required")
	}
	now := spec.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	parentPath := cleanPath(spec.ParentPath)
	if parentPath == "" {
		parentPath = RootPath
	}
	if _, err := ParseAgentPath(parentPath); err != nil {
		return Metadata{}, err
	}
	parentID := strings.TrimSpace(spec.ParentID)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[id]; exists {
		return Metadata{}, fmt.Errorf("thread id %q already exists", id)
	}
	if parentID == "" {
		parentID = r.byPath[parentPath]
	}
	path, taskName, err := r.nextChildPathLocked(parentPath, spec.TaskName)
	if err != nil {
		return Metadata{}, err
	}
	sourceKind := spec.SourceKind
	if sourceKind == "" {
		sourceKind = SourceThreadSpawn
	}
	status := spec.Status
	if status == "" {
		status = StatusPending
	}
	meta := Metadata{
		ID:              id,
		SessionID:       strings.TrimSpace(spec.SessionID),
		ParentID:        parentID,
		Path:            path,
		TaskName:        taskName,
		AgentProfile:    strings.TrimSpace(spec.AgentProfile),
		Role:            strings.TrimSpace(spec.Role),
		LastTaskMessage: strings.TrimSpace(spec.LastTaskMessage),
		CWD:             strings.TrimSpace(spec.CWD),
		Model:           strings.TrimSpace(spec.Model),
		Source: Source{
			Kind:           sourceKind,
			ParentThreadID: parentID,
			ParentPath:     parentPath,
			Depth:          depthForPath(path),
			ForkMode:       strings.TrimSpace(spec.ForkMode),
			EdgeStatus:     EdgeOpen,
		},
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.byID[id] = meta
	r.byPath[path] = id
	return meta, nil
}

func (r *Registry) Restore(meta Metadata) error {
	id := strings.TrimSpace(meta.ID)
	path := cleanPath(meta.Path)
	if id == "" {
		return errors.New("thread id is required")
	}
	if path == "" {
		return errors.New("thread path is required")
	}
	if _, err := ParseAgentPath(path); err != nil {
		return err
	}
	meta.ID = id
	meta.Path = path
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = meta.CreatedAt
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existingID, exists := r.byPath[path]; exists && existingID != id {
		return fmt.Errorf("thread path %q already exists", path)
	}
	if existing, exists := r.byID[id]; exists && existing.Path != path {
		return fmt.Errorf("thread id %q already exists at %q", id, existing.Path)
	}
	r.byID[id] = meta
	r.byPath[path] = id
	return nil
}

func (r *Registry) Resolve(target string) (Metadata, bool) {
	return r.ResolveFrom(RootPath, target)
}

func (r *Registry) ResolveFrom(currentPath, target string) (Metadata, bool) {
	key := strings.TrimSpace(target)
	if key == "" {
		return Metadata{}, false
	}
	current, err := ParseAgentPath(cleanPath(currentPath))
	if err != nil {
		current = RootAgentPath()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if meta, ok := r.byID[key]; ok {
		return meta, true
	}
	if id, ok := r.byPath[cleanPath(key)]; ok {
		return r.byID[id], true
	}
	if !strings.HasPrefix(key, "/") {
		if path, err := ResolveAgentPath(current, key); err == nil {
			if id, ok := r.byPath[string(path)]; ok {
				return r.byID[id], true
			}
		}
	}
	return Metadata{}, false
}

func (r *Registry) UpdateStatus(id string, status Status, now time.Time) (Metadata, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return Metadata{}, false
	}
	meta.Status = status
	meta.UpdatedAt = now
	r.byID[meta.ID] = meta
	return meta, true
}

func (r *Registry) UpdateLastTaskMessage(id, message string, now time.Time) (Metadata, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return Metadata{}, false
	}
	meta.LastTaskMessage = strings.TrimSpace(message)
	meta.UpdatedAt = now
	r.byID[meta.ID] = meta
	return meta, true
}

func (r *Registry) UpdateEdgeStatus(id string, status EdgeStatus, now time.Time) (Metadata, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return Metadata{}, false
	}
	meta.Source.EdgeStatus = status
	meta.UpdatedAt = now
	r.byID[meta.ID] = meta
	return meta, true
}

func (r *Registry) List() []Metadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Metadata, 0, len(r.byID))
	for _, meta := range r.byID {
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func (r *Registry) Subtree(id string) []Metadata {
	key := strings.TrimSpace(id)
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	root, ok := r.byID[key]
	if !ok {
		return nil
	}
	prefix := root.Path + "/"
	out := make([]Metadata, 0, len(r.byID))
	for _, meta := range r.byID {
		if meta.ID == root.ID || strings.HasPrefix(meta.Path, prefix) {
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func (r *Registry) nextChildPathLocked(parentPath, taskName string) (string, string, error) {
	trimmedName := strings.TrimSpace(taskName)
	if trimmedName == "" {
		return "", "", errors.New("task_name is required")
	}
	if err := ValidateAgentName(trimmedName); err != nil {
		return "", "", err
	}
	parent, err := ParseAgentPath(parentPath)
	if err != nil {
		return "", "", err
	}
	child, err := JoinAgentPath(parent, trimmedName)
	if err != nil {
		return "", "", err
	}
	path := string(child)
	if _, exists := r.byPath[path]; exists {
		return "", "", fmt.Errorf("agent path %q already exists", path)
	}
	return path, trimmedName, nil
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "/") {
		return strings.TrimRight(path, "/")
	}
	resolved, err := ResolveAgentPath(RootAgentPath(), path)
	if err != nil {
		return path
	}
	return string(resolved)
}

func depthForPath(path string) int {
	path = cleanPath(path)
	if path == RootPath {
		return 1
	}
	return strings.Count(strings.Trim(path, "/"), "/") + 1
}
