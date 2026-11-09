// Package participant defines persisted identities carried by subagent runs.
package participant

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Kind classifies a participant identity.
type Kind string

const (
	KindNamed     Kind = "named"
	KindEphemeral Kind = "ephemeral"
)

// Participant is one named-agent or task-worker identity.
type Participant struct {
	ID        string
	Kind      Kind
	Name      string
	Role      string // WorkerType name; empty for human/primary
	Avatar    string // legacy emoji glyph; no longer written or rendered
	Tagline   string
	Workspace string // persistent dir for named agents; empty otherwise
	Model     string // pinned model; empty = follow global
	CreatedAt time.Time
	UpdatedAt time.Time
	RetiredAt *time.Time
}

// Summary is the wire shape embedded in notifications and thread items.
type Summary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Role string `json:"role,omitempty"`
	// AvatarImage is the participant's uploaded avatar as a base64 data
	// URL ("data:image/png;base64,..."). The avatar bytes live in the
	// participant's workspace directory, not in the store, so
	// Participant.Summary() leaves this empty — the appserver fills it
	// (with a size cap, see appserver.participantSummaryAvatarMaxBytes)
	// because summaries can be duplicated across participant workers and agent
	// trees, where an unbounded payload would bloat thread snapshots.
	AvatarImage string `json:"avatar_image,omitempty"`
}

// Summary returns the wire shape for a participant.
func (p Participant) Summary() Summary {
	return Summary{
		ID:   p.ID,
		Name: p.Name,
		Kind: string(p.Kind),
		Role: p.Role,
	}
}

// NewID generates a participant ID: "prt-" + 16 hex chars.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "prt-" + hex.EncodeToString(b)
}

// DeriveEphemeralName builds a display name for an ephemeral task worker:
// capitalized worker type (or "Agent" when empty), joined to the task name
// with "·" when the task name is non-empty.
func DeriveEphemeralName(taskName, workerType string) string {
	if workerType == "" {
		workerType = "agent"
	}
	name := capitalize(workerType)
	if taskName != "" {
		name += "·" + taskName
	}
	return name
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-'a'+'A') + s[1:]
	}
	return s
}
