package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const (
	terminalProcessLogMaxBytes = int64(8 * 1024 * 1024)
	terminalProcessRetention   = 30 * 24 * time.Hour
)

var processLogCompactionMarker = []byte("[... older process output removed ...]\n")

// maintainTerminalStorage bounds terminal output and expires records that no
// longer carry a completion obligation. Live process state is never removed.
func (m *Manager) maintainTerminalStorage(now time.Time) error {
	if m == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	processes, err := m.List()
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(processes))
	var maintenanceErr error
	for _, p := range processes {
		known[p.ID] = struct{}{}
		if p.SeparateStreams {
			known[p.ID+".log.stdout"] = struct{}{}
			known[p.ID+".log.stderr"] = struct{}{}
		}
		if !isTerminalStatus(p.Status) {
			continue
		}
		expectedLog := filepath.Join(m.logDir, p.ID+".log")
		if samePath(p.LogPath, expectedLog) {
			beforeOut, beforeErr := p.StdoutBaseOffset, p.StderrBaseOffset
			maintenanceErr = errors.Join(maintenanceErr, compactProcessStreams(&p))
			if beforeOut != p.StdoutBaseOffset || beforeErr != p.StderrBaseOffset {
				maintenanceErr = errors.Join(maintenanceErr, m.save(&p))
			}
			if changed, discarded, err := compactProcessLog(expectedLog, terminalProcessLogMaxBytes); err != nil && !os.IsNotExist(err) {
				maintenanceErr = errors.Join(maintenanceErr, fmt.Errorf("compact process log %q: %w", p.ID, err))
			} else if changed {
				p.LogBaseOffset += discarded
				if err := m.save(&p); err != nil {
					maintenanceErr = errors.Join(maintenanceErr, fmt.Errorf("save compacted process log offset %q: %w", p.ID, err))
				}
			}
		}
		terminalAt := p.StoppedAt
		if terminalAt.IsZero() {
			terminalAt = p.UpdatedAt
		}
		if terminalAt.IsZero() || now.Sub(terminalAt) < terminalProcessRetention || processCompletionPending(p) {
			continue
		}
		if samePath(p.LogPath, expectedLog) {
			if p.SeparateStreams {
				for _, stream := range []string{"stdout", "stderr"} {
					if err := os.Remove(expectedLog + "." + stream + ".log"); err != nil && !os.IsNotExist(err) {
						maintenanceErr = errors.Join(maintenanceErr, err)
					}
				}
			}
			if err := os.Remove(expectedLog); err != nil && !os.IsNotExist(err) {
				maintenanceErr = errors.Join(maintenanceErr, fmt.Errorf("remove process log %q: %w", p.ID, err))
				continue
			}
		}
		recordPath, err := processRecordPath(m.registryDir, p.ID)
		if err != nil {
			maintenanceErr = errors.Join(maintenanceErr, err)
			continue
		}
		if err := os.Remove(recordPath); err != nil && !os.IsNotExist(err) {
			maintenanceErr = errors.Join(maintenanceErr, fmt.Errorf("remove process record %q: %w", p.ID, err))
		}
	}

	entries, err := os.ReadDir(m.logDir)
	if err != nil {
		return errors.Join(maintenanceErr, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".log")
		if _, ok := known[id]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < terminalProcessRetention {
			continue
		}
		if err := os.Remove(filepath.Join(m.logDir, entry.Name())); err != nil && !os.IsNotExist(err) {
			maintenanceErr = errors.Join(maintenanceErr, fmt.Errorf("remove orphan process log %q: %w", entry.Name(), err))
		}
	}
	return maintenanceErr
}

func isTerminalStatus(status Status) bool {
	return status == StatusStopped || status == StatusFailed
}

func processCompletionPending(p Process) bool {
	return p.CompletionMode != CompletionModeDetached &&
		p.TerminalCause == EventCauseNaturalExit &&
		isTerminalStatus(p.Status) &&
		p.CompletionDeliveredAt.IsZero()
}

func samePath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func compactProcessLog(path string, maxBytes int64) (bool, int64, error) {
	if maxBytes <= int64(len(processLogCompactionMarker)) {
		return false, 0, errors.New("process log limit is too small")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return false, 0, err
	}
	if info.Size() <= maxBytes {
		file.Close()
		return false, 0, nil
	}
	tailBytes := maxBytes - int64(len(processLogCompactionMarker))
	if _, err := file.Seek(info.Size()-tailBytes, io.SeekStart); err != nil {
		file.Close()
		return false, 0, err
	}
	tail, err := io.ReadAll(io.LimitReader(file, tailBytes))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return false, 0, errors.Join(err, closeErr)
	}
	content := make([]byte, 0, maxBytes)
	content = append(content, processLogCompactionMarker...)
	content = append(content, tail...)
	if err := securefs.WriteFileAtomic(path, content); err != nil {
		return false, 0, err
	}
	return true, info.Size() - int64(len(content)), nil
}
