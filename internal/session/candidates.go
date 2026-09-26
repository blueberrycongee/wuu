package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	CandidateApplied   = "applied"
	CandidateDiscarded = "discarded"
)

// ErrCandidateDecided rejects a second decision on the same candidate.
var ErrCandidateDecided = errors.New("candidate already has a decision")

// Candidate is a frozen snapshot of a managed session's worktree changes at
// the end of one turn. The user's disposition is the delivery decision.
type Candidate struct {
	SessionID    string
	TurnID       string
	BaseRepo     string
	BaseRevision string
	Revision     string
	ChangedFiles []string
	Disposition  string
	CreatedAt    time.Time
	DisposedAt   time.Time
}

const candidateColumns = `session_id,turn_id,base_repo,base_revision,revision,changed_files_json,disposition,created_at,COALESCE(disposed_at,'')`

// PutCandidate records a snapshot once; a replayed turn keeps the first one.
func PutCandidate(dir string, candidate Candidate) error {
	if candidate.SessionID == "" || candidate.TurnID == "" || candidate.Revision == "" || candidate.BaseRevision == "" {
		return errors.New("candidate requires a session, turn and revisions")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	files, err := json.Marshal(append([]string{}, candidate.ChangedFiles...))
	if err != nil {
		return err
	}
	db, err := openStore(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`INSERT OR IGNORE INTO session_candidates(session_id,turn_id,base_repo,base_revision,revision,changed_files_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		candidate.SessionID, candidate.TurnID, candidate.BaseRepo, candidate.BaseRevision, candidate.Revision, string(files), timeText(candidate.CreatedAt))
	return err
}

// ListCandidates returns the candidates of the given sessions, oldest first.
func ListCandidates(dir string, sessionIDs []string) ([]Candidate, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_candidates"); err != nil || !exists {
		return nil, err
	}
	args := make([]any, len(sessionIDs))
	for index, id := range sessionIDs {
		args[index] = id
	}
	rows, err := db.Query(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id IN (?`+strings.Repeat(",?", len(sessionIDs)-1)+`) ORDER BY created_at, turn_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []Candidate
	for rows.Next() {
		candidate, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// FindCandidate reads one candidate.
func FindCandidate(dir, sessionID, turnID string) (Candidate, bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return Candidate{}, false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_candidates"); err != nil || !exists {
		return Candidate{}, false, err
	}
	candidate, err := scanCandidate(db.QueryRow(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id=? AND turn_id=?`, sessionID, turnID))
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, false, nil
	}
	return candidate, err == nil, err
}

// LatestAppliedCandidate is the base for the session's next snapshot, so an
// applied change is never offered again.
func LatestAppliedCandidate(dir, sessionID string) (Candidate, bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return Candidate{}, false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_candidates"); err != nil || !exists {
		return Candidate{}, false, err
	}
	candidate, err := scanCandidate(db.QueryRow(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id=? AND disposition=? ORDER BY disposed_at DESC LIMIT 1`, sessionID, CandidateApplied))
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, false, nil
	}
	return candidate, err == nil, err
}

// DecideCandidate records the user's disposition exactly once.
func DecideCandidate(dir, sessionID, turnID, disposition string) (Candidate, error) {
	if disposition != CandidateApplied && disposition != CandidateDiscarded {
		return Candidate{}, errors.New("disposition must be applied or discarded")
	}
	db, err := openStore(dir)
	if err != nil {
		return Candidate{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	result, err := db.Exec(`UPDATE session_candidates SET disposition=?,disposed_at=? WHERE session_id=? AND turn_id=? AND disposition=''`,
		disposition, timeText(time.Now().UTC()), sessionID, turnID)
	if err != nil {
		return Candidate{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return Candidate{}, err
	} else if n != 1 {
		return Candidate{}, ErrCandidateDecided
	}
	return scanCandidate(db.QueryRow(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id=? AND turn_id=?`, sessionID, turnID))
}

type candidateScanner interface {
	Scan(dest ...any) error
}

func scanCandidate(row candidateScanner) (Candidate, error) {
	var candidate Candidate
	var files, created, disposed string
	if err := row.Scan(&candidate.SessionID, &candidate.TurnID, &candidate.BaseRepo, &candidate.BaseRevision, &candidate.Revision, &files, &candidate.Disposition, &created, &disposed); err != nil {
		return Candidate{}, err
	}
	if err := json.Unmarshal([]byte(files), &candidate.ChangedFiles); err != nil {
		return Candidate{}, err
	}
	candidate.CreatedAt, candidate.DisposedAt = parseTime(created), parseTime(disposed)
	return candidate, nil
}
