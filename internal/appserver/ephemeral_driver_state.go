package appserver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
)

// ephemeralDriverState belongs to the conversation, so runtime rebuilds keep
// its identity and checkpoint. It makes no persistence promise beyond the
// lifetime of the explicitly ephemeral thread.
type ephemeralDriverState struct {
	mu         sync.Mutex
	binding    loopdriver.Binding
	bound      bool
	checkpoint loopdriver.Checkpoint
	saved      bool
}

func (s *ephemeralDriverState) LoadBinding(ctx context.Context) (loopdriver.Binding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binding, s.bound, ctx.Err()
}

func (s *ephemeralDriverState) Bind(ctx context.Context, binding loopdriver.Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.bound && s.binding != binding {
		return fmt.Errorf("loop driver binding requires %+v; requested %+v", s.binding, binding)
	}
	s.binding, s.bound = binding, true
	return nil
}

func (s *ephemeralDriverState) Load(ctx context.Context) (loopdriver.Checkpoint, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	checkpoint := s.checkpoint
	checkpoint.State = append([]byte(nil), checkpoint.State...)
	return checkpoint, s.saved, ctx.Err()
}

func (s *ephemeralDriverState) Save(ctx context.Context, checkpoint loopdriver.Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.bound || checkpoint.ContractVersion != s.binding.ContractVersion || checkpoint.DriverID != s.binding.DriverID || checkpoint.DriverVersion != s.binding.DriverVersion {
		return errors.New("driver checkpoint does not match the ephemeral session binding")
	}
	checkpoint.State = append([]byte(nil), checkpoint.State...)
	s.checkpoint, s.saved = checkpoint, true
	return nil
}
