package harness

import (
	"context"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
	"os"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// DocumentNamespace is the provider-owned state for one root session.
const DocumentNamespace = "wuu.harness.v1"

// NewDocumentStore binds tasks, queue claims, reports and events to a selected
// provider and root session. The local path is used only for one-time migration
// and derived Markdown artifacts, never as a fallback after provider failure.
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
			importErr := func() error {
				for _, name := range []string{"tasks", "runs", "artifacts", "reports", "queue"} {
					var value any
					if err := readJSONFile(filepath.Join(dir, name+".json"), &value); err != nil {
						return err
					}
					if err := persistence.MergeLegacyCollection(image, name, value); err != nil {
						return err
					}
				}
				events, err := persistence.ReadLegacyEvents(filepath.Join(dir, "events.jsonl"))
				if err != nil {
					return err
				}
				return persistence.MergeLegacyCollection(image, "events", events)
			}()
			release()
			if importErr != nil {
				return importErr
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

func (s *Store) readJSONFile(path string, out any) error {
	if s.image != nil {
		return s.image.Get(fileCollection(path), out)
	}
	return readJSONFile(path, out)
}
func (s *Store) writeJSONFile(path string, value any) error {
	if s.image != nil {
		return s.image.Put(fileCollection(path), value)
	}
	return writeJSONFile(path, value)
}
func fileCollection(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}

// Derived artifacts are outside the document commit. Serialize materialization
// and reread authoritative content so a delayed older writer cannot clobber a
// newer report after both commits have succeeded.
func (s *Store) materializeReport(taskID, reportID string) error {
	lock, err := storelock.Acquire(s.dir)
	if err != nil {
		return fmt.Errorf("report committed; lock derived artifact: %w", err)
	}
	defer lock.Release()
	var report Report
	var found bool
	if reportID == "" {
		report, found, err = s.ReportForTask(taskID)
	} else {
		var reports []Report
		reports, err = s.ListReports()
		for _, candidate := range reports {
			if candidate.TaskID == taskID && candidate.ID == reportID {
				report, found = candidate, true
				break
			}
		}
		// A structured report may have superseded this generated record while
		// materialization waited; do not resurrect its retired index entry.
		if err == nil && !found {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("report committed; read derived artifact source: %w", err)
	}
	if !found {
		return fmt.Errorf("report committed; derived artifact source is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(report.ReportPath), securefs.DirMode); err != nil {
		return fmt.Errorf("report committed; create derived artifact: %w", err)
	}
	data := []byte(renderReportMarkdown(report))
	var writeErr error
	if reportID != "" {
		writeErr = writeGeneratedReport(report.ReportPath, data)
	} else {
		writeErr = securefs.WriteFileAtomic(report.ReportPath, data)
	}
	if writeErr != nil {
		return fmt.Errorf("report committed; write derived artifact: %w", writeErr)
	}
	return nil
}
