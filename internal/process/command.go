package process

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

const DefaultStopGracePeriod = 2 * time.Second

// ProcessTree identifies the platform-specific process tree rooted at a
// command started by Wuu. On Unix the id is a process group id; on Windows it
// is the root pid used by taskkill /T.
type ProcessTree struct {
	id int
}

// PrepareCommand isolates cmd so its complete descendant tree can be
// signalled without affecting Wuu itself.
func PrepareCommand(cmd *exec.Cmd) {
	configureProcessGroup(cmd)
}

// ProcessTreeForPID resolves the tree created for a newly started command.
func ProcessTreeForPID(pid int) ProcessTree {
	return ProcessTree{id: lookupProcessGroup(pid)}
}

// ProcessTreeFromID restores a tree reference from a recorded platform id.
func ProcessTreeFromID(id int) ProcessTree {
	return ProcessTree{id: id}
}

func (t ProcessTree) ID() int {
	return t.id
}

func (t ProcessTree) Terminate() error {
	if err := t.validate(); err != nil {
		return err
	}
	return terminateProcessGroup(t.id)
}

func (t ProcessTree) Kill() error {
	if err := t.validate(); err != nil {
		return err
	}
	return killProcessGroup(t.id)
}

func (t ProcessTree) validate() error {
	if t.id <= 1 {
		return fmt.Errorf("unsafe process tree id %d", t.id)
	}
	return nil
}

// CommandHandle owns a non-durable command and its complete process tree.
// Durable background metadata and output remain the Manager's responsibility.
type CommandHandle struct {
	tree     ProcessTree
	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
	stopErr  error
}

func StartCommand(cmd *exec.Cmd) (*CommandHandle, error) {
	if cmd == nil {
		return nil, errors.New("command is required")
	}
	PrepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &CommandHandle{
		tree: ProcessTreeForPID(cmd.Process.Pid),
		done: make(chan struct{}),
	}
	go func() {
		h.waitErr = cmd.Wait()
		close(h.done)
	}()
	return h, nil
}

func (h *CommandHandle) Done() <-chan struct{} {
	if h == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return h.done
}

func (h *CommandHandle) Wait() error {
	if h == nil {
		return errors.New("command handle is nil")
	}
	<-h.done
	return h.waitErr
}

// Stop gives the full tree a bounded graceful shutdown window, then always
// sends a force-kill to remove descendants that ignored SIGTERM after their
// direct parent exited.
func (h *CommandHandle) Stop(grace time.Duration) error {
	if h == nil {
		return errors.New("command handle is nil")
	}
	if grace <= 0 {
		grace = DefaultStopGracePeriod
	}
	h.stopOnce.Do(func() {
		select {
		case <-h.done:
			return
		default:
		}

		terminateErr := h.tree.Terminate()
		stopped := waitForCommand(h.done, grace)
		killErr := h.tree.Kill()
		if !stopped && !waitForCommand(h.done, grace) {
			h.stopErr = errors.Join(
				terminateErr,
				killErr,
				fmt.Errorf("process tree %d did not stop after force-kill", h.tree.ID()),
			)
			return
		}
		h.stopErr = errors.Join(terminateErr, killErr)
	})
	return h.stopErr
}

func waitForCommand(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
