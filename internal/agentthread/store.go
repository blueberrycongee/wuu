package agentthread

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/storelock"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: strings.TrimSpace(dir)}
}

func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) UpsertThread(meta Metadata) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create thread store: %w", err)
	}
	threads, err := s.loadThreads()
	if err != nil {
		return err
	}
	replaced := false
	for i := range threads {
		if threads[i].ID == meta.ID {
			threads[i] = meta
			replaced = true
			break
		}
	}
	if !replaced {
		threads = append(threads, meta)
	}
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].Path < threads[j].Path
	})
	if err := s.writeThreadsLocked(threads); err != nil {
		return err
	}
	return s.appendEventLocked(Event{
		Type:      EventThreadUpsert,
		ThreadID:  meta.ID,
		Path:      meta.Path,
		Status:    meta.Status,
		Metadata:  &meta,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Store) RecordStatus(meta Metadata) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create thread store: %w", err)
	}
	threads, err := s.loadThreads()
	if err != nil {
		return err
	}
	for i := range threads {
		if threads[i].ID == meta.ID {
			threads[i] = meta
			break
		}
	}
	if err := s.writeThreadsLocked(threads); err != nil {
		return err
	}
	return s.appendEventLocked(Event{
		Type:      EventStatusChange,
		ThreadID:  meta.ID,
		Path:      meta.Path,
		Status:    meta.Status,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Store) RecordEdgeStatus(meta Metadata) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create thread store: %w", err)
	}
	threads, err := s.loadThreads()
	if err != nil {
		return err
	}
	for i := range threads {
		if threads[i].ID == meta.ID {
			threads[i] = meta
			break
		}
	}
	if err := s.writeThreadsLocked(threads); err != nil {
		return err
	}
	return s.appendEventLocked(Event{
		Type:       EventEdgeChange,
		ThreadID:   meta.ID,
		Path:       meta.Path,
		EdgeStatus: meta.Source.EdgeStatus,
		CreatedAt:  time.Now().UTC(),
	})
}

func (s *Store) RecordCommunication(threadID string, communication InterAgentCommunication) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create thread store: %w", err)
	}
	return s.appendEventLocked(Event{
		Type:          EventMessage,
		ThreadID:      strings.TrimSpace(threadID),
		Path:          string(communication.Recipient),
		AuthorPath:    string(communication.Author),
		RecipientPath: string(communication.Recipient),
		Message:       communication.Content,
		TriggerTurn:   communication.TriggerTurn,
		CreatedAt:     time.Now().UTC(),
	})
}

// RecordResultCommunication durably installs one idempotent parent inbox item
// for a terminal child result. Replays may call it repeatedly, but the stable
// result ID ensures the parent event log contains only one pending delivery.
func (s *Store) RecordResultCommunication(threadID, resultID string, communication InterAgentCommunication) (bool, error) {
	if s == nil || s.dir == "" {
		return true, nil
	}
	threadID = strings.TrimSpace(threadID)
	resultID = strings.TrimSpace(resultID)
	if resultID == "" {
		return false, errors.New("result id is required")
	}
	release, err := s.lockStore()
	if err != nil {
		return false, err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return false, fmt.Errorf("create thread store: %w", err)
	}
	events, err := s.loadEvents()
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Type == EventMessage && strings.TrimSpace(event.ThreadID) == threadID && strings.TrimSpace(event.ResultID) == resultID {
			return false, nil
		}
	}
	return true, s.appendEventLocked(Event{
		Type:          EventMessage,
		ThreadID:      threadID,
		ResultID:      resultID,
		Path:          string(communication.Recipient),
		AuthorPath:    string(communication.Author),
		RecipientPath: string(communication.Recipient),
		Message:       communication.Content,
		TriggerTurn:   communication.TriggerTurn,
		CreatedAt:     time.Now().UTC(),
	})
}

func (s *Store) AppendEvent(event Event) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create thread store: %w", err)
	}
	return s.appendEventLocked(event)
}

func (s *Store) ListThreads() ([]Metadata, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadThreads()
}

func (s *Store) ReadEvents() ([]Event, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadEvents()
}

// ClaimResultDelivery compares and appends a result claim while holding the
// durable event-store lock. Exactly one process can claim an unconsumed result.
func (s *Store) ClaimResultDelivery(event Event) (bool, string, error) {
	if s == nil || s.dir == "" {
		return false, "", nil
	}
	resultID := strings.TrimSpace(event.ResultID)
	consumer := strings.TrimSpace(event.Consumer)
	if resultID == "" {
		return false, "", errors.New("result id is required")
	}
	if consumer == "" {
		return false, "", errors.New("result consumer is required")
	}
	release, err := s.lockStore()
	if err != nil {
		return false, "", err
	}
	defer release()
	events, err := s.loadEvents()
	if err != nil {
		return false, "", err
	}
	ready, consumedBy := resultDeliveryState(events, resultID)
	if !ready {
		return false, "", nil
	}
	if consumedBy != "" {
		return false, consumedBy, nil
	}
	event.Type = EventResultClaim
	event.ResultID = resultID
	event.Consumer = consumer
	if err := s.appendEventLocked(event); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// TransitionResultDelivery atomically advances a two-phase consumer claim.
// It is used by durable inbox delivery: pending reserves the result, and the
// applied consumer is recorded only after the recipient's terminal history
// proves that the inbox message reached a completed turn.
func (s *Store) TransitionResultDelivery(event Event, fromConsumer string) (bool, string, error) {
	if s == nil || s.dir == "" {
		return false, "", nil
	}
	resultID := strings.TrimSpace(event.ResultID)
	fromConsumer = strings.TrimSpace(fromConsumer)
	consumer := strings.TrimSpace(event.Consumer)
	if resultID == "" {
		return false, "", errors.New("result id is required")
	}
	if fromConsumer == "" || consumer == "" {
		return false, "", errors.New("result consumers are required")
	}
	release, err := s.lockStore()
	if err != nil {
		return false, "", err
	}
	defer release()
	events, err := s.loadEvents()
	if err != nil {
		return false, "", err
	}
	ready, consumedBy := resultDeliveryState(events, resultID)
	if !ready || consumedBy != fromConsumer {
		return false, consumedBy, nil
	}
	event.Type = EventResultClaim
	event.ResultID = resultID
	event.Consumer = consumer
	if err := s.appendEventLocked(event); err != nil {
		return false, consumedBy, err
	}
	return true, consumer, nil
}

// ReleaseResultDelivery compares and appends a result release while holding
// the durable event-store lock. A stale or non-owner release is a no-op.
func (s *Store) ReleaseResultDelivery(event Event) (bool, error) {
	if s == nil || s.dir == "" {
		return false, nil
	}
	resultID := strings.TrimSpace(event.ResultID)
	consumer := strings.TrimSpace(event.Consumer)
	if resultID == "" {
		return false, errors.New("result id is required")
	}
	if consumer == "" {
		return false, errors.New("result consumer is required")
	}
	release, err := s.lockStore()
	if err != nil {
		return false, err
	}
	defer release()
	events, err := s.loadEvents()
	if err != nil {
		return false, err
	}
	ready, consumedBy := resultDeliveryState(events, resultID)
	if !ready || consumedBy != consumer {
		return false, nil
	}
	event.Type = EventResultRelease
	event.ResultID = resultID
	event.Consumer = consumer
	if err := s.appendEventLocked(event); err != nil {
		return false, err
	}
	return true, nil
}

// lockStore serializes durable read-modify-write transactions, in-process and
// across processes. Pure read methods must not take it: the thread index is
// replaced via atomic rename and the event log is append-only with a scanner
// that skips an incomplete tail line, so direct reads always observe a
// complete snapshot. Reads also must not block on s.mu, because a writer
// waiting on the cross-process lock holds s.mu for the whole wait and would
// stall every read behind a wedged transaction in another process.
func (s *Store) lockStore() (func(), error) {
	s.mu.Lock()
	durable, err := storelock.Acquire(s.dir)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("acquire thread store lock: %w", err)
	}
	return func() {
		_ = durable.Release()
		s.mu.Unlock()
	}, nil
}

// loadEvents reads the append-only event log. It is safe without the store
// lock because appends land whole lines and the scanner skips an incomplete
// tail; transactional methods call it under lockStore for read-modify-write
// atomicity.
func (s *Store) loadEvents() ([]Event, error) {
	path := filepath.Join(s.dir, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open thread events: %w", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan thread events: %w", err)
	}
	return events, nil
}

func resultDeliveryState(events []Event, resultID string) (bool, string) {
	ready := false
	consumedBy := ""
	for _, event := range events {
		if strings.TrimSpace(event.ResultID) != resultID {
			continue
		}
		switch event.Type {
		case EventResultReady:
			ready = true
		case EventResultClaim:
			consumedBy = strings.TrimSpace(event.Consumer)
		case EventResultRelease:
			if consumedBy == strings.TrimSpace(event.Consumer) {
				consumedBy = ""
			}
		}
	}
	return ready, consumedBy
}

// agentIndexCache avoids re-reading every session's thread index on each
// thread/list. A workspace can hold thousands of these files, and one list
// used to parse each of them twice.
var agentIndexCache sync.Map // dir -> agentIndexCacheEntry

type agentIndexCacheEntry struct {
	modUnixNano int64
	size        int64
	threads     []Metadata
}

func cloneThreadIndex(threads []Metadata) []Metadata {
	if threads == nil {
		return nil
	}
	out := make([]Metadata, len(threads))
	copy(out, threads)
	return out
}

// loadThreads reads the thread index. It is safe without the store lock
// because writers replace the index atomically; write transactions call it
// under lockStore for read-modify-write atomicity.
func (s *Store) loadThreads() ([]Metadata, error) {
	path := filepath.Join(s.dir, "threads.json")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat thread index: %w", err)
	}
	if cached, ok := agentIndexCache.Load(s.dir); ok {
		entry := cached.(agentIndexCacheEntry)
		if entry.modUnixNano == info.ModTime().UnixNano() && entry.size == info.Size() {
			return cloneThreadIndex(entry.threads), nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read thread index: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		agentIndexCache.Store(s.dir, agentIndexCacheEntry{modUnixNano: info.ModTime().UnixNano(), size: info.Size()})
		return nil, nil
	}
	var threads []Metadata
	if err := json.Unmarshal(data, &threads); err != nil {
		return nil, fmt.Errorf("decode thread index: %w", err)
	}
	// Drop the cache entry when the file changed while it was being read,
	// so a writer that landed between Stat and ReadFile is not stored under
	// the earlier timestamp.
	after, statErr := os.Stat(path)
	if statErr == nil && after.ModTime().UnixNano() == info.ModTime().UnixNano() && after.Size() == info.Size() {
		agentIndexCache.Store(s.dir, agentIndexCacheEntry{
			modUnixNano: info.ModTime().UnixNano(),
			size:        info.Size(),
			threads:     cloneThreadIndex(threads),
		})
	}
	return threads, nil
}

func (s *Store) writeThreadsLocked(threads []Metadata) error {
	agentIndexCache.Delete(s.dir)
	data, err := json.MarshalIndent(threads, "", "  ")
	if err != nil {
		return fmt.Errorf("encode thread index: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".threads.*.tmp")
	if err != nil {
		return fmt.Errorf("create thread index tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write thread index tmp: %w", err)
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write thread index newline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close thread index tmp: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, "threads.json")); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename thread index: %w", err)
	}
	return nil
}

func (s *Store) appendEventLocked(event Event) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	path := filepath.Join(s.dir, "events.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open thread events: %w", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(event); err != nil {
		return fmt.Errorf("write thread event: %w", err)
	}
	return nil
}
