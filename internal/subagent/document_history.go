package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// HistoryDocumentNamespace stores worker terminal and pending driver snapshots
// together, preserving their distinct recovery and result-delivery meanings.
const HistoryDocumentNamespace = "wuu.workers.v1"

func workerHistoryImage(documents persistence.Documents, rootID, workerID string, paths []string, body func(*persistence.DocumentImage) error) error {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(workerID) == "" {
		return persistence.ErrInvalid
	}
	return persistence.AccessDocumentImage(context.Background(), documents, HistoryDocumentNamespace, rootID+"/"+workerID, func(image *persistence.DocumentImage) error {
		newest := map[string]time.Time{}
		for _, path := range paths {
			if path == "" {
				continue
			}
			for _, key := range []string{"terminal", "pending"} {
				source := path
				if key == "pending" {
					source += ".driver-state"
				}
				raw, err := os.ReadFile(source)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				var record historyRecord
				if err := json.Unmarshal(raw, &record); err != nil {
					return err
				}
				stamp := record.StartedAt
				if record.CompletedAt.After(stamp) {
					stamp = record.CompletedAt
				}
				if _, found := image.Values[key]; found && !stamp.After(newest[key]) {
					continue
				}
				if err := image.Put(key, json.RawMessage(raw)); err != nil {
					return err
				}
				newest[key] = stamp
			}
		}
		return nil
	}, body)
}

func LoadPersistedRunFromDocuments(documents persistence.Documents, rootID, workerID string, legacyPaths ...string) (PersistedRun, error) {
	var run PersistedRun
	err := workerHistoryImage(documents, rootID, workerID, legacyPaths, func(image *persistence.DocumentImage) error {
		var err error
		run, err = decodePersistedRecords(image.Values["terminal"], image.Values["pending"])
		return err
	})
	return run, err
}

func writeDocumentWorkerRecord(documents persistence.Documents, rootID, workerID, path, key string, record historyRecord) error {
	return workerHistoryImage(documents, rootID, workerID, []string{path}, func(image *persistence.DocumentImage) error { return image.Put(key, record) })
}

func MarkDocumentRunInterrupted(documents persistence.Documents, rootID, workerID, reason string, now time.Time, legacyPaths ...string) (bool, error) {
	changed := false
	err := workerHistoryImage(documents, rootID, workerID, legacyPaths, func(image *persistence.DocumentImage) error {
		changed = false
		if len(image.Values["terminal"]) == 0 {
			return nil
		}
		var record historyRecord
		if err := image.Get("terminal", &record); err != nil {
			return err
		}
		if IsTerminal(Status(record.Status)) || Status(record.Status) == StatusWaitingChildren {
			return nil
		}
		record.Status = string(StatusInterrupted)
		if strings.TrimSpace(record.Error) == "" {
			record.Error = reason
		}
		if record.CompletedAt.IsZero() {
			record.CompletedAt = now
		}
		changed = true
		return image.Put("terminal", record)
	})
	return changed, err
}
