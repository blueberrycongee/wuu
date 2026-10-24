package session

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

type remoteJournalRuntime struct {
	store  *Store
	handle string
}
type remoteInferenceJournal struct {
	runtime *remoteJournalRuntime
	owner   string
}

func (s *Store) NewInferenceJournalRuntime(scope string) (*InferenceJournalRuntime, error) {
	var handle resourceHandle
	if err := s.invoke("NewInferenceJournalRuntime", []any{scope}, []any{&handle}); err != nil {
		return nil, err
	}
	if handle.Handle == "" || handle.RuntimeID == "" {
		return nil, errors.New("session provider returned an empty journal handle")
	}
	return &InferenceJournalRuntime{runtimeID: handle.RuntimeID, remote: &remoteJournalRuntime{s.WithContext(context.Background()), handle.Handle}}, nil
}
func (r *remoteJournalRuntime) invoke(method string, args []any, results []any) error {
	return r.store.invoke("Journal."+method, append([]any{r.handle}, args...), results)
}
func (j *remoteInferenceJournal) invoke(ctx context.Context, method string, args ...any) error {
	return j.runtime.store.WithContext(ctx).invoke("Journal."+method, append([]any{j.runtime.handle, j.owner}, args...), []any{})
}

var _ providers.InferenceJournal = (*remoteInferenceJournal)(nil)

// The OS gate and descriptor are host mechanisms shared across every selected
// session generation. Only membership and persistent reset state are delegated.
func (s *Store) TryAcquireThreadExecutionLease(dir, id string) (*DocumentThreadExecutionLease, bool, error) {
	return s.TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, 0)
}
func (s *Store) TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id string, limit int) (*DocumentThreadExecutionLease, bool, error) {
	dir, id = strings.TrimSpace(dir), strings.TrimSpace(id)
	if dir == "" || id == "" {
		return nil, false, errors.New("session directory and thread id are required")
	}
	gate, err := acquireThreadAdmission(s.ctx, dir)
	if err != nil {
		return nil, false, err
	}
	defer gate.Close()
	defer unlockThreadExecutionFile(gate)
	file, err := securefs.OpenFile(threadExecutionLeasePath(dir, id), os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, false, err
	}
	acquired, err := tryLockThreadExecutionFile(file)
	if err != nil || !acquired {
		_ = file.Close()
		return nil, false, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = unlockThreadExecutionFile(file)
			_ = file.Close()
		}
	}()
	var generation int64
	for {
		active, known := []string{}, []string{}
		if limit > 0 {
			rows, err := s.List(0)
			if err != nil {
				return nil, false, err
			}
			for _, row := range rows {
				known = append(known, row.ID)
				if row.ID == id {
					continue
				}
				live, err := ThreadExecutionActive(dir, row.ID)
				if err != nil {
					return nil, false, err
				}
				if live {
					active = append(active, row.ID)
				}
			}
		}
		err := s.invoke("AdmitThreadExecution", []any{id, limit, active, known}, []any{&generation})
		if errors.Is(err, errAdmissionChanged) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		break
	}
	keep = true
	return &DocumentThreadExecutionLease{file: file, threadID: id, resetGeneration: generation, remote: s.WithContext(context.Background())}, true, nil
}
func (s *Store) RequestThreadExecutionReset(dir, id string) (bool, error) {
	dir, id = strings.TrimSpace(dir), strings.TrimSpace(id)
	if dir == "" || id == "" {
		return false, errors.New("session directory and thread id are required")
	}
	gate, err := acquireThreadAdmission(s.ctx, dir)
	if err != nil {
		return false, err
	}
	defer gate.Close()
	defer unlockThreadExecutionFile(gate)
	active, err := ThreadExecutionActive(dir, id)
	if err != nil || !active {
		return false, err
	}
	err = s.invoke("RequestThreadExecutionResetRecord", []any{id}, []any{})
	return err == nil, err
}
