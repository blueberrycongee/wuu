// Package conversations defines the text-only account history copy.
// It excludes tool payloads, hidden prompts and attachment bytes.
package conversations

import (
	"encoding/json"
	"errors"
	"strings"
)

const MaxSnapshotBytes = 4 << 20

type Message struct {
	ID     string `json:"id"`
	TurnID string `json:"turn_id"`
	Role   string `json:"role"`
	Text   string `json:"text"`
}
type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt string    `json:"updated_at"`
	Status    string    `json:"status,omitempty"`
	Messages  []Message `json:"messages"`
}
type Settings struct {
	Host       string `json:"host"`
	Enabled    bool   `json:"enabled"`
	Generation string `json:"generation"`
}
type Entry struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	Revision  string `json:"revision"`
	Digest    string `json:"digest"`
	Deleted   bool   `json:"deleted"`
}
type Changes struct {
	Settings
	Entries []Entry `json:"entries"`
	Cursor  string  `json:"cursor"`
	More    bool    `json:"more"`
}

// Compare-and-swap rejects late uploads from an older worker. Disabling sync
// invalidates the generation and deletes its server copy.
type Mutation struct {
	Generation string `json:"generation"`
	Expected   string `json:"expected"`
	Deleted    bool   `json:"deleted"`
	Thread     Thread `json:"thread"`
}

func (t Thread) Validate() error {
	if !validID(t.ID) || len(t.Title) > 4096 || len(t.UpdatedAt) > 64 || len(t.Status) > 64 || len(t.Messages) > 50000 {
		return errors.New("invalid conversation snapshot")
	}
	seen := make(map[string]bool, len(t.Messages))
	for _, m := range t.Messages {
		key := m.TurnID + "\x00" + m.ID
		if !validID(m.ID) || !validID(m.TurnID) || seen[key] || (m.Role != "user" && m.Role != "assistant") {
			return errors.New("invalid conversation message")
		}
		seen[key] = true
	}
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > MaxSnapshotBytes {
		return errors.New("conversation exceeds the 4 MiB sync limit; no content was truncated")
	}
	return nil
}
func validID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsRune(id, '\x00')
}
