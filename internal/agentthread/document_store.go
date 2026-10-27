package agentthread

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

// DocumentNamespace is the provider-owned state for one root session.
const DocumentNamespace = "wuu.agentthreads.v1"

// NewDocumentStore binds child metadata and delivery claims to one selected
// provider and root session. Legacy files are imported once when no domain
// image exists; errors from the provider never trigger file fallback.
func NewDocumentStore(documents persistence.Documents, sessionID string, legacyDirs ...string) *Store {
	dir := ""
	if len(legacyDirs) > 0 {
		dir = legacyDirs[0]
	}
	return &Store{documents: documents, documentKey: sessionID, dir: dir, legacyDirs: append([]string(nil), legacyDirs...)}
}

func (s *Store) access(body func(*Store) error) error {
	now := time.Now().UTC()
	return persistence.AccessDocumentImage(context.Background(), s.documents, DocumentNamespace, s.documentKey, func(image *persistence.DocumentImage) error {

		for _, dir := range s.legacyDirs {
			if dir == "" {
				continue
			}
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			legacy := NewStore(dir)
			release, err := legacy.lockStore()
			if err != nil {
				return err
			}
			threads, readErr := legacy.ListThreads()
			events, eventErr := persistence.ReadLegacyEvents(filepath.Join(dir, "events.jsonl"))
			release()
			if readErr != nil {
				return readErr
			}
			if eventErr != nil {
				return eventErr
			}
			if err := persistence.MergeLegacyCollection(image, "threads", threads); err != nil {
				return err
			}
			if err := persistence.MergeLegacyCollection(image, "events", events); err != nil {
				return err
			}
		}
		return nil
	}, func(image *persistence.DocumentImage) error {
		return body(&Store{dir: s.dir, documentKey: s.documentKey, image: image, operationTime: now})
	})
}

func (s *Store) now() time.Time {
	if !s.operationTime.IsZero() {
		return s.operationTime
	}
	return time.Now().UTC()
}

func (s *Store) ensureDir() error {
	if s.image != nil {
		return nil
	}
	return os.MkdirAll(s.dir, securefs.DirMode)
}
