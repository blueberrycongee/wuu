package appserver

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

type stdioResumeDispatchKey struct{}

type stdioResumeRequest struct {
	done chan struct{}
	// True only while every preceding operation in this lane is a resume read.
	// Stop may bypass reads, but never earlier queued input or mutations.
	readOnly bool
}

// Resume reads, confirmed archive settlement, and their successors leave ordered ingress.
// The dependency graph follows ingress order, so multi-session operations can
// wait for both predecessors without taking application locks while waiting.
func (s *Server) deferStdioResume(ctx context.Context, req Request) (bool, error) {
	resources, err := requestResources(req)
	if err != nil {
		// The authorized dispatcher owns invalid input.
		return false, nil
	}
	var ids []string
	for _, resource := range resources {
		if resource.Kind == "session" {
			ids = append(ids, strings.TrimSpace(resource.ID))
		}
	}
	// Model configuration remains globally authorized, but its thread-scoped
	// variant must still follow a prior read of that same conversation.
	if req.Method == MethodConfigModelUpdate {
		var params struct {
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(req.Params, &params) == nil && strings.TrimSpace(params.ThreadID) != "" {
			ids = append(ids, strings.TrimSpace(params.ThreadID))
		}
	}
	if len(ids) == 0 {
		// Implicit most-recent resume resolves synchronously.
		return false, nil
	}
	resume := req.Method == MethodThreadResume
	var archiveParams ThreadArchiveParams
	archive := req.Method == MethodThreadArchive && json.Unmarshal(req.Params, &archiveParams) == nil && archiveParams.Archived && archiveParams.Force
	s.stdioResumeMu.Lock()
	var predecessors []*stdioResumeRequest
	readOnly := true
	for _, id := range ids {
		if previous := s.stdioResumeTail[id]; previous != nil {
			predecessors = append(predecessors, previous)
			readOnly = readOnly && previous.readOnly
		}
	}
	if !resume && !archive && len(predecessors) == 0 {
		s.stdioResumeMu.Unlock()
		return false, nil
	}
	s.stdioResumeMu.Unlock()
	// An active Stop can pass pure reads. If an earlier queued input/mutation
	// is waiting too, preserve its order so it cannot start work after Stop.
	if req.Method == MethodTurnInterrupt && len(ids) == 1 && readOnly {
		if s.activeExecutionRunID(ids[0]) != "" || threadIsRunning(s.thread(ids[0])) {
			return false, nil
		}
	}
	var binding *runtime.State
	var params ThreadResumeParams
	if resume && len(predecessors) == 0 {
		binding, req, params, err = s.retainThreadResume(req)
		if err != nil {
			return true, s.writeResponse(req.ID, nil, err)
		}
	}
	if archive && len(predecessors) == 0 {
		binding, err = s.rt.RetainState()
		if err != nil {
			return true, s.writeResponse(req.ID, nil, err)
		}
	}
	current := &stdioResumeRequest{done: make(chan struct{}), readOnly: resume && readOnly}
	s.stdioResumeMu.Lock()
	if s.stdioResumeTail == nil {
		s.stdioResumeTail = make(map[string]*stdioResumeRequest)
	}
	for _, id := range ids {
		s.stdioResumeTail[id] = current
	}
	s.stdioResumeMu.Unlock()
	finish := func() {
		if binding != nil {
			_ = binding.Close()
		}
		s.stdioResumeMu.Lock()
		for _, id := range ids {
			if s.stdioResumeTail[id] == current {
				delete(s.stdioResumeTail, id)
			}
		}
		close(current.done)
		s.stdioResumeMu.Unlock()
	}
	waitStarted := req.timing.now()
	if !s.startBackground(func() {
		defer finish()
		// A cancelled request cannot release its lane ahead of physical
		// predecessor completion. Provider calls keep their existing bound
		// cancellation semantics; shutdown uses the owned-work bounded drain.
		for _, previous := range predecessors {
			<-previous.done
		}
		req.timing.span("resume_dispatch_wait", waitStarted)
		req.timing.mark("resume_dispatch_begin")
		defer req.timing.mark("resume_dispatch_end")
		if err := ctx.Err(); err != nil {
			_ = s.writeResponse(req.ID, nil, err)
			return
		}
		if s.closed.Load() {
			_ = s.writeResponse(req.ID, nil, errServerClosed)
			return
		}
		if _, err := s.requireRequestAccess(req, req.Method, nil); err != nil {
			_ = s.writeResponse(req.ID, nil, err)
			return
		}
		var err error
		if resume {
			// A queued read selects its state only after predecessor mutations
			// have completed, matching the original ordered admission semantics.
			if binding == nil {
				binding, req, params, err = s.retainThreadResume(req)
				if err != nil {
					_ = s.writeResponse(req.ID, nil, err)
					return
				}
			}
			err = s.handleThreadResumeForState(binding, req, params)
		} else if archive {
			if binding == nil {
				binding, err = s.rt.RetainState()
				if err != nil {
					_ = s.writeResponse(req.ID, nil, err)
					return
				}
			}
			err = s.handleThreadArchiveForState(ctx, binding, req)
		} else {
			err = s.dispatchAuthorizedRequest(ctx, req)
		}
		if err != nil {
			log.Printf("wuu: ordered session request: %v", err)
		}
	}) {
		finish()
		return true, s.writeResponse(req.ID, nil, errServerClosed)
	}
	return true, nil
}
