package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ContextSeedVersionV1 = 1

	maxContextSeedBodyBytes       = 24 * 1024
	maxContextSeedEstimatedTokens = 6000
	maxContextSeedReferences      = 32
	maxContextSeedArtifacts       = 16

	SessionLaunchDraft           = "draft"
	SessionLaunchWaitingBoundary = "waiting_boundary"
	SessionLaunchPreparing       = "preparing"
	SessionLaunchReady           = "ready"
	SessionLaunchCommitted       = "committed"
	SessionLaunchCancelled       = "cancelled"
	SessionLaunchFailed          = "failed"
	SessionLaunchInterrupted     = "interrupted"

	SessionLaunchKindHandoff = "handoff"
)

var (
	ErrContextSeedInvalid    = errors.New("context seed is invalid")
	ErrSessionLaunchConflict = errors.New("session launch request conflicts with an existing request")
	ErrSessionLaunchNotFound = errors.New("session launch request not found")
)

type HistorySnapshot struct {
	SessionID  string `json:"session_id"`
	ThroughSeq int    `json:"through_seq"`
}

type HistoryRef struct {
	Snapshot HistorySnapshot `json:"snapshot"`
	StartSeq int             `json:"start_seq"`
	EndSeq   int             `json:"end_seq"`
}

type ContextSeedReference struct {
	ID      string     `json:"id"`
	Label   string     `json:"label,omitempty"`
	History HistoryRef `json:"history"`
}

type ContextSeedArtifact struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type ContextSeedProvenance struct {
	Producer    string `json:"producer"`
	SourceModel string `json:"source_model,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type ContextSeed struct {
	Version    int                    `json:"version"`
	ID         string                 `json:"id"`
	Body       string                 `json:"body"`
	Source     HistorySnapshot        `json:"source"`
	References []ContextSeedReference `json:"references,omitempty"`
	Artifacts  []ContextSeedArtifact  `json:"artifacts,omitempty"`
	Provenance ContextSeedProvenance  `json:"provenance"`
}

type SessionRuntimeSelection struct {
	ModelOptions   map[string]string `json:"model_options,omitempty"`
	Speed          string            `json:"speed,omitempty"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Variant        string            `json:"variant,omitempty"`
	Effort         string            `json:"effort,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
}

// Empty and omitted option maps encode identically and must compare equally
// when an idempotent launch request is replayed after persistence.
func (s SessionRuntimeSelection) equal(other SessionRuntimeSelection) bool {
	return s.Provider == other.Provider && s.Model == other.Model &&
		s.Variant == other.Variant && s.Effort == other.Effort &&
		s.Speed == other.Speed && s.PermissionMode == other.PermissionMode &&
		maps.Equal(s.ModelOptions, other.ModelOptions)
}

type SessionLaunchInput struct {
	Prompt      string `json:"prompt,omitempty"`
	Intent      string `json:"intent,omitempty"`
	ContextJSON string `json:"context_json,omitempty"`
}

type SessionLaunchRecord struct {
	RequestID     string
	Revision      int
	Kind          string
	Status        string
	SourceSession string
	SourceCutoff  int
	SeedID        string
	TargetSession string
	InitialTurnID string
	Owner         string
	Producer      string
	Runtime       SessionRuntimeSelection
	Input         SessionLaunchInput
	Error         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CommittedAt   *time.Time
}

func ValidateContextSeed(seed ContextSeed) error {
	if seed.Version != ContextSeedVersionV1 {
		return fmt.Errorf("%w: version must be %d", ErrContextSeedInvalid, ContextSeedVersionV1)
	}
	if strings.TrimSpace(seed.Body) == "" {
		return fmt.Errorf("%w: body is required", ErrContextSeedInvalid)
	}
	if len([]byte(seed.Body)) > maxContextSeedBodyBytes {
		return fmt.Errorf("%w: body exceeds %d bytes", ErrContextSeedInvalid, maxContextSeedBodyBytes)
	}
	if utf8.RuneCountInString(seed.Body) > maxContextSeedEstimatedTokens*4 {
		return fmt.Errorf("%w: body exceeds estimated token budget", ErrContextSeedInvalid)
	}
	if strings.TrimSpace(seed.Source.SessionID) == "" || seed.Source.ThroughSeq < 1 {
		return fmt.Errorf("%w: source snapshot is required", ErrContextSeedInvalid)
	}
	if len(seed.References) > maxContextSeedReferences {
		return fmt.Errorf("%w: too many references", ErrContextSeedInvalid)
	}
	if len(seed.Artifacts) > maxContextSeedArtifacts {
		return fmt.Errorf("%w: too many artifacts", ErrContextSeedInvalid)
	}
	seen := make(map[string]struct{}, len(seed.References))
	for _, reference := range seed.References {
		id := strings.TrimSpace(reference.ID)
		if id == "" {
			return fmt.Errorf("%w: reference id is required", ErrContextSeedInvalid)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: duplicate reference id %q", ErrContextSeedInvalid, id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(reference.History.Snapshot.SessionID) == "" {
			return fmt.Errorf("%w: reference %q is missing a session id", ErrContextSeedInvalid, id)
		}
		if reference.History.StartSeq < 1 || reference.History.EndSeq < reference.History.StartSeq {
			return fmt.Errorf("%w: reference %q has an invalid seq range", ErrContextSeedInvalid, id)
		}
		if reference.History.EndSeq > reference.History.Snapshot.ThroughSeq {
			return fmt.Errorf("%w: reference %q exceeds its snapshot", ErrContextSeedInvalid, id)
		}
	}
	if strings.TrimSpace(seed.Provenance.Producer) == "" {
		return fmt.Errorf("%w: producer is required", ErrContextSeedInvalid)
	}
	return nil
}

func parseLaunchTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty time")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339, value)
}

func seedHistoryRecord(seed ContextSeed) HistoryRecord {
	raw, _ := json.Marshal(seed)
	return HistoryRecord{
		Role:             "assistant",
		Name:             "context_seed",
		Content:          seed.Body,
		DisplayContent:   "Session handoff brief",
		Origin:           "handoff",
		PresentationKind: "context_seed",
		RelatedSessionID: seed.Source.SessionID,
		ReadOnly:         true,
		Hidden:           true,
		ToolResult:       raw,
	}
}
