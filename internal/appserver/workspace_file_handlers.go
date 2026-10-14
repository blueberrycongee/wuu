package appserver

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/workspaces"
	"github.com/blueberrycongee/wuu/internal/workspaceview"
)

const (
	MethodWorkspaceDirectoryList = workspaceview.MethodWorkspaceDirectoryList
	MethodWorkspaceFileRead      = workspaceview.MethodWorkspaceFileRead
	MethodWorkspaceFileChunk     = workspaceview.MethodWorkspaceFileChunk
	MethodWorkspaceFileResolve   = workspaceview.MethodWorkspaceFileResolve
)

func (s *Server) handleWorkspaceView(ctx context.Context, req Request) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var params workspaceview.Params
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.ThreadID != "" || params.Target != nil {
		if params.Target == nil || params.ThreadID == "" || params.ThreadID != params.Target.ThreadID {
			return s.writeResponse(req.ID, nil, errors.New("workspace target does not match the session"))
		}
		binding, err := s.executionEnvironmentBinding(params.ThreadID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if params.Root != "" && filepath.Clean(params.Root) != filepath.Clean(binding.CWD) {
			return s.writeResponse(req.ID, nil, errors.New("session workspace changed; reopen the file view"))
		}
		summary, err := s.rt.ExecutionEnvironmentSummary(params.ThreadID, binding.CWD)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if summary.ID != "" && agentengine.NormalizeEngineID(binding.EngineID) != agentengine.EngineWuu {
			return s.writeResponse(req.ID, nil, errors.New("execution workspace is unavailable for this engine"))
		}
		result, err := s.rt.ReadExecutionWorkspace(ctx, params.ThreadID, binding.CWD, req.Method, params)
		if err == nil {
			current, bindingErr := s.executionEnvironmentBinding(params.ThreadID)
			if bindingErr != nil {
				err = bindingErr
			} else if current.CWD != binding.CWD || current.EngineID != binding.EngineID {
				err = errors.New("session workspace changed; reopen the file view")
			}
		}
		return s.writeResponse(req.ID, result, err)
	}
	rootPath, err := s.workspaceViewRoot(params.Root)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	result, err := workspaceview.Read(rootPath, req.Method, params, nil)
	return s.writeResponse(req.ID, result, err)
}

// A browser may navigate a registered workspace or a session's worktree. It
// cannot turn a caller-supplied root into a machine-wide file server.
func (s *Server) workspaceViewRoot(requested string) (string, error) {
	if requested == "" {
		requested = s.rt.RootDir
	}
	if !filepath.IsAbs(requested) {
		return "", errors.New("workspace root must be absolute")
	}
	target, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", err
	}
	allowed := func(path string) bool {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(real, target)
		return err == nil && (rel == "." || filepath.IsLocal(rel))
	}
	if allowed(s.rt.RootDir) {
		return target, nil
	}
	registered, err := workspaces.List(s.rt.WuuHome)
	if err != nil {
		return "", err
	}
	for _, workspace := range registered {
		if allowed(workspace.Root) {
			return target, nil
		}
	}
	sessions, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return "", err
	}
	for _, metadata := range sessions {
		if metadata.CWD != "" && allowed(metadata.CWD) {
			return target, nil
		}
	}
	return "", errors.New("directory is not a known workspace or session directory")
}
