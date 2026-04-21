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
	"syscall"
	"time"
)

// withIndexLock serializes access to the session index file across
// processes. Two concurrent wuu sessions in the same workspace can
// otherwise race between appendIndex and UpdateIndex's truncate-rewrite,
// losing session entries. Blocking is fine — index operations are brief.
func withIndexLock(sessDir string, exclusive bool, fn func() error) error {
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	lockPath := filepath.Join(sessDir, ".index.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open index lock: %w", err)
	}
	defer f.Close()
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		return fmt.Errorf("acquire index lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

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
// If id is non-empty, it is used as the session ID; otherwise a new one is generated.
func Create(sessDir string, id ...string) (*Session, error) {
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	sessID := NewID()
	if len(id) > 0 && id[0] != "" {
		sessID = id[0]
	}

	sess := &Session{
		ID:        sessID,
		CreatedAt: time.Now().UTC(),
	}

	// Hold the index lock for both the data-file create and the index
	// append so a concurrent UpdateIndex cannot snapshot the index
	// between the two and rewrite it without this session's entry.
	if err := withIndexLock(sessDir, true, func() error {
		dataPath := FilePath(sessDir, sess.ID)
		f, err := os.Create(dataPath)
		if err != nil {
			return fmt.Errorf("create session file: %w", err)
		}
		f.Close()
		return appendIndexLocked(sessDir, sess)
	}); err != nil {
		return nil, err
	}
	return sess, nil
}

// List reads the index and returns the most recent sessions (up to limit).
func List(sessDir string, limit int) ([]Session, error) {
	var sessions []Session
	err := withIndexLock(sessDir, false, func() error {
		var err error
		sessions, err = listLocked(sessDir, limit)
		return err
	})
	return sessions, err
}

// listLocked reads the index assuming the caller already holds the lock.
func listLocked(sessDir string, limit int) ([]Session, error) {
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
	return withIndexLock(sessDir, true, func() error {
		sessions, err := listLocked(sessDir, 0)
		if err != nil {
			return err
		}

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
			return nil
		}

		// Sort chronologically for stable output, then write to a
		// temp file + rename so a crash mid-write can't leave a
		// truncated index.
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
		})
		indexPath := IndexPath(sessDir)
		tmp, err := os.CreateTemp(sessDir, ".index.*.tmp")
		if err != nil {
			return fmt.Errorf("rewrite index: %w", err)
		}
		tmpName := tmp.Name()
		enc := json.NewEncoder(tmp)
		for _, s := range sessions {
			if err := enc.Encode(s); err != nil {
				tmp.Close()
				os.Remove(tmpName)
				return fmt.Errorf("write index: %w", err)
			}
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("close index tmp: %w", err)
		}
		if err := os.Rename(tmpName, indexPath); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("rename index: %w", err)
		}
		return nil
	})
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

// appendIndexLocked appends to the index assuming the caller already holds
// the exclusive lock (via withIndexLock). O_APPEND is atomic for small
// writes, but the Create→append sequence in Create must be atomic as a
// whole against UpdateIndex, which is why the lock is required.
func appendIndexLocked(sessDir string, sess *Session) error {
	indexPath := IndexPath(sessDir)
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open index for append: %w", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(sess)
}
