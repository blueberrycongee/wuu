// Package engineusage scans engine-local ledgers — the transcript files and
// databases that agent CLIs persist outside Wuu — so the settings usage page
// can report per-engine token consumption for the whole machine, not only the
// turns Wuu itself drove. Each supported engine owns one parser; native
// sessions a Wuu thread already claims (via its persisted engine reference)
// are skipped because Wuu's token_usage trail counts them already.
package engineusage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Supported is the set of engines with a readable local ledger.
var Supported = map[string]bool{"claude": true, "codex": true, "devin": true, "opencode": true}

// Row is one usage observation in an engine-local ledger. At records the
// observation time when the ledger carries one.
type Row struct {
	Engine              string
	SessionID           string
	At                  time.Time
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

// Total is the row's context-weighted token sum.
func (r Row) Total() int {
	return r.InputTokens + r.OutputTokens + r.CacheCreationTokens + r.CacheReadTokens
}

// ScanResult reports what one scan produced. Rows only covers engines whose
// scan completed; Failed names engines whose ledger exists but could not be
// read, so callers can mark that outside column unavailable instead of
// silently reporting zero.
type ScanResult struct {
	Rows   []Row
	Failed map[string]error
}

// ExcludeRefs maps engine id -> native session references already claimed by
// Wuu threads (Session.EngineRef values).
type ExcludeRefs map[string]map[string]struct{}

func (x ExcludeRefs) contains(engine, ref string) bool {
	if x == nil || ref == "" {
		return false
	}
	_, ok := x[engine][ref]
	return ok
}

// Scanner scans engine-local ledgers. A cached file is only re-read when its
// size or mtime changes, which keeps repeat scans cheap on multi-GB ledger
// directories; ledger files are append-only, and a rewritten file simply
// re-parses from its new content.
type Scanner struct {
	mu    sync.Mutex
	files map[string]cachedFile

	// HomeDir overrides the user home the default ledger locations derive
	// from; empty uses os.UserHomeDir. The per-engine fields below override
	// individual locations.
	HomeDir    string
	ClaudeDir  string
	CodexDirs  []string
	DevinDB    string
	OpenCodeDB string
}

type cachedFile struct {
	size    int64
	modTime time.Time
	rows    []Row
}

// cachedRows and storeRows key a ledger file's parsed rows on a caller-chosen
// size/mtime fingerprint, so composite sources (a WAL database and its
// sidecar) can fold extra files into the key.
func (s *Scanner) cachedRows(path string, size int64, modTime time.Time) ([]Row, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[path]
	if !ok || f.size != size || !f.modTime.Equal(modTime) {
		return nil, false
	}
	return f.rows, true
}

func (s *Scanner) storeRows(path string, size int64, modTime time.Time, rows []Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = make(map[string]cachedFile)
	}
	s.files[path] = cachedFile{size: size, modTime: modTime, rows: rows}
}

func (s *Scanner) home() string {
	if s.HomeDir != "" {
		return s.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (s *Scanner) claudeDir() string {
	if s.ClaudeDir != "" {
		return s.ClaudeDir
	}
	return filepath.Join(s.home(), ".claude", "projects")
}

func (s *Scanner) codexDirs() []string {
	if len(s.CodexDirs) > 0 {
		return s.CodexDirs
	}
	return []string{
		filepath.Join(s.home(), ".codex", "sessions"),
		filepath.Join(s.home(), ".codex", "archived_sessions"),
	}
}

func (s *Scanner) devinDB() string {
	if s.DevinDB != "" {
		return s.DevinDB
	}
	return filepath.Join(s.home(), ".local", "share", "devin", "cli", "sessions.db")
}

func (s *Scanner) openCodeDB() string {
	if s.OpenCodeDB != "" {
		return s.OpenCodeDB
	}
	return filepath.Join(s.home(), ".local", "share", "opencode", "opencode.db")
}

// openLedgerDB opens a SQLite ledger read-only. query_only and a bounded busy
// timeout keep a live writer's locks from stalling the scan.
func openLedgerDB(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

// Scan reads every supported ledger. A missing directory or database is not a
// failure — the engine simply has no recorded usage on this machine.
func (s *Scanner) Scan(ctx context.Context, exclude ExcludeRefs) ScanResult {
	var result ScanResult
	fail := func(engine string, err error) {
		if err == nil {
			return
		}
		if result.Failed == nil {
			result.Failed = make(map[string]error)
		}
		result.Failed[engine] = err
	}
	if ctx.Err() == nil {
		rows, err := s.scanClaude(ctx, exclude.contains)
		result.Rows = append(result.Rows, rows...)
		fail("claude", err)
	}
	if ctx.Err() == nil {
		rows, err := s.scanCodex(ctx, exclude.contains)
		result.Rows = append(result.Rows, rows...)
		fail("codex", err)
	}
	if ctx.Err() == nil {
		rows, err := s.scanDevin(ctx)
		result.Rows = append(result.Rows, rows...)
		fail("devin", err)
	}
	if ctx.Err() == nil {
		rows, err := s.scanOpenCode(ctx, exclude.contains)
		result.Rows = append(result.Rows, rows...)
		fail("opencode", err)
	}
	return result
}
