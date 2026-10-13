package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type fileLockInvoker struct{ parent *kernelHostServices }

func (k *fileLockInvoker) ID() string                { return k.parent.ID() }
func (k *fileLockInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *fileLockInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	k.parent.mu.RLock()
	services := k.parent.services[params.Caller]
	workspaceStateDir := k.parent.workspaceStateDir
	k.parent.mu.RUnlock()
	if services == nil {
		return nil, serviceError("service_unavailable", "kernel service caller is unavailable")
	}
	return services.invokeFileLock(ctx, params.Method, params.Params, workspaceStateDir)
}

func (s *pluginHostServices) invokeFileLock(ctx context.Context, method string, raw json.RawMessage, workspaceStateDir string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	open, active := s.open, s.active
	valid := strings.TrimSpace(s.subjectID) != "" && strings.TrimSpace(s.fingerprint) != ""
	generationContext := s.lockContext
	roots := map[string]string{"workspace_state": workspaceStateDir, "wuu_home": s.wuuHome, "project": s.projectRoot}
	s.mu.RUnlock()
	if !open {
		return nil, serviceError("generation_closed", "plugin generation is no longer active")
	}
	if !active {
		return nil, serviceError("service_unavailable", "host service is unavailable during generation prepare")
	}
	if !valid {
		return nil, serviceError("generation_invalid", "plugin generation has no stable package identity")
	}
	switch method {
	case "acquire":
		var input pluginhost.FileLockAcquireParams
		if err := decodeServiceParams(raw, &input); err != nil {
			return nil, err
		}
		root := roots[input.Root]
		if strings.TrimSpace(root) == "" {
			return nil, serviceError("invalid_params", "file lock requires an available workspace_state, wuu_home, or project root")
		}
		// os.Root enforces confinement during filesystem operations, including
		// symlink traversal, rather than relying only on a lexical path check.
		if !filepath.IsLocal(input.Path) || input.Path == "." || strings.Contains(input.Path, "\\") {
			return nil, serviceError("invalid_params", "file lock path must be a relative file within its root")
		}
		rootDir, err := os.OpenRoot(root)
		if err != nil {
			return nil, fmt.Errorf("open file lock root: %w", err)
		}
		defer rootDir.Close()
		if err := rootDir.MkdirAll(filepath.Dir(input.Path), 0o755); err != nil {
			return nil, fmt.Errorf("create file lock directory: %w", err)
		}
		file, err := rootDir.OpenFile(input.Path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open file lock: %w", err)
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return nil, serviceError("invalid_params", "file lock path must name a regular file")
		}
		waitContext, cancel := context.WithCancel(ctx)
		stopGeneration := context.AfterFunc(generationContext, cancel)
		defer func() { stopGeneration(); cancel() }()
		unlock, err := acquireOSFileLock(waitContext, file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		var once sync.Once
		release := func() { once.Do(func() { unlock(); _ = file.Close() }) }
		var idBytes [16]byte
		if _, err := rand.Read(idBytes[:]); err != nil {
			release()
			return nil, err
		}
		id := hex.EncodeToString(idBytes[:])
		s.mu.Lock()
		if !s.open || waitContext.Err() != nil {
			s.mu.Unlock()
			release()
			return nil, serviceError("generation_closed", "file lock acquisition was cancelled")
		}
		s.fileLocks[id] = release
		s.mu.Unlock()
		return marshalServiceResult(pluginhost.FileLockAcquireResult{LockID: id})
	case "release":
		var input pluginhost.FileLockReleaseParams
		if err := decodeServiceParams(raw, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.LockID) == "" {
			return nil, serviceError("invalid_params", "file lock release requires lock_id")
		}
		s.mu.Lock()
		release := s.fileLocks[input.LockID]
		delete(s.fileLocks, input.LockID)
		s.mu.Unlock()
		if release != nil {
			release()
		}
		return marshalServiceResult(pluginhost.FileLockReleaseResult{Released: release != nil})
	default:
		return nil, serviceError("method_not_found", "file lock method is unavailable")
	}
}

// Nonblocking OS attempts let cancellation stop a waiter without closing a
// descriptor concurrently with an in-flight locking syscall.
func waitFileLockRetry(ctx context.Context) error {
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
