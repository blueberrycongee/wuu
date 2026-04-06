package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session represents one conversation session.
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Summary   string    `json:"summary,omitempty"`
	Entries   int       `json:"entries"`
}

// NewID generates a human-readable, sortable session ID: YYYYMMDD-HHMMSS-xxxx.
func NewID() string {
	b := make([]byte, 2)
	rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Dir returns the sessions directory for a workspace.
func Dir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".wuu", "sessions")
}

// FilePath returns the data file path for a session ID.
func FilePath(sessDir, id string) string {
	return filepath.Join(sessDir, id+".jsonl")
}

// IndexPath returns the index file path.
func IndexPath(sessDir string) string {
	return filepath.Join(sessDir, "index.jsonl")
}

// Create initializes a new session: creates the directory, data file, and index entry.
func Create(sessDir string) (*Session, error) {
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	sess := &Session{
		ID:        NewID(),
		CreatedAt: time.Now().UTC(),
	}

	// Create empty data file.
	dataPath := FilePath(sessDir, sess.ID)
	f, err := os.Create(dataPath)
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}
	f.Close()

	// Append to index.
	if err := appendIndex(sessDir, sess); err != nil {
		return nil, err
	}

	return sess, nil
}

// List reads the index and returns the most recent sessions (up to limit).
func List(sessDir string, limit int) ([]Session, error) {
	indexPath := IndexPath(sessDir)
	f, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()

	var sessions []Session
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s Session
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue // skip corrupt lines
		}
		sessions = append(sessions, s)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan index: %w", err)
	}

	// Sort by created_at descending (most recent first).
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})

	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

// Load returns the data file path for a session ID, verifying it exists.
func Load(sessDir, id string) (string, error) {
	path := FilePath(sessDir, id)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("session %q not found", id)
	}
	return path, nil
}

// UpdateIndex updates the entries count and summary for a session in the index.
func UpdateIndex(sessDir string, id string, entries int, summary string) error {
	indexPath := IndexPath(sessDir)

	// Read all entries.
	sessions, err := List(sessDir, 0)
	if err != nil {
		return err
	}

	// Update the matching session.
	found := false
	for i := range sessions {
		if sessions[i].ID == id {
			sessions[i].Entries = entries
			if summary != "" && sessions[i].Summary == "" {
				sessions[i].Summary = summary
			}
			found = true
			break
		}
	}
	if !found {
		return nil // nothing to update
	}

	// Rewrite index.
	f, err := os.Create(indexPath)
	if err != nil {
		return fmt.Errorf("rewrite index: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	// Sort chronologically for stable output.
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})
	for _, s := range sessions {
		enc.Encode(s)
	}
	return nil
}

// MostRecent returns the most recent session ID, or empty string if none.
func MostRecent(sessDir string) (string, error) {
	sessions, err := List(sessDir, 1)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

func appendIndex(sessDir string, sess *Session) error {
	indexPath := IndexPath(sessDir)
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open index for append: %w", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(sess)
}
