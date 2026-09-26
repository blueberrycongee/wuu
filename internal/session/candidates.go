package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// A candidate's disposition. Applied and published candidates are delivered:
// the session's next candidate is measured from the latest of them. A
// superseded candidate was replaced by a newer turn of the same session.
const (
	CandidateApplied    = "applied"
	CandidateDiscarded  = "discarded"
	CandidatePublished  = "published"
	CandidateSuperseded = "superseded"
)

var (
	// ErrCandidateDecided rejects a second decision on the same candidate.
	ErrCandidateDecided = errors.New("candidate already has a decision")
	// ErrCandidateSuperseded rejects a decision on a candidate a newer turn replaced.
	ErrCandidateSuperseded = errors.New("a newer proposal from this session replaced this one")
)

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
	// URL is where a published candidate was sent for review.
	URL        string
	CreatedAt  time.Time
	DisposedAt time.Time
}

const candidateColumns = `session_id,turn_id,base_repo,base_revision,revision,changed_files_json,disposition,url,created_at,COALESCE(disposed_at,'')`

// PutCandidate records a snapshot once; a replayed turn keeps the first one.
// Each candidate contains every undelivered change of its session, so a new
// one supersedes the session's older undecided candidates.
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
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO session_candidates(session_id,turn_id,base_repo,base_revision,revision,changed_files_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		candidate.SessionID, candidate.TurnID, candidate.BaseRepo, candidate.BaseRevision, candidate.Revision, string(files), timeText(candidate.CreatedAt))
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 0 {
		return err
	}
	if _, err := tx.Exec(`UPDATE session_candidates SET disposition=?,disposed_at=? WHERE session_id=? AND turn_id<>? AND disposition=''`,
		CandidateSuperseded, timeText(candidate.CreatedAt), candidate.SessionID, candidate.TurnID); err != nil {
		return err
	}
	return tx.Commit()
}

// SupersedeCandidates retires a session's undecided candidates once its
// worktree holds no undelivered change, and reports how many it retired.
func SupersedeCandidates(dir, sessionID string) (int64, error) {
	db, err := openStore(dir)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	result, err := db.Exec(`UPDATE session_candidates SET disposition=?,disposed_at=? WHERE session_id=? AND disposition=''`,
		CandidateSuperseded, timeText(time.Now().UTC()), sessionID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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

// PendingCandidateCounts counts undecided candidates of live project sessions
// by session and by the project that manages them, in one store read.
func PendingCandidateCounts(dir, sessionSource string) (bySession, byProject map[string]int, err error) {
	bySession, byProject = map[string]int{}, map[string]int{}
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return bySession, byProject, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_candidates"); err != nil || !exists {
		return bySession, byProject, err
	}
	rows, err := db.Query(`SELECT c.session_id,s.parent_id,COUNT(*) FROM session_candidates c JOIN sessions s ON s.id=c.session_id
		WHERE c.disposition='' AND s.source=? AND s.archived_at IS NULL GROUP BY c.session_id,s.parent_id`, sessionSource)
	if err != nil {
		return bySession, byProject, err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID, projectID string
		var count int
		if err := rows.Scan(&sessionID, &projectID, &count); err != nil {
			return bySession, byProject, err
		}
		bySession[sessionID] += count
		byProject[projectID] += count
	}
	return bySession, byProject, rows.Err()
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

// LatestDeliveredCandidate is the base for the session's next snapshot, so a
// change that was applied or published is never offered again.
func LatestDeliveredCandidate(dir, sessionID string) (Candidate, bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return Candidate{}, false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_candidates"); err != nil || !exists {
		return Candidate{}, false, err
	}
	candidate, err := scanCandidate(db.QueryRow(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id=? AND disposition IN (?,?) ORDER BY disposed_at DESC LIMIT 1`,
		sessionID, CandidateApplied, CandidatePublished))
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, false, nil
	}
	return candidate, err == nil, err
}

// DecideCandidate records the user's disposition exactly once. url records
// where a published candidate went.
func DecideCandidate(dir, sessionID, turnID, disposition, url string) (Candidate, error) {
	switch disposition {
	case CandidateApplied, CandidateDiscarded:
		url = ""
	case CandidatePublished:
		if strings.TrimSpace(url) == "" {
			return Candidate{}, errors.New("a published candidate needs its URL")
		}
	default:
		return Candidate{}, errors.New("disposition must be applied, discarded or published")
	}
	db, err := openStore(dir)
	if err != nil {
		return Candidate{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	result, err := db.Exec(`UPDATE session_candidates SET disposition=?,url=?,disposed_at=? WHERE session_id=? AND turn_id=? AND disposition=''`,
		disposition, strings.TrimSpace(url), timeText(time.Now().UTC()), sessionID, turnID)
	if err != nil {
		return Candidate{}, err
	}
	decided, err := scanCandidate(db.QueryRow(`SELECT `+candidateColumns+` FROM session_candidates WHERE session_id=? AND turn_id=?`, sessionID, turnID))
	if err != nil {
		return Candidate{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return Candidate{}, err
	} else if n != 1 {
		if decided.Disposition == CandidateSuperseded {
			return Candidate{}, ErrCandidateSuperseded
		}
		return Candidate{}, ErrCandidateDecided
	}
	return decided, nil
}

type candidateScanner interface {
	Scan(dest ...any) error
}

func scanCandidate(row candidateScanner) (Candidate, error) {
	var candidate Candidate
	var files, created, disposed string
	if err := row.Scan(&candidate.SessionID, &candidate.TurnID, &candidate.BaseRepo, &candidate.BaseRevision, &candidate.Revision, &files, &candidate.Disposition, &candidate.URL, &created, &disposed); err != nil {
		return Candidate{}, err
	}
	if err := json.Unmarshal([]byte(files), &candidate.ChangedFiles); err != nil {
		return Candidate{}, err
	}
	candidate.CreatedAt, candidate.DisposedAt = parseTime(created), parseTime(disposed)
	return candidate, nil
}
