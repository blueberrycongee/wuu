package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// browserTabsSchemaVersion tags the on-disk file so a future format change can be
// migrated instead of silently misread.
const browserTabsSchemaVersion = "1"

// BrowserTabFileStore is the durable, per-thread tab registry the embedded
// browser tool persists between turns and across core restarts. It mirrors
// goalruntime.Store: a single JSON file guarded by a mutex, rewritten
// atomically (temp file + rename) on every mutation so a crash mid-write never
// leaves a half-written registry. It implements BrowserTabStore.
//
// The file lives under the thread's artifact directory
// (statepath.ThreadBrowserTabsPath), so deleting the thread reclaims it for free
// via os.RemoveAll(SessionArtifactDir).
type BrowserTabFileStore struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

type browserTabsFile struct {
	SchemaVersion string             `json:"schema_version"`
	Tabs          []BrowserTabRecord `json:"tabs"`
}

// NewBrowserTabStore returns a store backed by the given file. The file is
// created lazily on the first Put; a missing file reads as an empty registry.
func NewBrowserTabStore(path string) *BrowserTabFileStore {
	return &BrowserTabFileStore{
		path: strings.TrimSpace(path),
		now:  func() time.Time { return time.Now().UTC() },
	}
}

// Path returns the backing file path (empty for a nil store).
func (s *BrowserTabFileStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// SetClock overrides the timestamp source (tests inject a deterministic clock).
func (s *BrowserTabFileStore) SetClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
}

func (s *BrowserTabFileStore) configured() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return errors.New("browser tab store path is required")
	}
	return nil
}

// List returns every persisted record, ordered by tab_id for deterministic
// output.
func (s *BrowserTabFileStore) List() ([]BrowserTabRecord, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := append([]BrowserTabRecord(nil), file.Tabs...)
	sort.Slice(out, func(i, j int) bool { return out[i].TabID < out[j].TabID })
	return out, nil
}

// Get returns the record for tabID and whether it was found.
func (s *BrowserTabFileStore) Get(tabID string) (BrowserTabRecord, bool, error) {
	if err := s.configured(); err != nil {
		return BrowserTabRecord{}, false, err
	}
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return BrowserTabRecord{}, false, errors.New("tab_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return BrowserTabRecord{}, false, err
	}
	for _, rec := range file.Tabs {
		if rec.TabID == tabID {
			return rec, true, nil
		}
	}
	return BrowserTabRecord{}, false, nil
}

// Put upserts a record by tab_id. UpdatedAt is stamped when the caller left it
// zero so every mutation advances the record's clock (the frontend merges
// activity events by updated_at).
func (s *BrowserTabFileStore) Put(rec BrowserTabRecord) error {
	if err := s.configured(); err != nil {
		return err
	}
	rec.TabID = strings.TrimSpace(rec.TabID)
	if rec.TabID == "" {
		return errors.New("tab_id is required")
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	replaced := false
	for i := range file.Tabs {
		if file.Tabs[i].TabID == rec.TabID {
			file.Tabs[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		file.Tabs = append(file.Tabs, rec)
	}
	return s.saveLocked(file)
}

// Delete removes the record for tabID. Removing a missing tab is a no-op.
func (s *BrowserTabFileStore) Delete(tabID string) error {
	if err := s.configured(); err != nil {
		return err
	}
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("tab_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	filtered := file.Tabs[:0]
	removed := false
	for _, rec := range file.Tabs {
		if rec.TabID == tabID {
			removed = true
			continue
		}
		filtered = append(filtered, rec)
	}
	if !removed {
		return nil
	}
	file.Tabs = filtered
	return s.saveLocked(file)
}

func (s *BrowserTabFileStore) loadLocked() (browserTabsFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return browserTabsFile{SchemaVersion: browserTabsSchemaVersion}, nil
		}
		return browserTabsFile{}, err
	}
	var file browserTabsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return browserTabsFile{}, err
	}
	return file, nil
}

func (s *BrowserTabFileStore) saveLocked(file browserTabsFile) error {
	file.SchemaVersion = browserTabsSchemaVersion
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".browser-tabs-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
